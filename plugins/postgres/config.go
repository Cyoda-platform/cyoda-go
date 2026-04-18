package postgres

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// config holds parsed plugin configuration.
type config struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnIdleTime time.Duration
	AutoMigrate     bool
}

// parseConfig reads CYODA_POSTGRES_* env vars via the injected getenv.
// For CYODA_POSTGRES_URL, the _FILE suffix pattern is supported: if
// CYODA_POSTGRES_URL_FILE is set it takes precedence over CYODA_POSTGRES_URL.
func parseConfig(getenv func(string) string) (config, error) {
	url, err := resolveSecretWith(getenv, "CYODA_POSTGRES_URL")
	if err != nil {
		return config{}, err
	}
	cfg := config{
		URL:             url,
		MaxConns:        int32(envInt(getenv, "CYODA_POSTGRES_MAX_CONNS", 25)),
		MinConns:        int32(envInt(getenv, "CYODA_POSTGRES_MIN_CONNS", 5)),
		MaxConnIdleTime: envDuration(getenv, "CYODA_POSTGRES_MAX_CONN_IDLE_TIME", 5*time.Minute),
		AutoMigrate:     envBool(getenv, "CYODA_POSTGRES_AUTO_MIGRATE", true),
	}
	if cfg.URL == "" {
		return cfg, fmt.Errorf("CYODA_POSTGRES_URL is required")
	}
	return cfg, nil
}

// resolveSecretWith honours the _FILE suffix pattern using the injected getenv
// for the var name lookup, and os.ReadFile for the actual file read.
//
// Precedence: <name>_FILE wins if both are set. Trailing whitespace is trimmed.
// Returns an error if _FILE is set but the file cannot be read.
func resolveSecretWith(getenv func(string) string, name string) (string, error) {
	fileVar := name + "_FILE"
	if path := getenv(fileVar); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s=%q: %w", fileVar, path, err)
		}
		return strings.TrimRight(string(data), " \t\n\r"), nil
	}
	return getenv(name), nil
}

func envInt(getenv func(string) string, key string, dflt int) int {
	v := getenv(key)
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return dflt
	}
	return n
}

func envBool(getenv func(string) string, key string, dflt bool) bool {
	v := getenv(key)
	if v == "" {
		return dflt
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return dflt
	}
	return b
}

func envDuration(getenv func(string) string, key string, dflt time.Duration) time.Duration {
	v := getenv(key)
	if v == "" {
		return dflt
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return dflt
	}
	return d
}

// newPool creates the pgxpool using the plugin-scoped config.
func newPool(ctx context.Context, cfg config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres URL: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

// DBConfig is the exported config type retained for test-fixture callers.
// Production code in the plugin uses the internal config{} directly via
// parseConfig(getenv). Tests can construct a DBConfig, convert to config,
// and call NewPool as a thin wrapper.
type DBConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnIdleTime string
	AutoMigrate     bool
}

func (d DBConfig) toInternal() config {
	idle, _ := time.ParseDuration(d.MaxConnIdleTime)
	if idle == 0 {
		idle = 5 * time.Minute
	}
	return config{
		URL: d.URL, MaxConns: d.MaxConns, MinConns: d.MinConns,
		MaxConnIdleTime: idle, AutoMigrate: d.AutoMigrate,
	}
}

// NewPool is a test-fixture entry point that wraps the internal newPool.
func NewPool(ctx context.Context, cfg DBConfig) (*pgxpool.Pool, error) {
	return newPool(ctx, cfg.toInternal())
}
