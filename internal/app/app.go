package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	genapi "github.com/cyoda-platform/cyoda-go/api"
	internalapi "github.com/cyoda-platform/cyoda-go/internal/api"
	"github.com/cyoda-platform/cyoda-go/internal/api/middleware"
	"github.com/cyoda-platform/cyoda-go/internal/auth"
	clusterdispatch "github.com/cyoda-platform/cyoda-go/internal/cluster/dispatch"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/lifecycle"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/proxy"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/registry"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/token"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/contract"
	"github.com/cyoda-platform/cyoda-go/internal/domain/account"
	"github.com/cyoda-platform/cyoda-go/internal/domain/audit"
	"github.com/cyoda-platform/cyoda-go/internal/domain/entity"
	"github.com/cyoda-platform/cyoda-go/internal/domain/messaging"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model"
	"github.com/cyoda-platform/cyoda-go/internal/domain/search"
	"github.com/cyoda-platform/cyoda-go/internal/domain/workflow"
	internalgrpc "github.com/cyoda-platform/cyoda-go/internal/grpc"
	mockiam "github.com/cyoda-platform/cyoda-go/internal/iam/mock"
	"github.com/cyoda-platform/cyoda-go/internal/observability"
	"github.com/cyoda-platform/cyoda-go/internal/skeleton"
)

type App struct {
	config             Config
	storeFactory       spi.StoreFactory
	transactionManager spi.TransactionManager
	authService        contract.AuthenticationService
	authzService       contract.AuthorizationService
	workflowEngine     *workflow.Engine
	searchService      *search.SearchService
	auditService       contract.AuditService
	clusterService     contract.ClusterService
	memberRegistry     *internalgrpc.MemberRegistry
	grpcServer         *internalgrpc.Server
	handler            http.Handler
	tokenSigner        *token.Signer
	nodeRegistry       contract.NodeRegistry
	txLifecycle        *lifecycle.Manager
	stopReaper         chan struct{}
	stopSearchReaper   chan struct{}
}

