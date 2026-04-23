package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/app"
	"github.com/cyoda-platform/cyoda-go/internal/admin"
	"github.com/cyoda-platform/cyoda-go/internal/logging"
	"github.com/cyoda-platform/cyoda-go/internal/observability"

	// Stock storage plugins — blank-imported so their init() runs
	// and they register themselves with the spi registry.
	_ "github.com/cyoda-platform/cyoda-go/plugins/memory"
	_ "github.com/cyoda-platform/cyoda-go/plugins/postgres"
	_ "github.com/cyoda-platform/cyoda-go/plugins/sqlite"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// printVersion writes a one-line parse-friendly version summary.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "cyoda version %s (commit %s, built %s)\n", version, commit, buildDate)
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h":
			printHelp()
			return
		case "--version", "-v":
			printVersion(os.Stdout)
			return
		case "init":
			os.Exit(runInit(os.Args[2:]))
		case "health":
			os.Exit(runHealth(os.Args[2:]))
		case "migrate":
			os.Exit(runMigrate(os.Args[2:]))
		}
	}

	app.LoadEnvFiles()
	cfg := app.DefaultConfig()
	logging.Init(cfg.LogLevel)

	if err := app.ValidateIAM(cfg.IAM); err != nil {
		slog.Error("IAM validation failed", "error", err)
		os.Exit(1)
	}

	printBanner(cfg)
	printMockAuthWarningTo(os.Stdout, cfg)

	if cfg.OTelEnabled {
		nodeID := cfg.Cluster.NodeID
		if nodeID == "" {
			nodeID = "standalone"
		}
		shutdown, err := observability.Init(context.Background(), "cyoda", nodeID)
		if err != nil {
			slog.Error("failed to initialize OTel", "error", err)
			os.Exit(1)
		}
		defer shutdown(context.Background())
	}

	a := app.New(cfg)

	// Ignore SIGPIPE: when piped through tee (./bin/cyoda | tee log),
	// Ctrl+C kills tee first, breaking the pipe. Go's default SIGPIPE behavior
	// for stdout writes is to exit immediately — before our SIGINT handler can
	// send LeaveGroup. Ignoring SIGPIPE lets the broken-pipe write fail silently
	// while the SIGINT handler runs the graceful shutdown.
	signal.Ignore(syscall.SIGPIPE)

	// Graceful shutdown: SIGINT (Ctrl+C) and SIGTERM trigger orderly teardown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start gRPC server
	grpcAddr := fmt.Sprintf(":%d", cfg.GRPC.Port)
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		slog.Error("gRPC listen failed", "error", err)
		os.Exit(1)
	}
	go func() {
		slog.Info("gRPC server starting", "addr", grpcAddr)
		if err := a.GRPCServer().Serve(lis); err != nil {
			slog.Error("gRPC server failed", "error", err)
		}
	}()

	// Start HTTP server
	httpAddr := fmt.Sprintf(":%d", cfg.HTTPPort)
	httpServer := &http.Server{Addr: httpAddr, Handler: a.Handler()}
	go func() {
		slog.Info("HTTP server starting", "addr", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
		}
	}()

	// Start admin listener. /livez and /readyz are unauth (kubelet has
	// no bearer); /metrics can optionally be bearer-gated via
	// CYODA_METRICS_BEARER — wired here from the validated config.
	adminAddr := fmt.Sprintf("%s:%d", cfg.Admin.BindAddress, cfg.Admin.Port)
	adminServer := &http.Server{
		Addr: adminAddr,
		Handler: admin.NewHandler(admin.Options{
			Readiness:          a.ReadinessCheck,
			MetricsBearerToken: cfg.Admin.MetricsBearerToken,
		}),
	}
	go func() {
		slog.Info("admin server starting", "addr", adminAddr)
		if err := adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("admin server failed", "error", err)
		}
	}()

	// Block until signal received.
	sig := <-sigCh
	slog.Info("received signal, starting graceful shutdown", "signal", sig)

	// Shut down HTTP server with a deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown failed", "error", err)
	}
	if err := adminServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("admin server shutdown failed", "error", err)
	}

	// Close app — releases backend resources (e.g. database pool).
	if err := a.Close(); err != nil {
		slog.Error("app shutdown failed", "error", err)
	}

	slog.Info("shutdown complete")
}

