# SQLite Storage Plugin — Design Specification

## Understanding Summary

- **What:** A new storage plugin (`plugins/sqlite/`) using SQLite as a persistent,
  zero-ops alternative to the memory backend, with search predicate pushdown to SQL.
- **Why:** Fills the gap between memory (ephemeral) and PostgreSQL (requires external
  server). Provides persistent storage for local development and embedded single-node
  production (edge, IoT, small teams). Search pushdown establishes the implementation
  pattern for later PostgreSQL conversion (issue #37).
- **Who:** Developers running locally, small self-hosted deployments, containerized
  single-node production.
- **Key constraints:**
  - Single-node only — SQLite is embedded in the cyoda-go process.
  - Application-layer SSI (ported from memory plugin) — SQLite is the persistence
    layer, not the concurrency controller.
  - No CGO — uses `ncruces/go-sqlite3` (WASM-based) for clean cross-compilation
    and future `sqlite-vec` vector search support.
  - Tenant isolation is application-layer only (no RLS).
- **Explicit non-goals:** Multi-node, vector search (future work), PostgreSQL search
  pushdown conversion (issue #37, separate task).

## Decisions

| # | Decision | Alternatives Considered | Rationale |
|---|----------|------------------------|-----------|
| 1 | Mirror memory plugin SSI architecture | Lean on SQLite native SERIALIZABLE; extract shared SSI module | SQLite has database-level write locking (zero write concurrency) — SSI is redundant at that level. Application-layer SSI gives behavioral parity with memory plugin. Shared module is premature until two consumers exist. |
| 2 | `ncruces/go-sqlite3` driver | `modernc.org/sqlite` (pure Go); `mattn/go-sqlite3` (CGO) | No CGO (clean Docker/cross-compile). Official `sqlite-vec` WASM bindings exist for future vector search. `modernc.org/sqlite` cannot load C extensions — dead end for sqlite-vec. |
| 3 | golang-migrate with embedded SQL files | Auto-create tables on startup | Consistent with PostgreSQL plugin. Supports schema evolution across upgrades. |
| 4 | Mirror PostgreSQL logical schema with SQLite optimizations | Exact mirror; fully divergent schema | Same table/column names reduce cognitive load. `STRICT`, `WITHOUT ROWID`, INTEGER timestamps give 15-25% space reduction and 15-30% faster point lookups with minimal divergence. |
| 5 | TEXT for JSON columns | JSONB | Search pushdown uses `json_extract()` which works on TEXT. JSONB (SQLite 3.45.0+) offers 2-5x faster `json_extract()` but requires verifying `ncruces` bundles a recent enough SQLite. Start with TEXT; migrate to JSONB if version permits — transparent to application code. |
| 6 | Application-layer tenant isolation only | Separate DB per tenant | Memory plugin has the same trust model. Single-node embedded use case doesn't warrant physical isolation complexity. |
| 7 | Search predicate pushdown with greedy dissection | In-memory filtering only (match current implementation) | Establishes the pattern/template for PostgreSQL conversion. Avoids shipping a known architectural gap. |
| 8 | Generic `spi.Filter` representation | Import domain predicate types in SPI | Domain predicate syntax may change. Anti-corruption layer keeps the SPI stable. |
| 9 | XDG default path with env var override | Working directory; hardcoded path | `$XDG_DATA_HOME/cyoda-go/cyoda.db` follows FreeDesktop standard for CLI processes. `CYODA_SQLITE_PATH` env var overrides for containers (`/var/lib/cyoda-go/cyoda.db`). |

---

## Architecture

### In-Memory vs SQLite Split

```
┌─────────────────────────────────────────┐
│           SQLite StoreFactory           │
├─────────────┬───────────────────────────┤
│  In-Memory  │       SQLite (WAL)        │
│             │                           │
│ committedLog│  entity_versions          │
│ active txs  │  entities (current state) │
│ buffers     │  models                   │
│ readSets    │  kv_store                 │
│ writeSets   │  messages                 │
│ savepoints  │  workflows                │
│ deletes     │  sm_audit_events          │
│             │  search_jobs/results      │
│             │  submit_times             │
└─────────────┴───────────────────────────┘
```

Transaction-scoped state (buffers, readSets, writeSets, savepoints) is inherently
ephemeral. The committedLog only needs entries newer than the oldest active
transaction's snapshot — on restart with no active transactions, it starts empty.

`submit_times` is persisted in SQLite for the `GetSubmitTime` API.

On restart: all in-flight transactions are lost (same as memory plugin). Entity data
is fully recovered from SQLite.

### Transaction Lifecycle

| Phase | Behavior |
|---|---|
| **Begin** | Capture `snapshotTime = clock.Now()`, allocate in-memory buffers (readSet, writeSet, buffer, deletes) |
| **Read (in tx)** | Check buffer → `SELECT FROM entity_versions WHERE submit_time <= ?` at snapshot time. Record in readSet. |
| **Write (in tx)** | Add to in-memory buffer. Record in writeSet. |
| **Commit: validate** | Walk in-memory committedLog — if any tx committed after our snapshotTime wrote an entity in our readSet or writeSet, return `spi.ErrConflict` |
| **Commit: flush** | `BEGIN IMMEDIATE` SQLite transaction → `INSERT INTO entity_versions` + `UPSERT entities` for each buffered write/delete → record in committedLog → `COMMIT` |
| **Rollback** | Discard in-memory buffers. Remove from active map. |
| **Savepoint** | Deep-copy buffer/readSet/writeSet/deletes |
| **RollbackToSavepoint** | Restore maps from snapshot |

Commit flush uses `BEGIN IMMEDIATE` to acquire the write lock eagerly. SQLite's
single-writer semantics align naturally with the SSI engine — committedLog validation
happens in Go before acquiring the SQLite write lock, and the flush is atomic.

Non-transactional writes: direct INSERT/UPSERT under SQLite transaction, no SSI tracking.

### Clock

Same injectable `Clock` interface as memory plugin. `TestClock` with `Advance(d)`
for conformance tests. Wall clock for production.

---

## SQLite Schema

### Core Tables

```sql
CREATE TABLE entities (
    tenant_id     TEXT NOT NULL,
    entity_id     TEXT NOT NULL,
    model_name    TEXT NOT NULL,
    model_version TEXT NOT NULL,
    version       INTEGER NOT NULL,
    data          TEXT NOT NULL,
    meta          TEXT,
    deleted       INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, entity_id)
) STRICT, WITHOUT ROWID;

CREATE TABLE entity_versions (
    tenant_id      TEXT NOT NULL,
    entity_id      TEXT NOT NULL,
    model_name     TEXT NOT NULL,
    model_version  TEXT NOT NULL,
    version        INTEGER NOT NULL,
    data           TEXT,
    meta           TEXT,
    change_type    TEXT NOT NULL,
    transaction_id TEXT NOT NULL,
    submit_time    INTEGER NOT NULL,
    user_id        TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, entity_id, version)
) STRICT, WITHOUT ROWID;

CREATE TABLE submit_times (
    tx_id       TEXT NOT NULL PRIMARY KEY,
    submit_time INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
```

Plus equivalent tables for `models`, `kv_store`, `messages`, `workflows`,
`sm_audit_events`, `search_jobs`, `search_job_results` — mirroring PostgreSQL
schema structure.

### Timestamps

All timestamps stored as INTEGER Unix microseconds. Conversion at the
application boundary (Go `time.Time` ↔ `int64`).

### Indexes

```sql
CREATE INDEX idx_entity_versions_submit_time
    ON entity_versions(tenant_id, entity_id, submit_time);

CREATE INDEX idx_entities_model
    ON entities(tenant_id, model_name, model_version)
    WHERE NOT deleted;

CREATE INDEX idx_submit_times_ttl
    ON submit_times(submit_time);
```

### Differences from PostgreSQL Schema

- No Row-Level Security (tenant isolation is application-layer `WHERE tenant_id = ?`)
- `STRICT` enforces type affinity at write time
- `WITHOUT ROWID` on all UUID-keyed tables — clustered B-tree, no hidden rowid
- INTEGER timestamps (Unix microseconds) instead of `TIMESTAMPTZ`
- TEXT for UUIDs instead of `UUID` type
- TEXT for JSON instead of `JSONB` (may migrate to JSONB later)

---

## Search Predicate Pushdown

### SPI Additions

New types in `cyoda-go-spi`:

```go
// filter.go

type Filter struct {
    Op       FilterOp
    Path     string      // JSON path (e.g., "address.city") or meta field
    Source   FieldSource // Data vs Meta
    Value    any
    Values   []any       // for BETWEEN
    Children []Filter    // for AND, OR
}

type FilterOp string
const (
    // Logical
    FilterAnd FilterOp = "and"
    FilterOr  FilterOp = "or"

    // Core comparison
    FilterEq  FilterOp = "eq"
    FilterNe  FilterOp = "ne"
    FilterGt  FilterOp = "gt"
    FilterLt  FilterOp = "lt"
    FilterGte FilterOp = "gte"
    FilterLte FilterOp = "lte"

    // String
    FilterContains   FilterOp = "contains"
    FilterStartsWith FilterOp = "starts_with"
    FilterEndsWith   FilterOp = "ends_with"
    FilterLike       FilterOp = "like"

    // Null
    FilterIsNull  FilterOp = "is_null"
    FilterNotNull FilterOp = "not_null"

    // Extended (may require post-filtering)
    FilterBetween      FilterOp = "between"
    FilterMatchesRegex FilterOp = "matches_regex"

    // Case-insensitive variants (post-filter for Unicode correctness)
    FilterIEq              FilterOp = "ieq"
    FilterINe              FilterOp = "ine"
    FilterIContains        FilterOp = "icontains"
    FilterINotContains     FilterOp = "inot_contains"
    FilterIStartsWith      FilterOp = "istarts_with"
    FilterINotStartsWith   FilterOp = "inot_starts_with"
    FilterIEndsWith        FilterOp = "iends_with"
    FilterINotEndsWith     FilterOp = "inot_ends_with"
)

type FieldSource int
const (
    SourceData FieldSource = iota
    SourceMeta
)
```

```go
// searcher.go

type Searcher interface {
    Search(ctx context.Context, filter Filter, opts SearchOptions) ([]*Entity, error)
}

type SearchOptions struct {
    ModelName    string
    ModelVersion string
    PointInTime  *time.Time
    Limit        int
    Offset       int
}
```

### Translation Flow

```
Domain predicate          SPI Filter            SQL WHERE clause
(may change syntax)  →  (stable contract)  →  (plugin-specific)

SimpleCondition{         Filter{               WHERE json_extract(data,
  JsonPath: "$.city",      Op: FilterEq,         '$.city') = ?
  Operator: "EQUALS",      Path: "city",
  Value: "Berlin",         Source: SourceData,
}                          Value: "Berlin",
                         }
```

Three independently ownable layers:
1. **Domain → SPI translation** (`internal/domain/search/`): converts condition types
   to `spi.Filter`. Changes when predicate syntax changes.
2. **SPI Filter** (`cyoda-go-spi`): stable storage-level contract. Rarely changes.
3. **SPI → SQL translation** (each plugin): converts `spi.Filter` to SQL.

### SearchService Integration

The SearchService checks if the plugin's EntityStore implements `Searcher`:
- If yes → delegate to plugin (SQL pushdown + internal post-filtering)
- If no → fall back to `GetAll` + in-memory filtering (memory plugin unchanged)

### Query Planner (Greedy Dissection)

Inspired by the Cassandra plugin's `GreedyAndPlanner`. Simplified for SQLite
(no sharding, no index tables).

**Input:** `spi.Filter` tree
**Output:** SQL WHERE clause + bound args + residual `*spi.Filter` for post-filtering

**Dissection rules:**

| Filter node | Pushable? | SQL generation |
|---|---|---|
| `eq` | Yes | `json_extract(data, '$.path') = ?` |
| `ne` | Yes | `json_extract(data, '$.path') != ?` |
| `gt/lt/gte/lte` | Yes | `json_extract(data, '$.path') > ?` etc. |
| `contains` | Yes | `json_extract(data, '$.path') LIKE '%' \|\| ? \|\| '%'` |
| `starts_with` | Yes | `json_extract(data, '$.path') LIKE ? \|\| '%'` |
| `ends_with` | Yes | `json_extract(data, '$.path') LIKE '%' \|\| ?` |
| `like` | Yes | `json_extract(data, '$.path') LIKE ?` |
| `is_null` | Yes | `json_extract(data, '$.path') IS NULL` |
| `not_null` | Yes | `json_extract(data, '$.path') IS NOT NULL` |
| `between` | Yes | `json_extract(data, '$.path') BETWEEN ? AND ?` |
| `and` | Partial | Push pushable children, collect rest as residual |
| `or` | Only if all children pushable | Otherwise entire OR is residual |
| `matches_regex` | No | SQLite has no native regex |
| `ieq`, `icontains`, etc. | No | Unicode case folding needs Go |
| `SourceMeta` fields | Yes | Direct column reference: `state = ?`, `created_at > ?` |

**Greedy AND strategy:** Extract all pushable conditions from AND groups into SQL.
Non-pushable conditions become the residual filter applied in Go on the result set.

**Conservative OR strategy:** Only push down if ALL children are pushable. If any
child is non-pushable, the entire OR becomes residual.

**Post-filter flow:** If a residual filter exists, the plugin applies it in Go on
the SQL result set. Pagination (LIMIT/OFFSET) is applied after post-filtering to
ensure correct counts.

### Query Templates

```sql
-- Current state search
SELECT entity_id, data, meta, version, created_at, updated_at
FROM entities
WHERE tenant_id = ?
  AND model_name = ?
  AND model_version = ?
  AND NOT deleted
  AND (/* pushed-down filter tree */)
ORDER BY entity_id
LIMIT ? OFFSET ?;

-- Point-in-time search
SELECT ev.entity_id, ev.data, ev.meta, ev.version
FROM entity_versions ev
INNER JOIN (
    SELECT entity_id, MAX(version) AS max_ver
    FROM entity_versions
    WHERE tenant_id = ? AND submit_time <= ?
    GROUP BY entity_id
) latest ON ev.entity_id = latest.entity_id
       AND ev.version = latest.max_ver
WHERE ev.tenant_id = ?
  AND ev.change_type != 'DELETED'
  AND (/* pushed-down filter tree */)
ORDER BY ev.entity_id
LIMIT ? OFFSET ?;
```

### Future: Generated Column Indexes

For commonly queried JSON paths, future migrations can add:

```sql
ALTER TABLE entities ADD COLUMN status TEXT
    GENERATED ALWAYS AS (json_extract(data, '$.status')) STORED;
CREATE INDEX idx_entities_status ON entities(tenant_id, status);
```

This is a future optimization — the baseline works without it.

---

## Configuration

### Environment Variables

```
CYODA_SQLITE_PATH           Database file path
                            Default: $XDG_DATA_HOME/cyoda-go/cyoda.db
                            Container: /var/lib/cyoda-go/cyoda.db

CYODA_SQLITE_AUTO_MIGRATE   Run migrations on startup (default: true)

CYODA_SQLITE_BUSY_TIMEOUT   Wait time for write lock (default: 5s)

CYODA_SQLITE_CACHE_SIZE     Page cache in KiB (default: 64000 ≈ 64MB)
```

### Pragmas

Applied at connection time:

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;
PRAGMA cache_size = -64000;
PRAGMA foreign_keys = ON;
PRAGMA mmap_size = 268435456;   -- 256MB memory-mapped I/O
```

### Plugin Registration

```go
func init() { spi.Register(&plugin{}) }
func (p *plugin) Name() string { return "sqlite" }
```

Selected via `CYODA_STORAGE_BACKEND=sqlite`.

Stock binary updated with blank import:
```go
_ "github.com/cyoda-platform/cyoda-go/plugins/sqlite"
```

---

## File Structure

```
plugins/sqlite/
├── plugin.go              # Registration, Plugin interface, ConfigVars
├── config.go              # Env var parsing, pragma config
├── store_factory.go       # StoreFactory, DB connection, WAL setup
├── entity_store.go        # EntityStore + Searcher implementation
├── model_store.go         # ModelStore
├── kv_store.go            # KeyValueStore
├── message_store.go       # MessageStore
├── workflow_store.go      # WorkflowStore
├── sm_audit_store.go      # StateMachineAuditStore
├── search_store.go        # AsyncSearchStore
├── txmanager.go           # SSI transaction manager (ported from memory)
├── clock.go               # Injectable clock
├── query_planner.go       # Filter → SQL WHERE + residual Filter
├── migrate.go             # golang-migrate runner
├── migrations/
│   └── 000001_initial_schema.up.sql
│   └── 000001_initial_schema.down.sql
├── conformance_test.go    # spitest.StoreFactoryConformance wrapper
└── query_planner_test.go  # Filter → SQL translation unit tests

e2e/parity/sqlite/
└── sqlite_test.go         # Parity test wrapper (BackendFixture)

# SPI additions (cyoda-go-spi module)
filter.go                  # Filter, FilterOp, FieldSource types
searcher.go                # Searcher interface, SearchOptions
```

---

## Testing Strategy

### 1. Query Planner Unit Tests (`query_planner_test.go`)

Table-driven tests: input `spi.Filter` → expected SQL WHERE clause + bound args +
residual filter.

- Each pushable operator produces correct SQL
- Greedy AND: mixed pushable/non-pushable children split correctly
- Conservative OR: all-pushable vs any-non-pushable
- Nested groups
- `SourceMeta` maps to column references, not `json_extract`
- Edge cases: empty filter, single-node, deeply nested

### 2. SPI Conformance Tests (`conformance_test.go`)

Wraps `spitest.StoreFactoryConformance(t, harness)`:

- Uses `TestClock` with `clock.Advance` for deterministic temporal tests
- SQLite temp file per test run, cleaned up on teardown
- No `Skip` entries — full conformance from day one

### 3. HTTP Parity Tests (`e2e/parity/sqlite/sqlite_test.go`)

- Implements `parity.BackendFixture`
- Launches cyoda-go binary with `CYODA_STORAGE_BACKEND=sqlite` and temp file path
- Iterates `parity.AllTests()` — all 34 scenarios
- No containers needed — fastest parity suite to run

### 4. Searcher Integration Tests (`entity_store_test.go`)

- SQL pushdown produces correct results for each operator
- Post-filtering for non-pushable operators
- Mixed push/post-filter returns same results as pure post-filter
- Point-in-time search with version chains
- Pagination correctness after post-filtering

---

## Cross-Cutting Changes

Beyond the `plugins/sqlite/` package, this work requires:

1. **`cyoda-go-spi` module** — Add `Filter`, `FilterOp`, `FieldSource`, `Searcher`,
   `SearchOptions` types. Requires a minor version bump (e.g., v0.4.0).
2. **`internal/domain/search/service.go`** — Modify `SearchService` to check if the
   plugin's `EntityStore` implements `spi.Searcher` via type assertion. If yes,
   delegate; if no, fall back to existing `GetAll` + in-memory filtering.
3. **`internal/domain/search/`** — Add translation layer: domain `Condition` types →
   `spi.Filter`. This is where the anti-corruption boundary lives.
4. **`cmd/cyoda-go/main.go`** — Add blank import for the sqlite plugin.
5. **`app/config.go` / `printHelp()`** — Document `sqlite` as a valid backend and
   its env vars.

---

## Related Issues

- **#37** — Search predicate pushdown for PostgreSQL (follow-up, uses SQLite as template)
- **#24** — `GetStatisticsByState` scalability (related `GetAll` pattern)
