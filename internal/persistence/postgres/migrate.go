package postgres

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// openDB creates an independent *sql.DB from the pool's config.
// Unlike stdlib.OpenDBFromPool, this does not share the pool's connections,
// so db.Close() is safe and does not interfere with the pool.
func openDB(pool *pgxpool.Pool) *sql.DB {
	return stdlib.OpenDB(*pool.Config().ConnConfig)
}

// Migrate applies all pending migrations to the database.
// Returns nil if already up to date.
func Migrate(pool *pgxpool.Pool) error {
	db := openDB(pool)
	defer db.Close()

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migration failed: %w", err)
	}

	return nil
}

// MigrateDown rolls back all migrations. Used in tests for cleanup.
func MigrateDown(pool *pgxpool.Pool) error {
	db := openDB(pool)
	defer db.Close()

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migration rollback failed: %w", err)
	}

	return nil
}
