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
  - Single-node, single-process only — SQLite is embedded in the cyoda-go process.
    Exclusive file lock (`flock`) enforced at startup; a second instance against the
    same file fails fast. Required because the in-memory SSI engine (committedLog,
    active transactions) is per-process — two processes sharing a file would have
    independent SSI state and silently corrupt conflict detection.
  - Application-layer SSI (ported from memory plugin) — SQLite is the persistence
    layer, not the concurrency controller.
  - No CGO — uses `ncruces/go-sqlite3` (WASM-based, ~2-3x slower than native C) for
    clean cross-compilation and future `sqlite-vec` vector search support. The WASM
    overhead is accepted for deploy simplicity and the sqlite-vec roadmap.
  - Tenant isolation is application-layer only (no RLS).
  - `flock` does not work on NFS — but SQLite itself is unreliable on NFS, so this
    is already an implicit constraint of choosing SQLite.
- **Explicit non-goals:** Multi-node, multi-process, vector search (future work),
  PostgreSQL search pushdown conversion (issue #37, separate task).

## Decisions

| # | Decision | Alternatives Considered | Rationale |
|---|----------|------------------------|-----------|
| 1 | Mirror memory plugin SSI architecture | Lean on SQLite native SERIALIZABLE; extract shared SSI module | SQLite has database-level write locking (zero write concurrency) — SSI is redundant at that level. Application-layer SSI gives behavioral parity with memory plugin. Shared module is premature until two consumers exist. |
| 2 | `ncruces/go-sqlite3` driver | `modernc.org/sqlite` (pure Go); `mattn/go-sqlite3` (CGO) | No CGO (clean Docker/cross-compile). Official `sqlite-vec` WASM bindings exist for future vector search. `modernc.org/sqlite` cannot load C extensions — dead end for sqlite-vec. ~2-3x WASM overhead accepted for deploy simplicity. |
| 3 | golang-migrate with embedded SQL files | Auto-create tables on startup | Consistent with PostgreSQL plugin. Supports schema evolution across upgrades. |
| 4 | Mirror PostgreSQL logical schema with SQLite optimizations | Exact mirror; fully divergent schema | Same table/column names reduce cognitive load. `STRICT`, `WITHOUT ROWID` (on append-only tables — see note below), INTEGER timestamps give 15-25% space reduction and 15-30% faster point lookups with minimal divergence. |
| 5 | JSONB via BLOB columns + `jsonb()` / `json()` | TEXT JSON | `ncruces/go-sqlite3` bundles SQLite 3.46+, so JSONB is available. 2-5x faster `json_extract()` on binary format. Plugin asserts `sqlite_version() >= 3.45.0` on startup. Readable views shipped alongside JSON-bearing tables for CLI inspection. |
| 6 | Application-layer tenant isolation only | Separate DB per tenant | Memory plugin has the same trust model. Single-node embedded use case doesn't warrant physical isolation complexity. |
| 7 | Search predicate pushdown with greedy dissection | In-memory filtering only (match current implementation) | Establishes the pattern/template for PostgreSQL conversion. Avoids shipping a known architectural gap. |
| 8 | Generic `spi.Filter` representation | Import domain predicate types in SPI | Domain predicate syntax may change. Anti-corruption layer keeps the SPI stable. |
| 9 | XDG default path with env var override | Working directory; hardcoded path | `$XDG_DATA_HOME/cyoda-go/cyoda.db` follows FreeDesktop standard for CLI processes. `CYODA_SQLITE_PATH` env var overrides for containers (`/var/lib/cyoda-go/cyoda.db`). |
| 10 | Exclusive `flock` for entire process lifetime | flock around migration only; no locking | In-memory SSI state is per-process. Two processes sharing one file would have independent committedLogs, causing silent lost-update corruption. Whole-process flock is the only correct option. |
| 11 | Case-insensitive operators post-filter in Go (Unicode-correct) | ASCII-only via SQL `COLLATE NOCASE` | SQLite `LOWER()` / `COLLATE NOCASE` only fold ASCII. Real-world data includes non-ASCII (É, ß, İ). Unicode correctness is the safe default; ASCII fast path is a future optimization. |
| 12 | `WITHOUT ROWID` on append-only tables only | WITHOUT ROWID on all tables | `entity_versions` (append-only, UUID PK) benefits unambiguously. `entities` (high-UPSERT, large data BLOB) may suffer — WITHOUT ROWID rewrites the full clustered row on every update. Benchmark during implementation; use rowid for `entities` if UPSERT perf is worse. |
| 13 | Align with memory plugin (single-process) | Align with PostgreSQL plugin (multi-process, shared file) | See "Alternative Considered: Multi-Process PostgreSQL Alignment" below. |

### Alternative Considered: Multi-Process PostgreSQL Alignment

SQLite's WAL mode supports multiple processes on the same database file (concurrent
readers, single writer). This raised the question of whether the SQLite plugin should
align with the PostgreSQL plugin's multi-node architecture instead of the memory
plugin's single-process model — reusing SWIM gossip, transaction routing, and
first-committer-wins validation. The two plugins would share most code, differing
only in SQL dialect and connection management.

