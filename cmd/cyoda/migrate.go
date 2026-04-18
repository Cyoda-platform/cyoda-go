package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/cyoda-platform/cyoda-go/app"
	pgplugin "github.com/cyoda-platform/cyoda-go/plugins/postgres"
)

type migrateConfig struct {
	Timeout time.Duration
}

func parseMigrateArgs(args []string) (*migrateConfig, error) {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum duration for migration run")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return &migrateConfig{Timeout: *timeout}, nil
}

// runMigrate is the entry point for `cyoda migrate`. Returns exit code:
// 0 on success, non-zero on any error.
//
// Behavior:
//   - Loads the same config the server does (via app.DefaultConfig; honors
//     _FILE suffix resolution and every CYODA_* env var identically).
//   - Dispatches on CYODA_STORAGE_BACKEND:
//     memory  — no-op, exits 0
//     sqlite  — no-op (migrations applied lazily at open), exits 0
//     postgres — runs the plugin's migration logic
//     other   — exits 1 with "unknown storage backend"
//   - Respects the schema-compatibility contract: refuses to run if the
//     database schema is newer than code's embedded max version.
//   - Exits cleanly: no admin listener, no background loops, no lingering
//     goroutines. Short-lived process.
func runMigrate(args []string) int {
	cfg, err := parseMigrateArgs(args)
	if err != nil {
		// flag package already wrote the error to stderr
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	appCfg := app.DefaultConfig()

	switch appCfg.StorageBackend {
	case "memory":
		slog.Info("memory backend has no migrations — no-op")
		return 0
	case "sqlite":
		slog.Info("sqlite backend applies migrations lazily on first open — no-op for migrate subcommand")
		return 0
	case "postgres":
		return runPostgresMigrate(ctx)
	default:
		slog.Error("unknown storage backend", "backend", appCfg.StorageBackend)
		return 1
	}
}

func runPostgresMigrate(ctx context.Context) int {
	dsn := getPostgresDSN()

	start := time.Now()
	err := pgMigrate(ctx, dsn)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Error("migration timed out", "err", err)
			return 1
		}
		slog.Error("migration failed", "err", err)
		return 1
	}
	slog.Info("migrations applied", "duration", time.Since(start))
	return 0
}

// pgMigrate wraps the postgres plugin's migration entry point.
// Package-level var so tests can inject a fake if needed.
var pgMigrate = func(ctx context.Context, dsn string) error {
	return pgplugin.RunMigrateWithDSN(ctx, dsn)
}

// getPostgresDSN reads CYODA_POSTGRES_URL (or CYODA_POSTGRES_URL_FILE) using
// the same precedence logic as the plugin: _FILE wins when both are set.
// The DSN value is never logged to avoid leaking credentials.
func getPostgresDSN() string {
	if p := os.Getenv("CYODA_POSTGRES_URL_FILE"); p != "" {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
		fmt.Fprintf(os.Stderr, "cyoda migrate: failed to read CYODA_POSTGRES_URL_FILE=%q: %v\n", p, err)
	}
	return os.Getenv("CYODA_POSTGRES_URL")
}
