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
	"syscall"
	"time"

	"github.com/muesli/termenv"
	"golang.org/x/term"

	"github.com/cyoda-platform/cyoda-go/app"
	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help"
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

// runHelpCmd is the entry point for `cyoda help [args...]`.
func runHelpCmd(args []string) int {
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	style := ""
	if isTTY {
		if termenv.NewOutput(os.Stdout).HasDarkBackground() {
			style = "dark"
		} else {
			style = "light"
		}
	}
	return help.RunHelp(help.DefaultTree, args, os.Stdout, version, isTTY, style)
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h":
			// Delegate to the help subsystem so there is a single source
			// of truth. No positional args → writeTreeSummary with USAGE +
			// FLAGS + TOPICS block. Users can still run 'cyoda help cli'
			// for the full CLI reference.
			os.Exit(runHelpCmd(nil))
		case "--version", "-v":
			printVersion(os.Stdout)
			return
		case "help":
			os.Exit(runHelpCmd(os.Args[2:]))
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
	cfg.Version = version
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

