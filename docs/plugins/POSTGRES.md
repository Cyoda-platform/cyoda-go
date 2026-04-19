# `postgres` storage plugin

## Capabilities

Durable multi-node storage backed by PostgreSQL. Each transaction
holds a `pgx.Tx` handle in one cyoda node's process memory — cyoda's
multi-node architecture pins each transaction to its owning node via
`txID → pgx.Tx` affinity, giving active-active HA without
distributed-transaction overhead.

**Works against any managed PostgreSQL 14+ platform:** AWS RDS, Google
Cloud SQL, Azure Database for PostgreSQL, Supabase, Neon, Aiven,
Crunchy Bridge, Render, Fly.io Postgres, DigitalOcean Managed
Databases, and self-hosted.

## Concurrency model

The postgres plugin runs every transaction under PostgreSQL's
`REPEATABLE READ` isolation (snapshot isolation) and layers
**application-level, row-granular first-committer-wins** validation on
top at commit time. SERIALIZABLE is not used: the plugin's
`TransactionManager` calls `pool.BeginTx(ctx, pgx.TxOptions{IsoLevel:
pgx.RepeatableRead})` and relies on a per-transaction `readSet` /
`writeSet` to detect conflicts that snapshot isolation alone would
miss.

Before `pgxTx.Commit(ctx)`, the TM re-reads the current committed
versions of every entity the transaction read, compares them against
the snapshot captured at read time, and aborts with
`spi.ErrConflict` on any mismatch. Write-write conflicts are handled
by PostgreSQL's own tuple-level locks raised from
`INSERT`/`UPDATE`/`DELETE` statements — those surface as SQLSTATE
`40001` at DML time or commit time.

**Error-code handling (`classifyError`):** two PostgreSQL error
classes mean "the database rolled this transaction back cleanly, a
retry on a fresh snapshot is safe" — `40001`
(`serialization_failure`) and `40P01` (`deadlock_detected`). Both are
wrapped into `spi.ErrConflict` so callers can retry uniformly; the
original `*pgconn.PgError` stays in the error chain for observability.

Every transaction sets `app.current_tenant` via
`SELECT set_config('app.current_tenant', $1, true)` immediately after
`BEGIN`, which RLS policies on every table consult to enforce tenant
isolation at the row level.

## Transaction manager

Full transaction-lifecycle implementation
(`plugins/postgres/transaction_manager.go`, ~366 lines) covering:

- **Lifecycle:** `Begin` / `Commit` / `Rollback` / `Join` /
  `GetSubmitTime`. `Begin` allocates a time-ordered UUID, starts a
  `REPEATABLE READ` `pgx.Tx`, sets the RLS tenant, and registers the
  transaction in the in-process `txRegistry`.
- **Savepoints:** full `Savepoint` / `RollbackToSavepoint` /
  `ReleaseSavepoint` support, backed by PostgreSQL's native
  `SAVEPOINT` / `ROLLBACK TO` / `RELEASE SAVEPOINT` plus a per-txState
  savepoint stack that snapshots and restores the application
  readSet/writeSet in lockstep with the database.
- **Row-granular validation:** commit-time re-read of the readSet
  (`validateInChunks`) drives the first-committer-wins check.
- **Transaction registry:** the `txRegistry` is a mutex-guarded
  `txID → pgx.Tx` map — the single source of truth for active
  transactions on a node.
- **Submit-time bookkeeping:** each committed transaction captures
  `SELECT CURRENT_TIMESTAMP` before `COMMIT` and records it with a
  1-hour TTL, surfaced via `GetSubmitTime`.

The real serialization guarantee is the combination of PostgreSQL's
`REPEATABLE READ` snapshot + tuple locks + the TM's first-committer
validation — not `SERIALIZABLE` alone.

### `pgx.Tx` single-owner property

A `pgx.Tx` is held by exactly one goroutine on exactly one node.
There is no mechanism for two nodes to share a PostgreSQL transaction
handle: the handle is a pointer into a `pgxpool.Conn` that only exists
in the process that acquired it.

Consequences:

- No distributed locking is needed for transaction access.
- No fencing tokens are needed to prevent stale writes from a revoked
  owner — if the owning node dies, PostgreSQL rolls back the
  transaction on connection loss / idle timeout.
- cyoda's multi-node dispatch routes every subsequent operation on a
  `txID` back to the node that began it. The gossip-backed cluster
  registry advertises which node owns which transaction; any peer
  that receives a request for someone else's txID proxies the
  request rather than trying to rehydrate the handle locally.
- The `txRegistry` (`sync.RWMutex`-protected `map[string]pgx.Tx`) is
  the single source of truth for active transactions on a node.

## Data model and schema

The postgres plugin uses a normalized relational schema with JSONB
columns for flexible document storage and GIN indexes on the JSONB
columns where search requires it.

**Bi-temporal versioning:** `entity_versions` is the append-only
history table:

```sql
CREATE TABLE entity_versions (
    tenant_id        TEXT        NOT NULL,
    entity_id        TEXT        NOT NULL,
    model_name       TEXT        NOT NULL,
    model_version    TEXT        NOT NULL,
    version          BIGINT      NOT NULL,
    valid_time       TIMESTAMPTZ NOT NULL,
    transaction_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    wall_clock_time  TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    doc              JSONB       NOT NULL,
    PRIMARY KEY (tenant_id, entity_id, version)
);
```