func printBanner(cfg app.Config) {
	printBannerTo(os.Stdout, cfg)
}

func printBannerTo(w io.Writer, cfg app.Config) {
	if os.Getenv("CYODA_SUPPRESS_BANNER") == "true" {
		return
	}

	teal := "\033[38;5;80m"
	reset := "\033[0m"

	// Disable color if not a terminal
	if f, ok := w.(*os.File); !ok {
		teal = ""
		reset = ""
	} else if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		teal = ""
		reset = ""
	}

	fmt.Fprintf(w, "%s", teal)
	fmt.Fprintln(w, `   ██████╗██╗   ██╗ ██████╗ ██████╗  █████╗`)
	fmt.Fprintln(w, `  ██╔════╝╚██╗ ██╔╝██╔═══██╗██╔══██╗██╔══██╗`)
	fmt.Fprintln(w, `  ██║      ╚████╔╝ ██║   ██║██║  ██║███████║`)
	fmt.Fprintln(w, `  ██║       ╚██╔╝  ██║   ██║██║  ██║██╔══██║`)
	fmt.Fprintln(w, `  ╚██████╗   ██║   ╚██████╔╝██████╔╝██║  ██║`)
	fmt.Fprintln(w, `   ╚═════╝   ╚═╝    ╚═════╝ ╚═════╝ ╚═╝  ╚═╝`)
	fmt.Fprintf(w, "%s", reset)
	fmt.Fprintf(w, "  Cyoda-Go %s (%s) built %s\n", version, commit, buildDate)
	fmt.Fprintf(w, "  HTTP :%d | gRPC :%d | IAM %s | Path %s | Profiles %s\n\n",
		cfg.HTTPPort, cfg.GRPC.Port, cfg.IAM.Mode, cfg.ContextPath, app.ProfileBanner())
}

// printMockAuthWarningTo is silent unless IAM mode is "mock". Respects
// CYODA_SUPPRESS_BANNER.
func printMockAuthWarningTo(w io.Writer, cfg app.Config) {
	if os.Getenv("CYODA_SUPPRESS_BANNER") == "true" {
		return
	}
	if cfg.IAM.Mode != "mock" {
		return
	}
	yellow := "\033[33m"
	reset := "\033[0m"
	if f, ok := w.(*os.File); !ok {
		yellow = ""
		reset = ""
	} else if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		yellow = ""
		reset = ""
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, yellow+"========================================================================"+reset)
	fmt.Fprintln(w, yellow+"  WARNING: MOCK AUTH IS ACTIVE"+reset)
	fmt.Fprintln(w, yellow+"  All requests are accepted without authentication."+reset)
	fmt.Fprintln(w, yellow+"  This instance MUST NOT be exposed to untrusted networks."+reset)
	fmt.Fprintln(w, yellow+"  Set CYODA_IAM_MODE=jwt and CYODA_JWT_SIGNING_KEY to enable real auth."+reset)
	fmt.Fprintln(w, yellow+"  Suppress this banner with CYODA_SUPPRESS_BANNER=true (CI/tests only)."+reset)
	fmt.Fprintln(w, yellow+"========================================================================"+reset)
	fmt.Fprintln(w)
}

func printStorageHelp() {
	fmt.Println("STORAGE")
	fmt.Println("  CYODA_STORAGE_BACKEND              Active storage plugin (default: memory)")
	fmt.Printf("                                     Available: %s\n", strings.Join(spi.RegisteredPlugins(), ", "))
	fmt.Println("  CYODA_STARTUP_TIMEOUT              Deadline for plugin.NewFactory and TM init (default: 30s)")
	fmt.Println("  CYODA_SCHEMA_SAVEPOINT_INTERVAL    Number of extensions between savepoint rows in the schema-extension")
	fmt.Println("                                     log. Honored by postgres, sqlite, cassandra plugins. Ignored by memory.")
	fmt.Println("                                     (default: 64)")
	fmt.Println("  CYODA_SCHEMA_EXTEND_MAX_RETRIES    Plugin-layer retry budget for ExtendSchema on backends with a native")
	fmt.Println("                                     conflict surface. Honored by sqlite (SQLITE_BUSY), cassandra (LWT).")
	fmt.Println("                                     Ignored by memory and postgres (no schema-write conflict surface).")
	fmt.Println("                                     (default: 8)")
	fmt.Println()

	for _, name := range spi.RegisteredPlugins() {
		p, _ := spi.GetPlugin(name)
		fmt.Printf("  [%s]\n", name)
		dp, ok := p.(spi.DescribablePlugin)
		if !ok {
			fmt.Println("  No configuration required.")
			fmt.Println()
			continue
		}
		vars := dp.ConfigVars()
		if len(vars) == 0 {
			fmt.Println("  No configuration required.")
			fmt.Println()
			continue
		}
		for _, v := range vars {
			tag := ""
			switch {
			case v.Required:
				tag = " (required)"
			case v.Default != "":
				tag = " (default: " + v.Default + ")"
			}
			fmt.Printf("  %-36s %s%s\n", v.Name, v.Description, tag)
		}
		fmt.Println()
	}
}

