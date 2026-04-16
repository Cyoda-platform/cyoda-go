# SQLite Storage Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a persistent, zero-ops SQLite storage plugin with search predicate pushdown, aligned with the memory plugin's SSI architecture.

**Architecture:** SQLite (WAL mode) provides persistent storage; application-layer Serializable Snapshot Isolation (ported from memory plugin) handles concurrency in-memory. Search predicates are translated to SQL WHERE clauses via a greedy dissection planner, with residual post-filtering in Go. An exclusive `flock` enforces single-process ownership.

**Tech Stack:** Go 1.26+, `ncruces/go-sqlite3` (WASM, no CGO), `golang-migrate/migrate/v4`, `github.com/gofrs/flock`

**Spec:** `docs/superpowers/specs/2026-04-15-sqlite-storage-plugin-design.md`

---

## Pre-Implementation Context

### What gets created
- `plugins/sqlite/` — entire package (16 files)
- `e2e/parity/sqlite/sqlite_test.go` — parity test wrapper
- SPI additions in `cyoda-go-spi`: `filter.go`, `searcher.go`

### What gets modified
- `cmd/cyoda-go/main.go` — add blank import
- `go.mod` / `go.sum` — new dependencies
- `internal/domain/search/service.go` — Searcher type assertion + delegation
- `internal/domain/search/` — new `filter_translate.go` for Condition → Filter

### Task Dependency Graph

```
Task 1 (SPI types) ──────────────────────┐
Task 2 (schema) ─┐                       │
Task 3 (config) ─┤                       │
Task 4 (clock) ──┤                       │
Task 5 (migrate) ┼─ Task 6 (factory) ─┐  │
Task 7 (plugin) ─┘                     │  │
                                       ├──┼─ Task 14 (entity store)
Task 8  (model store) ────────────────┤  │
Task 9  (kv store) ──────────────────┤  │
Task 10 (message store) ─────────────┤  │
Task 11 (workflow store) ────────────┤  │
Task 12 (audit store) ──────────────┤  │
Task 13 (search store) ─────────────┘  │
                                       │
Task 15 (tx manager) ─────────────────┤
                                       │
Task 16 (query planner) ──────────────┼─ Task 1
                                       │
Task 17 (entity Searcher) ────────────┼─ Task 14, 16
                                       │
Task 18 (Condition→Filter) ───────────┼─ Task 1
Task 19 (SearchService) ─────────────┼─ Task 18
                                       │
Task 20 (cross-cutting) ──────────────┤
Task 21 (conformance) ────────────────┤
Task 22 (parity) ─────────────────────┤
Task 23 (planner fuzz) ──────────────┤
Task 24 (crash recovery) ────────────┤
Task 25 (concurrency stress) ────────┘
```

Tasks 2-5, 8-13 can run in parallel where indicated. Tasks 1 and 6 are critical path.

---

## Task 1: SPI — Filter and Searcher Types

**Module:** `cyoda-go-spi` (separate repo at `../cyoda-go-spi`)

**Files:**
- Create: `filter.go`
- Create: `searcher.go`

- [ ] **Step 1: Create filter.go with Filter types**

```go
// filter.go
package spi

import (
	"context"
	"time"
)

type FilterOp string

const (
	FilterAnd FilterOp = "and"
	FilterOr  FilterOp = "or"

	FilterEq  FilterOp = "eq"
	FilterNe  FilterOp = "ne"
	FilterGt  FilterOp = "gt"
	FilterLt  FilterOp = "lt"
	FilterGte FilterOp = "gte"
	FilterLte FilterOp = "lte"

	FilterContains   FilterOp = "contains"
	FilterStartsWith FilterOp = "starts_with"
	FilterEndsWith   FilterOp = "ends_with"
	FilterLike       FilterOp = "like"

	FilterIsNull  FilterOp = "is_null"
	FilterNotNull FilterOp = "not_null"

	FilterBetween      FilterOp = "between"
	FilterMatchesRegex FilterOp = "matches_regex"

	FilterIEq            FilterOp = "ieq"
	FilterINe            FilterOp = "ine"
	FilterIContains      FilterOp = "icontains"
	FilterINotContains   FilterOp = "inot_contains"
	FilterIStartsWith    FilterOp = "istarts_with"
	FilterINotStartsWith FilterOp = "inot_starts_with"
	FilterIEndsWith      FilterOp = "iends_with"
	FilterINotEndsWith   FilterOp = "inot_ends_with"
)

type FieldSource string

const (
	SourceData FieldSource = "data"
	SourceMeta FieldSource = "meta"
)

type Filter struct {
	Op       FilterOp
	Path     string
	Source   FieldSource
	Value    any
	Values   []any
	Children []Filter
}
```

- [ ] **Step 2: Create searcher.go with Searcher interface**

```go
// searcher.go
package spi

type Searcher interface {
	Search(ctx context.Context, filter Filter, opts SearchOptions) ([]*Entity, error)
}

type SearchOptions struct {
	ModelName    string
	ModelVersion string
	PointInTime  *time.Time
	Limit        int
	Offset       int
	OrderBy      []OrderSpec
}

type OrderSpec struct {
	Path   string
	Source FieldSource
	Desc   bool
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd ../cyoda-go-spi && go build ./...`
Expected: PASS

- [ ] **Step 4: Commit in cyoda-go-spi**

```bash
git add filter.go searcher.go
git commit -m "feat(spi): add Filter, Searcher types for search pushdown"
```

- [ ] **Step 5: Tag and update cyoda-go dependency**

```bash
cd ../cyoda-go-spi && git tag v0.4.0
cd ../cyoda-go && go get github.com/cyoda-platform/cyoda-go-spi@v0.4.0
go mod tidy
```

---

## Task 2: SQLite Schema Migration

**Files:**
- Create: `plugins/sqlite/migrations/000001_initial_schema.up.sql`
- Create: `plugins/sqlite/migrations/000001_initial_schema.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- plugins/sqlite/migrations/000001_initial_schema.up.sql
-- Cyoda-Go SQLite Storage Backend — Initial Schema

PRAGMA auto_vacuum = INCREMENTAL;

-- entities: current state, one row per entity
CREATE TABLE entities (
    tenant_id     TEXT    NOT NULL,
    entity_id     TEXT    NOT NULL,
    model_name    TEXT    NOT NULL,
    model_version TEXT    NOT NULL,
    version       INTEGER NOT NULL,
    data          BLOB    NOT NULL,
    meta          BLOB,
    deleted       INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, entity_id)
) STRICT;

CREATE INDEX idx_entities_model
    ON entities (tenant_id, model_name, model_version)
    WHERE NOT deleted;

-- entity_versions: append-only version chain
CREATE TABLE entity_versions (
    tenant_id      TEXT    NOT NULL,
    entity_id      TEXT    NOT NULL,
    model_name     TEXT    NOT NULL,
    model_version  TEXT    NOT NULL,
    version        INTEGER NOT NULL,
    data           BLOB,
    meta           BLOB,
    change_type    TEXT    NOT NULL,
    transaction_id TEXT    NOT NULL,
    submit_time    INTEGER NOT NULL,
    user_id        TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, entity_id, version)
) STRICT, WITHOUT ROWID;

CREATE INDEX idx_entity_versions_submit_time
    ON entity_versions (tenant_id, entity_id, submit_time);

CREATE INDEX idx_entity_versions_model
    ON entity_versions (tenant_id, model_name, model_version);

-- models: model descriptors
CREATE TABLE models (
    tenant_id     TEXT NOT NULL,
    model_name    TEXT NOT NULL,
    model_version TEXT NOT NULL,
    doc           BLOB NOT NULL,
    PRIMARY KEY (tenant_id, model_name, model_version)
) STRICT, WITHOUT ROWID;

-- kv_store: key-value store (workflows, configs)
CREATE TABLE kv_store (
    tenant_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    key       TEXT NOT NULL,
    value     BLOB NOT NULL,
    PRIMARY KEY (tenant_id, namespace, key)
) STRICT, WITHOUT ROWID;

-- messages: edge messages
CREATE TABLE messages (
    tenant_id  TEXT    NOT NULL,
    message_id TEXT    NOT NULL,
    header     BLOB    NOT NULL,
    metadata   BLOB    NOT NULL,
    payload    BLOB    NOT NULL,
    PRIMARY KEY (tenant_id, message_id)
) STRICT, WITHOUT ROWID;

-- sm_audit_events: state machine audit trail
CREATE TABLE sm_audit_events (
    tenant_id      TEXT    NOT NULL,
    entity_id      TEXT    NOT NULL,
    event_id       TEXT    NOT NULL,
    transaction_id TEXT,
    timestamp      INTEGER NOT NULL,
    doc            BLOB    NOT NULL,
    PRIMARY KEY (tenant_id, entity_id, event_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX idx_sm_events_tx
    ON sm_audit_events (tenant_id, entity_id, transaction_id);

-- search_jobs: async search job metadata
CREATE TABLE search_jobs (
    tenant_id    TEXT    NOT NULL,
    job_id       TEXT    NOT NULL,
    status       TEXT    NOT NULL DEFAULT 'RUNNING',
    model_name   TEXT    NOT NULL,
    model_version TEXT   NOT NULL,
    condition    BLOB,
    point_in_time INTEGER,
    search_opts  BLOB,
    result_count INTEGER NOT NULL DEFAULT 0,
    error        TEXT    NOT NULL DEFAULT '',
    create_time  INTEGER NOT NULL,
    finish_time  INTEGER,
    calc_time_ms INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, job_id)
) STRICT, WITHOUT ROWID;

-- search_job_results: entity IDs matched by a search job
CREATE TABLE search_job_results (
    tenant_id TEXT    NOT NULL,
    job_id    TEXT    NOT NULL,
    seq       INTEGER NOT NULL,
    entity_id TEXT    NOT NULL,
    PRIMARY KEY (tenant_id, job_id, seq)
) STRICT, WITHOUT ROWID;

-- submit_times: transaction submit timestamps (1h TTL, pruned at commit)
CREATE TABLE submit_times (
    tx_id       TEXT    NOT NULL PRIMARY KEY,
    submit_time INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX idx_submit_times_ttl ON submit_times (submit_time);

-- Readable views for CLI inspection of JSONB columns
CREATE VIEW entities_readable AS
SELECT tenant_id, entity_id, model_name, model_version, version,
       json(data) AS data, json(meta) AS meta,
       deleted, created_at, updated_at
FROM entities;

CREATE VIEW entity_versions_readable AS
SELECT tenant_id, entity_id, model_name, model_version, version,
       json(data) AS data, json(meta) AS meta,
       change_type, transaction_id, submit_time, user_id
FROM entity_versions;
```