- `valid_time` — application-supplied timestamp (entity's logical
  time).
- `transaction_time` — database `CURRENT_TIMESTAMP` (when PG recorded
  the row).
- `wall_clock_time` — `clock_timestamp()` (actual wall-clock,
  independent of transaction).

As-at queries filter by `valid_time`:

```sql
SELECT doc FROM entity_versions
WHERE tenant_id = $1 AND entity_id = $2 AND valid_time <= $3
ORDER BY valid_time DESC, transaction_time DESC
LIMIT 1;
```

**Row-level security (RLS):** every table has RLS enabled with a
policy that compares `tenant_id` against the session variable
`app.current_tenant`, set via `set_config(..., true)` at transaction
start:

```sql
ALTER TABLE entities ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_entities ON entities
    USING (tenant_id = current_setting('app.current_tenant', true));
```

This is defense-in-depth: even a tenant-scoping bug in application
code cannot leak data, because PostgreSQL enforces the isolation at
the row level.

**Schema (all tables):**

| Table | Purpose | Primary key |
|-------|---------|-------------|
| `entities` | Current entity state (one row per entity) | `(tenant_id, entity_id)` |
| `entity_versions` | Append-only bi-temporal history | `(tenant_id, entity_id, version)` |
| `models` | Model descriptors (JSON) | `(tenant_id, model_name, model_version)` |
| `kv_store` | Generic key-value (workflows, configs) | `(tenant_id, namespace, key)` |
| `messages` | Edge messages with binary payload | `(tenant_id, message_id)` |
| `sm_audit_events` | State-machine audit trail | `(tenant_id, entity_id, event_id)` |
| `search_jobs` | Async search job metadata | `id` (with `tenant_id` indexed) |
| `search_job_results` | Entity ID results per job | `(job_id, seq)`, FK to `search_jobs` |

Workflows live in `kv_store` under a dedicated namespace.

**Migrations:** SQL migrations ship embedded in the binary via
`//go:embed migrations/*.sql` and are applied on startup by
`golang-migrate` when `CYODA_POSTGRES_AUTO_MIGRATE=true` (the
default). Schema compatibility is verified at startup before any
migration runs: if the database schema is newer than the binary's
embedded migrations, the binary refuses to start rather than risk
running against an incompatible schema. Dirty migration state is
surfaced as a fatal error requiring manual intervention. A dedicated
`cyoda migrate` subcommand (`RunMigrateWithDSN`) is available for
operators who prefer to apply migrations out-of-band.

## Configuration (env vars)

The plugin advertises its env vars via
`DescribablePlugin.ConfigVars()` (`plugins/postgres/plugin.go`); they
are rendered in the binary's `--help`.

| Var | Default | Purpose |
|---|---|---|
| `CYODA_POSTGRES_URL` (or `CYODA_POSTGRES_URL_FILE`) | *(required)* | PostgreSQL connection string. The `_FILE` variant reads the value from a file path and takes precedence if both are set (trailing whitespace trimmed). Implemented in `resolveSecretWith`. |
| `CYODA_POSTGRES_MAX_CONNS` | `25` | `pgxpool.Pool` maximum connections. |
| `CYODA_POSTGRES_MIN_CONNS` | `5` | `pgxpool.Pool` minimum (warm) connections. |
| `CYODA_POSTGRES_MAX_CONN_IDLE_TIME` | `5m` | Idle connection reap threshold (Go duration syntax). |
| `CYODA_POSTGRES_AUTO_MIGRATE` | `true` | Run embedded SQL migrations on startup. When `false`, the binary refuses to start if the database schema is older than the code. |

### Managed-platform notes

Platforms that front PostgreSQL with **PgBouncer in transaction
pooling mode** (Supabase port 6543, Neon pooled endpoint) strip
prepared-statement caching mid-session. `pgx`'s default extended-query
protocol uses prepared statements.

Options:

- Use the platform's **direct-connection endpoint** (Supabase 5432,
  Neon direct) — recommended for cyoda.
- Set `default_query_exec_mode=exec` on the `pgx` pool to force
  simple-query mode — accepts a small per-query overhead in exchange
  for pooler compatibility.

cyoda uses transaction-scoped `set_config(..., true)` for RLS
(tenant isolation) only — no session-level state fights PgBouncer
transaction mode beyond the prepared-statement cache.

## Operational notes and limits

- Requires PostgreSQL 14+.
- Recommended HA mode: primary + streaming replica with automatic
  failover.
- Cluster-mode cyoda uses Postgres for durable storage and cyoda's
  own gossip registry for node discovery and transaction-owner
  routing — the two are orthogonal.
- Scale-out is bounded by the PostgreSQL primary's write capacity.
  Read-replicas are not yet wired in to cyoda.
- Schema-compatibility contract: the binary refuses to start if the
  database schema is newer than the code, and (with
  `CYODA_POSTGRES_AUTO_MIGRATE=false`) if it is older. Dirty
  migration state is fatal.

## When to use / when not to use

**Use:** clustered production, high consistency requirements,
audit/compliance workloads, any deployment where a managed PostgreSQL
platform is the infrastructure baseline.

**Don't use:** single-process desktop deployments (use `sqlite`),
workloads whose write volume exceeds what a single Postgres primary
can sustain (consider the commercial `cassandra` plugin).