func printHelp() {
	fmt.Print(`Cyoda-Go — Lightweight digital twin of the Cyoda platform

Usage:
  cyoda [flags]           Run the server with current config.
  cyoda --help            Show this help.

Subcommands:
  cyoda init [--force]    Write a starter user config enabling sqlite.
  cyoda health            Probe /readyz on the admin listener (exits 0 if ready).
                          Uses CYODA_ADMIN_PORT, default 9091.
  cyoda migrate [--timeout <duration>]
                          Run schema migrations for the configured backend and exit.
                          Dispatches on CYODA_STORAGE_BACKEND:
                            memory  → no-op, exit 0
                            sqlite  → no-op, exit 0 (migrations are applied lazily)
                            postgres → run migrations, exit 0 on success, 1 on error
                          Refuses if the DB schema is newer than the code.
                          Exit codes: 0 success, 1 runtime error, 2 flag-parse error.
                          Default timeout: 5 minutes.
                          Used by the Helm chart pre-install/pre-upgrade Job.

All configuration is via environment variables. Variables can be placed in .env
files and loaded automatically using profiles.

PROFILES
  CYODA_PROFILES               Comma-separated profile names             (default: none)
                                    Loading order (later overrides earlier):
                                      1. .env            — base defaults
                                      2. .env.{profile}  — per-profile overrides
                                    Shell environment variables always win over file values.
                                    Example: CYODA_PROFILES=postgres,otel

SERVER
  CYODA_HTTP_PORT              HTTP listen port                          (default: 8080)
  CYODA_GRPC_PORT              gRPC listen port                          (default: 9090)
  CYODA_ADMIN_PORT             Admin port for health and metrics         (default: 9091)
  CYODA_ADMIN_BIND_ADDRESS     Admin listener bind address               (default: 127.0.0.1)
  CYODA_METRICS_REQUIRE_AUTH   Require Bearer auth on /metrics           (default: false)
                                    Coupled with CYODA_METRICS_BEARER — startup fails
                                    if required but no bearer is set. /livez and /readyz
                                    stay unauthenticated regardless (kubelet carries no
                                    bearer). The Helm chart sets this to true.
  CYODA_METRICS_BEARER         Static Bearer token for GET /metrics       (default: unset)
                                    Non-empty enables auth on /metrics with constant-time
                                    compare. Honors _FILE suffix.
  CYODA_CONTEXT_PATH           Context path prefix for all routes        (default: /api)
  CYODA_ERROR_RESPONSE_MODE    Error detail level: sanitized | verbose   (default: sanitized)
  CYODA_MAX_STATE_VISITS       Max visits per state in workflow cascade   (default: 10)
  CYODA_LOG_LEVEL              Log level: debug | info | warn | error    (default: info)
  CYODA_SUPPRESS_BANNER        Silence startup + mock-auth banners       (default: false)

`)
	printStorageHelp()
	fmt.Print(`AUTHENTICATION (IAM)
  CYODA_IAM_MODE               Auth mode: mock | jwt                     (default: mock)
  CYODA_REQUIRE_JWT            Refuse to start unless jwt mode + key set  (default: false)

  Mock mode (default):
    All requests authenticated as a configurable default user. No tokens needed.
    CYODA_IAM_MOCK_ROLES       Comma-separated default user roles        (default: ROLE_ADMIN,ROLE_M2M)

  JWT mode:
    CYODA_JWT_SIGNING_KEY      RSA private key (PEM). Required in jwt mode.
    CYODA_JWT_ISSUER           JWT issuer claim                          (default: cyoda)
    CYODA_JWT_AUDIENCE         Required aud claim on inbound JWTs        (default: unset, no check)
    CYODA_JWT_EXPIRY_SECONDS   Token lifetime in seconds                 (default: 3600)

BOOTSTRAP (jwt mode only)
  CYODA_BOOTSTRAP_CLIENT_ID    Bootstrap M2M client ID. Optional.
                                    Coupled with CYODA_BOOTSTRAP_CLIENT_SECRET: both must be set
                                    (bootstrap client created at startup) or both must be empty
                                    (no bootstrap client; operator authenticates via JWKS/external keys).
                                    Half-configured states are rejected at startup with a clear error.
                                    Ignored in mock mode.
  CYODA_BOOTSTRAP_CLIENT_SECRET  Bootstrap M2M client secret. Must be set if
                                    CYODA_BOOTSTRAP_CLIENT_ID is set (and vice
                                    versa); both unset = no bootstrap client.
                                    In mock mode, both are ignored.
  CYODA_BOOTSTRAP_TENANT_ID    Tenant for the bootstrap client            (default: default-tenant)
  CYODA_BOOTSTRAP_USER_ID      User ID for the bootstrap client           (default: admin)
  CYODA_BOOTSTRAP_ROLES        Comma-separated roles                      (default: ROLE_ADMIN,ROLE_M2M)

CREDENTIAL _FILE VARIANTS
  The following credential env vars accept a _FILE variant that reads the value from the
  file at the given path. _FILE takes precedence when both are set. Trailing whitespace
  is stripped — safe for DSN strings and multi-line PEM keys.
    CYODA_POSTGRES_URL_FILE           → file path for CYODA_POSTGRES_URL
    CYODA_JWT_SIGNING_KEY_FILE        → file path for CYODA_JWT_SIGNING_KEY
    CYODA_HMAC_SECRET_FILE            → file path for CYODA_HMAC_SECRET
    CYODA_BOOTSTRAP_CLIENT_SECRET_FILE → file path for CYODA_BOOTSTRAP_CLIENT_SECRET
    CYODA_METRICS_BEARER_FILE         → file path for CYODA_METRICS_BEARER
  This is the canonical Docker/Kubernetes pattern for wiring credentials from Secrets
  to the pod without exposing them in env output.

OBSERVABILITY
  CYODA_OTEL_ENABLED           Enable OpenTelemetry tracing/metrics      (default: false)
  OTEL_EXPORTER_OTLP_ENDPOINT       OTLP endpoint (standard OTel env var)     (default: http://localhost:4318)

gRPC EXTERNALIZED PROCESSING
  CYODA_KEEPALIVE_INTERVAL     Keep-alive send interval in seconds        (default: 10)
  CYODA_KEEPALIVE_TIMEOUT      Keep-alive timeout in seconds              (default: 30)

QUICK START (mock mode, in-memory)
  go run ./cmd/cyoda

QUICK START (with profiles)
  # Use the local profile (in-memory, mock auth, debug logging)
  CYODA_PROFILES=local go run ./cmd/cyoda

  # Combine profiles: postgres storage + observability
  CYODA_PROFILES=postgres,otel go run ./cmd/cyoda

QUICK START (jwt mode, PostgreSQL)
  # Generate a signing key
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out signing.pem

  # Create .env.jwt with your settings, then:
  CYODA_PROFILES=postgres,jwt \
  CYODA_JWT_SIGNING_KEY="$(cat signing.pem)" \
  go run ./cmd/cyoda

  # Get a token:
  TOKEN=$(curl -s -X POST http://localhost:8080/api/oauth/token \
    -u "my-app:my-secret" -d "grant_type=client_credentials" | jq -r .access_token)

  # Use it:
  curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/entity/stats

DOCKER
  ./scripts/dev/run-docker-dev.sh Generate .env.docker and start with docker compose

SHELL SCRIPT
  ./scripts/dev/run-local.sh      Run locally with CYODA_PROFILES=local
`)
}