**Why it was rejected:**

1. **It creates a monolith.** PostgreSQL's value is client-server separation — the
   database is an independent service, nodes connect over the network, different
   failure domains. SQLite on a shared filesystem collapses all of that into one
   machine, one volume, one failure domain. You get the operational complexity of
   multi-node with none of the resilience benefits.

2. **No write scaling.** SQLite allows only one writer at a time (database-level
   lock). Multiple nodes queuing behind a single write lock is strictly worse than
   one process writing directly. The multi-node infrastructure adds overhead without
   throughput gain.

3. **Filesystem constraints.** The shared file must be on a local filesystem —
   SQLite's locking is unreliable on NFS and most network filesystems. This limits
   multi-process deployments to same-host scenarios, undermining the point of
   multi-node.

4. **Limited fault tolerance.** If the writer crashes, surviving nodes can only
   serve reads until a new writer acquires the lock. "Restart the process" achieves
   the same recovery as a standby failover, more simply.

5. **The plugins serve different architectural tiers.** SQLite's value proposition
   is zero-ops, embedded, single-node simplicity. PostgreSQL's is multi-node,
   client-server scalability. Forcing one into the other's shape compromises both.

**Code sharing note:** The SQL generation layer (store implementations, query
building) will have natural overlap between the two plugins. This is best addressed
as a refactoring concern *after* both plugins exist and the actual duplication is
visible — not by coupling their architectures prematurely.

---

## Architecture

### Exclusive Process Lock

On startup, before opening the database:

```go
lock := flock.New(dbPath + ".lock")
if !lock.TryLock() {
    return fmt.Errorf("another cyoda-go instance is using %s", dbPath)
}
// Hold lock until process shutdown
```

This is a hard requirement, not optional safety. The in-memory SSI engine cannot
be shared across processes.

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

**Commit mutex:** A single `sync.Mutex` (`commitMu`) serializes the entire commit
path, mirroring the memory plugin's `m.mu`. This is required for SSI correctness —
without it, two commits could both validate against a stale committedLog and both
succeed, missing a conflict.

**Submit-time monotonicity:** Wall clocks can move backwards (NTP steps, VM
pause/migrate, leap-second smearing). The commit section enforces monotonicity:

```go
submitTime := max(clock.Now().UnixMicro(), lastSubmitTime + 1)
```

`lastSubmitTime` is kept on the txmanager struct (no atomics needed — commitMu
guards it). On startup, seeded from `SELECT MAX(submit_time) FROM entity_versions`.

**Snapshot-time convention:** `committedLog` entries with
`submitTime == snapshotTime` are **not** visible to the reader (strict inequality:
a tx sees only entries with `submitTime < snapshotTime`). This matches the memory
plugin's `!v.submitTime.After(snapshotTime)` convention.

