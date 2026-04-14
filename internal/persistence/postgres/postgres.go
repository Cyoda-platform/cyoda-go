package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DBConfig holds PostgreSQL connection pool settings.
// It is defined here to avoid an import cycle with the app package.
type DBConfig struct {
	URL             string
	MaxConns        int
	MinConns        int
	MaxConnIdleTime string
	AutoMigrate     bool
}

// NewPool creates a pgxpool.Pool from the given DBConfig.
// It validates connectivity before returning.
func NewPool(ctx context.Context, cfg DBConfig) (*pgxpool.Pool, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("CYODA_DB_URL is required for postgres backend")
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DB URL: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.MaxConns)
	poolCfg.MinConns = int32(cfg.MinConns)

	if cfg.MaxConnIdleTime != "" {
		d, err := time.ParseDuration(cfg.MaxConnIdleTime)
		if err != nil {
			return nil, fmt.Errorf("invalid MaxConnIdleTime %q: %w", cfg.MaxConnIdleTime, err)
		}
		poolCfg.MaxConnIdleTime = d
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Validate connectivity
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	return pool, nil
}