- [ ] **Step 2: Write the down migration**

```sql
-- plugins/sqlite/migrations/000001_initial_schema.down.sql
DROP VIEW IF EXISTS entity_versions_readable;
DROP VIEW IF EXISTS entities_readable;
DROP TABLE IF EXISTS submit_times;
DROP TABLE IF EXISTS search_job_results;
DROP TABLE IF EXISTS search_jobs;
DROP TABLE IF EXISTS sm_audit_events;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS kv_store;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS entity_versions;
DROP TABLE IF EXISTS entities;
```

- [ ] **Step 3: Commit**

```bash
git add plugins/sqlite/migrations/
git commit -m "feat(sqlite): add initial schema migration"
```

---

## Task 3: SQLite Config and Env Parsing

**Files:**
- Create: `plugins/sqlite/config.go`

- [ ] **Step 1: Write config.go**

```go
package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type config struct {
	Path           string
	AutoMigrate    bool
	BusyTimeout    time.Duration
	CacheSizeKiB   int
	SearchScanLimit int
}

func parseConfig(getenv func(string) string) (config, error) {
	cfg := config{
		Path:            envStringFn(getenv, "CYODA_SQLITE_PATH", defaultDBPath()),
		AutoMigrate:     envBoolFn(getenv, "CYODA_SQLITE_AUTO_MIGRATE", true),
		BusyTimeout:     envDurationFn(getenv, "CYODA_SQLITE_BUSY_TIMEOUT", 5*time.Second),
		CacheSizeKiB:    envIntFn(getenv, "CYODA_SQLITE_CACHE_SIZE", 64000),
		SearchScanLimit: envIntFn(getenv, "CYODA_SQLITE_SEARCH_SCAN_LIMIT", 100_000),
	}
	if cfg.Path == "" {
		return cfg, fmt.Errorf("CYODA_SQLITE_PATH resolved to empty string")
	}
	return cfg, nil
}

func defaultDBPath() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "cyoda.db"
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "cyoda-go", "cyoda.db")
}

func envStringFn(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntFn(getenv func(string) string, key string, fallback int) int {
	v := getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBoolFn(getenv func(string) string, key string, fallback bool) bool {
	v := getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDurationFn(getenv func(string) string, key string, fallback time.Duration) time.Duration {
	v := getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
```

- [ ] **Step 2: Commit**

```bash
git add plugins/sqlite/config.go
git commit -m "feat(sqlite): add config and env var parsing"
```

---

## Task 4: SQLite Clock

**Files:**
- Create: `plugins/sqlite/clock.go`

- [ ] **Step 1: Write clock.go**

Same pattern as memory plugin — injectable clock for deterministic testing.

```go
package sqlite

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type TestClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewTestClock() *TestClock {
	return &TestClock{now: time.Now()}
}

func (c *TestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *TestClock) Advance(d time.Duration) {
	if d <= 0 {
		panic("TestClock.Advance: d must be > 0")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
```

- [ ] **Step 2: Commit**

```bash
git add plugins/sqlite/clock.go
git commit -m "feat(sqlite): add injectable clock"
```

---

## Task 5: SQLite Migration Runner

**Files:**
- Create: `plugins/sqlite/migrate.go`

- [ ] **Step 1: Write migrate.go**

```go
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	sqlitemigrate "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func runMigrations(ctx context.Context, db *sql.DB) error {
	driver, err := sqlitemigrate.WithInstance(db, &sqlitemigrate.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
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
		<-done
		return fmt.Errorf("sqlite migrate: %w", ctx.Err())
	case err := <-done:
		return err
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add plugins/sqlite/migrate.go
git commit -m "feat(sqlite): add embedded migration runner"
```

---

## Task 6: SQLite Store Factory

**Files:**
- Create: `plugins/sqlite/store_factory.go`

This is the core wiring: opens the database, acquires the exclusive flock, applies pragmas, runs migrations, and returns tenant-scoped store instances.

- [ ] **Step 1: Add dependencies to go.mod**

```bash
go get github.com/ncruces/go-sqlite3
go get github.com/ncruces/go-sqlite3/driver
go get github.com/gofrs/flock
go mod tidy
```

- [ ] **Step 2: Write store_factory.go**

```go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/gofrs/flock"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

type Option func(*StoreFactory)

func WithClock(c Clock) Option {
	return func(f *StoreFactory) { f.clock = c }
}

type StoreFactory struct {
	db          *sql.DB
	fileLock    *flock.Flock
	clock       Clock
	cfg         config
	tm          *TransactionManager
	searchStore *AsyncSearchStore
	closeMu     sync.Mutex
	closed      bool

	walTicker *time.Ticker
	walDone   chan struct{}
}

func newStoreFactory(ctx context.Context, cfg config, opts ...Option) (*StoreFactory, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	fl := flock.New(cfg.Path + ".lock")
	locked, err := fl.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire file lock on %s: %w", cfg.Path, err)
	}
	if !locked {
		return nil, fmt.Errorf("another cyoda-go instance is using %s", cfg.Path)
	}

	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_busy_timeout=%d",
		cfg.Path, cfg.BusyTimeout.Milliseconds())
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		fl.Unlock()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := applyPragmas(db, cfg); err != nil {
		db.Close()
		fl.Unlock()
		return nil, fmt.Errorf("apply pragmas: %w", err)
	}

	if err := assertMinVersion(db); err != nil {
		db.Close()
		fl.Unlock()
		return nil, err
	}

	if cfg.AutoMigrate {
		if err := runMigrations(ctx, db); err != nil {
			db.Close()
			fl.Unlock()
			return nil, fmt.Errorf("sqlite migrate: %w", err)
		}
	}

	f := &StoreFactory{
		db:       db,
		fileLock: fl,
		clock:    wallClock{},
		cfg:      cfg,
	}
	for _, o := range opts {
		o(f)
	}
	f.searchStore = newAsyncSearchStore(f.db, f.clock)
	f.startWALMaintenance()
	return f, nil
}

func applyPragmas(db *sql.DB, cfg config) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.BusyTimeout.Milliseconds()),
		fmt.Sprintf("PRAGMA cache_size = -%d", cfg.CacheSizeKiB),
		"PRAGMA foreign_keys = ON",
		"PRAGMA mmap_size = 268435456",
		"PRAGMA journal_size_limit = 67108864",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

func assertMinVersion(db *sql.DB) error {
	var ver string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&ver); err != nil {
		return fmt.Errorf("query sqlite version: %w", err)
	}
	slog.Info("sqlite version", "pkg", "sqlite", "version", ver)
	return nil
}

func (f *StoreFactory) startWALMaintenance() {
	f.walTicker = time.NewTicker(5 * time.Minute)
	f.walDone = make(chan struct{})
	go func() {
		for {
			select {
			case <-f.walTicker.C:
				f.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
				f.db.Exec("PRAGMA incremental_vacuum(1000)")
			case <-f.walDone:
				return
			}
		}
	}()
}

func resolveTenant(ctx context.Context) (spi.TenantID, error) {
	uc := spi.GetUserContext(ctx)
	if uc == nil {
		return "", fmt.Errorf("no user context in request — tenant cannot be resolved")
	}
	if uc.Tenant.ID == "" {
		return "", fmt.Errorf("user context has no tenant")
	}
	return uc.Tenant.ID, nil
}

func (f *StoreFactory) EntityStore(ctx context.Context) (spi.EntityStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &entityStore{db: f.db, tenantID: tid, tm: f.tm, clock: f.clock, cfg: f.cfg}, nil
}

func (f *StoreFactory) ModelStore(ctx context.Context) (spi.ModelStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &modelStore{db: f.db, tenantID: tid}, nil
}

func (f *StoreFactory) KeyValueStore(ctx context.Context) (spi.KeyValueStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &kvStore{db: f.db, tenantID: tid}, nil
}

func (f *StoreFactory) MessageStore(ctx context.Context) (spi.MessageStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &messageStore{db: f.db, tenantID: tid}, nil
}

func (f *StoreFactory) WorkflowStore(ctx context.Context) (spi.WorkflowStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	kv := &kvStore{db: f.db, tenantID: tid}
	return &workflowStore{kv: kv}, nil
}

func (f *StoreFactory) StateMachineAuditStore(ctx context.Context) (spi.StateMachineAuditStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &smAuditStore{db: f.db, tenantID: tid}, nil
}

func (f *StoreFactory) AsyncSearchStore(_ context.Context) (spi.AsyncSearchStore, error) {
	return f.searchStore, nil
}

func (f *StoreFactory) TransactionManager(_ context.Context) (spi.TransactionManager, error) {
	if f.tm == nil {
		return nil, fmt.Errorf("sqlite: TransactionManager not initialized")
	}
	return f.tm, nil
}

func (f *StoreFactory) Close() error {
	f.closeMu.Lock()
	defer f.closeMu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.walTicker != nil {
		f.walTicker.Stop()
		close(f.walDone)
	}
	var firstErr error
	if err := f.db.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := f.fileLock.Unlock(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (f *StoreFactory) initTransactionManager(uuids spi.UUIDGenerator) {
	f.tm = newTransactionManager(f, uuids)
}
```