| Phase | Behavior |
|---|---|
| **Begin** | Capture `snapshotTime = clock.Now()`, allocate in-memory buffers (readSet, writeSet, buffer, deletes) |
| **Read (in tx)** | Check deletes → check buffer → snapshot query (see below). Record in readSet. |
| **Write (in tx)** | Add to in-memory buffer. Record in writeSet. |
| **Commit** | Acquire `commitMu` → validate committedLog → capture monotonic `submitTime` → `BEGIN IMMEDIATE` → flush writes/deletes to SQLite → `COMMIT` → append to committedLog → prune → release `commitMu` |
| **Rollback** | Discard in-memory buffers. Remove from active map. |
| **Savepoint** | Deep-copy buffer/readSet/writeSet/deletes |
| **RollbackToSavepoint** | Restore maps from snapshot |

**Snapshot read query** (used for both single-entity Get and GetAll):

```sql
SELECT entity_id, data, meta, version
FROM entity_versions ev
INNER JOIN (
    SELECT entity_id, MAX(version) AS max_ver
    FROM entity_versions
    WHERE tenant_id = ? AND entity_id = ? AND submit_time < ?
    GROUP BY entity_id
) latest ON ev.entity_id = latest.entity_id
       AND ev.version = latest.max_ver
WHERE ev.tenant_id = ?
  AND ev.change_type != 'DELETED';
```

For single-entity reads, the `entity_id = ?` filter is included in the subquery.
For GetAll, it is omitted and a `model_name = ?` filter is added. This is the
same query shape as the point-in-time search template — a single code path serves
both.

**Non-transactional writes:** Used by bootstrap (initial model/entity setup),
audit trail appends, and search job persistence. These are direct INSERT/UPSERT
under a SQLite transaction with no SSI tracking. They touch tables that are either
write-only from the application perspective (audit, search jobs) or written once
at startup (bootstrap). None of these tables are read by transactional entity
operations, so there is no isolation concern.

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
    data          BLOB NOT NULL,
    meta          BLOB,
    deleted       INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, entity_id)
) STRICT;

