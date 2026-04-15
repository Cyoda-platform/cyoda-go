package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
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
func openDB(pool *pgxpool.Pool) *sql.DB {
	return stdlib.OpenDB(*pool.Config().ConnConfig)
}

// runMigrations applies pending migrations. Uses m.GracefulStop to honor
// context cancellation at migration-step boundaries (golang-migrate's
// m.Up() itself takes no context).
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	db := openDB(pool)
	defer db.Close()

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		err := m.Up()
		if errors.Is(err, migrate.ErrNoChange) {
			err = nil
		}
		done <- err
	}()

	select {
	case <-ctx.Done():
		select {
		case m.GracefulStop <- true:
		default:
		}
		<-done // wait for the goroutine to exit so we don't leak it
		return fmt.Errorf("postgres migrate: %w", ctx.Err())
	case err := <-done:
		return err
	}
}

// Migrate preserves the existing exported API for test fixtures.
func Migrate(pool *pgxpool.Pool) error {
	return runMigrations(context.Background(), pool)
}

// MigrateDown is preserved for test cleanup.
func MigrateDown(pool *pgxpool.Pool) error {
	db := openDB(pool)
	defer db.Close()

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("down: %w", err)
	}
	return nil
}