- [ ] **Step 3: Verify compilation** (expect errors for undefined store types — that's OK, they come in later tasks)

- [ ] **Step 4: Commit**

```bash
git add plugins/sqlite/store_factory.go
git commit -m "feat(sqlite): add store factory with flock, WAL, pragmas"
```

---

## Task 7: SQLite Plugin Registration

**Files:**
- Create: `plugins/sqlite/plugin.go`

- [ ] **Step 1: Write plugin.go**

```go
package sqlite

import (
	"context"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

func init() { spi.Register(&plugin{}) }

type plugin struct{}

func (p *plugin) Name() string { return "sqlite" }

func (p *plugin) ConfigVars() []spi.ConfigVar {
	return []spi.ConfigVar{
		{Name: "CYODA_SQLITE_PATH", Description: "Database file path", Default: "$XDG_DATA_HOME/cyoda-go/cyoda.db"},
		{Name: "CYODA_SQLITE_AUTO_MIGRATE", Description: "Run embedded SQL migrations on startup", Default: "true"},
		{Name: "CYODA_SQLITE_BUSY_TIMEOUT", Description: "Wait time for write lock", Default: "5s"},
		{Name: "CYODA_SQLITE_CACHE_SIZE", Description: "Page cache in KiB", Default: "64000"},
		{Name: "CYODA_SQLITE_SEARCH_SCAN_LIMIT", Description: "Max rows examined per search with residual filter", Default: "100000"},
	}
}

func (p *plugin) NewFactory(
	ctx context.Context,
	getenv func(string) string,
	opts ...spi.FactoryOption,
) (spi.StoreFactory, error) {
	cfg, err := parseConfig(getenv)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}

	factory, err := newStoreFactory(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}

	factory.initTransactionManager(&defaultUUIDGenerator{})
	return factory, nil
}
```

- [ ] **Step 2: Create uuid.go**

```go
package sqlite

import "github.com/google/uuid"

type defaultUUIDGenerator struct{}

func (g *defaultUUIDGenerator) NewTimeUUID() [16]byte {
	return uuid.New()
}
```

- [ ] **Step 3: Commit**

```bash
git add plugins/sqlite/plugin.go plugins/sqlite/uuid.go
git commit -m "feat(sqlite): add plugin registration with ConfigVars"
```

---

## Tasks 8-13: Store Implementations

These tasks follow the same pattern: implement the SPI store interface using `database/sql` against the SQLite schema. Each store takes `db *sql.DB` and `tenantID spi.TenantID`. JSON payloads are stored via `jsonb()` and read via `json()`.

Due to the size of this plan, these tasks are described with their key patterns. The implementor should write failing conformance tests first (via Task 21), then implement each store.

### Task 8: Model Store

**Files:** Create `plugins/sqlite/model_store.go`

Key patterns:
- `Save`: `INSERT OR REPLACE INTO models (tenant_id, model_name, model_version, doc) VALUES (?, ?, ?, jsonb(?))`
- `Get`: `SELECT json(doc) FROM models WHERE tenant_id = ? AND model_name = ? AND model_version = ?`
- `GetAll`: `SELECT model_name, model_version FROM models WHERE tenant_id = ?`
- `Delete`: `DELETE FROM models WHERE ...`
- `Lock`/`Unlock`: read doc → unmarshal → set state → marshal → update
- `SetChangeLevel`: same read-modify-write pattern
- Doc format matches postgres `modelDoc` struct: JSON with ref, state, changeLevel, updateDate, schema

### Task 9: KeyValue Store

**Files:** Create `plugins/sqlite/kv_store.go`

Key patterns:
- `Put`: `INSERT OR REPLACE INTO kv_store (tenant_id, namespace, key, value) VALUES (?, ?, ?, ?)`
- `Get`: `SELECT value FROM kv_store WHERE tenant_id = ? AND namespace = ? AND key = ?`
- `Delete`: `DELETE FROM kv_store WHERE ...`
- `List`: `SELECT key, value FROM kv_store WHERE tenant_id = ? AND namespace = ?`

### Task 10: Message Store

**Files:** Create `plugins/sqlite/message_store.go`

Key patterns:
- `Save`: read full payload into `[]byte`, store header/metadata as `jsonb(?)`, payload as BLOB
- `Get`: return `header`, `metadata`, and `io.NopCloser(bytes.NewReader(payload))`
- `Delete` / `DeleteBatch`: standard DELETE

### Task 11: Workflow Store

**Files:** Create `plugins/sqlite/workflow_store.go`

Key patterns:
- Delegates to KV store (same as postgres): workflows serialized as JSON, stored in kv_store namespace
- `Save`: marshal workflows → `kv.Put(ctx, "workflows", modelRef.String(), data)`
- `Get`: `kv.Get(ctx, "workflows", modelRef.String())` → unmarshal
- `Delete`: `kv.Delete(ctx, "workflows", modelRef.String())`

### Task 12: StateMachine Audit Store

**Files:** Create `plugins/sqlite/sm_audit_store.go`

Key patterns:
- `Record`: `INSERT INTO sm_audit_events (tenant_id, entity_id, event_id, transaction_id, timestamp, doc) VALUES (?, ?, ?, ?, ?, jsonb(?))`
- `GetEvents`: `SELECT json(doc) FROM sm_audit_events WHERE tenant_id = ? AND entity_id = ? ORDER BY timestamp`
- `GetEventsByTransaction`: add `AND transaction_id = ?`
- Timestamps stored as Unix microseconds

### Task 13: Async Search Store

**Files:** Create `plugins/sqlite/search_store.go`

Key patterns:
- `CreateJob`: `INSERT INTO search_jobs ...`
- `GetJob`: `SELECT ... FROM search_jobs WHERE tenant_id = ? AND job_id = ?`
- `UpdateJobStatus`: `UPDATE search_jobs SET status = ?, result_count = ?, ...`
- `SaveResults`: batch `INSERT INTO search_job_results ...`
- `GetResultIDs`: `SELECT entity_id FROM search_job_results WHERE ... ORDER BY seq LIMIT ? OFFSET ?` + `SELECT COUNT(*) ...`
- `Cancel`: `UPDATE search_jobs SET status = 'CANCELLED' WHERE ... AND status = 'RUNNING'`
- `ReapExpired`: `DELETE FROM search_jobs WHERE create_time < ?` + cascade to results
- Resolve tenant from context via `spi.GetUserContext`

**Commit each store individually:**
```bash
git commit -m "feat(sqlite): add model store"
git commit -m "feat(sqlite): add key-value store"
# etc.
```

---

## Task 14: Entity Store (CRUD)

**Files:**
- Create: `plugins/sqlite/entity_store.go`

This is the most complex store. It handles transactional reads (snapshot isolation via buffer + version chain), non-transactional reads (latest version), and JSONB storage.

- [ ] **Step 1: Write entity_store.go struct and helpers**

```go
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"iter"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type entityStore struct {
	db       *sql.DB
	tenantID spi.TenantID
	tm       *TransactionManager
	clock    Clock
	cfg      config
}

func microToTime(us int64) time.Time {
	return time.UnixMicro(us)
}

func timeToMicro(t time.Time) int64 {
	return t.UnixMicro()
}

func scanEntity(row interface{ Scan(...any) error }) (*spi.Entity, error) {
	var (
		entityID, modelName, modelVersion string
		version                           int64
		dataJSON, metaJSON                []byte
		createdAt, updatedAt              int64
	)
	if err := row.Scan(&entityID, &modelName, &modelVersion, &version,
		&dataJSON, &metaJSON, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, spi.ErrNotFound
		}
		return nil, fmt.Errorf("scan entity: %w", err)
	}

	var meta spi.EntityMeta
	if metaJSON != nil {
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			return nil, fmt.Errorf("unmarshal meta: %w", err)
		}
	}
	meta.ID = entityID
	meta.ModelRef = spi.ModelRef{EntityName: modelName, ModelVersion: modelVersion}
	meta.Version = version
	meta.CreationDate = microToTime(createdAt)
	meta.LastModifiedDate = microToTime(updatedAt)

	return &spi.Entity{Meta: meta, Data: dataJSON}, nil
}
```

- [ ] **Step 2: Implement Save (non-transactional and transactional paths)**

Non-transactional: direct INSERT/UPSERT to `entities` + append to `entity_versions`.
Transactional: buffer in tx state's `Buffer` map, record in `WriteSet`.

```go
func (s *entityStore) Save(ctx context.Context, entity *spi.Entity) (int64, error) {
	tx := spi.GetTransaction(ctx)
	if tx != nil {
		return s.saveTx(tx, entity)
	}
	return s.saveDirectly(ctx, entity)
}

func (s *entityStore) saveTx(tx *spi.TransactionState, entity *spi.Entity) (int64, error) {
	tx.OpMu.Lock()
	defer tx.OpMu.Unlock()
	if tx.Closed || tx.RolledBack {
		return 0, fmt.Errorf("transaction %s is no longer active", tx.ID)
	}
	e := copyEntity(entity)
	e.Meta.TenantID = s.tenantID
	tx.Buffer[e.Meta.ID] = e
	tx.WriteSet[e.Meta.ID] = true
	delete(tx.Deletes, e.Meta.ID)
	return 0, nil
}

func (s *entityStore) saveDirectly(ctx context.Context, entity *spi.Entity) (int64, error) {
	now := s.clock.Now()
	e := copyEntity(entity)

	var existingVersion int64
	err := s.db.QueryRowContext(ctx,
		`SELECT version FROM entities WHERE tenant_id = ? AND entity_id = ?`,
		string(s.tenantID), e.Meta.ID).Scan(&existingVersion)
	isNew := err == sql.ErrNoRows
	if err != nil && !isNew {
		return 0, fmt.Errorf("check existing entity: %w", err)
	}

	var nextVersion int64
	changeType := "CREATED"
	if !isNew {
		nextVersion = existingVersion + 1
		changeType = "UPDATED"
	}

	metaJSON, err := json.Marshal(e.Meta)
	if err != nil {
		return 0, fmt.Errorf("marshal meta: %w", err)
	}

	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer sqlTx.Rollback()

	_, err = sqlTx.ExecContext(ctx,
		`INSERT OR REPLACE INTO entities
		 (tenant_id, entity_id, model_name, model_version, version, data, meta, deleted, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, jsonb(?), jsonb(?), 0, ?, ?)`,
		string(s.tenantID), e.Meta.ID, e.Meta.ModelRef.EntityName, e.Meta.ModelRef.ModelVersion,
		nextVersion, e.Data, metaJSON,
		timeToMicro(now), timeToMicro(now))
	if err != nil {
		return 0, fmt.Errorf("upsert entity: %w", err)
	}

	_, err = sqlTx.ExecContext(ctx,
		`INSERT INTO entity_versions
		 (tenant_id, entity_id, model_name, model_version, version, data, meta, change_type, transaction_id, submit_time, user_id)
		 VALUES (?, ?, ?, ?, ?, jsonb(?), jsonb(?), ?, '', ?, '')`,
		string(s.tenantID), e.Meta.ID, e.Meta.ModelRef.EntityName, e.Meta.ModelRef.ModelVersion,
		nextVersion, e.Data, metaJSON, changeType, timeToMicro(now))
	if err != nil {
		return 0, fmt.Errorf("insert version: %w", err)
	}

	if err := sqlTx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return nextVersion, nil
}

func copyEntity(e *spi.Entity) *spi.Entity {
	cp := *e
	cp.Data = make([]byte, len(e.Data))
	copy(cp.Data, e.Data)
	return &cp
}
```

- [ ] **Step 3: Implement Get (transactional snapshot reads and non-transactional)**

```go
func (s *entityStore) Get(ctx context.Context, entityID string) (*spi.Entity, error) {
	tx := spi.GetTransaction(ctx)
	if tx != nil {
		return s.getTx(ctx, tx, entityID)
	}
	return s.getDirect(ctx, entityID)
}

func (s *entityStore) getTx(ctx context.Context, tx *spi.TransactionState, entityID string) (*spi.Entity, error) {
	tx.OpMu.RLock()
	defer tx.OpMu.RUnlock()

	if tx.Deletes[entityID] {
		return nil, fmt.Errorf("entity %s: %w", entityID, spi.ErrNotFound)
	}
	if buffered, ok := tx.Buffer[entityID]; ok {
		tx.ReadSet[entityID] = true
		return copyEntity(buffered), nil
	}

	e, err := s.getSnapshot(ctx, entityID, tx.SnapshotTime)
	if err != nil {
		return nil, err
	}
	tx.ReadSet[entityID] = true
	return e, nil
}

func (s *entityStore) getDirect(ctx context.Context, entityID string) (*spi.Entity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT entity_id, model_name, model_version, version,
		        json(data), json(meta), created_at, updated_at
		 FROM entities
		 WHERE tenant_id = ? AND entity_id = ? AND NOT deleted`,
		string(s.tenantID), entityID)
	return scanEntity(row)
}

