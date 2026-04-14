# Cyoda-Go Architecture

**Version:** 2.0
**Date:** 2026-04-14
**Status:** Target state after the storage-plugin architecture refactor (Plans 1–5). See `docs/superpowers/specs/2026-04-13-storage-plugin-architecture-design.md` for the refactor plan.

Technical architecture reference for Cyoda-Go, a Go implementation of the Cyoda platform with a pluggable storage layer. This document targets system architects familiar with distributed systems concepts (CAP theorem, SERIALIZABLE isolation, SWIM gossip protocols, Serializable Snapshot Isolation).

For product-level context, see the [PRD](PRD.md).

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [Storage Architecture](#2-storage-architecture)
3. [Transaction Model](#3-transaction-model)
4. [Multi-Node Routing Architecture](#4-multi-node-routing-architecture)
5. [Workflow Engine](#5-workflow-engine)
6. [gRPC & Externalized Processing](#6-grpc--externalized-processing)
7. [Authentication & Authorization](#7-authentication--authorization)
8. [Error Model](#8-error-model)
9. [Configuration Reference](#9-configuration-reference)
10. [Deployment Architecture](#10-deployment-architecture)
11. [Observability](#11-observability)
12. [Planned Features](#12-planned-features-not-yet-implemented)
13. [Design Decisions Log](#13-design-decisions-log)
14. [Non-Functional Limits and Design Boundaries](#14-non-functional-limits-and-design-boundaries)

---

## 1. System Overview

Cyoda-Go is a **modular monolith** organized by Domain-Driven Design boundaries, with a **pluggable storage layer** defined by an external SPI module. The storage contract (`cyoda-go-spi`) is a small, stable, stdlib-only Go module. Storage implementations live in separately versioned Go modules — stock plugins (`plugins/memory`, `plugins/postgres`) under this repository, proprietary and third-party plugins in their own repositories. The `cyoda-go` binary resolves its active plugin at startup via `spi.GetPlugin(cfg.StorageBackend)`; a custom binary including a third-party plugin is a one-file edit (blank import) of the `main` package.

Non-storage cross-cutting concerns (authentication, audit, processing dispatch, cluster registry) are defined as internal-to-cyoda-go Go interfaces in `internal/contract/`. These are consumer-side contracts between cyoda-go's own layers — not plugin concerns.

### Repositories

| Module | Path | Purpose | License |
|--------|------|---------|---------|
| `cyoda-go` | github.com/cyoda-platform/cyoda-go | Application core + stock plugins | Apache 2.0 |
| `cyoda-go-spi` | github.com/cyoda-platform/cyoda-go-spi | Storage-plugin contract (stdlib only) | Apache 2.0 |
| `cyoda-go-cassandra` | github.com/cyoda-platform/cyoda-go-cassandra | Proprietary Cassandra storage plugin + full binary | Proprietary |

### Package Layout (`cyoda-go`)

```
cmd/
  cyoda-go/main.go        Entrypoint; blank-imports stock plugins
  compute-test-client/    Local compute harness for parity tests
go.mod                    module github.com/cyoda-platform/cyoda-go
go.work                   Lists ., plugins/memory, plugins/postgres

plugins/                  Each plugin is its own Go module
  memory/
    go.mod                module github.com/cyoda-platform/cyoda-go/plugins/memory
    plugin.go             init() → spi.Register; Name() + NewFactory()
    store_factory.go      Implements spi.StoreFactory
    txmanager.go          Implements spi.TransactionManager (in-process SSI)
    entity_store.go
    model_store.go, kv_store.go, message_store.go, workflow_store.go
    sm_audit_store.go, search_store.go
    doc.go                Reference example for plugin authors
  postgres/
    go.mod                module github.com/cyoda-platform/cyoda-go/plugins/postgres
    plugin.go             init() → spi.Register; Name() + NewFactory() + ConfigVars()
    store_factory.go      Implements spi.StoreFactory
    txmanager.go          Lifecycle-only tx manager (~150 loc)
    entity_store.go, entity_doc.go
    model_store.go, kv_store.go, message_store.go, workflow_store.go
    sm_audit_store.go, search_store.go
    postgres.go           pgx pool setup; reads CYODA_POSTGRES_*
    migrate.go, querier.go
    migrations/           Embedded SQL migrations (golang-migrate)
    doc.go                Reference example for plugin authors

internal/
  app/                    Application wiring, Config, startup; resolves plugin via spi.GetPlugin
  common/                 AppError formatting, error codes, diagnostics, tags, concrete UUIDGenerator
  contract/               Consumer-side interfaces internal to cyoda-go:
                          AuthenticationService, AuthorizationService, AuditService,
                          ExternalProcessingService, ClusterService, NodeRegistry
  match/                  gjson-based predicate match engine (consumed by memory plugin;
                          operates on spi/predicate.Condition AST)
  logging/                slog wrappers
  observability/          OpenTelemetry SDK init, tracing decorators
  auth/                   JWT (RS256, JWKS, M2M, OBO), key management
  iam/mock/               Mock authentication for development
  domain/
    entity/               Entity CRUD, state machine integration
    model/                Model descriptors, import/export, locking
    workflow/             FSM engine, cascade logic, criteria/processor dispatch
    search/               Sync + async search, predicate evaluation
    account/              Account management
    messaging/            Edge message store
    audit/                Audit trail
  grpc/                   CloudEventsService, streaming, dispatch
  api/                    HTTP handlers (generated OpenAPI types); middleware/
  cluster/
    token/                HMAC-signed transaction routing tokens
    proxy/                HTTP reverse proxy + gRPC routing helpers
    registry/             Gossip (memberlist) and local node registries; MemberlistBroadcaster
                          (implements spi.ClusterBroadcaster, passed to plugins via
                          spi.WithClusterBroadcaster)
    dispatch/             Cross-node compute dispatch (strategy, selector, forwarder)
    lifecycle/            Transaction lifecycle manager (TTL, reaper, outcomes)
  testing/localproc/      In-process processor for E2E tests

api/                      Generated OpenAPI types, gRPC protobuf stubs
proto/                    Protobuf definitions
e2e/parity/               Backend-agnostic parity scenarios (importable by plugin authors)
```

### The `cyoda-go-spi` Module

`cyoda-go-spi` is the stable contract module. It has zero external dependencies (stdlib only) so plugin authors do not inherit transitive dependencies beyond what they add themselves.

Two packages:

- **`spi`** — storage-plugin interfaces and value types:
  - Store interfaces: `StoreFactory`, `EntityStore`, `ModelStore`, `KeyValueStore`, `MessageStore`, `WorkflowStore`, `StateMachineAuditStore`, `AsyncSearchStore`, `SelfExecutingSearchStore`
  - `TransactionManager` interface (Begin/Commit/Rollback/Join/GetSubmitTime/Savepoint)
  - Value types: `Entity`, `EntityMeta`, `EntityVersion`, `ModelRef`, `ModelDescriptor`, `WorkflowDefinition`, `StateDefinition`, `TransitionDefinition`, `StateMachineEvent`, `TransactionState`, `MessageHeader`, `MessageMetaData`, `ProcessorDefinition`, `SearchJob`
  - Context: `UserContext`, `Tenant`, `TenantID`, `WithUserContext`/`GetUserContext`, `WithTransaction`/`GetTransaction`
  - Errors: sentinel `ErrNotFound`, `ErrConflict`, `ErrEpochMismatch`
  - `UUIDGenerator` interface — returns `[16]byte` to keep the module stdlib-only (callers use zero-cost `uuid.UUID(x)` conversion if they want the google/uuid type)
  - `ClusterBroadcaster` interface — fire-and-forget, best-effort topic broadcast
  - Plugin machinery: `Plugin`, `DescribablePlugin`, `Startable`, `ConfigVar`, `FactoryOption`, `FactoryConfig`, `WithClusterBroadcaster`, `ApplyFactoryOptions`, `Register`, `GetPlugin`, `RegisteredPlugins`
  - Helper: `DefaultSaveAll` (sequential fallback for `EntityStore.SaveAll`)
- **`spi/predicate`** — search AST types and JSON parse/marshal:
  - `Condition` (interface), `GroupCondition`, `SimpleCondition`, `ArrayCondition`, `LifecycleCondition`, `FunctionCondition` + operator constants
  - `ParseCondition(body []byte) (Condition, error)` + marshalers

The AST is stdlib-only. A plugin that translates predicates to its own query dialect (SQL, CQL) can import `spi/predicate` without pulling in a match engine. The stock match engine (gjson-based, used by the `memory` plugin) lives in `cyoda-go/internal/match/`.

### Plugin Contract (summary)

```go
// In github.com/cyoda-platform/cyoda-go-spi

type Plugin interface {
    Name() string
    NewFactory(getenv func(string) string, opts ...FactoryOption) (StoreFactory, error)
}

type DescribablePlugin interface {   // optional — for --help rendering
    Plugin
    ConfigVars() []ConfigVar
}

type Startable interface {            // optional — for plugins with background work
    Start(ctx context.Context) error
}

type ConfigVar struct {
    Name, Description, Default string
    Required                   bool
}

type FactoryOption func(*factoryConfig)

func WithClusterBroadcaster(b ClusterBroadcaster) FactoryOption
func ApplyFactoryOptions(opts []FactoryOption) FactoryConfig

func Register(p Plugin)               // panics on duplicate Name() — init-time error
func GetPlugin(name string) (Plugin, bool)
func RegisteredPlugins() []string
```

A plugin registers itself from `init()`. The `cyoda-go/main.go` blank-imports the plugins it ships with:

```go
import (
    _ "github.com/cyoda-platform/cyoda-go/plugins/memory"
    _ "github.com/cyoda-platform/cyoda-go/plugins/postgres"
)
```

A third-party plugin is added to a custom binary by a one-line blank import. No code changes to cyoda-go are required.

### Non-Storage Internal Contracts (`internal/contract/`)

Interfaces between cyoda-go's own layers — HTTP middleware, services, cluster:

```go
// Auth — consumed by internal/api/middleware, implemented by internal/auth and iam/mock
type AuthenticationService interface { ... }
type AuthorizationService interface { ... }

// Audit — consumed by domain services, implemented by internal/domain/audit
type AuditService interface { ... }

// Processing dispatch — consumed by workflow engine, implemented by cluster/dispatch and grpc
type ExternalProcessingService interface {
    DispatchProcessor(ctx, entity, processor, workflowName, transitionName, txID) (*spi.Entity, error)
    DispatchCriteria(ctx, entity, criterion, target, workflowName, transitionName, processorName, txID) (bool, error)
}

// Cluster — consumed by HTTP admin API, implemented by cluster/registry
type ClusterService interface { ... }
type NodeRegistry interface { ... }
```

Plugin authors never implement these — they are internal to the cyoda-go application.

Multi-tenancy is intrinsic. Every request context carries a resolved `UserContext` with `TenantID`. All stores, across all plugins, partition by tenant.

---

## 2. Storage Architecture

A running cyoda-go binary hosts exactly one active storage plugin, resolved at startup:

```go
plugin, ok := spi.GetPlugin(cfg.StorageBackend)   // default: "memory"
if !ok {
    slog.Error("unknown storage backend", "backend", cfg.StorageBackend,
        "available", spi.RegisteredPlugins())
    os.Exit(1)
}

var opts []spi.FactoryOption
if clusterSvc != nil && clusterSvc.Broadcaster() != nil {
    opts = append(opts, spi.WithClusterBroadcaster(clusterSvc.Broadcaster()))
}

factory, err := plugin.NewFactory(os.Getenv, opts...)
txMgr, _ := factory.TransactionManager(ctx)

if s, ok := factory.(spi.Startable); ok {
    s.Start(ctx)   // for plugins with background goroutines
}
```

No per-store routing. No swap logic for transaction managers. Every store in the binary comes from the same plugin, and the plugin supplies its own `TransactionManager` whose semantics match its storage engine.

### 2.1 The `memory` Plugin

The default plugin. A single standalone instance with zero external dependencies — all data lives in Go maps protected by synchronization primitives. Not multi-node compatible. Intended for rapid development and agent-driven application engineering.

**Concurrency model:**

- **Service-level `sync.RWMutex`** on `StoreFactory` -- serializes writes to the shared entity data maps during transaction commit.
- **Per-transaction `OpMu`** (`sync.RWMutex` on `TransactionState`) -- gates concurrent operations within a single transaction and ensures in-flight operations complete before commit/rollback.

**Serializable Snapshot Isolation (SSI):**

Each transaction captures a `SnapshotTime` at `Begin()`. Reads see only data committed before the snapshot. Writes are buffered in a per-transaction `Buffer` map. At commit time, the committed log is scanned for conflicts:

```go
type TransactionState struct {
    ID           string
    TenantID     TenantID
    SnapshotTime time.Time
    ReadSet      map[string]bool      // entity IDs read
    WriteSet     map[string]bool      // entity IDs written
    Buffer       map[string]*Entity   // buffered writes
    Deletes      map[string]bool      // buffered deletes
    OpMu         sync.RWMutex         // per-tx operation gate
    Closed       bool
    RolledBack   bool
}
```

**Conflict detection algorithm (committed log scan):**

```
FOR EACH committed_tx IN committedLog WHERE committed_tx.submitTime > tx.SnapshotTime:
    FOR EACH entityID IN committed_tx.writeSet:
        IF entityID IN tx.ReadSet OR entityID IN tx.WriteSet:
            ABORT with ErrConflict
```

**TOCTOU guard:** A `committing` map prevents double-commit races. The commit sequence acquires `tx.OpMu.Lock()` (waits for in-flight ops), then `factory.mu.Lock()` (exclusive data access), then `tm.mu.Lock()` (committed log check), in that order.

**Committed log pruning:** After each commit, entries older than the oldest active transaction's snapshot time are removed. When no transactions are active, the entire log is cleared.

### 2.2 The `postgres` Plugin

Production-grade storage backed by PostgreSQL with `SERIALIZABLE` isolation. The postgres plugin's `TransactionManager` is a lightweight in-process lifecycle tracker — the durable work happens inside stores via pgx. The actual serialization guarantee comes from the database, not the TM.

**Connection pooling:** pgx `pgxpool.Pool` with configurable bounds (default: 25 max, 5 min connections).

**Bi-temporal versioning:**

The `entity_versions` table provides full temporal history:

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

- `valid_time`: application-supplied timestamp (entity's logical time)
- `transaction_time`: database `CURRENT_TIMESTAMP` (when PG recorded the row)
- `wall_clock_time`: `clock_timestamp()` (actual wall-clock, independent of transaction)

As-at queries filter by `valid_time`:

```sql
SELECT doc FROM entity_versions
WHERE tenant_id = $1 AND entity_id = $2 AND valid_time <= $3
ORDER BY valid_time DESC, transaction_time DESC
LIMIT 1;
```

**Row-level security (RLS):**

Every table has RLS enabled with a policy that compares `tenant_id` against the session variable `app.current_tenant`, set via `SET LOCAL` at transaction start:

```sql
ALTER TABLE entities ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_entities ON entities
    USING (tenant_id = current_setting('app.current_tenant', true));
```

This provides defense-in-depth: even if application code has a tenant-scoping bug, PostgreSQL enforces isolation at the row level.

**Schema (all tables):**

| Table | Purpose | Partitioned by |
|-------|---------|----------------|
| `entities` | Current entity state (one row per entity) | `(tenant_id, entity_id)` PK |
| `entity_versions` | Append-only bi-temporal history | `(tenant_id, entity_id, version)` PK |
| `models` | Model descriptors (JSON) | `(tenant_id, model_name, model_version)` PK |
| `kv_store` | Generic key-value (workflows, configs) | `(tenant_id, namespace, key)` PK |
| `messages` | Edge messages with binary payload | `(tenant_id, message_id)` PK |
| `workflows` | (stored in `kv_store`) | via namespace |
| `sm_audit_events` | State machine audit trail | `(tenant_id, entity_id, event_id)` PK |
| `search_jobs` | Async search job metadata | `id` PK, `tenant_id` indexed |
| `search_job_results` | Entity ID results per job | `(job_id, seq)` PK, FK to `search_jobs` |

**Migrations:** Embedded SQL files via `embed.FS`, applied at startup with `golang-migrate` when `CYODA_POSTGRES_AUTO_MIGRATE=true`.

### 2.3 The `cassandra` Plugin (proprietary, ships as `cyoda-go-cassandra`)

The `cassandra` plugin is a proprietary module in a separate repository (`github.com/cyoda-platform/cyoda-go-cassandra`). It implements the same `spi.Plugin` contract as the stock plugins. The `cyoda-go-cassandra` binary is a custom `main.go` that blank-imports memory + postgres + cassandra, so operators choose any backend at runtime via `CYODA_STORAGE_BACKEND`.

Cassandra alone does not provide cross-partition serializability. The plugin achieves SSI by layering a custom coordinator protocol on top of Cassandra primitives, with a message broker (Kafka / Redpanda) providing work queuing and shard-ownership rebalancing. Summary of the approach:

- **Hybrid Logical Clock (HLC)** — every write uses `USING TIMESTAMP hlc.Now()` so Cassandra's Last-Writer-Wins fences out zombie writers.
- **Shard epoch ownership** — shards are assigned to nodes via the broker's consumer-group rebalancing; each takeover increments an `epoch` via a Cassandra LWT and cancels in-flight writes from the revoked owner via a fenced context.
- **Transaction coordinator** — a per-node singleton that consumes `CommitRequest` messages, runs a 5-phase 2PC protocol, and publishes the result. Phases: set PENDING → load read/write sets → fan out version checks to entity shard owners → write COMMITTED (linearization point) → materialize to the durable version + index + checkpoint tables.
- **Transaction reaper** — scans `transaction_log_idx` on startup and periodically, idempotently replaying materialization for committed-but-unfinished transactions (LWW makes replay safe).
- **`ClusterBroadcaster` integration** — the cassandra plugin consumes `spi.ClusterBroadcaster` (supplied by cyoda-go's `MemberlistBroadcaster`) via an internal adapter for its clock-gossip channel. All other channels (commit commands, version checks, search events) remain strictly inside the plugin over the broker.

**Full design:** see `docs/CASSANDRA_BACKEND_DESIGN.md` inside the proprietary `cyoda-go-cassandra` repository. Operators interacting only with `cyoda-go` need not read it; operators running the cassandra-included binary should.

**Configuration:** plugin-namespaced env vars `CYODA_CASSANDRA_*` and `CYODA_REDPANDA_*`. The plugin advertises them via `spi.DescribablePlugin.ConfigVars()` so the binary's `--help` output renders them automatically.

---

## 3. Transaction Model

### 3.1 TransactionManager SPI

```go
type TransactionManager interface {
    Begin(ctx context.Context) (txID string, txCtx context.Context, err error)
    Commit(ctx context.Context, txID string) error
    Rollback(ctx context.Context, txID string) error
    Join(ctx context.Context, txID string) (txCtx context.Context, err error)
    GetSubmitTime(ctx context.Context, txID string) (time.Time, error)
}
```

- `Begin`: Resolves tenant from context, generates a UUID txID, creates a transaction, returns a new context carrying the `TransactionState`.
- `Join`: Attaches to an existing active transaction by txID. Used when a proxied CRUD request arrives at the transaction-owning node. Verifies tenant match.
- `Commit`: Validates, flushes, records. Returns `common.ErrConflict` on serialization failure.
- `Rollback`: Marks transaction rolled back, clears from active map. Waits for in-flight operations via `OpMu`.
- `GetSubmitTime`: Returns the database timestamp captured at commit. Used for temporal ordering.

### 3.2 In-Memory SSI Conflict Detection

The in-memory `TransactionManager` implements Serializable Snapshot Isolation with a committed log:

**Commit sequence (critical section):**

```
1. Acquire tx.OpMu.Lock()         -- wait for in-flight ops to finish
2. Acquire factory.mu.Lock()      -- exclusive access to shared data
3. Acquire tm.mu.Lock()           -- scan committed log
4. FOR EACH committed_tx where submitTime > tx.SnapshotTime:
     IF committed_tx.writeSet intersects (tx.ReadSet UNION tx.WriteSet):
       ABORT → ErrConflict
5. Flush tx.Buffer to factory.entityData (deep copy)
6. Apply tx.Deletes (append tombstone versions)
7. Append to committedLog: {txID, submitTime, writeSet}
8. Record submitTime in submitTimes map
9. Remove from active map
10. Prune committedLog (entries older than oldest active snapshot)
11. Release all locks
```

The committed log grows only as long as there are concurrent transactions. When all active transactions complete, the log is cleared entirely. The `submitTimes` map grows without bound -- acceptable for in-memory single-node use.

### 3.3 PostgreSQL SERIALIZABLE + Error Code 40001

The PostgreSQL `TransactionManager` delegates conflict detection entirely to the database:

```go
pgxTx, err := tm.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
```

Every transaction begins with `SET LOCAL app.current_tenant = $1` for RLS. On commit, PostgreSQL detects serialization failures and returns error code `40001`, which is mapped to `common.ErrConflict`:

```go
func classifyError(err error) error {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) && pgErr.Code == "40001" {
        return common.ErrConflict
    }
    return err
}
```

Before committing, the transaction captures `SELECT CURRENT_TIMESTAMP` for the submit time -- this is the database's view of commit time, not the application's wall clock.

### 3.4 Transaction Lifecycle Manager

The `lifecycle.Manager` provides TTL enforcement and outcome tracking for multi-node scenarios:

```go
type Manager struct {
    active     map[string]txEntry      // txID → {nodeID, expiresAt}
    outcomes   map[string]outcomeEntry // txID → {outcome, recordedAt}
    outcomeTTL time.Duration
}
```

- **Registration:** `Register(txID, nodeID, ttl)` -- records a new active transaction with deadline.
- **TTL enforcement:** `ReapExpired()` -- background goroutine rolls back transactions that exceed their deadline. Outcome recorded as `OutcomeRolledBack`.
- **Outcome tracking:** `RecordOutcome(txID, committed|rolledBack)` -- moves from active to outcomes map. Outcomes expire after `outcomeTTL`.
- **Cluster visibility:** `ListByNode(nodeID)` -- returns all active transactions owned by a specific node.

### 3.5 pgx.Tx Single-Owner Property

A critical safety property: **a `pgx.Tx` is held by exactly one goroutine on exactly one node.** There is no mechanism for two nodes to share a PostgreSQL transaction. This means:

- No distributed locking is needed for transaction access.
- No fencing tokens are needed to prevent stale writes.
- If the owning node dies, PostgreSQL automatically rolls back the transaction (idle timeout).
- The `txRegistry` (a `sync.RWMutex`-protected map of `txID -> pgx.Tx`) is the single source of truth for active transactions on a node.

This is why DD-2 (fencing tokens not required) holds. See [Section 13](#13-design-decisions-log).

### 3.6 Plugin-Specific Transaction Managers

Each plugin provides its own `TransactionManager` whose semantics match its storage engine:

- **memory plugin** — in-process SSI with entity-level read/write sets and a committed-transaction log (Section 3.2).
- **postgres plugin** — lightweight in-process lifecycle tracker. The real serialization guarantee comes from PostgreSQL's `SERIALIZABLE` isolation inside the stores (Section 3.3). The TM assigns IDs, tracks active/committed sets with timestamps, and supports savepoints as a local stack.
- **cassandra plugin** — a coordinator-managed 2PC over a message broker, with HLC fencing, per-shard epoch ownership, and committed-log replay for recovery (see Section 2.3). Owns its own TM because Cassandra has neither `SERIALIZABLE` nor native multi-partition transactions.

The core `cyoda-go` never picks a TM. It asks the plugin via `factory.TransactionManager(ctx)` and wraps the result with its tracing decorator when OTel is enabled.

---

## 4. Multi-Node Routing Architecture

Multi-node cluster mode is **opt-in** via `CYODA_CLUSTER_ENABLED` (default: `false`). Single-node is the default deployment pattern. The routing, gossip, and transaction forwarding described in this section are only active when cluster mode is enabled.

This is the most architecturally significant section. Cyoda-Go supports multi-node deployment where any node can receive any request, with transactions pinned to their originating node.

### 4.1 Cluster Discovery

**Protocol:** SWIM gossip via HashiCorp `memberlist` (pure Go, embedded, no external infrastructure).

**Encryption:** AES-256-GCM encrypted gossip using a shared HMAC secret (`CYODA_HMAC_SECRET`). The same secret is used for gossip encryption and transaction token signing.

**Node metadata** (JSON, serialized in memberlist node meta):

```go
type nodeMeta struct {
    ID   string              `json:"id"`   // stable, operator-assigned
    Addr string              `json:"addr"` // HTTP address (e.g., "http://node-1:8123")
    Tags map[string][]string `json:"tags"` // tenantID → compute member tags
}
```

Tags are updated whenever a compute member joins or leaves a node. The update is pushed to the memberlist via `UpdateNode()`, and gossip propagates the change to all peers within milliseconds.

**Bootstrap algorithm:**

```
1. Filter self-address from seed list
2. Attempt list.Join(seeds) with exponential backoff:
     initial = 500ms, max = 10s, deadline = 2min
3. After successful join, poll member count every 200ms
4. Block until member count is stable for StabilityWindow (default 2s)
5. Only then: mark node ready, open gRPC server
```

This handles simultaneous startup of all nodes. Memberlist is self-healing and merges transient split clusters before the stability window elapses.

**Failure detection:** Automatic via SWIM protocol. Dead nodes are evicted from the membership list within seconds. No manual intervention required.

**Graceful leave:** `Deregister()` calls `list.Leave(5s)` then `list.Shutdown()`, giving peers time to update their membership views.

### 4.2 Transaction Routing

**Token structure (HMAC-SHA256 signed, base64url-encoded):**

```go
type Claims struct {
    NodeID    string `json:"n"`   // ID of the node holding the transaction
    TxRef     string `json:"t"`   // UUID, key into the node's local tx map
    ExpiresAt int64  `json:"e"`   // Unix timestamp
}
```

Token format: `base64url(json_payload).base64url(hmac_sha256(json_payload, secret))`

The token is opaque to the client. The router decodes it to extract `nodeID` without any network call -- address resolution is a local scan over `list.Members()`.

**HTTP reverse proxy middleware (`proxy.HTTPRouting`):**

```
1. Extract X-Tx-Token header from request
2. If absent → serve locally (next handler)
3. Verify HMAC signature
4. If claims.NodeID == self → serve locally
5. If claims.NodeID != self → lookup address in gossip registry
6. If node alive → httputil.ReverseProxy to target node
7. If node dead/unknown → 503 TRANSACTION_NODE_UNAVAILABLE
```

The proxy is transparent: the target node receives the original request with all headers intact, including the `X-Tx-Token`. Transport is a shared `http.Transport` with connection pooling (100 max idle, 10 per host, 90s idle timeout).

**Token error handling:**

| Error | HTTP Status | Code |
|-------|-------------|------|
| Token expired | 400 | `TRANSACTION_EXPIRED` |
| HMAC mismatch / invalid format | 400 | `BAD_REQUEST` |
| Target node dead | 503 | `TRANSACTION_NODE_UNAVAILABLE` |
| Target node unreachable | 503 | `TRANSACTION_NODE_UNAVAILABLE` |

**gRPC routing helpers:**

For gRPC streams, the `proxy` package provides:

```go
// ExtractGRPCToken reads tx-token from gRPC incoming metadata.
func ExtractGRPCToken(ctx context.Context) string

// ResolveTarget determines whether a request should be proxied.
// Returns: addr, shouldProxy, err
func ResolveTarget(ctx, signer, registry, selfNodeID, tok) (string, bool, error)
```

gRPC routing is not a transparent proxy -- the gRPC handler checks `ResolveTarget` and either serves locally or returns an error directing the client to retry against the target node.

### 4.3 Compute Dispatch Routing

Three strategy interfaces, each with a default implementation:

| Component | Interface | Default Impl | Purpose |
|-----------|-----------|--------------|---------|
| Dispatch Strategy | `spi.ExternalProcessingService` | `ClusterDispatcher` | Local first, then cluster |
| Peer Selection | `PeerSelector` | `RandomSelector` | Pick from candidates |
| Forwarding Transport | `DispatchForwarder` | `HTTPForwarder` | HTTP POST to peer |

**`ClusterDispatcher` algorithm:**

```
1. Try local dispatch: registry.FindByTags(tenantID, tags)
   - If found → dispatch locally (existing gRPC stream path)
   - If error is NOT "no matching member" → return error

2. Cluster lookup with polling:
   a. Query gossip: registry.List() → filter by:
      - Not self
      - Alive
      - Tags[tenantID] overlaps required tags
   b. If candidates found → PeerSelector.Select(candidates) → forward
   c. If no candidates → wait 200ms, retry
   d. After CYODA_DISPATCH_WAIT_TIMEOUT (default 5s) → fail
      with NO_COMPUTE_MEMBER_FOR_TAG

3. Forward to selected peer:
   HTTPForwarder.ForwardProcessor(ctx, peer.Addr, request)
   - POST to http://peer/internal/dispatch/processor
   - HMAC-SHA256 signed body (X-Dispatch-HMAC header)
   - Peer deserializes, calls its own local dispatch, returns result
```

**Internal dispatch endpoints:**

```
POST /internal/dispatch/processor
POST /internal/dispatch/criteria
```

- Authenticated via HMAC-SHA256 (same cluster secret)
- Not exposed through nginx (only `/api/*` is proxied)
- 10MB max body size
- Reconstruct `UserContext` from request fields (tenantID, userID, roles)

**Dispatch request/response types:**

```go
type DispatchProcessorRequest struct {
    Entity         json.RawMessage
    EntityMeta     common.EntityMeta
    Processor      common.ProcessorDefinition
    WorkflowName   string
    TransitionName string
    TxID           string
    TenantID       string
    Tags           string
    UserID         string
    Roles          []string
}

type DispatchProcessorResponse struct {
    EntityData json.RawMessage
    Success    bool
    Error      string
    Warnings   []string
}
```

**Error handling:**

| Scenario | Behavior | Error Code |
|----------|----------|------------|
| No local member, no peer with tag | Poll gossip for wait timeout, then fail | `NO_COMPUTE_MEMBER_FOR_TAG` |
| Peer selected but unreachable | Fail (single attempt) | `DISPATCH_FORWARD_FAILED` |
| Peer dispatch times out | HTTP timeout, transaction rolls back | `DISPATCH_TIMEOUT` |
| Peer's local member disconnects | Peer returns error, propagated | `COMPUTE_MEMBER_DISCONNECTED` |
| Gossip metadata stale | Peer returns "no member for tag" | `NO_COMPUTE_MEMBER_FOR_TAG` |

### 4.4 Transaction Flow -- Complete Swimlane

Participants:

| Participant | Role | Holds |
|-------------|------|-------|
| Client | External caller (REST API) | HTTP connection |
| Node A | Receives primary event, owns transaction | `pgx.Tx` for tx-123, flow chain state |
| Compute | External processor (gRPC member) | Business logic, stateless |
| Node B | Receives callback CRUD from compute | Nothing -- proxies to tx owner |
| PostgreSQL | Source of truth | Transaction tx-123, all data |

**Happy path:**

```
t0   Client --> POST /entity create --> Node A
t1   Node A: BEGIN tx-123, generate txToken --> PG: BEGIN SERIALIZABLE
t2   Node A: Save entity --> PG: INSERT entity (in tx-123)
t3   Node A: SM engine dispatches to processor
t3a  Node A: Check local MemberRegistry --> not found
t3b  Node A: Query gossip --> Node B has tag for this tenant
t3c  Node A: PeerSelector picks Node B (random from candidates)
t3d  Node A: HTTPForwarder --> POST Node B /internal/dispatch/processor
t3e  Node B: Receives forwarded dispatch, finds local member, dispatches via gRPC stream
t4   Compute: Receives CloudEvent w/ tx-123 (from Node B's stream)
t5   Compute: Executes business logic
t6   Compute: CRUD callback w/ tx-123 --> Node B receives request
t7   Node B: Decode txToken --> extract Node A from claims --> proxy to Node A
t8   Node A: Receive proxied CRUD, Join tx-123 --> PG: INSERT/UPDATE (in tx-123)
t9   Node A: CRUD OK --> respond to Node B --> Node B forwards to Compute
t10  Compute: Receive CRUD OK --> finish logic
t11  Compute: Respond OK to Node A (via Node B dispatch return)
t12  Node A: SM complete, all processors finished
t13  Node A: COMMIT tx-123 --> PG: COMMIT
t14  Node A: 200 OK {entityId, transactionId} --> Client
```

Key observations:
- Node A is the single transaction owner throughout. All writes go through Node A.
- Node B acts as a transparent proxy for CRUD callbacks (via `X-Tx-Token`) and as a local dispatch host for its compute members.
- The compute member is stateless -- it receives entity data via CloudEvent payload and returns modified data the same way.
- The dispatch forward (t3d) and the CRUD proxy (t7) are distinct network paths that can fail independently.

### 4.5 Network Partition Analysis

**Network links:**

| Link | Label | Protocol |
|------|-------|----------|
| L1 | Client <-> Node A | HTTP (REST) |
| L2 | Node A <-> Compute | gRPC bidirectional stream |
| L3 | Compute <-> Node B | gRPC / HTTP (CRUD callback) |
| L4 | Node B <-> Node A | Internal proxy (HTTP) |
| L5 | Node A <-> PostgreSQL | TCP (pgx connection) |
| L6 | Node B <-> PostgreSQL | TCP (proxy resolution only, not tx) |
| L7 | Node A <-> Node B | HTTP POST /internal/dispatch (NEW) |

---

#### Phase 1: t0--t3 (Entity create, SM dispatches to processor)

**L1 partitions (Client <-> Node A):**

Client's HTTP request times out. Node A may have already begun tx-123 and dispatched to processor.

- *Before t1:* Request never arrived. Clean.
- *After t1:* Node A has an open transaction, flow chain running. Client is gone. Node A eventually completes or times out the flow chain. Transaction commits or rolls back without the client ever knowing.

ISSUE: Client retries create a duplicate entity. **Requires idempotency keys.**

**L5 partitions (Node A <-> PG):**

Node A cannot write to PG. The INSERT at t2 fails. Node A detects error, aborts flow chain, rolls back.

If the partition is brief and the pgx TCP connection survives (keepalive has not fired): Node A may not notice until the next PG operation fails.

SAFE: PG operation fails -> Node A rolls back -> client gets error.

---

#### Phase 2: t4--t5 (Compute executing processor logic)

**L2 partitions (Node A <-> Compute):**

Node A dispatched the CloudEvent to compute. gRPC stream breaks. Node A's dispatch call returns error or times out (gRPC keepalive). Node A detects failure -> rolls back tx-123.

Meanwhile: Compute may still be executing business logic, unaware the stream is dead. When it tries to callback (t6), it will fail.

SAFE: Node A rolls back. Compute's work is discarded (stateless).

**L5 partitions (Node A <-> PG):**

Node A is waiting for compute response. PG connection may drop. Two sub-cases:

1. *pgx connection killed by PG:* tx-123 is rolled back server-side. When compute responds and Node A tries to use the tx, pgx returns error. Node A detects and aborts.
2. *pgx connection survives (brief partition):* No PG operations happening during this phase. Transaction still alive. If partition heals before t8, everything proceeds normally.

SAFE: Either PG kills the tx, or partition heals and flow continues.

---

#### Phase 3: t6--t9 (CRUD callback through Node B, proxied to Node A)

**L3 partitions (Compute <-> Node B):**

Compute's CRUD callback cannot reach Node B. Compute gets connection error. Compute reports failure back to Node A (via the gRPC stream, if L2 is still up). Node A receives processor failure -> rolls back tx-123.

SAFE: Clean failure propagation up the chain.

**L4 partitions (Node B <-> Node A):**

The critical proxy link. Node B receives CRUD request with tx-123. Extracts Node A's ID from token claims. Tries to proxy to Node A. Cannot reach it.

Node B returns error to compute. Compute reports failure to Node A (via L2, if up). Node A rolls back.

If L2 is *also* down: Node A is waiting for compute response. Compute cannot reach Node B, cannot complete its work, but also cannot report back to Node A. Node A's dispatch call eventually times out (gRPC keepalive/deadline). Node A rolls back.

SAFE: Multiple failure modes, but all lead to rollback. May be slow (timeout-dependent).

**L5 partitions during t8 (Node A <-> PG):**

Node B proxied the CRUD to Node A. Node A tries to INSERT/UPDATE in tx-123. PG connection is dead. pgx returns error. Node A aborts the CRUD operation, responds error to Node B -> Node B -> Compute -> Node A (processor failure). Node A rolls back.

SAFE: PG error propagates back through entire chain.

**L1 partitions during Phase 3 (Client <-> Node A):**

Client's HTTP connection drops. But Node A's flow chain is autonomous at this point -- it does not need the client connection to complete. Flow chain may still commit successfully. Client never gets the response.

ISSUE: Same as Phase 1 -- client retries create duplicates. **Requires idempotency keys.**

---

#### Phase 4: t12--t14 (SM complete, commit, respond)

**L5 partitions at COMMIT (Node A <-> PG):**

The most dangerous moment. Node A sends COMMIT to PG. Three outcomes:

1. **COMMIT succeeds, ACK lost:** PG committed. Node A does not know. pgx returns error. Node A assumes failure, tells client error. But data IS committed.
2. **COMMIT never reaches PG:** PG never committed. Transaction eventually rolled back by PG (idle timeout). Node A tells client error. Correct.
3. **Partition before COMMIT sent:** Node A detects dead connection, rolls back locally, tells client error. Correct.

ISSUE: Case 1 is the classic **commit ambiguity**. Node A cannot distinguish cases 1 and 2. **Requires a commit marker/confirmation mechanism** (see [#56](#11-planned-features-not-yet-implemented)).

**L1 partitions at response (Node A <-> Client):**

Transaction committed successfully. HTTP response cannot reach client. Client retries, may create duplicate.

ISSUE: Committed but client does not know. **Idempotency key** would detect the retry and return the original result.

---

#### L7 partition analysis (dispatch forward: Node A <-> Node B)

**Before forward sent:** Node A detects connection error, tries another peer (if available) or fails with `NO_COMPUTE_MEMBER_FOR_TAG`.

**Forward sent, waiting for response:** HTTP timeout fires. Node A returns timeout error to workflow engine -> transaction rolls back. Node B may still be dispatching -- its local dispatch will eventually complete or timeout, but the result is discarded (no one listening).

**Response lost:** Same as above -- timeout -> rollback. No split-brain because the dispatch is read-only from Node A's perspective (the entity update has not been applied yet).

SAFE: All cases lead to rollback or retry. No data corruption possible because the dispatch response must be received by Node A before it updates the entity.

---

#### Findings Summary

| Category | Finding | Needed Mechanism |
|----------|---------|-----------------|
| **Consistency** | All partition scenarios lead to rollback or clean commit. No split-brain possible because `pgx.Tx` is single-owner. PG `SERIALIZABLE` catches conflicting concurrent writes. | None (inherently safe) |
| **Duplicate operations** | Client <-> Node A partition at any point can cause the client to retry, creating a second transaction for the same intent. Both may commit without conflicting. | Idempotency keys |
| **Commit ambiguity** | L5 partition at COMMIT time: Node A cannot tell if PG committed or not. | Commit marker (write marker row before COMMIT; check on reconnect) |
| **Timeout / liveness** | Several failure modes depend on timeouts (gRPC keepalive, PG TCP keepalive) that may be slow (minutes). Flow chain can hang waiting for dead compute nodes. | Transaction TTL + deadline propagation via context |
| **Resource exhaustion** | Stuck transactions hold PG connections. With bounded pool (25 default), a few stuck txns can starve the node. | Transaction TTL with forced rollback |
| **Observability** | No cluster-wide view of open transactions, their owners, or their age. | Transaction registry |

### 4.6 Persistent Search Snapshots

**`AsyncSearchStore` SPI:**

```go
type AsyncSearchStore interface {
    CreateJob(ctx, job *SearchJob) error
    GetJob(ctx, jobID string) (*SearchJob, error)
    UpdateJobStatus(ctx, jobID, status, resultCount, errMsg, finishTime, calcTimeMs) error
    SaveResults(ctx, jobID string, entityIDs []string) error
    GetResultIDs(ctx, jobID string, offset, limit int) ([]string, int, error)
    DeleteJob(ctx, jobID string) error
    ReapExpired(ctx, ttl time.Duration) (int, error)
}
```

**Design principles (DD-10, DD-11, DD-12):**

- Results table stores **entity IDs only** (no entity data). This keeps the results table compact and avoids data staleness. Entity data is re-fetched from the entity store when the client reads results.
- `pointInTime` is **always populated** on `SearchJob`. If the client does not supply one, the service uses `time.Now()`. This ensures search results are deterministic -- repeated reads at the same `pointInTime` return the same set.
- **TTL-based cleanup** for both implementations. A background reaper goroutine runs on a configurable interval (`CYODA_SEARCH_REAP_INTERVAL`, default 5m) and deletes jobs older than `CYODA_SEARCH_SNAPSHOT_TTL` (default 1h). The PostgreSQL implementation uses `CASCADE` on the foreign key from `search_job_results` to `search_jobs`.

**PostgreSQL schema:**

```sql
CREATE TABLE search_jobs (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'RUNNING',
    model_name    TEXT NOT NULL,
    model_ver     TEXT NOT NULL,
    condition     JSONB NOT NULL,
    point_in_time TIMESTAMPTZ NOT NULL,
    search_opts   JSONB,
    result_count  INTEGER DEFAULT 0,
    error         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    calc_ms       BIGINT DEFAULT 0
);

CREATE TABLE search_job_results (
    job_id    TEXT NOT NULL REFERENCES search_jobs(id) ON DELETE CASCADE,
    seq       INTEGER NOT NULL,
    entity_id TEXT NOT NULL,
    PRIMARY KEY (job_id, seq)
);
```

---

## 5. Workflow Engine

The workflow engine (`internal/domain/workflow/Engine`) implements a finite state machine (FSM) model for entity lifecycle management.

### 5.1 FSM Model

A `WorkflowDefinition` contains:
- **States:** Named states (e.g., `NEW`, `PROCESSING`, `DONE`).
- **Transitions:** Named edges between states, each with:
  - `type`: `AUTOMATIC` or `MANUAL`
  - `criteria`: Optional conditions (predicate or function) that must be satisfied.
  - `processors`: Ordered list of processors executed when the transition fires.
- **Initial state:** The starting state for new entities.
- **Criterion:** Optional workflow-level criterion for workflow selection.

### 5.2 Execution Modes

Three entry points into the engine:

1. **`Execute(entity, transitionName)`** -- Entity creation. Selects matching workflow, sets initial state, optionally fires a named transition, cascades automated transitions.
2. **`ManualTransition(entity, transitionName)`** -- Fires a named transition on an existing entity, then cascades.
3. **`Loopback(entity)`** -- Re-evaluates automated transitions from the current state without firing a specific transition. Used when entity data is updated by a processor callback and the workflow should re-check conditions.

### 5.3 Cascade Logic

After any transition fires, the engine cascades: it scans all `AUTOMATIC` transitions from the new state and fires the first whose criteria match. This continues until no automatic transition matches or a safety limit is hit.

**Loop protection:**

- `maxStateVisits` (default 10, configurable via `CYODA_MAX_STATE_VISITS`): Per-state visit counter. If the entity visits the same state more than `maxStateVisits` times during a single engine invocation, the cascade stops.
- `maxCascadeDepth` (absolute limit: 100): Total cascade steps across all states. Prevents runaway chains.

### 5.4 Processor Execution

Processors are dispatched via the `ExternalProcessingService` SPI. In multi-node mode, this is the `ClusterDispatcher` (see Section 4.3). Three execution modes are defined in the Cyoda model:

| Mode | Behavior |
|------|----------|
| `SYNC` | Processor executes within the current transaction. Entity data is updated in-place before the next transition. |
| `ASYNC_SAME_TX` | Processor executes asynchronously but joins the same transaction. CRUD callbacks are routed back to the transaction owner. |
| `ASYNC_NEW_TX` | Processor executes sequentially within a SAVEPOINT of the parent transaction. Fire-and-forget error semantics: failure rolls back the SAVEPOINT only, parent pipeline continues. Entity mutations returned by the processor are discarded. Parent rollback discards all ASYNC_NEW_TX work. The `ASYNC` label is preserved for Cyoda Cloud configuration compatibility — execution is sequential in cyoda-go. See canonical semantics: `docs/superpowers/specs/2026-04-01-workflow-processor-execution-design.md` |

### 5.5 Audit Trail

The engine records state machine events to `StateMachineAuditStore` throughout execution. 13 event types:

| Event Type | Constant | Meaning |
|------------|----------|---------|
| `STATE_MACHINE_START` | `SMEventStarted` | Engine invocation begins |
| `STATE_MACHINE_FINISH` | `SMEventFinished` | Engine invocation completes |
| `CANCEL` | `SMEventCancelled` | Engine cancelled |
| `FORCE_SUCCESS` | `SMEventForcedSuccess` | Forced successful completion |
| `WORKFLOW_FOUND` | `SMEventWorkflowFound` | Matching workflow selected |
| `WORKFLOW_NOT_FOUND` | `SMEventWorkflowNotFound` | No workflow matches |
| `WORKFLOW_SKIP` | `SMEventWorkflowSkipped` | Workflow criterion not matched |
| `TRANSITION_MAKE` | `SMEventTransitionMade` | Transition fired |
| `TRANSITION_NOT_FOUND` | `SMEventTransitionNotFound` | Named transition not in workflow |
| `TRANSITION_NOT_MATCH_CRITERION` | `SMEventTransitionCriterionNoMatch` | Transition criterion failed |
| `PROCESS_NOT_MATCH_CRITERION` | `SMEventProcessCriterionNoMatch` | Processor criterion failed |
| `PAUSE_FOR_PROCESSING` | `SMEventProcessingPaused` | Waiting for async processor |
| `STATE_PROCESS_RESULT` | `SMEventStateProcessResult` | Processor result received |

---

## 6. gRPC & Externalized Processing

### 6.1 CloudEventsService

The gRPC service implements 6 RPCs matching the Cyoda platform:

```protobuf
service CloudEventsService {
    rpc SendCloudEvent(CloudEvent) returns (CloudEvent);
    rpc SendCloudEventStream(stream CloudEvent) returns (CloudEvent);
    rpc ReceiveCloudEventStream(CloudEvent) returns (stream CloudEvent);
    rpc StartStreaming(stream CloudEvent) returns (stream CloudEvent);
    rpc SendCloudEvents(stream CloudEvent) returns (CloudEvent);
    rpc ReceiveCloudEvents(CloudEvent) returns (stream CloudEvent);
}

```

`StartStreaming` is the primary RPC -- a bidirectional stream used for the full member lifecycle.

### 6.2 Member Lifecycle

```
join --> greet --> keep-alive --> dispatch/response --> leave
```

1. **Join:** Client sends `CalculationMemberJoinEvent` as first message. Server registers member in `MemberRegistry`, extracts tags and tenant from payload. Returns `CalculationMemberGreetEvent` with assigned member ID.

2. **Keep-alive:** Server sends `CalculationMemberKeepAliveEvent` at configurable interval (default 10s). Client must respond with a keep-alive within the timeout (default 30s). If not, the server considers the member dead and unregisters it.

3. **Dispatch/Response:** Server sends `EntityProcessorCalculationRequest` or `EntityCriteriaCalculationRequest`. Client processes and returns the corresponding `Response` type. Correlation is by `requestID` field in the CloudEvent payload.

4. **Leave:** Stream closes (client disconnect or server eviction). `MemberRegistry.Unregister()` is called, which fails all pending requests for that member.

### 6.3 Tag-Based Member Selection

`MemberRegistry.FindByTags(tenantID, tagsCSV)` returns the first member matching the tenant whose tags overlap with the required tags (CSV comparison). If `tagsCSV` is empty, any member for that tenant matches.

### 6.4 Response Correlation

Each dispatch request generates a unique `requestID` (TimeUUID). The dispatcher:

1. Creates a buffered channel: `member.TrackRequest(requestID) -> chan *ProcessingResponse`
2. Sends the CloudEvent to the member's stream
3. Waits on the channel with a configurable timeout

When the member responds, the streaming handler matches the response's `requestID` to the pending channel and delivers the result. If the member disconnects, `FailAllPending()` sends error responses to all waiting channels.

### 6.5 CloudEvent Types

**Streaming/calculation:** `CalculationMemberJoinEvent`, `CalculationMemberGreetEvent`, `CalculationMemberKeepAliveEvent`, `EntityProcessorCalculationRequest/Response`, `EntityCriteriaCalculationRequest/Response`, `EventAckResponse`

**Entity management:** `EntityCreateRequest`, `EntityCreateCollectionRequest`, `EntityUpdateRequest`, `EntityUpdateCollectionRequest`, `EntityTransactionResponse`, `EntityDeleteRequest/Response`, `EntityDeleteAllRequest/Response`, `EntityTransitionRequest/Response`

**Model management:** `EntityModelImportRequest/Response`, `EntityModelExportRequest/Response`, `EntityModelTransitionRequest/Response`, `EntityModelDeleteRequest/Response`, `EntityModelGetAllRequest/Response`

**Search/query:** `EntityGetRequest`, `EntityGetAllRequest`, `EntitySnapshotSearchRequest/Response`, `EntityResponse`, `SnapshotCancelRequest`, `SnapshotGetRequest`, `SnapshotGetStatusRequest`, `EntitySearchRequest`, `EntityStatsGetRequest/Response`, `EntityStatsByStateGetRequest/Response`, `EntityChangesMetadataGetRequest/Response`

---

## 7. Authentication & Authorization

Two modes, selected via `CYODA_IAM_MODE`:

### 7.1 Mock Mode (default)

`mockiam.NewAuthenticationService(defaultUser)` -- returns a fixed `UserContext` for every request. Used for development and testing.

Default mock user: `mock-user-001`, tenant `mock-tenant`, roles `[ROLE_ADMIN, ROLE_M2M]` (override via `CYODA_IAM_MOCK_ROLES`). The defaults grant admin HTTP access and gRPC streaming (which requires `ROLE_M2M`).

### 7.2 JWT Mode

Full RS256 JWT authentication with JWKS discovery and M2M client support.

**Components:**

| Component | Purpose |
|-----------|---------|
| `AuthService` | Wires all auth components, exposes HTTP handlers |
| `InMemoryKeyStore` | Manages RSA key pairs (active signing key + rotated keys) |
| `TrustedKeyStore` | Stores trusted external public keys (in-memory or KV-backed) |
| `InMemoryM2MClientStore` | Machine-to-machine client credentials |
| `JWKSHandler` | `GET /.well-known/jwks.json` -- standard JWKS endpoint |
| `TokenHandler` | `POST /oauth/token` -- issues JWTs (client_credentials, OBO exchange) |
| `JWKSValidator` | Validates JWTs against the JWKS endpoint (with caching) |
| `DelegatingAuthenticator` | Implements `spi.AuthenticationService`, delegates to validator |

**Deterministic KID derivation:**

```go
pubDER, _ := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
kidHash := sha256.Sum256(pubDER)
kid := hex.EncodeToString(kidHash[:16])  // first 16 bytes of SHA-256
```

This is critical for multi-node clusters: all nodes sharing the same RSA private key produce the same KID. Any node can validate tokens issued by any other node without key synchronization.

**OBO (On-Behalf-Of) exchange:** A compute member authenticated via M2M credentials can exchange its token for an OBO token carrying the original user's tenant and identity. This allows CRUD callbacks to carry the correct authorization context.

**Bootstrap M2M client:** Optionally created at startup via `CYODA_BOOTSTRAP_CLIENT_ID`. Secret is either provided via `CYODA_BOOTSTRAP_CLIENT_SECRET` or auto-generated and printed to stdout.

### 7.3 Authorization

Currently `mockiam.NewAuthorizationService()` -- a permissive stub. The gRPC streaming endpoint enforces `ROLE_M2M` for calculation members.

---

## 8. Error Model

### 8.1 Three-Tier Classification

```go
type ErrorLevel int
const (
    LevelOperational ErrorLevel = iota  // 4xx client errors
    LevelInternal                       // 500 unexpected errors
    LevelFatal                          // 500 + marks system unhealthy
)
```

| Tier | HTTP Status | Client Detail | Logging |
|------|-------------|---------------|---------|
| Operational | 4xx | Full domain error code + message | INFO |
| Internal | 500 | Generic message + ticket UUID | ERROR with ticket + full detail |
| Fatal | 500 | Generic message + ticket UUID | ERROR "FATAL" with ticket + full detail |

### 8.2 RFC 9457 Problem Details

All errors are returned as `application/problem+json`:

```go
type ProblemDetail struct {
    Type     string         `json:"type"`
    Title    string         `json:"title"`
    Status   int            `json:"status"`
    Detail   string         `json:"detail,omitempty"`
    Instance string         `json:"instance"`
    Ticket   string         `json:"ticket,omitempty"`
    Props    map[string]any `json:"properties,omitempty"`
}
```

Props always include `errorCode`. For conflict errors, `retryable: true` is set.

In `verbose` mode (`CYODA_ERROR_RESPONSE_MODE=verbose`), internal error details are included in responses. In `sanitized` mode (default), only the ticket UUID is exposed.

### 8.3 Error Code Taxonomy

**Domain errors:**

| Code | Meaning |
|------|---------|
| `MODEL_NOT_FOUND` | Requested model does not exist |
| `MODEL_NOT_LOCKED` | Model must be locked before entity operations |
| `ENTITY_NOT_FOUND` | Requested entity does not exist |
| `VALIDATION_FAILED` | Entity data fails model schema validation |
| `TRANSITION_NOT_FOUND` | Named transition not in workflow |
| `WORKFLOW_FAILED` | Workflow engine error |
| `CONFLICT` | Optimistic concurrency conflict (retryable) |
| `BAD_REQUEST` | Malformed request |
| `UNAUTHORIZED` | Missing or invalid credentials |
| `FORBIDDEN` | Insufficient permissions |
| `SERVER_ERROR` | Internal server error (with ticket) |

**Cluster errors:**

| Code | Meaning |
|------|---------|
| `TRANSACTION_NODE_UNAVAILABLE` | Transaction owner node is dead/unreachable |
| `TRANSACTION_EXPIRED` | Transaction token TTL elapsed |
| `TRANSACTION_NOT_FOUND` | Transaction ID unknown on this node |
| `CLUSTER_NODE_NOT_REGISTERED` | Node not in gossip membership |
| `IDEMPOTENCY_CONFLICT` | Duplicate idempotency key |

**Dispatch errors:**

| Code | Meaning |
|------|---------|
| `NO_COMPUTE_MEMBER_FOR_TAG` | No compute member matches required tags |
| `DISPATCH_FORWARD_FAILED` | Cross-node dispatch HTTP failure |
| `DISPATCH_TIMEOUT` | Dispatch response not received in time |
| `COMPUTE_MEMBER_DISCONNECTED` | Compute member stream closed during dispatch |

### 8.4 Warning/Error Accumulation

```go
common.AddWarning(ctx, "message")
common.AddError(ctx, "message")
```

Warnings and errors are accumulated in the request context and propagated to the caller. Processor/criteria response warnings are prefixed with the processor/criteria name and added to the context. Surfaced in gRPC `warnings` array and HTTP response body.

---

## 9. Configuration Reference

All values configurable via environment variables with the `CYODA_` prefix. Plugin-specific variables use the plugin's name as a secondary namespace (`CYODA_POSTGRES_*`, `CYODA_CASSANDRA_*`). `./cyoda-go --help` on any binary renders the variables for the plugins it ships with — the help text is generated at runtime from the registered plugins' `ConfigVars()`.

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_HTTP_PORT` | `8080` | HTTP server listen port |
| `CYODA_CONTEXT_PATH` | `/api` | URL prefix for all API routes |
| `CYODA_ERROR_RESPONSE_MODE` | `sanitized` | `sanitized` or `verbose` (dev only) |
| `CYODA_MAX_STATE_VISITS` | `10` | Per-state visit limit for cascade loop protection |
| `CYODA_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |

### Storage — plugin selection

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_STORAGE_BACKEND` | `memory` | Name of the active plugin. Must match a registered plugin (one of those blank-imported by the binary's `main.go`). Unknown names fail fast at startup with a listing of available plugins. |

Per-store routing is **not supported** — a running binary uses one plugin for all stores. Mixing backends per store type is by design not part of the plugin contract.

### PostgreSQL plugin (`CYODA_STORAGE_BACKEND=postgres`)

Advertised via `DescribablePlugin.ConfigVars()`; rendered in the binary's `--help`.

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_POSTGRES_URL` | (none, **required**) | PostgreSQL connection string |
| `CYODA_POSTGRES_MAX_CONNS` | `25` | Maximum pool connections |
| `CYODA_POSTGRES_MIN_CONNS` | `5` | Minimum pool connections |
| `CYODA_POSTGRES_MAX_CONN_IDLE_TIME` | `5m` | Max idle time before connection is closed |
| `CYODA_POSTGRES_AUTO_MIGRATE` | `true` | Run embedded SQL migrations at startup |

### Cassandra plugin (`CYODA_STORAGE_BACKEND=cassandra`, available only in the `cyoda-go-cassandra` binary)

The cassandra plugin has the largest config surface of any shipped plugin. Only the most common operator-facing options are listed here; the plugin's `ConfigVars()` (rendered in `--help`) is authoritative.

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_CASSANDRA_CONTACT_POINTS` | (none, **required**) | Comma-separated `host:port` list |
| `CYODA_CASSANDRA_KEYSPACE` | `cyoda_go` | Keyspace name |
| `CYODA_CASSANDRA_REPLICATION` | SimpleStrategy RF=3 | CQL replication strategy JSON |
| `CYODA_CASSANDRA_NUM_SHARDS` | `32` | Virtual shard count (immutable after first use) |
| `CYODA_CASSANDRA_AUTO_MIGRATE` | `true` | Run schema migrations at startup |
| `CYODA_CASSANDRA_CONSISTENCY_LEVEL` | `QUORUM` | CQL consistency level for reads and writes |
| `CYODA_CASSANDRA_MAX_CONCURRENT_WRITES` | `32` | Max concurrent CQL writes per node |
| `CYODA_CASSANDRA_USERNAME` | (none) | Auth username (optional) |
| `CYODA_CASSANDRA_PASSWORD` | (none) | Auth password (optional) |
| `CYODA_REDPANDA_BROKERS` | (none, **required**) | Comma-separated broker list (Kafka-compatible) |
| `CYODA_REDPANDA_CONSUMER_GROUP` | `cyoda-go` | Consumer group identifier |
| `CYODA_CASSANDRA_REDPANDA_SESSION_TIMEOUT_SEC` | `30` | Consumer session timeout |

### IAM

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_IAM_MODE` | `mock` | `mock` (dev) or `jwt` (production) |
| `CYODA_JWT_SIGNING_KEY` | (none) | PEM-encoded RSA private key (or base64-encoded PEM) |
| `CYODA_JWT_ISSUER` | `cyoda-go` | JWT issuer claim |
| `CYODA_JWT_EXPIRY_SECONDS` | `3600` | Token expiry in seconds |

### Bootstrap

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_BOOTSTRAP_CLIENT_ID` | (none) | M2M client ID to create at startup |
| `CYODA_BOOTSTRAP_CLIENT_SECRET` | (none) | M2M client secret (generated if empty) |
| `CYODA_BOOTSTRAP_TENANT_ID` | `default-tenant` | Tenant for bootstrap client |
| `CYODA_BOOTSTRAP_USER_ID` | `admin` | User ID for bootstrap client |
| `CYODA_BOOTSTRAP_ROLES` | `ROLE_ADMIN,ROLE_M2M` | Comma-separated roles |

### gRPC

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_GRPC_PORT` | `9090` | gRPC server listen port |
| `CYODA_KEEPALIVE_INTERVAL` | `10` | Keep-alive ping interval (seconds) |
| `CYODA_KEEPALIVE_TIMEOUT` | `30` | Keep-alive timeout before eviction (seconds) |

### Cluster

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_CLUSTER_ENABLED` | `false` | Enable multi-node cluster mode |
| `CYODA_NODE_ID` | (none) | Stable unique node identifier (required if cluster enabled) |
| `CYODA_NODE_ADDR` | `http://localhost:8080` | This node's reachable HTTP address (must include scheme) |
| `CYODA_GOSSIP_ADDR` | `:7946` | Memberlist gossip bind address |
| `CYODA_SEED_NODES` | (none) | Comma-separated `host:port` for gossip seeds |
| `CYODA_GOSSIP_STABILITY_WINDOW` | `2s` | Wait for stable membership count after join |
| `CYODA_TX_TTL` | `60s` | Transaction idle timeout |
| `CYODA_TX_REAP_INTERVAL` | `10s` | Frequency of transaction TTL reaper |
| `CYODA_PROXY_TIMEOUT` | `30s` | HTTP proxy response header timeout |
| `CYODA_TX_OUTCOME_TTL` | `5m` | How long completed transaction outcomes are retained |
| `CYODA_HMAC_SECRET` | (none) | Hex-encoded secret for token signing + gossip encryption (required if cluster enabled) |
| `CYODA_DISPATCH_WAIT_TIMEOUT` | `5s` | How long to poll for a compute member with matching tags |
| `CYODA_DISPATCH_FORWARD_TIMEOUT` | `30s` | HTTP timeout for cross-node dispatch forwarding |

### Search

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_SEARCH_SNAPSHOT_TTL` | `1h` | TTL for async search job results |
| `CYODA_SEARCH_REAP_INTERVAL` | `5m` | Frequency of search snapshot reaper |

---

## 10. Deployment Architecture

### 10.1 Single-Node

```bash
# Direct
go build -o bin/cyoda-go ./cmd/cyoda-go
./bin/cyoda-go

# Docker (with PostgreSQL)
./cyoda-go-docker.sh
```

The Docker script generates a fresh JWT signing key, writes `.env.docker`, and runs `docker compose up`. PostgreSQL is started as a sidecar container.

### 10.2 Multi-Node Cluster

```bash
# Start a 3-node cluster with nginx load balancer
./cyoda-go-docker.sh --nodes 3
```

Architecture:

```
                    +-----------+
     Client ------->|  nginx LB |
                    +-----------+
                    /     |      \
              +------+ +------+ +------+
              |Node 1| |Node 2| |Node 3|
              +------+ +------+ +------+
                  \       |       /
                   +------+------+
                   | PostgreSQL  |
                   +-------------+
```

- **nginx:** Round-robin load balancer. Proxies `/api/*` paths only. Internal paths (`/internal/*`) are not exposed.
- **Gossip:** Each node runs a memberlist listener on a distinct port. Seed nodes are configured so all nodes discover each other.
- **Shared PostgreSQL:** All nodes connect to the same PostgreSQL instance. `SERIALIZABLE` isolation + RLS ensure correctness.
- **Shared secrets:** All nodes share the same HMAC secret (for token verification and gossip encryption) and the same JWT signing key (for deterministic KID derivation).

**Scripts:**

| Script | Purpose |
|--------|---------|
| `scripts/multi-node-docker/start-cluster.sh` | Generate secrets, nginx config, docker-compose, start cluster |
| `scripts/multi-node-docker/stop-cluster.sh` | Stop and clean up cluster containers |

The start script:
1. Generates secrets once, persists to `.env` (reused on restart)
2. Generates nginx config with upstream entries for N nodes
3. Generates `docker-compose.yml` with N node services + postgres + nginx
4. Runs `docker compose up`

---

## 11. Observability

OpenTelemetry is integrated end-to-end. The OTel SDK is initialised in `internal/observability/init.go` with OTLP HTTP exporters for traces and metrics. W3C Trace Context and Baggage propagation are configured as the default global propagator.

**HTTP middleware:** the generated API router is wrapped in `otelhttp.NewMiddleware` (enabled when `CYODA_OTEL_ENABLED=true`), producing `http.server` spans for every request and auto-extracting upstream trace context from `traceparent` headers.

**Transaction manager decorator:** `TracingTransactionManager` wraps the underlying transaction manager and adds spans (`tx.begin`, `tx.commit`, `tx.rollback`, `tx.savepoint`) plus metrics (`cyoda.tx.duration`, `cyoda.tx.active`, `cyoda.tx.conflicts`).

**Workflow and dispatch:** spans for `workflow.execute`, `workflow.manual_transition`, `workflow.loopback`; `dispatch.processor` and `dispatch.criteria` with `cyoda.dispatch.duration` and `cyoda.dispatch.count` metrics.

**Plugin-level instrumentation:** plugins are free to add their own spans and metrics under a plugin-specific namespace (e.g., `cyoda.cassandra.cql.duration`, `cyoda.cassandra.batch.duration`, `cyoda.cassandra.limiter.*`, commit-protocol phase spans). The `memory` and `postgres` plugins emit minimal plugin-level telemetry (their behavior is well-captured by the core transaction/workflow/dispatch spans); the `cassandra` plugin adds detailed CQL, batch, and commit-protocol instrumentation because its hot-path semantics warrant finer visibility.

**Exporter endpoint:** `OTEL_EXPORTER_OTLP_ENDPOINT` (standard OTel env var). The bundled docker setup ships a Grafana / Prometheus / Tempo stack via `grafana/otel-lgtm` with a pre-provisioned `Cyoda-Go Overview` dashboard covering HTTP, transactions, and workflow/dispatch.

**Runtime sampler control.** The trace sampler is swappable at runtime via `POST /api/admin/trace-sampler` (requires `ROLE_ADMIN`), mirroring `/api/admin/log-level`. Operators can toggle between 100% sampling, probabilistic sampling, and off without restarting the service. The initial sampler honors the standard OTel env vars `OTEL_TRACES_SAMPLER` and `OTEL_TRACES_SAMPLER_ARG` at startup.

**Known gaps (carried forward from `cyoda-light-go`):** trace context propagation through the cassandra plugin's broker messages, search pipeline, and external-processor gRPC/CloudEvents is incomplete. Issues will be re-opened in their respective repositories when scheduled.

---

## 12. Planned Features (Not Yet Implemented)

Items carried forward from the `cyoda-light-go` predecessor repository. Issues will be re-opened in the appropriate repository (`cyoda-go` for core, `cyoda-go-cassandra` for cassandra plugin) when each item is scheduled.

### Carried to `cyoda-go`

| Feature | Purpose |
|---------|---------|
| Commit markers (PostgreSQL plugin) | Resolve transaction commit ambiguity (L5 partition at COMMIT — see Section 4.5 Phase 4) |
| Strict context deadline propagation | Ensure all downstream operations inherit the request deadline |
| Multi-node E2E tests with proxy routing | Automated testing of the full cluster topology |
| Batch `SaveResults` with `pgx.CopyFrom` (PostgreSQL plugin) | Performance optimization for async search result insertion |
| Idempotency keys | Client-provided keys to prevent duplicate operations on retry |
| Plugin conformance test suite (`cyoda-go-spi/spitest/`) | Shared behavioral conformance harness any plugin can run against its own `StoreFactory`. Scheduled after the cassandra plugin extraction. |

### Carried to `cyoda-go-cassandra`

| Feature | Purpose |
|---------|---------|
| HLC monotonicity fix on shard takeover | Close the clock-gossip monotonicity gap documented in ADRs 0001 and 0002 (see Plan 4 in the storage plugin spec) |
| Parallelize version-check publish + await | Cut commit latency for large collection saves |
| Bound shard takeover scans | Make startup time data-independent |
| Parallelize `MutationBatch.ExecuteChunked` | Cut materialize latency for large writes |
| Trace context propagation through the broker | Unified commit trace waterfall |

### Cross-cutting

| Feature | Purpose |
|---------|---------|
| Trace propagation through search pipeline | Unified search trace waterfall |
| Trace propagation to external processors via gRPC/CloudEvents | End-to-end workflow tracing |

---

## 13. Design Decisions Log

### DD-1: HMAC Token + Separate UUID

**Context:** How to route requests to the correct transaction-owning node.

**Decision:** HMAC-signed opaque token containing `{nodeID, txRef, expiresAt}`. The `txRef` is a separate UUID used as a key into the node's local transaction map. The `nodeID` is extracted locally (no network call) for routing.

**Rationale:** The token is opaque to clients. HMAC verification is a CPU-local operation. No distributed registry lookup is needed for routing decisions.

### DD-2: Fencing Tokens Not Required

**Context:** Whether to use fencing tokens to prevent stale writes from zombie transactions.

**Decision:** Not required. The `pgx.Tx` single-owner property guarantees that only one goroutine on one node holds a physical PostgreSQL transaction. If the owning node dies, PostgreSQL rolls back the transaction automatically via idle timeout.

**Rationale:** Fencing tokens solve a problem that does not exist here. There is no mechanism for two nodes to hold the same transaction. The lifecycle manager provides TTL, registry, and observability without the complexity of fencing.

### DD-3: Transparent Proxy

**Context:** How to handle requests that arrive at the wrong node.

**Decision:** HTTP middleware (`proxy.HTTPRouting`) uses `httputil.ReverseProxy` to transparently forward requests to the correct node. The target node sees the original request with all headers intact.

**Rationale:** Minimizes client complexity. The client does not need to know about cluster topology. The proxy is a standard reverse proxy pattern with connection pooling.

### DD-4: Gossip Over PostgreSQL for Registry

**Context:** How nodes discover each other.

**Decision:** HashiCorp memberlist (SWIM gossip) instead of a PostgreSQL-backed registry table.

**Rationale:** Gossip provides sub-second failure detection, requires no additional infrastructure, and scales to the target cluster size (2-20 nodes). A PostgreSQL registry would add polling latency and another failure mode on the critical path.

### DD-5: Operator-Assigned Node IDs

**Context:** How to identify nodes.

**Decision:** Node IDs are stable strings assigned by the operator via `CYODA_NODE_ID`, not auto-generated UUIDs.

**Rationale:** Stable IDs survive restarts, simplify log correlation, and make cluster configuration deterministic. Docker scripts generate them as `node-1`, `node-2`, etc.

### DD-6: Random Peer Selection

**Context:** How to pick among multiple peers with matching compute tags.

**Decision:** `RandomSelector` -- uniform random selection from alive candidates.

**Rationale:** Simple, stateless, no coordination needed. Load balancing across peers is acceptable for the expected cluster size. More sophisticated strategies (round-robin, least-loaded) can be added by implementing the `PeerSelector` interface.

### DD-7: Gossip Metadata for Tag Discovery

**Context:** How to find which node has a compute member with the required tags.

**Decision:** Each node publishes its compute member tags in gossip metadata, organized per tenant. Tag updates are pushed to memberlist on member join/leave and propagated via SWIM gossip.

**Rationale:** Avoids a centralized registry. Tag lookups are local memory reads against the gossip view. Convergence is within milliseconds for LAN configurations.

### DD-8: HTTP for Dispatch Forwarding

**Context:** What protocol to use for cross-node dispatch forwarding.

**Decision:** HTTP POST to `/internal/dispatch/processor` and `/internal/dispatch/criteria`, authenticated with HMAC-SHA256.

**Rationale:** Reuses the existing HTTP infrastructure. The dispatch payload is a single request-response pair (not a stream), making HTTP a natural fit. HMAC authentication reuses the cluster secret without adding TLS complexity for internal traffic.

### DD-9: Poll-Based Wait for Missing Compute Members

**Context:** What to do when no compute member matches the required tags.

**Decision:** Poll gossip metadata every 200ms for up to `CYODA_DISPATCH_WAIT_TIMEOUT` (default 5s).

**Rationale:** Compute members may be joining. A brief wait avoids spurious failures during cluster startup or member reconnection. The 200ms interval is short enough to be responsive but does not hammer the gossip view. After the timeout, the failure is deterministic.

## 14. Non-Functional Limits and Design Boundaries

This section describes where Cyoda-Go is expected to encounter limits. These are not bugs — they are the explicit trade-offs of the architecture. Understanding them is essential for sizing, capacity planning, and deciding when Cyoda-Go is the right tool vs. a horizontally scalable alternative like Cyoda Cloud.

### 14.1 Horizontal Scalability

**Design boundary:** Cyoda-Go targets 3–10 node clusters. It is not limitlessly horizontally scalable.

| Dimension | Scaling Behavior | Limit |
|-----------|-----------------|-------|
| **Node count** | Linear improvement in compute dispatch capacity (more nodes = more compute members). No improvement in write throughput — all writes go through PostgreSQL. | 10–20 nodes practical maximum. Beyond this, gossip metadata size grows (per-tenant tag sets × nodes), and the probability of proxy hops increases. |
| **Write throughput** | Bounded by PostgreSQL SERIALIZABLE isolation. Every transaction holds a `pgx.Tx` for its full duration (including external compute phases). | Single PG instance is the bottleneck. Connection pool default is 25 per node; with 10 nodes that's 250 concurrent PG connections. Long-held transactions reduce effective throughput. |
| **Read throughput** | Scales with node count for non-transactional reads (entity queries, search). Each node can serve reads independently from PG. | Bounded by PG read capacity. Point-in-time queries require version table scans. |
| **Compute throughput** | Scales with compute member count across the cluster. Each node can host multiple compute members. Cross-node dispatch adds one HTTP hop (~1ms intra-cluster). | Bounded by compute member availability per tag. If only one node has a member for a given tag, that node is the bottleneck for that tag. |

**Contrast with Cyoda Cloud:** Cyoda Cloud uses a fully distributed storage layer (Cassandra) with no single-node write bottleneck. Cyoda-Go trades unlimited write scalability for PostgreSQL's stronger isolation guarantees and simpler operational model.

### 14.2 Transaction Timing and Duration

**Design boundary:** Transactions are held open for the full workflow execution, including external compute phases.

| Constraint | Value | Consequence |
|------------|-------|-------------|
| **Transaction TTL** | Default 60s (configurable via `CYODA_TX_TTL`) | Workflow chains that exceed TTL are reaped. Long-running processors must complete within this window. |
| **PG idle_in_transaction_session_timeout** | Should match or exceed TTL | PostgreSQL will kill transactions that idle beyond this limit, regardless of the application-level TTL. |
| **Connection hold time** | Duration of entire flow chain (BEGIN → workflow → compute dispatch → callbacks → COMMIT) | Each in-flight transaction consumes one PG connection for its full lifetime. With 25 connections per node and 10 nodes, the cluster supports ~250 concurrent transactions. |
| **Proxy timeout** | Default 30s (configurable) | Cross-node proxy hops for CRUD callbacks must complete within this window. |
| **Dispatch forward timeout** | Default 30s (configurable) | Cross-node compute dispatch forwarding must complete within this window. |
| **Compute member response timeout** | Per-processor configurable (default 30s) | If a compute member doesn't respond within this window, the dispatch fails and the transaction rolls back. |

**Expected bottleneck:** The most common performance issue will be long-running compute phases holding PG connections. A processor that takes 10 seconds holds one PG connection for 10+ seconds. With 25 connections and 10-second processors, a single node can sustain ~2.5 new transactions per second.

**Mitigation:** The `ASYNC_NEW_TX` execution mode runs processors within a SAVEPOINT of the parent transaction. ASYNC_NEW_TX failures do not affect the parent pipeline — the SAVEPOINT is rolled back and execution continues. This allows non-critical side-effect processors to fail without aborting the entire cascade. In cyoda-go, all processors execute sequentially (the `ASYNC` label exists for Cyoda Cloud configuration compatibility). The PG connection hold time includes all processors regardless of mode.

### 14.3 Data Volume Limits

| Dimension | Practical Limit | Reason |
|-----------|----------------|--------|
| **Entity size** | ~10 MB per entity (HTTP body limit) | Entity data is stored as JSONB in PostgreSQL. Very large entities degrade query performance and increase replication lag. |
| **Entities per model** | Millions (PostgreSQL) | Bounded by PG table size and query performance. Point-in-time queries scan `entity_versions` which grows with write volume. Indexing helps but doesn't eliminate the cost. |
| **Entity version history** | Unbounded (append-only) | The `entity_versions` table grows monotonically. No built-in compaction or archival. Long-lived entities with frequent updates will accumulate large version histories. |
| **Concurrent models** | Hundreds | Model metadata is small. No practical limit from the storage layer. |
| **Search result sets** | Tens of thousands | Async search stores entity IDs (not data), so the results table is compact. But re-fetching entity data on read means page retrieval cost scales with page size × entity fetch cost. |
| **In-memory mode** | Limited by process heap | Single-node standalone only (not multi-node compatible). All entities, versions, models, search results held in process memory. Intended for rapid development and agentic application engineering. Not for production data volumes. |

### 14.4 Fault Tolerance and Reliability

| Scenario | Behavior | Recovery |
|----------|----------|----------|
| **Node crash** | PG rolls back all open transactions on that node. Gossip detects failure within seconds. Other nodes see `TRANSACTION_NODE_UNAVAILABLE` for in-flight tokens. | Automatic. Clients retry with new transactions on surviving nodes. No data loss (uncommitted work was never durable). |
| **Node network partition (from cluster)** | Partitioned node continues operating if it can reach PG. Other nodes cannot proxy to it. Transactions owned by the partitioned node continue normally if PG link is up. | Gossip re-merges when partition heals. Outstanding tokens for the partitioned node fail on other nodes. |
| **Node partition from PostgreSQL** | PG kills the connection after TCP timeout. All open transactions on that node are rolled back by PG. Node detects dead connection on next PG operation. | Node must reconnect to PG. All in-flight work is lost (rolled back). Clients get errors and retry. |
| **PostgreSQL failure** | All nodes lose write capability simultaneously. No new transactions can begin. Existing transactions cannot commit. | Requires PG recovery (HA failover, restart). Cyoda-Go nodes reconnect automatically via pgx pool. |
| **Compute member disconnect** | Pending dispatch requests fail with "member disconnected." Gossip tag metadata updated within seconds. Subsequent dispatches route to other members with the same tag. | Automatic if other members exist. If no member for the tag, dispatches fail after poll timeout. |
| **nginx LB failure** | All external traffic stops. Nodes are healthy but unreachable. | LB must be restored. Nodes continue gossiping and can handle direct traffic if clients bypass the LB. |

**Single point of failure:** PostgreSQL. If PG is down, the cluster is effectively down for writes. This is by design — PG is the consistency authority. HA PostgreSQL (streaming replication with automatic failover) is the recommended mitigation.

**No split-brain:** The `pgx.Tx` single-owner property ensures that no two nodes can commit the same transaction. PostgreSQL SERIALIZABLE isolation catches conflicting concurrent writes from different transactions. There is no application-level consensus needed because PG is the sole arbiter.

### 14.5 Consistency Guarantees and Caveats

| Guarantee | Strength | Caveat |
|-----------|----------|--------|
| **Read-your-own-writes** | Strong (within a transaction) | Guaranteed by `pgx.Tx` — all reads within a transaction see its own buffered writes. Across transactions, reads are snapshot-isolated. |
| **Snapshot isolation** | Strong (SERIALIZABLE in PG) | PG SERIALIZABLE may abort transactions with serialization failures (error 40001). The application retries. Under high contention, retry storms are possible. |
| **Cross-node consistency** | Strong (PG is the authority) | All nodes share the same PG instance. There is no eventual consistency between nodes — they all see the same data at the same isolation level. Gossip metadata (node registry, compute tags) is eventually consistent with sub-second convergence. |
| **Temporal consistency** | Strong (point-in-time queries) | `GetAsAt` returns the entity as it was at a specific timestamp. Accuracy depends on PG clock precision (microsecond) and correct use of `transaction_time` vs `wall_clock_time`. |
| **Commit ambiguity** | **Gap** (planned: #56) | If the network partitions between Node A and PG at COMMIT time, Node A cannot determine whether PG committed or not. The planned commit marker (#56) will resolve this. Until then, the client may see a false failure for a transaction that actually committed. |
| **Idempotency** | **Gap** (planned) | Client retries after timeout may create duplicate entities. There is no built-in idempotency key mechanism. Until implemented, clients must handle deduplication at the application level. |

### 14.6 Operational Limits

| Parameter | Default | Hard Limit | Notes |
|-----------|---------|------------|-------|
| PG connections per node | 25 | Configurable, bounded by PG `max_connections` | Each in-flight transaction holds one connection. |
| Gossip metadata size | ~100 bytes per node (without tags) | memberlist `MetaMaxSize` = 512 bytes | With many tenants and many tags, metadata could exceed 512 bytes. Monitor and alert. |
| Search snapshot TTL | 1 hour | Configurable | Snapshots older than TTL are reaped. Increase for long-running batch workflows. |
| Transaction TTL | 60 seconds | Configurable | Must be shorter than PG `idle_in_transaction_session_timeout`. |
| Max state visits per workflow | 10 | Configurable | Prevents infinite loops in workflow cascading. Increase for deeply nested state machines. |
| HTTP body limit | 10 MB | Hardcoded in entity handler | Increase requires code change. |
| gRPC keep-alive interval | 10 seconds | Configurable | Shorter intervals detect compute member failure faster but increase network overhead. |
| Dispatch poll interval | 200 ms | Hardcoded | Polls local gossip metadata (no network I/O). Low overhead. |
| Dispatch wait timeout | 5 seconds | Configurable | Time to wait for a compute member when none is available for the required tag. |

### 14.7 Performance Expectations

These are order-of-magnitude expectations for a 3-node cluster with PostgreSQL on the same network:

| Operation | Expected Latency | Throughput |
|-----------|-----------------|------------|
| Entity create (no workflow) | 5–20 ms | 50–200/s per node |
| Entity create (with sync processor, local compute) | 50–500 ms (dominated by processor) | Bounded by processor speed |
| Entity create (with sync processor, cross-node dispatch) | +1–5 ms over local | One HTTP hop for dispatch forward |
| Entity read (current) | 1–5 ms | 200–1000/s per node |
| Entity read (point-in-time) | 2–10 ms | Depends on version count |
| Sync search (small result set) | 10–100 ms | Bounded by entity count × predicate cost |
| Async search (large result set) | Seconds to minutes | Background, non-blocking |
| Transaction commit (no conflicts) | 1–5 ms | PG-bound |
| Transaction commit (with conflict) | Immediate error (40001) | Client retries |
| Cross-node proxy hop (CRUD callback) | 1–3 ms intra-cluster | Transparent, adds to overall latency |
| Gossip convergence (new member) | 1–3 seconds | Depends on cluster size |

**Key insight for sizing:** The dominant factor in transaction latency is compute phase duration. If processors complete in 100ms, a 3-node cluster with 25 PG connections per node can sustain ~750 concurrent transactions, yielding ~7,500 transactions/second at 100ms each. If processors take 10 seconds, the same cluster sustains ~75 concurrent transactions, yielding ~7.5 transactions/second. Processor speed is the lever.

### DD-10: Store Entity IDs Only in Search Results

**Context:** What to store in async search result tables.

**Decision:** Only entity IDs are stored. Entity data is re-fetched from the entity store when results are read.

**Rationale:** Keeps the results table compact. Avoids data staleness -- the entity may have been updated between search execution and result retrieval. `pointInTime` on the search job ensures deterministic re-fetch.

### DD-11: pointInTime Always Populated

**Context:** Whether `pointInTime` should be optional on search jobs.

**Decision:** Always populated. If the client does not supply one, the service uses `time.Now()`.

**Rationale:** Ensures search results are deterministic. Repeated reads at the same `pointInTime` return the same set. Eliminates an entire class of bugs around "what time was this search as of?"

### DD-12: TTL-Based Cleanup for Both Implementations

**Context:** How to clean up expired search jobs.

**Decision:** Background reaper goroutine with configurable interval and TTL, implemented for both in-memory and PostgreSQL backends.

**Rationale:** Consistent behavior regardless of storage backend. The PostgreSQL implementation leverages `ON DELETE CASCADE` on the foreign key. The in-memory implementation scans and deletes. Both are driven by the same configuration variables.