func New(cfg Config) *App {
	a := &App{config: cfg}

	common.SetErrorResponseMode(cfg.ErrorResponseMode)

	// cfg.StorageBackend is populated at config-construction time from the
	// CYODA_STORAGE_BACKEND env var with "memory" as the default.
	plugin, ok := spi.GetPlugin(cfg.StorageBackend)
	if !ok {
		slog.Error("unknown storage backend",
			"backend", cfg.StorageBackend,
			"available", spi.RegisteredPlugins())
		os.Exit(1)
	}

	slog.Info("storage backend selected",
		"backend", plugin.Name(),
		"available", spi.RegisteredPlugins())

	var factoryOpts []spi.FactoryOption
	// ClusterBroadcaster wiring lands in Plan 4 (cassandra plugin consumes it).

	// startupCtx carries a deadline so unreachable infrastructure fails fast
	// instead of hanging in pgxpool or gocql.
	startupCtx, cancel := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	defer cancel()

	factory, err := plugin.NewFactory(startupCtx, os.Getenv, factoryOpts...)
	if err != nil {
		panic(fmt.Sprintf("create storage factory for %s: %v", plugin.Name(), err))
	}
	a.storeFactory = factory

	txMgr, err := factory.TransactionManager(startupCtx)
	if err != nil {
		panic(fmt.Sprintf("get transaction manager from %s: %v", plugin.Name(), err))
	}
	a.transactionManager = txMgr

	// Decorator wrap order (innermost → outermost, per D13 of the spec):
	//   plugin TM → metrics → tracing → logging → domain-service consumers
	// Today only tracing is wired; add future decorators between tracing and
	// the plugin TM in the order named here.
	if cfg.OTelEnabled {
		a.transactionManager = observability.NewTracingTransactionManager(a.transactionManager)
	}

	// Auth service: JWT or mock mode
	var authSvc *auth.AuthService
	if cfg.IAM.Mode == "jwt" {
		if cfg.IAM.JWTSigningKey == "" {
			panic("CYODA_JWT_SIGNING_KEY is required when IAM mode is jwt")
		}
		// Create a KV-backed trusted key store for persistence across restarts.
		systemCtx := spi.WithUserContext(context.Background(), &spi.UserContext{
			UserID:   "system",
			UserName: "System",
			Tenant:   spi.Tenant{ID: spi.SystemTenantID, Name: "System"},
		})
		kvStore, err := a.storeFactory.KeyValueStore(systemCtx)
		if err != nil {
			panic(fmt.Sprintf("failed to get KV store for trusted keys: %v", err))
		}
		trustedKeyStore, err := auth.NewKVTrustedKeyStore(systemCtx, kvStore)
		if err != nil {
			panic(fmt.Sprintf("failed to create KV trusted key store: %v", err))
		}
		authSvc, err = auth.NewAuthService(auth.AuthConfig{
			SigningKeyPEM:   cfg.IAM.JWTSigningKey,
			Issuer:          cfg.IAM.JWTIssuer,
			ExpirySeconds:   cfg.IAM.JWTExpiry,
			TrustedKeyStore: trustedKeyStore,
		})
		if err != nil {
			panic(fmt.Sprintf("failed to create auth service: %v", err))
		}
		contextPath := strings.TrimRight(cfg.ContextPath, "/")
		jwksURL := fmt.Sprintf("http://localhost:%d%s/.well-known/jwks.json", cfg.HTTPPort, contextPath)
		validator := auth.NewJWKSValidator(jwksURL, authSvc.Issuer(), 5*time.Minute)
		a.authService = auth.NewDelegatingAuthenticator(validator)

		// Bootstrap M2M client if configured
		if cfg.Bootstrap.ClientID != "" {
			roles := strings.Split(cfg.Bootstrap.Roles, ",")
			for i := range roles {
				roles[i] = strings.TrimSpace(roles[i])
			}
			secret := cfg.Bootstrap.ClientSecret
			if secret != "" {
				// Use provided secret
				err := authSvc.M2MClientStore().CreateWithSecret(
					cfg.Bootstrap.ClientID,
					cfg.Bootstrap.TenantID,
					cfg.Bootstrap.UserID,
					secret,
					roles,
				)
				if err != nil {
					panic(fmt.Sprintf("failed to create bootstrap M2M client: %v", err))
				}
			} else {
				// Generate random secret
				var err error
				secret, err = authSvc.M2MClientStore().Create(
					cfg.Bootstrap.ClientID,
					cfg.Bootstrap.TenantID,
					cfg.Bootstrap.UserID,
					roles,
				)
				if err != nil {
					panic(fmt.Sprintf("failed to create bootstrap M2M client: %v", err))
				}
			}
			maskedSecret := secret
			if len(maskedSecret) > 8 {
				maskedSecret = maskedSecret[:8] + "..."
			}
			slog.Info("bootstrap M2M client created",
				"clientId", cfg.Bootstrap.ClientID,
				"tenantId", cfg.Bootstrap.TenantID,
				"roles", roles,
			)
			fmt.Fprintf(os.Stderr, "  Client Secret: %s (shown once, store securely)\n", maskedSecret)
			fmt.Fprintf(os.Stderr, "  Get token: curl -s -X POST http://localhost:%d/%s/oauth/token \\\n", cfg.HTTPPort, strings.TrimLeft(cfg.ContextPath, "/"))
			fmt.Fprintf(os.Stderr, "    -u \"%s:%s\" \\\n", cfg.Bootstrap.ClientID, maskedSecret)
			fmt.Fprintf(os.Stderr, "    -d \"grant_type=client_credentials\"\n\n")
		}
	} else {
		defaultUser := &spi.UserContext{
			UserID:   cfg.IAM.MockUserID,
			UserName: cfg.IAM.MockUserName,
			Tenant: spi.Tenant{
				ID:   spi.TenantID(cfg.IAM.MockTenantID),
				Name: cfg.IAM.MockTenantName,
			},
			Roles: cfg.IAM.MockRoles,
		}
		a.authService = mockiam.NewAuthenticationService(defaultUser)
	}
	a.authzService = mockiam.NewAuthorizationService()

	a.memberRegistry = internalgrpc.NewMemberRegistry()
	localDispatcher := internalgrpc.NewProcessorDispatcher(a.memberRegistry, common.NewDefaultUUIDGenerator())
	searchStore, err := a.storeFactory.AsyncSearchStore(context.Background())
	if err != nil {
		panic(fmt.Sprintf("failed to get async search store: %v", err))
	}
	a.searchService = search.NewSearchService(a.storeFactory, common.NewDefaultUUIDGenerator(), searchStore)

	// Search snapshot TTL reaper (uses stopSearchReaper for graceful shutdown)
	a.stopSearchReaper = make(chan struct{})
	go func() {
		ticker := time.NewTicker(cfg.SearchReapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				reaped, err := searchStore.ReapExpired(context.Background(), cfg.SearchSnapshotTTL)
				if err != nil {
					slog.Error("search snapshot reaper error", "pkg", "search", "err", err)
				} else if reaped > 0 {
					slog.Info("reaped expired search snapshots", "pkg", "search", "count", reaped)
				}
			case <-a.stopSearchReaper:
				return
			}
		}
	}()

	a.auditService = skeleton.NewAuditService()
	a.clusterService = internalgrpc.NewClusterService(a.memberRegistry)

	// Cluster components
	a.txLifecycle = lifecycle.NewManager(cfg.Cluster.OutcomeTTL)
	// Wire the TM so the TTL reaper can roll back the underlying transaction
	// when a cluster-level timeout fires; otherwise the plugin's physical
	// handle is orphaned until the database's own idle timeout catches it.
	a.txLifecycle.SetTransactionManager(a.transactionManager)
	if cfg.Cluster.Enabled {
		if cfg.Cluster.NodeID == "" {
			slog.Error("CYODA_NODE_ID is required when cluster mode is enabled", "pkg", "cluster")
			os.Exit(1)
		}
		if len(cfg.Cluster.HMACSecret) == 0 {
			slog.Error("CYODA_HMAC_SECRET is required when cluster mode is enabled", "pkg", "cluster")
			os.Exit(1)
		}
		if !strings.HasPrefix(cfg.Cluster.NodeAddr, "http://") && !strings.HasPrefix(cfg.Cluster.NodeAddr, "https://") {
			slog.Error("CYODA_NODE_ADDR must include scheme (http:// or https://)", "pkg", "cluster", "addr", cfg.Cluster.NodeAddr)
			os.Exit(1)
		}
		var signerErr error
		a.tokenSigner, signerErr = token.NewSigner(cfg.Cluster.HMACSecret)
		if signerErr != nil {
			slog.Error("failed to create token signer", "pkg", "cluster", "err", signerErr)
			os.Exit(1)
		}

		gossipHost, gossipPortStr, err := net.SplitHostPort(cfg.Cluster.GossipAddr)
		if err != nil {
			slog.Error("invalid CYODA_GOSSIP_ADDR", "pkg", "cluster", "addr", cfg.Cluster.GossipAddr, "err", err)
			os.Exit(1)
		}
		gossipPort, err := strconv.Atoi(gossipPortStr)
		if err != nil {
			slog.Error("invalid gossip port", "pkg", "cluster", "port", gossipPortStr, "err", err)
			os.Exit(1)
		}

		gossipReg, err := registry.NewGossip(registry.GossipConfig{
			NodeID:          cfg.Cluster.NodeID,
			NodeAddr:        cfg.Cluster.NodeAddr,
			BindAddr:        gossipHost,
			BindPort:        gossipPort,
			Seeds:           cfg.Cluster.SeedNodes,
			StabilityWindow: cfg.Cluster.StabilityWindow,
			SecretKey:       cfg.Cluster.HMACSecret,
		})
		if err != nil {
			slog.Error("failed to create gossip registry", "pkg", "cluster", "err", err)
			os.Exit(1)
		}
		if err := gossipReg.Register(context.Background(), cfg.Cluster.NodeID, cfg.Cluster.NodeAddr); err != nil {
			slog.Error("failed to register with gossip cluster", "pkg", "cluster", "err", err)
			os.Exit(1)
		}
		a.nodeRegistry = gossipReg

		slog.Info("cluster mode enabled", "pkg", "cluster", "nodeID", cfg.Cluster.NodeID, "gossipAddr", cfg.Cluster.GossipAddr)

		// Start TTL reaper goroutine with shutdown support
		a.stopReaper = make(chan struct{})
		go func() {
			ticker := time.NewTicker(cfg.Cluster.TxReapInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					reaped, err := a.txLifecycle.ReapExpired(context.Background())
					if err != nil {
						slog.Error("tx reaper error", "pkg", "cluster", "err", err)
					} else if reaped > 0 {
						slog.Info("reaped expired transactions", "pkg", "cluster", "count", reaped)
					}
				case <-a.stopReaper:
					return
				}
			}
		}()
	} else {
		a.nodeRegistry = registry.NewLocal("local", fmt.Sprintf("localhost:%d", cfg.HTTPPort))
	}

	// Wire external processing dispatcher
	var extProc contract.ExternalProcessingService
	if cfg.ExternalProcessing != nil {
		extProc = cfg.ExternalProcessing
	} else if cfg.Cluster.Enabled {
		extProc = clusterdispatch.NewClusterDispatcher(
			localDispatcher,
			a.nodeRegistry,
			cfg.Cluster.NodeID,
			clusterdispatch.NewRandomSelector(),
			clusterdispatch.NewHTTPForwarder(cfg.Cluster.HMACSecret, cfg.Cluster.DispatchForwardTimeout),
			cfg.Cluster.DispatchWaitTimeout,
		)
	} else {
		extProc = localDispatcher
	}
	if cfg.OTelEnabled {
		extProc = observability.NewTracingExternalProcessingService(extProc)
	}
	a.workflowEngine = workflow.NewEngine(a.storeFactory, common.NewDefaultUUIDGenerator(), a.transactionManager,
		workflow.WithExternalProcessing(extProc),
		workflow.WithMaxStateVisits(cfg.MaxStateVisits))

	// Wire MemberRegistry onChange to gossip tag updates
	if cfg.Cluster.Enabled {
		a.memberRegistry.SetOnChange(func(tags map[string][]string) {
			if gossipReg, ok := a.nodeRegistry.(*registry.Gossip); ok {
				if err := gossipReg.UpdateTags(tags); err != nil {
					slog.Error("failed to update gossip tags", "pkg", "cluster", "err", err)
				}
			}
		})
	}

	// Domain handlers
	entityHandler := entity.New(a.storeFactory, a.transactionManager, common.NewDefaultUUIDGenerator(), a.workflowEngine)
	modelHandler := model.New(a.storeFactory)
	server := internalapi.NewServer()
	server.Entity = entityHandler
	server.Model = modelHandler
	server.Workflow = workflow.New(a.storeFactory, a.workflowEngine)
	server.Search = search.NewHandler(a.searchService)
	server.Audit = audit.New(a.storeFactory)
	server.Messaging = messaging.New(a.storeFactory, common.NewDefaultUUIDGenerator())
	server.Account = account.New(a.authService, a.authzService)

	// Build HTTP handler
	mux := http.NewServeMux()

	healthFlag := &atomic.Bool{}
	healthFlag.Store(true)

	// Infrastructure routes (no auth, receives health flag)
	internalapi.RegisterHealthRoutes(mux, healthFlag)

	// Auth service routes: public endpoints (no auth needed)
	if authSvc != nil {
		mux.Handle("/.well-known/", authSvc.Handler())
		mux.Handle("POST /oauth/token", authSvc.Handler())
	}

	// Admin routes (with auth)
	authMW := middleware.Auth(a.authService)

	// Auth admin routes: key management, M2M clients, trusted keys (requires auth)
	if authSvc != nil {
		mux.Handle("/oauth/keys/", authMW(authSvc.AdminHandler()))
		mux.Handle("/account/m2m/", authMW(authSvc.AdminHandler()))
		mux.Handle("/account/m2m", authMW(authSvc.AdminHandler()))
	}
	mux.Handle("GET /admin/log-level", authMW(http.HandlerFunc(internalapi.HandleGetLogLevel)))
	mux.Handle("POST /admin/log-level", authMW(http.HandlerFunc(internalapi.HandleSetLogLevel)))
	mux.Handle("GET /admin/trace-sampler", authMW(http.HandlerFunc(internalapi.HandleGetTraceSampler)))
	mux.Handle("POST /admin/trace-sampler", authMW(http.HandlerFunc(internalapi.HandleSetTraceSampler)))

	// Entity transition routes (with auth, outside generated API mux)
	mux.Handle("GET /entity/{entityId}/transitions", authMW(http.HandlerFunc(entityHandler.HandleGetTransitions)))
	mux.Handle("GET /platform-api/entity/fetch/transitions", authMW(http.HandlerFunc(entityHandler.HandleFetchTransitions)))

	// Generated API routes (with recovery + auth) — uses chi to avoid ServeMux
	// wildcard-conflict panics in overlapping /model/… paths.
	apiHandler := genapi.HandlerFromMux(server, internalapi.NewChiMux())
	if cfg.OTelEnabled {
		apiHandler = otelhttp.NewMiddleware("cyoda-go")(apiHandler)
	}
	mux.Handle("/", middleware.Recovery(healthFlag)(
		middleware.Auth(a.authService)(apiHandler),
	))

	// Context path — wrap all routes under configurable prefix
	contextPath := strings.TrimRight(cfg.ContextPath, "/")
	if contextPath != "" {
		outerMux := http.NewServeMux()
		outerMux.Handle(contextPath+"/", http.StripPrefix(contextPath, mux))
		// Discovery routes at root (no auth, no context path)
		internalapi.RegisterDiscoveryRoutes(outerMux, contextPath)
		// Internal dispatch routes at root (HMAC-authenticated, not under context path)
		if cfg.Cluster.Enabled {
			dispatchHandler, dhErr := clusterdispatch.NewDispatchHandler(localDispatcher, cfg.Cluster.HMACSecret)
			if dhErr != nil {
				slog.Error("failed to create dispatch handler", "pkg", "cluster", "err", dhErr)
				os.Exit(1)
			}
			dispatchHandler.Register(outerMux)
		}
		a.handler = outerMux
	} else {
		// No context path — discovery routes on the main mux
		internalapi.RegisterDiscoveryRoutes(mux, "")
		// Internal dispatch routes (HMAC-authenticated)
		if cfg.Cluster.Enabled {
			dispatchHandler, dhErr := clusterdispatch.NewDispatchHandler(localDispatcher, cfg.Cluster.HMACSecret)
			if dhErr != nil {
				slog.Error("failed to create dispatch handler", "pkg", "cluster", "err", dhErr)
				os.Exit(1)
			}
			dispatchHandler.Register(mux)
		}
		a.handler = mux
	}

	// Cluster routing middleware — outermost layer, before auth and recovery.
	// The proxy forwards the original request including auth headers to the
	// target node, where auth is applied locally.
	if cfg.Cluster.Enabled {
		a.handler = proxy.HTTPRouting(a.tokenSigner, a.nodeRegistry, cfg.Cluster.NodeID, cfg.Cluster.ProxyTimeout)(a.handler)
	}

	// gRPC server — uses inner handler (without context path prefix)
	a.grpcServer = internalgrpc.NewServer(a.authService, a.memberRegistry, a.transactionManager, entityHandler, modelHandler, a.searchService, cfg.OTelEnabled)

	return a
}

