package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
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
	defer func() {
		if sErr, dErr := m.Close(); sErr != nil || dErr != nil {
			slog.Warn("migrate close returned errors", "sourceErr", sErr, "driverErr", dErr)
		}
	}()

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

// dropSchema drops all application tables and the migration tracking table by
// running DROP SCHEMA CASCADE + CREATE SCHEMA. This is faster and more robust
// than MigrateDown for test cleanup because MigrateDown can fail when test
// data violates constraints introduced by a DOWN migration (e.g. duplicate
// primary-key values across tenants when reverting a composite-PK migration).
//
// dropSchema is intentionally unexported. Test code accesses it through
// DropSchemaForTest declared in export_test.go. This prevents production
// binaries from ever being able to call it, even by mistake (e.g. a
// misconfigured CYODA_TEST_DB_URL pointing at a production database).
//
// Before dropping the schema, dropSchema terminates all OTHER backends
// connected to the same database. This is necessary because DROP SCHEMA
// CASCADE requires an AccessExclusive lock on every object it drops, and idle
// connections from the main conformance pool can hold those locks open
// (preventing the DROP from proceeding) even after the pool's Close() has
// been called but before puddle has fully drained its wait-group.
func dropSchema(pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		return fmt.Errorf("acquire connection for dropSchema: %w", err)
	}
	defer conn.Release()

	// Terminate other backends so DROP SCHEMA can acquire its exclusive locks
	// without waiting. This is safe in test environments.
	_, _ = conn.Exec(context.Background(),
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND datname = current_database()")

	if _, err := conn.Exec(context.Background(),
		"DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public"); err != nil {
		return fmt.Errorf("dropSchema: %w", err)
	}
	return nil
}

// checkSchemaCompat enforces the schema-compatibility contract on startup:
//   - schema newer than code → fatal, regardless of autoMigrate
//   - schema older than code, autoMigrate=false → fatal
//   - schema older than code, autoMigrate=true → caller proceeds with runMigrations
//   - schema matches → proceed
//   - dirty state → fatal, manual intervention required
func checkSchemaCompat(ctx context.Context, db *sql.DB, autoMigrate bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("schema compat: context cancelled: %w", err)
	}
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("schema compat: create driver: %w", err)
	}
	src, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("schema compat: open migration source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("schema compat: create migrator: %w", err)
	}
	defer func() {
		if sErr, dErr := m.Close(); sErr != nil || dErr != nil {
			slog.Warn("migrate close returned errors", "sourceErr", sErr, "driverErr", dErr)
		}
	}()

	maxVersion, err := maxMigrationVersion(src)
	if err != nil {
		return fmt.Errorf("schema compat: scan embedded migrations: %w", err)
	}

	dbVersion, dirty, err := m.Version()
	switch {
	case errors.Is(err, migrate.ErrNilVersion):
		dbVersion = 0 // fresh database — treat as older-than-code
	case err != nil:
		return fmt.Errorf("schema compat: read DB version: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema compat: database migration state is dirty at version %d — manual intervention required", dbVersion)
	}

	switch {
	case dbVersion > maxVersion:
		return fmt.Errorf("schema compat: database schema version %d is newer than this binary's max migration version %d — refusing to start to avoid data corruption", dbVersion, maxVersion)
	case dbVersion < maxVersion && !autoMigrate:
		return fmt.Errorf("schema compat: database schema version %d is older than code (%d) and CYODA_POSTGRES_AUTO_MIGRATE=false — set CYODA_POSTGRES_AUTO_MIGRATE=true and restart, or apply migrations out-of-band", dbVersion, maxVersion)
	}
	return nil
}

// maxMigrationVersion walks the embedded migration source and returns
// the highest version present.
func maxMigrationVersion(src source.Driver) (uint, error) {
	v, err := src.First()
	if err != nil {
		return 0, fmt.Errorf("first migration: %w", err)
	}
	max := v
	for {
		next, err := src.Next(max)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("next migration after %d: %w", max, err)
		}
		max = next
	}
	return max, nil
}

// migrateDown rolls back all applied migrations. Intentionally unexported —
// exposed to test code only through MigrateDownForTest in export_test.go.
func migrateDown(pool *pgxpool.Pool) error {
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
	defer func() {
		if sErr, dErr := m.Close(); sErr != nil || dErr != nil {
			slog.Warn("migrate close returned errors", "sourceErr", sErr, "driverErr", dErr)
		}
	}()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("down: %w", err)
	}
	return nil
}