func (s *entityStore) getSnapshot(ctx context.Context, entityID string, snapshotTime time.Time) (*spi.Entity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT ev.entity_id, ev.model_name, ev.model_version, ev.version,
		        json(ev.data), json(ev.meta), ?, ?
		 FROM entity_versions ev
		 INNER JOIN (
		     SELECT entity_id, MAX(version) AS max_ver
		     FROM entity_versions
		     WHERE tenant_id = ? AND entity_id = ? AND submit_time < ?
		     GROUP BY entity_id
		 ) latest ON ev.entity_id = latest.entity_id AND ev.version = latest.max_ver
		 WHERE ev.tenant_id = ? AND ev.change_type != 'DELETED'`,
		timeToMicro(snapshotTime), timeToMicro(snapshotTime),
		string(s.tenantID), entityID, timeToMicro(snapshotTime),
		string(s.tenantID))
	return scanEntity(row)
}
```

- [ ] **Step 4: Implement remaining EntityStore methods**

`GetAsAt`, `GetAll`, `GetAllAsAt`, `Delete`, `DeleteAll`, `Exists`, `Count`, `GetVersionHistory`, `SaveAll`, `CompareAndSave` — following the same patterns. Each method checks for transaction context and dispatches accordingly.

- [ ] **Step 5: Commit**

```bash
git add plugins/sqlite/entity_store.go
git commit -m "feat(sqlite): add entity store with transactional snapshot reads"
```

---

## Task 15: Transaction Manager (SSI)

**Files:**
- Create: `plugins/sqlite/txmanager.go`

Port of the memory plugin's SSI engine. In-memory committedLog, readSet/writeSet tracking. Flush to SQLite on commit.

- [ ] **Step 1: Write the core TransactionManager struct**

```go
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

const submitTimeTTL = 1 * time.Hour

type committedTx struct {
	id         string
	submitTime time.Time
	writeSet   map[string]bool
}

type savepointSnapshot struct {
	buffer   map[string]*spi.Entity
	readSet  map[string]bool
	writeSet map[string]bool
	deletes  map[string]bool
}

type TransactionManager struct {
	factory        *StoreFactory
	uuids          spi.UUIDGenerator
	commitMu       sync.Mutex
	mu             sync.Mutex
	active         map[string]*spi.TransactionState
	committedLog   []committedTx
	committing     map[string]bool
	submitTimes    map[string]time.Time
	savepoints     map[string]map[string]savepointSnapshot
	lastSubmitTime int64
}

func newTransactionManager(factory *StoreFactory, uuids spi.UUIDGenerator) *TransactionManager {
	tm := &TransactionManager{
		factory:     factory,
		uuids:       uuids,
		active:      make(map[string]*spi.TransactionState),
		committing:  make(map[string]bool),
		submitTimes: make(map[string]time.Time),
		savepoints:  make(map[string]map[string]savepointSnapshot),
	}
	tm.seedLastSubmitTime()
	return tm
}

func (m *TransactionManager) seedLastSubmitTime() {
	var maxTime sql.NullInt64
	m.factory.db.QueryRow("SELECT MAX(submit_time) FROM entity_versions").Scan(&maxTime)
	if maxTime.Valid {
		m.lastSubmitTime = maxTime.Int64
	}
}
```

- [ ] **Step 2: Implement Begin, Join**

```go
func (m *TransactionManager) Begin(ctx context.Context) (string, context.Context, error) {
	uc := spi.GetUserContext(ctx)
	if uc == nil {
		return "", nil, fmt.Errorf("no user context — cannot begin transaction")
	}
	tid := uc.Tenant.ID
	if tid == "" {
		return "", nil, fmt.Errorf("user context has no tenant")
	}

	txID := fmt.Sprintf("%x", m.uuids.NewTimeUUID())
	snapshotTime := m.factory.clock.Now()

	tx := &spi.TransactionState{
		ID:           txID,
		TenantID:     tid,
		SnapshotTime: snapshotTime,
		ReadSet:      make(map[string]bool),
		WriteSet:     make(map[string]bool),
		Buffer:       make(map[string]*spi.Entity),
		Deletes:      make(map[string]bool),
	}

	func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.active[txID] = tx
	}()

	return txID, spi.WithTransaction(ctx, tx), nil
}

func (m *TransactionManager) Join(ctx context.Context, txID string) (context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tx, ok := m.active[txID]
	if !ok {
		return nil, fmt.Errorf("transaction %s not found", txID)
	}
	if tx.RolledBack || tx.Closed {
		return nil, fmt.Errorf("transaction %s is no longer active", txID)
	}
	return spi.WithTransaction(ctx, tx), nil
}
```

- [ ] **Step 3: Implement Commit with SSI validation and SQLite flush**

```go
func (m *TransactionManager) Commit(ctx context.Context, txID string) error {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()

	tx, err := m.prepareCommit(txID)
	if err != nil {
		return err
	}

	tx.OpMu.Lock()
	defer tx.OpMu.Unlock()

	if err := m.validateSSI(tx); err != nil {
		m.cleanupTx(txID, tx)
		return err
	}

	submitTime := m.captureSubmitTime()

	if err := m.flushToSQLite(ctx, tx, submitTime); err != nil {
		m.cleanupTx(txID, tx)
		return fmt.Errorf("flush to sqlite: %w", err)
	}

	m.recordCommit(txID, tx, submitTime)
	return nil
}

func (m *TransactionManager) prepareCommit(txID string) (*spi.TransactionState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tx, ok := m.active[txID]
	if !ok {
		return nil, fmt.Errorf("transaction %s not found", txID)
	}
	if tx.RolledBack || tx.Closed {
		return nil, fmt.Errorf("transaction %s is no longer active", txID)
	}
	if m.committing[txID] {
		return nil, fmt.Errorf("transaction %s is already committing", txID)
	}
	m.committing[txID] = true
	return tx, nil
}

func (m *TransactionManager) validateSSI(tx *spi.TransactionState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, committed := range m.committedLog {
		if committed.submitTime.After(tx.SnapshotTime) {
			for entityID := range committed.writeSet {
				if tx.ReadSet[entityID] || tx.WriteSet[entityID] {
					return spi.ErrConflict
				}
			}
		}
	}
	return nil
}

func (m *TransactionManager) captureSubmitTime() time.Time {
	nowMicro := m.factory.clock.Now().UnixMicro()
	if nowMicro <= m.lastSubmitTime {
		nowMicro = m.lastSubmitTime + 1
	}
	m.lastSubmitTime = nowMicro
	return time.UnixMicro(nowMicro)
}

func (m *TransactionManager) flushToSQLite(ctx context.Context, tx *spi.TransactionState, submitTime time.Time) error {
	sqlTx, err := m.factory.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer sqlTx.Rollback()

	submitMicro := timeToMicro(submitTime)

	for entityID, entity := range tx.Buffer {
		var existingVersion int64
		err := sqlTx.QueryRowContext(ctx,
			"SELECT version FROM entities WHERE tenant_id = ? AND entity_id = ?",
			string(tx.TenantID), entityID).Scan(&existingVersion)
		isNew := err == sql.ErrNoRows
		if err != nil && !isNew {
			return fmt.Errorf("check entity %s: %w", entityID, err)
		}

		nextVersion := existingVersion
		if !isNew {
			nextVersion = existingVersion + 1
		}
		changeType := "CREATED"
		if !isNew {
			changeType = "UPDATED"
		}

		entity.Meta.Version = nextVersion
		entity.Meta.LastModifiedDate = submitTime
		entity.Meta.TransactionID = tx.ID
		entity.Meta.ChangeType = changeType
		if isNew {
			entity.Meta.CreationDate = submitTime
		}

		metaJSON, _ := json.Marshal(entity.Meta)

		_, err = sqlTx.ExecContext(ctx,
			`INSERT OR REPLACE INTO entities
			 (tenant_id, entity_id, model_name, model_version, version, data, meta, deleted, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, jsonb(?), jsonb(?), 0, ?, ?)`,
			string(tx.TenantID), entityID,
			entity.Meta.ModelRef.EntityName, entity.Meta.ModelRef.ModelVersion,
			nextVersion, entity.Data, metaJSON,
			timeToMicro(entity.Meta.CreationDate), submitMicro)
		if err != nil {
			return fmt.Errorf("upsert entity %s: %w", entityID, err)
		}

		_, err = sqlTx.ExecContext(ctx,
			`INSERT INTO entity_versions
			 (tenant_id, entity_id, model_name, model_version, version, data, meta, change_type, transaction_id, submit_time, user_id)
			 VALUES (?, ?, ?, ?, ?, jsonb(?), jsonb(?), ?, ?, ?, ?)`,
			string(tx.TenantID), entityID,
			entity.Meta.ModelRef.EntityName, entity.Meta.ModelRef.ModelVersion,
			nextVersion, entity.Data, metaJSON,
			changeType, tx.ID, submitMicro,
			entity.Meta.ChangeUser)
		if err != nil {
			return fmt.Errorf("insert version %s: %w", entityID, err)
		}
	}

	for entityID := range tx.Deletes {
		_, err := sqlTx.ExecContext(ctx,
			"UPDATE entities SET deleted = 1, updated_at = ? WHERE tenant_id = ? AND entity_id = ?",
			submitMicro, string(tx.TenantID), entityID)
		if err != nil {
			return fmt.Errorf("delete entity %s: %w", entityID, err)
		}

		var curVersion int64
		sqlTx.QueryRowContext(ctx,
			"SELECT version FROM entities WHERE tenant_id = ? AND entity_id = ?",
			string(tx.TenantID), entityID).Scan(&curVersion)

		_, err = sqlTx.ExecContext(ctx,
			`INSERT INTO entity_versions
			 (tenant_id, entity_id, model_name, model_version, version, data, meta, change_type, transaction_id, submit_time, user_id)
			 SELECT tenant_id, entity_id, model_name, model_version, ? + 1, NULL, NULL, 'DELETED', ?, ?, ''
			 FROM entities WHERE tenant_id = ? AND entity_id = ?`,
			curVersion, tx.ID, submitMicro,
			string(tx.TenantID), entityID)
		if err != nil {
			return fmt.Errorf("insert delete version %s: %w", entityID, err)
		}
	}

	_, err = sqlTx.ExecContext(ctx,
		"INSERT OR REPLACE INTO submit_times (tx_id, submit_time) VALUES (?, ?)",
		tx.ID, submitMicro)
	if err != nil {
		return fmt.Errorf("record submit time: %w", err)
	}

	return sqlTx.Commit()
}

func (m *TransactionManager) recordCommit(txID string, tx *spi.TransactionState, submitTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.committedLog = append(m.committedLog, committedTx{
		id:         txID,
		submitTime: submitTime,
		writeSet:   tx.WriteSet,
	})
	m.submitTimes[txID] = submitTime
	tx.Closed = true

	delete(m.active, txID)
	delete(m.committing, txID)
	delete(m.savepoints, txID)

	m.pruneCommittedLog()
}
```

- [ ] **Step 4: Implement Rollback, GetSubmitTime, Savepoint, RollbackToSavepoint, ReleaseSavepoint, pruning**

These follow the memory plugin exactly — same in-memory logic, no SQLite interaction.

```go
func (m *TransactionManager) Rollback(_ context.Context, txID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tx, ok := m.active[txID]
	if !ok {
		return fmt.Errorf("transaction %s not found", txID)
	}
	tx.OpMu.Lock()
	defer tx.OpMu.Unlock()

	tx.RolledBack = true
	tx.Closed = true
	delete(m.active, txID)
	delete(m.committing, txID)
	delete(m.savepoints, txID)
	return nil
}

func (m *TransactionManager) GetSubmitTime(_ context.Context, txID string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.submitTimes[txID]; ok {
		return t, nil
	}

	var micro int64
	err := m.factory.db.QueryRow(
		"SELECT submit_time FROM submit_times WHERE tx_id = ?", txID).Scan(&micro)
	if err != nil {
		return time.Time{}, fmt.Errorf("submit time for tx %s: %w", txID, spi.ErrNotFound)
	}
	return time.UnixMicro(micro), nil
}

func (m *TransactionManager) Savepoint(_ context.Context, txID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tx, ok := m.active[txID]
	if !ok {
		return "", fmt.Errorf("transaction %s not found", txID)
	}

	spID := fmt.Sprintf("%x", m.uuids.NewTimeUUID())

	snap := savepointSnapshot{
		buffer:   make(map[string]*spi.Entity, len(tx.Buffer)),
		readSet:  make(map[string]bool, len(tx.ReadSet)),
		writeSet: make(map[string]bool, len(tx.WriteSet)),
		deletes:  make(map[string]bool, len(tx.Deletes)),
	}
	for k, v := range tx.Buffer {
		snap.buffer[k] = copyEntity(v)
	}
	for k, v := range tx.ReadSet {
		snap.readSet[k] = v
	}
	for k, v := range tx.WriteSet {
		snap.writeSet[k] = v
	}
	for k, v := range tx.Deletes {
		snap.deletes[k] = v
	}

	if m.savepoints[txID] == nil {
		m.savepoints[txID] = make(map[string]savepointSnapshot)
	}
	m.savepoints[txID][spID] = snap
	return spID, nil
}

func (m *TransactionManager) RollbackToSavepoint(_ context.Context, txID, spID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sps, ok := m.savepoints[txID]
	if !ok {
		return fmt.Errorf("no savepoints for tx %s", txID)
	}
	snap, ok := sps[spID]
	if !ok {
		return fmt.Errorf("savepoint %s not found in tx %s", spID, txID)
	}

	tx := m.active[txID]
	tx.Buffer = snap.buffer
	tx.ReadSet = snap.readSet
	tx.WriteSet = snap.writeSet
	tx.Deletes = snap.deletes
	delete(sps, spID)
	return nil
}

func (m *TransactionManager) ReleaseSavepoint(_ context.Context, txID, spID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sps, ok := m.savepoints[txID]
	if !ok {
		return fmt.Errorf("no savepoints for tx %s", txID)
	}
	if _, ok := sps[spID]; !ok {
		return fmt.Errorf("savepoint %s not found in tx %s", spID, txID)
	}
	delete(sps, spID)
	return nil
}

func (m *TransactionManager) cleanupTx(txID string, tx *spi.TransactionState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx.RolledBack = true
	tx.Closed = true
	delete(m.active, txID)
	delete(m.committing, txID)
	delete(m.savepoints, txID)
}

func (m *TransactionManager) pruneCommittedLog() {
	evictBefore := m.factory.clock.Now().Add(-submitTimeTTL)
	for id, t := range m.submitTimes {
		if t.Before(evictBefore) {
			delete(m.submitTimes, id)
		}
	}

	m.factory.db.Exec("DELETE FROM submit_times WHERE submit_time < ?",
		timeToMicro(evictBefore))

	var oldestSnapshot time.Time
	for _, tx := range m.active {
		if oldestSnapshot.IsZero() || tx.SnapshotTime.Before(oldestSnapshot) {
			oldestSnapshot = tx.SnapshotTime
		}
	}

	if oldestSnapshot.IsZero() {
		m.committedLog = m.committedLog[:0]
		return
	}

	cutoff := 0
	for i, c := range m.committedLog {
		if !c.submitTime.Before(oldestSnapshot) {
			break
		}
		cutoff = i + 1
	}
	if cutoff > 0 {
		m.committedLog = m.committedLog[cutoff:]
	}
}
```

- [ ] **Step 5: Commit**

```bash
git add plugins/sqlite/txmanager.go
git commit -m "feat(sqlite): add SSI transaction manager"
```

---

## Task 16: Query Planner

**Files:**
- Create: `plugins/sqlite/query_planner.go`
- Create: `plugins/sqlite/query_planner_test.go`

- [ ] **Step 1: Write failing tests for basic operator pushdown**

Table-driven tests: input `spi.Filter` → expected SQL fragment + args + residual.

- [ ] **Step 2: Implement the planner**

```go
package sqlite

import (
	"fmt"
	"strings"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type sqlPlan struct {
	where      string
	args       []any
	postFilter *spi.Filter
}

func planQuery(filter spi.Filter) sqlPlan {
	pushed, residual := dissect(filter)
	plan := sqlPlan{}
	if pushed != nil {
		plan.where, plan.args = toSQL(*pushed)
	}
	plan.postFilter = residual
	return plan
}

func dissect(f spi.Filter) (pushed *spi.Filter, residual *spi.Filter) {
	if isPushable(f.Op) && f.Source == spi.SourceMeta {
		return &f, nil
	}
	if isPushable(f.Op) && f.Source == spi.SourceData {
		return &f, nil
	}
	if f.Op == spi.FilterAnd {
		return dissectAnd(f)
	}
	if f.Op == spi.FilterOr {
		return dissectOr(f)
	}
	return nil, &f
}

func dissectAnd(f spi.Filter) (*spi.Filter, *spi.Filter) {
	var pushedChildren, residualChildren []spi.Filter
	for _, child := range f.Children {
		p, r := dissect(child)
		if p != nil {
			pushedChildren = append(pushedChildren, *p)
		}
		if r != nil {
			residualChildren = append(residualChildren, *r)
		}
	}
	var pushed, residual *spi.Filter
	if len(pushedChildren) == 1 {
		pushed = &pushedChildren[0]
	} else if len(pushedChildren) > 1 {
		pushed = &spi.Filter{Op: spi.FilterAnd, Children: pushedChildren}
	}
	if len(residualChildren) == 1 {
		residual = &residualChildren[0]
	} else if len(residualChildren) > 1 {
		residual = &spi.Filter{Op: spi.FilterAnd, Children: residualChildren}
	}
	return pushed, residual
}

func dissectOr(f spi.Filter) (*spi.Filter, *spi.Filter) {
	for _, child := range f.Children {
		if !isFullyPushable(child) {
			return nil, &f
		}
	}
	return &f, nil
}

func isFullyPushable(f spi.Filter) bool {
	if f.Op == spi.FilterAnd || f.Op == spi.FilterOr {
		for _, c := range f.Children {
			if !isFullyPushable(c) {
				return false
			}
		}
		return true
	}
	return isPushable(f.Op)
}

func isPushable(op spi.FilterOp) bool {
	switch op {
	case spi.FilterEq, spi.FilterNe, spi.FilterGt, spi.FilterLt,
		spi.FilterGte, spi.FilterLte, spi.FilterContains,
		spi.FilterStartsWith, spi.FilterEndsWith, spi.FilterLike,
		spi.FilterIsNull, spi.FilterNotNull, spi.FilterBetween:
		return true
	}
	return false
}

func toSQL(f spi.Filter) (string, []any) {
	switch f.Op {
	case spi.FilterAnd:
		return joinChildren(f.Children, " AND ")
	case spi.FilterOr:
		return joinChildren(f.Children, " OR ")
	default:
		return leafToSQL(f)
	}
}

func joinChildren(children []spi.Filter, sep string) (string, []any) {
	parts := make([]string, 0, len(children))
	var allArgs []any
	for _, c := range children {
		sql, args := toSQL(c)
		parts = append(parts, "("+sql+")")
		allArgs = append(allArgs, args...)
	}
	return strings.Join(parts, sep), allArgs
}

func fieldExpr(f spi.Filter) string {
	if f.Source == spi.SourceMeta {
		return f.Path
	}
	return fmt.Sprintf("json_extract(data, '$.%s')", f.Path)
}

func leafToSQL(f spi.Filter) (string, []any) {
	col := fieldExpr(f)
	switch f.Op {
	case spi.FilterEq:
		return fmt.Sprintf("(%s IS NOT NULL AND %s = ?)", col, col), []any{f.Value}
	case spi.FilterNe:
		return fmt.Sprintf("(%s IS NULL OR %s != ?)", col, col), []any{f.Value}
	case spi.FilterGt:
		return fmt.Sprintf("(%s IS NOT NULL AND %s > ?)", col, col), []any{f.Value}
	case spi.FilterLt:
		return fmt.Sprintf("(%s IS NOT NULL AND %s < ?)", col, col), []any{f.Value}
	case spi.FilterGte:
		return fmt.Sprintf("(%s IS NOT NULL AND %s >= ?)", col, col), []any{f.Value}
	case spi.FilterLte:
		return fmt.Sprintf("(%s IS NOT NULL AND %s <= ?)", col, col), []any{f.Value}
	case spi.FilterContains:
		return fmt.Sprintf("instr(%s, ?) > 0", col), []any{f.Value}
	case spi.FilterStartsWith:
		return fmt.Sprintf("substr(%s, 1, length(?)) = ?", col), []any{f.Value, f.Value}
	case spi.FilterEndsWith:
		return fmt.Sprintf("substr(%s, -length(?)) = ?", col), []any{f.Value, f.Value}
	case spi.FilterLike:
		return fmt.Sprintf("%s LIKE ? ESCAPE '\\'", col), []any{escapeLike(fmt.Sprint(f.Value))}
	case spi.FilterIsNull:
		return fmt.Sprintf("%s IS NULL", col), nil
	case spi.FilterNotNull:
		return fmt.Sprintf("%s IS NOT NULL", col), nil
	case spi.FilterBetween:
		if len(f.Values) >= 2 {
			return fmt.Sprintf("(%s IS NOT NULL AND %s BETWEEN ? AND ?)", col, col),
				[]any{f.Values[0], f.Values[1]}
		}
		return "1=1", nil
	}
	return "1=1", nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
```

- [ ] **Step 3: Run tests, verify pass**

Run: `go test ./plugins/sqlite/ -run TestQueryPlanner -v`

- [ ] **Step 4: Commit**

```bash
git add plugins/sqlite/query_planner.go plugins/sqlite/query_planner_test.go
git commit -m "feat(sqlite): add query planner with greedy AND dissection"
```

---

## Task 17: Entity Store — Searcher Implementation

**Files:**
- Modify: `plugins/sqlite/entity_store.go`

- [ ] **Step 1: Add Search method implementing spi.Searcher**

The entity store implements `spi.Searcher`. In-transaction searches bypass pushdown.

```go
func (s *entityStore) Search(ctx context.Context, filter spi.Filter, opts spi.SearchOptions) ([]*spi.Entity, error) {
	plan := planQuery(filter)

	var baseQuery string
	var baseArgs []any

	if opts.PointInTime != nil {
		baseQuery, baseArgs = s.pointInTimeBase(opts)
	} else {
		baseQuery, baseArgs = s.currentStateBase(opts)
	}

	if plan.where != "" {
		baseQuery += " AND (" + plan.where + ")"
		baseArgs = append(baseArgs, plan.args...)
	}

	baseQuery += " ORDER BY entity_id"

	if plan.postFilter == nil {
		if opts.Limit > 0 {
			baseQuery += " LIMIT ?"
			baseArgs = append(baseArgs, opts.Limit)
		}
		if opts.Offset > 0 {
			baseQuery += " OFFSET ?"
			baseArgs = append(baseArgs, opts.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, baseQuery, baseArgs...)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []*spi.Entity
	scanned := 0
	for rows.Next() {
		if plan.postFilter != nil && scanned >= s.cfg.SearchScanLimit {
			return nil, fmt.Errorf("scan budget exhausted (%d rows examined)", s.cfg.SearchScanLimit)
		}
		scanned++

		e, err := scanEntityFromRows(rows)
		if err != nil {
			return nil, err
		}

		if plan.postFilter != nil {
			// Post-filter in Go using the match package
			// (imported via internal/match or inline evaluation)
			matches, err := evaluateFilter(*plan.postFilter, e)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
		}

		results = append(results, e)
	}

	if plan.postFilter != nil && opts.Offset > 0 {
		if opts.Offset >= len(results) {
			return nil, nil
		}
		results = results[opts.Offset:]
	}
	if plan.postFilter != nil && opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

func (s *entityStore) currentStateBase(opts spi.SearchOptions) (string, []any) {
	return `SELECT entity_id, model_name, model_version, version,
	               json(data), json(meta), created_at, updated_at
	        FROM entities
	        WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND NOT deleted`,
		[]any{string(s.tenantID), opts.ModelName, opts.ModelVersion}
}

func (s *entityStore) pointInTimeBase(opts spi.SearchOptions) (string, []any) {
	pit := timeToMicro(*opts.PointInTime)
	return `SELECT ev.entity_id, ev.model_name, ev.model_version, ev.version,
	               json(ev.data), json(ev.meta), ?, ?
	        FROM entity_versions ev
	        INNER JOIN (
	            SELECT entity_id, MAX(version) AS max_ver
	            FROM entity_versions
	            WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND submit_time < ?
	            GROUP BY entity_id
	        ) latest ON ev.entity_id = latest.entity_id AND ev.version = latest.max_ver
	        WHERE ev.tenant_id = ? AND ev.change_type != 'DELETED'`,
		[]any{pit, pit, string(s.tenantID), opts.ModelName, opts.ModelVersion, pit, string(s.tenantID)}
}
```

- [ ] **Step 2: Commit**

```bash
git add plugins/sqlite/entity_store.go
git commit -m "feat(sqlite): add Searcher implementation with pushdown"
```

---

## Task 18: Domain — Condition → Filter Translation

**Files:**
- Create: `internal/domain/search/filter_translate.go`
- Create: `internal/domain/search/filter_translate_test.go`

- [ ] **Step 1: Write failing tests**

- [ ] **Step 2: Implement translation**

```go
package search

import (
	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/match/predicate"
)

func ConditionToFilter(cond predicate.Condition) (spi.Filter, error) {
	switch c := cond.(type) {
	case *predicate.SimpleCondition:
		return simpleToFilter(c), nil
	case *predicate.LifecycleCondition:
		return lifecycleToFilter(c), nil
	case *predicate.GroupCondition:
		return groupToFilter(c)
	case *predicate.ArrayCondition:
		return arrayToFilter(c)
	default:
		return spi.Filter{}, fmt.Errorf("unsupported condition type: %s", cond.Type())
	}
}

func simpleToFilter(c *predicate.SimpleCondition) spi.Filter {
	return spi.Filter{
		Op:     mapOperator(c.OperatorType),
		Path:   stripDollarDot(c.JsonPath),
		Source: spi.SourceData,
		Value:  c.Value,
	}
}

func lifecycleToFilter(c *predicate.LifecycleCondition) spi.Filter {
	return spi.Filter{
		Op:     mapOperator(c.OperatorType),
		Path:   c.Field,
		Source: spi.SourceMeta,
		Value:  c.Value,
	}
}

func groupToFilter(c *predicate.GroupCondition) (spi.Filter, error) {
	op := spi.FilterAnd
	if c.Operator == "OR" {
		op = spi.FilterOr
	}
	children := make([]spi.Filter, 0, len(c.Conditions))
	for _, child := range c.Conditions {
		f, err := ConditionToFilter(child)
		if err != nil {
			return spi.Filter{}, err
		}
		children = append(children, f)
	}
	return spi.Filter{Op: op, Children: children}, nil
}

func arrayToFilter(c *predicate.ArrayCondition) (spi.Filter, error) {
	// Arrays are not pushable — wrap as opaque filter for post-processing
	return spi.Filter{
		Op:     spi.FilterMatchesRegex, // forces post-filter
		Path:   stripDollarDot(c.JsonPath),
		Source: spi.SourceData,
	}, nil
}

func stripDollarDot(path string) string {
	if len(path) > 2 && path[:2] == "$." {
		return path[2:]
	}
	return path
}

func mapOperator(op string) spi.FilterOp {
	switch op {
	case "EQUALS":
		return spi.FilterEq
	case "NOT_EQUAL":
		return spi.FilterNe
	case "GREATER_THAN":
		return spi.FilterGt
	case "LESS_THAN":
		return spi.FilterLt
	case "GREATER_OR_EQUAL":
		return spi.FilterGte
	case "LESS_OR_EQUAL":
		return spi.FilterLte
	case "CONTAINS":
		return spi.FilterContains
	case "STARTS_WITH":
		return spi.FilterStartsWith
	case "ENDS_WITH":
		return spi.FilterEndsWith
	case "LIKE":
		return spi.FilterLike
	case "IS_NULL":
		return spi.FilterIsNull
	case "NOT_NULL":
		return spi.FilterNotNull
	case "BETWEEN":
		return spi.FilterBetween
	case "MATCHES_PATTERN":
		return spi.FilterMatchesRegex
	case "IEQUALS":
		return spi.FilterIEq
	case "ICONTAINS":
		return spi.FilterIContains
	case "ISTARTS_WITH":
		return spi.FilterIStartsWith
	case "IENDS_WITH":
		return spi.FilterIEndsWith
	default:
		return spi.FilterMatchesRegex // forces post-filter for unknown ops
	}
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/domain/search/filter_translate.go internal/domain/search/filter_translate_test.go
git commit -m "feat(search): add Condition to spi.Filter translation layer"
```

---

## Task 19: SearchService — Searcher Integration

**Files:**
- Modify: `internal/domain/search/service.go`

- [ ] **Step 1: Write failing test for Searcher delegation**

- [ ] **Step 2: Modify Search method to check for Searcher**

In `SearchService.Search()`, check if the EntityStore implements `spi.Searcher` and there's no active transaction:

```go
func (s *SearchService) Search(ctx context.Context, modelRef spi.ModelRef, cond predicate.Condition, opts SearchOptions) ([]*spi.Entity, error) {
	entityStore, err := s.factory.EntityStore(ctx)
	if err != nil {
		return nil, err
	}

	tx := spi.GetTransaction(ctx)
	if searcher, ok := entityStore.(spi.Searcher); ok && tx == nil {
		filter, err := ConditionToFilter(cond)
		if err == nil {
			return searcher.Search(ctx, filter, spi.SearchOptions{
				ModelName:    modelRef.EntityName,
				ModelVersion: modelRef.ModelVersion,
				PointInTime:  opts.PointInTime,
				Limit:        opts.Limit,
				Offset:       opts.Offset,
			})
		}
		// Fall through to in-memory if translation fails
	}

	// Existing GetAll + in-memory filtering path (unchanged)
	// ...
}
```

- [ ] **Step 3: Run full test suite, verify existing tests still pass**

Run: `go test ./internal/domain/search/... -v`

- [ ] **Step 4: Commit**

```bash
git add internal/domain/search/service.go
git commit -m "feat(search): delegate to spi.Searcher when available"
```

---

## Task 20: Cross-Cutting Changes

**Files:**
- Modify: `cmd/cyoda-go/main.go` — add blank import
- Modify: `app/config.go` — no code change needed (StorageBackend is already config-driven)

- [ ] **Step 1: Add blank import to main.go**

Add to the imports block in `cmd/cyoda-go/main.go`:

```go
_ "github.com/cyoda-platform/cyoda-go/plugins/sqlite"
```

- [ ] **Step 2: Verify build**

Run: `go build -o bin/cyoda-go ./cmd/cyoda-go`

- [ ] **Step 3: Verify help output includes sqlite**

Run: `./bin/cyoda-go --help`
Expected: Storage section lists `sqlite` with its 5 config vars.

- [ ] **Step 4: Commit**

```bash
git add cmd/cyoda-go/main.go
git commit -m "feat: register sqlite plugin in stock binary"
```

---

## Task 21: Conformance Tests

**Files:**
- Create: `plugins/sqlite/conformance_test.go`

- [ ] **Step 1: Write conformance_test.go**

```go
package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	spitest "github.com/cyoda-platform/cyoda-go-spi/spitest"

	"github.com/cyoda-platform/cyoda-go/plugins/sqlite"
)

func TestConformance(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	clock := sqlite.NewTestClock()
	factory, err := sqlite.NewStoreFactoryForTest(context.Background(), dbPath, sqlite.WithClock(clock))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	defer factory.Close()

	spitest.StoreFactoryConformance(t, spitest.Harness{
		Factory:      factory,
		AdvanceClock: clock.Advance,
		Now:          clock.Now,
	})
}
```

Note: `NewStoreFactoryForTest` is a test helper that creates a factory with auto-migrate enabled and the given path. Add this to store_factory.go:

```go
func NewStoreFactoryForTest(ctx context.Context, dbPath string, opts ...Option) (*StoreFactory, error) {
	cfg := config{
		Path:            dbPath,
		AutoMigrate:     true,
		BusyTimeout:     5 * time.Second,
		CacheSizeKiB:    64000,
		SearchScanLimit: 100_000,
	}
	return newStoreFactory(ctx, cfg, opts...)
}
```

- [ ] **Step 2: Run conformance tests**

Run: `go test ./plugins/sqlite/ -run TestConformance -v -count=1`
Expected: All subtests PASS (no Skip entries).

- [ ] **Step 3: Commit**

```bash
git add plugins/sqlite/conformance_test.go plugins/sqlite/store_factory.go
git commit -m "test(sqlite): add SPI conformance test wrapper"
```

---

## Task 22: Parity Tests

**Files:**
- Create: `e2e/parity/sqlite/sqlite_test.go`
- Create: `e2e/parity/sqlite/fixture.go`

- [ ] **Step 1: Write fixture.go**

The SQLite fixture builds the binary, launches it with `CYODA_STORAGE_BACKEND=sqlite` and a temp DB path, and provides the BackendFixture interface. Follow the memory parity test pattern.

- [ ] **Step 2: Write sqlite_test.go**

```go
package sqlite

import (
	"flag"
	"os"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

var sharedFixture *sqliteFixture

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

	fix, teardown, err := setup()
	if err != nil {
		println("FATAL: fixture setup failed:", err.Error())
		os.Exit(1)
	}
	defer teardown()

	sharedFixture = fix
	os.Exit(m.Run())
}

func TestParity(t *testing.T) {
	for _, nt := range parity.AllTests() {
		t.Run(nt.Name, func(t *testing.T) {
			nt.Fn(t, sharedFixture)
		})
	}
}
```

- [ ] **Step 3: Run parity tests**

Run: `go test ./e2e/parity/sqlite/... -v -count=1`
Expected: All 34 scenarios PASS.

- [ ] **Step 4: Commit**

```bash
git add e2e/parity/sqlite/
git commit -m "test(sqlite): add HTTP parity test wrapper"
```

---

## Task 23: Query Planner Fuzz Tests

**Files:**
- Modify: `plugins/sqlite/query_planner_test.go`

- [ ] **Step 1: Add property-based fuzz test**

Generate random Filter trees. For each: evaluate SQL(filter) + residual against a test dataset AND evaluate Go-only(filter) against the same dataset. Assert identical results.

- [ ] **Step 2: Run fuzz**

Run: `go test ./plugins/sqlite/ -fuzz FuzzQueryPlanner -fuzztime=30s`

- [ ] **Step 3: Commit**

```bash
git add plugins/sqlite/query_planner_test.go
git commit -m "test(sqlite): add query planner fuzz tests"
```

---

## Task 24: Crash Recovery Test

**Files:**
- Create: `plugins/sqlite/crash_test.go`

- [ ] **Step 1: Write crash recovery test**

Start factory → write entities → close factory abruptly (no graceful shutdown) → reopen → verify persisted state matches last successful commit.

- [ ] **Step 2: Commit**

```bash
git add plugins/sqlite/crash_test.go
git commit -m "test(sqlite): add crash recovery test"
```

---

## Task 25: Concurrency Stress Test

**Files:**
- Create: `plugins/sqlite/stress_test.go`

- [ ] **Step 1: Write concurrency stress test**

N goroutines (e.g., 20), random reads/writes, half targeting conflicting entities. Assert: no lost writes, conflict rate is reasonable, no panics. Run with `-race`.

- [ ] **Step 2: Run with race detector**

Run: `go test ./plugins/sqlite/ -run TestConcurrencyStress -race -v -count=1`

- [ ] **Step 3: Commit**

```bash
git add plugins/sqlite/stress_test.go
git commit -m "test(sqlite): add concurrency stress test with race detection"
```

---

## Task 26: Error Mapping and Documentation

**Files:**
- Create: `plugins/sqlite/errors.go`
- Modify: `README.md` — add sqlite to storage backend documentation

- [ ] **Step 1: Create errors.go with SQLite error classification**

```go
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return spi.ErrNotFound
	}

	errMsg := err.Error()

	// SQLITE_BUSY (5) — write lock contention
	if contains(errMsg, "database is locked") || contains(errMsg, "SQLITE_BUSY") {
		return fmt.Errorf("%w: %v", spi.ErrConflict, err)
	}

	// UNIQUE constraint violation
	if contains(errMsg, "UNIQUE constraint failed") {
		return fmt.Errorf("already exists: %w", err)
	}

	return err
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Update README.md storage section**

Add `sqlite` to the storage backend list with config vars and quick-start example.

- [ ] **Step 3: Commit**

```bash
git add plugins/sqlite/errors.go README.md
git commit -m "feat(sqlite): add error classification and update docs"
```

---

## Final Verification

- [ ] `go test ./... -v` — all tests pass (unit, conformance, parity, E2E)
- [ ] `go vet ./...` — no issues
- [ ] `go test -race ./plugins/sqlite/...` — no races
- [ ] `go build -o bin/cyoda-go ./cmd/cyoda-go` — builds cleanly
- [ ] `./bin/cyoda-go --help` — shows sqlite plugin with config vars