func (a *App) Handler() http.Handler { return a.handler }

func (a *App) StoreFactory() spi.StoreFactory             { return a.storeFactory }
func (a *App) TransactionManager() spi.TransactionManager { return a.transactionManager }
func (a *App) AuthenticationService() contract.AuthenticationService {
	return a.authService
}
func (a *App) AuthorizationService() contract.AuthorizationService {
	return a.authzService
}
func (a *App) WorkflowEngine() *workflow.Engine             { return a.workflowEngine }
func (a *App) SearchService() *search.SearchService         { return a.searchService }
func (a *App) AuditService() contract.AuditService          { return a.auditService }
func (a *App) ClusterService() contract.ClusterService      { return a.clusterService }
func (a *App) GRPCServer() *internalgrpc.Server             { return a.grpcServer }
func (a *App) MemberRegistry() *internalgrpc.MemberRegistry { return a.memberRegistry }
func (a *App) TokenSigner() *token.Signer                   { return a.tokenSigner }
func (a *App) NodeRegistry() contract.NodeRegistry          { return a.nodeRegistry }
func (a *App) TxLifecycle() *lifecycle.Manager              { return a.txLifecycle }

// Close performs graceful shutdown of all backend resources.
func (a *App) Close() error {
	slog.Info("shutting down")
	// Close StoreFactory first so it can release connection pools before the
	// gRPC stop, which can block waiting on in-flight streams.
	var err error
	if a.storeFactory != nil {
		err = a.storeFactory.Close()
	}
	if a.grpcServer != nil {
		a.grpcServer.GRPCServer().Stop() // hard stop — process is exiting, no need for graceful drain
	}
	return err
}

// Shutdown performs graceful cleanup of background goroutines and cluster resources.
func (a *App) Shutdown() {
	if a.stopSearchReaper != nil {
		close(a.stopSearchReaper)
	}
	if a.stopReaper != nil {
		close(a.stopReaper)
	}
	if a.nodeRegistry != nil && a.config.Cluster.Enabled {
		if err := a.nodeRegistry.Deregister(context.Background(), a.config.Cluster.NodeID); err != nil {
			slog.Warn("failed to deregister from cluster", "pkg", "cluster", "err", err)
		}
	}
	if a.storeFactory != nil {
		if err := a.storeFactory.Close(); err != nil {
			slog.Warn("failed to close store factory", "pkg", "app", "err", err)
		}
	}
}