CREATE TABLE entity_versions (
    tenant_id      TEXT NOT NULL,
    entity_id      TEXT NOT NULL,
    model_name     TEXT NOT NULL,
    model_version  TEXT NOT NULL,
    version        INTEGER NOT NULL,
    data           BLOB,
    meta           BLOB,
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

**Note:** `entities` uses default rowid (no `WITHOUT ROWID`) because it is a
high-UPSERT table with large BLOB payloads — clustered WITHOUT ROWID would
rewrite the full row on every update. `entity_versions` uses `WITHOUT ROWID`
because it is append-only. Benchmark during implementation and adjust if needed.

### JSONB Storage

JSON columns (`data`, `meta`) are stored as BLOB in SQLite's binary JSONB format.

- **Writes:** All INSERT/UPSERT use `jsonb(?)` to convert JSON text to binary.
- **Reads:** Queries returning data to application code wrap with `json(data)` to
  convert back to text. `json_extract()` in pushdown auto-detects binary format.
- **Startup:** Assert `sqlite_version() >= 3.45.0`; fail with clear error otherwise.
- **CLI inspection:** Readable views shipped in the initial migration:

```sql
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
- `WITHOUT ROWID` on append-only tables (entity_versions, submit_times) only
- INTEGER timestamps (Unix microseconds) instead of `TIMESTAMPTZ`
- TEXT for UUIDs instead of `UUID` type
- BLOB with JSONB binary format instead of PostgreSQL `JSONB` (different binary
  representations, same conceptual approach)

### Retention

- **entity_versions:** Unbounded. Having thousands of versions per entity is an
  antipattern for this system; existing warning/error logging flags this.
- **submit_times:** 1-hour TTL matching the memory plugin. Pruned at each commit
  by deleting rows where `submit_time < now() - 1 hour`.

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

type FieldSource string
const (
    SourceData FieldSource = "data"
    SourceMeta FieldSource = "meta"
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
    OrderBy      []OrderSpec
}

type OrderSpec struct {
    Path   string      // JSON path or meta field
    Source FieldSource
    Desc   bool
}
```

`OrderBy` defaults to `[{Path: "entity_id", Source: SourceMeta}]` (ascending)
when empty. The `Searcher` implementation translates to SQL `ORDER BY`; the
SearchService fallback sorts in-memory. This is a hint — if a plugin cannot
support the requested ordering, the SearchService fallback handles it.

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

**In-transaction searches:** When a search is issued inside a transaction with
buffered writes, pushdown is **not used**. The search falls back to GetAll (which
already merges buffer + snapshot) + in-memory filtering. This avoids the hard
problem of merging SQL results with in-memory buffer overlay. Pushdown is for
read-only / cross-transaction searches only.

### Query Planner (Greedy Dissection)

Inspired by the Cassandra plugin's `GreedyAndPlanner`. Simplified for SQLite
(no sharding, no index tables).

**Input:** `spi.Filter` tree
**Output:** SQL WHERE clause + bound args + residual `*spi.Filter` for post-filtering

**Dissection rules:**

| Filter node | Pushable? | SQL generation |
|---|---|---|
| `eq` | Yes | `(json_extract(data, '$.path') IS NOT NULL AND json_extract(data, '$.path') = ?)` |
| `ne` | Yes | `(json_extract(data, '$.path') IS NULL OR json_extract(data, '$.path') != ?)` |
| `gt/lt/gte/lte` | Yes | `(json_extract(data, '$.path') IS NOT NULL AND json_extract(data, '$.path') > ?)` etc. |
| `contains` | Yes | `instr(json_extract(data, '$.path'), ?) > 0` |
| `starts_with` | Yes | `substr(json_extract(data, '$.path'), 1, length(?)) = ?` |
| `ends_with` | Yes | `substr(json_extract(data, '$.path'), -length(?)) = ?` |
| `like` | Yes | `json_extract(data, '$.path') LIKE ? ESCAPE '\'` |
| `is_null` | Yes | `json_extract(data, '$.path') IS NULL` |
| `not_null` | Yes | `json_extract(data, '$.path') IS NOT NULL` |
| `between` | Yes | `(json_extract(data, '$.path') IS NOT NULL AND json_extract(data, '$.path') BETWEEN ? AND ?)` |
| `and` | Partial | Push pushable children, collect rest as residual |
| `or` | Only if all children pushable | Otherwise entire OR is residual |
| `matches_regex` | No | SQLite has no native regex |
| `ieq`, `icontains`, etc. | No | Unicode case folding needs Go |
| `SourceMeta` fields | Yes | Direct column reference: `state = ?`, `created_at > ?` |

**NULL/3VL handling:** SQL's three-valued logic differs from Go's in-memory filter.
`json_extract(data, '$.missing') != 'x'` evaluates to NULL in SQL (filtered out by
WHERE), but Go treats missing-field-not-equal-to-x as true. Pushed-down predicates
wrap with `IS NOT NULL AND ...` guards (or `IS NULL OR ...` for negations) to match
Go semantics. Conformance tests cover every operator × (present / missing / null
literal) to verify parity with the memory plugin.

**String operator safety:** `contains`, `starts_with`, `ends_with` use `instr()` /
`substr()` instead of `LIKE` to avoid SQL injection via wildcard characters (`%`, `_`)
in user input. Only the explicit `like` operator uses `LIKE`, with `ESCAPE '\'` and
value preprocessing (escape `%`, `_`, `\` in the bound value).

**JSON type coercion:** `json_extract()` returns the JSON value's native type.
Comparisons like `json_extract(data, '$.n') = ?` with `?` bound as Go `int64`
against a JSON `1.0` return false under SQLite's type affinity rules. The planner
normalizes numeric filter values to `float64` for consistent comparison. Covered
by fuzz tests.

**Greedy AND strategy:** Extract all pushable conditions from AND groups into SQL.
Non-pushable conditions become the residual filter applied in Go on the result set.

**Conservative OR strategy:** Only push down if ALL children are pushable. If any
child is non-pushable, the entire OR becomes residual.

### Pagination

Two distinct query paths based on whether a residual filter exists:

**Fully pushed (no residual):** LIMIT and OFFSET applied in SQL.

```sql
-- Current state search (fully pushed)
SELECT entity_id, json(data) AS data, json(meta) AS meta, version,
       created_at, updated_at
FROM entities
WHERE tenant_id = ?
  AND model_name = ?
  AND model_version = ?
  AND NOT deleted
  AND (/* pushed-down filter tree */)
ORDER BY entity_id
LIMIT ? OFFSET ?;
```

**Residual present (streaming path):** No LIMIT/OFFSET in SQL. Results streamed
through Go post-filter, then paginated. A scan budget
(`CYODA_SQLITE_SEARCH_SCAN_LIMIT`, default 100,000 rows) caps the number of rows
examined. If the budget is exhausted before the requested page is filled, the plugin
returns a distinguishable `ErrScanBudgetExhausted` error so callers know to tighten
their filter.

```sql
-- Current state search (streaming, no LIMIT)
SELECT entity_id, json(data) AS data, json(meta) AS meta, version,
       created_at, updated_at
FROM entities
WHERE tenant_id = ?
  AND model_name = ?
  AND model_version = ?
  AND NOT deleted
  AND (/* pushed-down subset */)
ORDER BY entity_id;
```

**Point-in-time search** (both paths):

```sql
SELECT ev.entity_id, json(ev.data) AS data, json(ev.meta) AS meta, ev.version
FROM entity_versions ev
INNER JOIN (
    SELECT entity_id, MAX(version) AS max_ver
    FROM entity_versions
    WHERE tenant_id = ? AND submit_time < ?
    GROUP BY entity_id
) latest ON ev.entity_id = latest.entity_id
       AND ev.version = latest.max_ver
WHERE ev.tenant_id = ?
  AND ev.model_name = ?
  AND ev.model_version = ?
  AND ev.change_type != 'DELETED'
  AND (/* pushed-down filter tree */)
ORDER BY ev.entity_id;
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
CYODA_SQLITE_PATH              Database file path
                               Default: $XDG_DATA_HOME/cyoda-go/cyoda.db
                               Container: /var/lib/cyoda-go/cyoda.db

CYODA_SQLITE_AUTO_MIGRATE      Run migrations on startup (default: true)

CYODA_SQLITE_BUSY_TIMEOUT      Wait time for write lock (default: 5s)

CYODA_SQLITE_CACHE_SIZE        Page cache in KiB (default: 64000 ≈ 64MB)

CYODA_SQLITE_SEARCH_SCAN_LIMIT Max rows examined per search with residual
                               filter (default: 100000)
```

### Pragmas

Applied at connection time:

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;
PRAGMA cache_size = -64000;
PRAGMA foreign_keys = ON;
PRAGMA mmap_size = 268435456;       -- 256MB memory-mapped I/O
PRAGMA journal_size_limit = 67108864; -- cap .wal file at 64MB on idle
```

Set at database creation time (before first table):

```sql
PRAGMA auto_vacuum = INCREMENTAL;
```

### WAL Management

SQLite's default `wal_autocheckpoint = 1000` moves committed pages back to the
main database file, but does not truncate the `.wal` file on disk. Under bursty
write loads the WAL can balloon and stay large.

- `journal_size_limit` caps idle WAL size at 64MB.
- A background goroutine issues `PRAGMA wal_checkpoint(TRUNCATE)` periodically
  (every 5 minutes) to reclaim space after write bursts.
- A background goroutine issues `PRAGMA incremental_vacuum(1000)` periodically
  to return free pages to the OS.
- Long-running reader transactions prevent the checkpointer from advancing.
  The plugin bounds read-transaction lifetime and forces rollback on shutdown
  so a forgotten `Begin()` cannot silently pin the WAL.

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

## Error Mapping

| SQLite Error | SPI Error | Retry? | Notes |
|---|---|---|---|
| `SQLITE_BUSY` (after busy_timeout) | `spi.ErrConflict` | Yes | Write lock contention — should be rare with single-writer SSI |
| SSI committedLog conflict | `spi.ErrConflict` | Yes | Application-layer first-committer-wins |
| `SQLITE_FULL` | `spi.ErrInternal` | No | Disk full |
| `SQLITE_CORRUPT` | `spi.ErrInternal` | No | Database corruption — log at ERROR with details |
| `SQLITE_READONLY` | `spi.ErrInternal` | No | Filesystem permissions |
| Constraint violation (UNIQUE) | `spi.ErrConflict` or `spi.ErrAlreadyExists` | Depends | Context-dependent |

---

## File Structure

```
plugins/sqlite/
├── plugin.go              # Registration, Plugin interface, ConfigVars
├── config.go              # Env var parsing, pragma config
├── store_factory.go       # StoreFactory, DB connection, WAL setup, flock
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
├── migrate.go             # golang-migrate runner (flock-protected)
├── migrations/
│   └── 000001_initial_schema.up.sql
│   └── 000001_initial_schema.down.sql
├── conformance_test.go    # spitest.StoreFactoryConformance wrapper
└── query_planner_test.go  # Filter → SQL translation unit tests

e2e/parity/sqlite/
└── sqlite_test.go         # Parity test wrapper (BackendFixture)

# SPI additions (cyoda-go-spi module)
filter.go                  # Filter, FilterOp, FieldSource types
searcher.go                # Searcher interface, SearchOptions, OrderSpec
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

**Property/fuzz suite:** Generate random Filter trees and assert that
`SQL(filter) + residual(filter)` applied to a shared dataset produces identical
results to `Go-only(filter)`. This catches LIKE-escaping bugs, 3VL mismatches,
and type coercion edge cases.

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
- Scan budget exhaustion returns `ErrScanBudgetExhausted`
- NULL/missing field behavior matches memory plugin per operator
- In-transaction search falls back to non-pushdown path

### 5. Crash-Recovery Test

- Start, write entities, `SIGKILL` the process
- Restart, verify persisted state matches the last successful commit
- No partial writes visible

### 6. Concurrency Stress Test

- N goroutines performing random reads/writes, half conflicting
- Verify conflict rate, throughput, absence of lost writes
- Run under race detector (`go test -race`)

---

## Cross-Cutting Changes

Beyond the `plugins/sqlite/` package, this work requires:

1. **`cyoda-go-spi` module** — Add `Filter`, `FilterOp`, `FieldSource`, `Searcher`,
   `SearchOptions`, `OrderSpec` types. Requires a minor version bump (e.g., v0.4.0).
2. **`internal/domain/search/service.go`** — Modify `SearchService` to check if the
   plugin's `EntityStore` implements `spi.Searcher` via type assertion. If yes and
   not in a transaction, delegate; otherwise fall back to existing `GetAll` + in-memory
   filtering. Add in-memory sort for `OrderBy` in the fallback path.
3. **`internal/domain/search/`** — Add translation layer: domain `Condition` types →
   `spi.Filter`. This is where the anti-corruption boundary lives.
4. **`cmd/cyoda-go/main.go`** — Add blank import for the sqlite plugin.
5. **`app/config.go` / `printHelp()`** — Document `sqlite` as a valid backend and
   its env vars.

---

## Related Issues

- **#37** — Search predicate pushdown for PostgreSQL (follow-up, uses SQLite as template)
- **#24** — `GetStatisticsByState` scalability (related `GetAll` pattern)
