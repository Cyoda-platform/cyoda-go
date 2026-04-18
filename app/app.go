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
	"github.com/cyoda-platform/cyoda-go/internal/cluster"
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
	// Validate and normalise bootstrap config before any auth wiring.
	validatedCfg, err := validateBootstrapConfig(&cfg)
	if err != nil {
		slog.Error("invalid bootstrap configuration", "pkg", "app", "err", err)
		os.Exit(1)
	}
	cfg = *validatedCfg

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

	// Cluster infrastructure the plugin factory may need (e.g. the cassandra
	// plugin uses the broadcaster for clock gossip) is created up-front when
	// cluster mode is on; the same instance is then bound as the app's node
	// registry later in this function. In single-node mode gossipReg stays
	// nil and plugins receive no broadcaster.
	var gossipReg *registry.Gossip
	if cfg.Cluster.Enabled {
		validateClusterConfig(cfg.Cluster)
		var signerErr error
		a.tokenSigner, signerErr = token.NewSigner(cfg.Cluster.HMACSecret)
		if signerErr != nil {
			slog.Error("failed to create token signer", "pkg", "cluster", "err", signerErr)
			os.Exit(1)
		}
		gossipReg = mustNewGossip(cfg.Cluster)
	}

	var factoryOpts []spi.FactoryOption
	if gossipReg != nil {
		factoryOpts = append(factoryOpts, spi.WithClusterBroadcaster(gossipReg))
	}

	// startupCtx carries a deadline so unreachable infrastructure fails fast
	// instead of hanging in pgxpool or gocql.
	startupCtx, cancel := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	defer cancel()

	factory, err := plugin.NewFactory(startupCtx, os.Getenv, factoryOpts...)
	if err != nil {
		panic(fmt.Sprintf("create storage factory for %s: %v", plugin.Name(), err))
	}
	a.storeFactory = factory

	// Startable plugins (cassandra, etc.) must complete Start BEFORE the
	// factory can serve TransactionManager: the initial takeover / shard-
	// rebalance / clock-cache warmup that Start drives is a precondition
	// for tx begin. Plugins with no background lifecycle (memory,
	// postgres) don't implement Startable, so this is a no-op for them.
	if s, ok := factory.(spi.Startable); ok {
		if err := s.Start(startupCtx); err != nil {
			panic(fmt.Sprintf("start storage factory for %s: %v", plugin.Name(), err))
		}
		slog.Info("storage plugin started", "pkg", "app", "backend", plugin.Name())
	}

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

		// Bootstrap M2M client if configured.
		// validateBootstrapConfig (called above) guarantees that in jwt mode,
		// ClientID and ClientSecret are coupled: both set or neither set.
		if cfg.Bootstrap.ClientID != "" {
			roles := strings.Split(cfg.Bootstrap.Roles, ",")
			for i := range roles {
				roles[i] = strings.TrimSpace(roles[i])
			}
			if err := authSvc.M2MClientStore().CreateWithSecret(
				cfg.Bootstrap.ClientID,
				cfg.Bootstrap.TenantID,
				cfg.Bootstrap.UserID,
				cfg.Bootstrap.ClientSecret,
				roles,
			); err != nil {
				panic(fmt.Sprintf("failed to create bootstrap M2M client: %v", err))
			}
			slog.Info("bootstrap M2M client registered",
				"pkg", "app",
				"clientId", cfg.Bootstrap.ClientID,
				"tenantId", cfg.Bootstrap.TenantID,
				"roles", roles,
			)
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
		// gossipReg was created above (before plugin.NewFactory) so the plugin
		// could subscribe to broadcast topics. Join the cluster now; subscribers
		// are already registered, so no messages are dropped.
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
		apiHandler = otelhttp.NewMiddleware("cyoda")(apiHandler)
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

// ReadinessCheck returns nil when the instance is ready to serve external
// traffic. Called synchronously by the /readyz admin endpoint on every
// probe — keep it cheap. By the time New() returns, the plugin factory
// has successfully opened connections and applied migrations (per the
// existing startup sequence), so a non-nil storeFactory is a sufficient
// readiness signal until the SPI gains a dedicated Ping method.
func (a *App) ReadinessCheck() error {
	if a.storeFactory == nil {
		return fmt.Errorf("storage not initialized")
	}
	return nil
}

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

// validateBootstrapConfig enforces bootstrap-secret policy:
//   - jwt mode: CYODA_BOOTSTRAP_CLIENT_SECRET is required (fatal startup error
//     if unset); the Helm chart always provides it via a Kubernetes Secret, so
//     auto-generation is never needed in a deployment context.
//   - mock mode: the secret is irrelevant; zero it to prevent accidental use.
//
// Returns a new Config with the policy applied, or an error the caller must
// surface as a fatal startup failure.
func validateBootstrapConfig(cfg *Config) (*Config, error) {
	out := *cfg
	if out.IAM.Mode != "jwt" {
		// Mock (or any non-jwt) mode: bootstrap is irrelevant. Zero the secret defensively so
		// downstream code can't accidentally use it.
		out.Bootstrap.ClientSecret = ""
		return &out, nil
	}
	idSet := out.Bootstrap.ClientID != ""
	secretSet := out.Bootstrap.ClientSecret != ""
	switch {
	case !idSet && !secretSet:
		// No bootstrap M2M client configured. System starts without one;
		// operator authenticates via JWKS / external signing keys.
		return &out, nil
	case idSet && secretSet:
		// Bootstrap M2M client configured. Creation happens in New().
		return &out, nil
	case idSet && !secretSet:
		return nil, fmt.Errorf(
			"CYODA_BOOTSTRAP_CLIENT_SECRET is required when CYODA_BOOTSTRAP_CLIENT_ID is set in jwt mode")
	default: // !idSet && secretSet
		return nil, fmt.Errorf(
			"CYODA_BOOTSTRAP_CLIENT_ID is required when CYODA_BOOTSTRAP_CLIENT_SECRET is set in jwt mode (secret would otherwise be unused)")
	}
}

// validateClusterConfig fails fast on missing/invalid cluster settings.
// Called before any cluster infrastructure is constructed so the failure
// surfaces at startup instead of during traffic.
func validateClusterConfig(c cluster.Config) {
	if c.NodeID == "" {
		slog.Error("CYODA_NODE_ID is required when cluster mode is enabled", "pkg", "cluster")
		os.Exit(1)
	}
	if len(c.HMACSecret) == 0 {
		slog.Error("CYODA_HMAC_SECRET is required when cluster mode is enabled", "pkg", "cluster")
		os.Exit(1)
	}
	if !strings.HasPrefix(c.NodeAddr, "http://") && !strings.HasPrefix(c.NodeAddr, "https://") {
		slog.Error("CYODA_NODE_ADDR must include scheme (http:// or https://)", "pkg", "cluster", "addr", c.NodeAddr)
		os.Exit(1)
	}
}

// mustNewGossip parses the gossip address, creates the memberlist-backed
// registry, and exits on any failure. Returns the registry so the caller
// can both (a) pass it to plugin.NewFactory as a broadcaster and (b) use
// it as the app's node registry after Register.
func mustNewGossip(c cluster.Config) *registry.Gossip {
	gossipHost, gossipPortStr, err := net.SplitHostPort(c.GossipAddr)
	if err != nil {
		slog.Error("invalid CYODA_GOSSIP_ADDR", "pkg", "cluster", "addr", c.GossipAddr, "err", err)
		os.Exit(1)
	}
	gossipPort, err := strconv.Atoi(gossipPortStr)
	if err != nil {
		slog.Error("invalid gossip port", "pkg", "cluster", "port", gossipPortStr, "err", err)
		os.Exit(1)
	}
	g, err := registry.NewGossip(registry.GossipConfig{
		NodeID:          c.NodeID,
		NodeAddr:        c.NodeAddr,
		BindAddr:        gossipHost,
		BindPort:        gossipPort,
		Seeds:           c.SeedNodes,
		StabilityWindow: c.StabilityWindow,
		SecretKey:       c.HMACSecret,
	})
	if err != nil {
		slog.Error("failed to create gossip registry", "pkg", "cluster", "err", err)
		os.Exit(1)
	}
	return g
}
