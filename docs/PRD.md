# Cyoda-Go: Product Requirements Document

**Version:** 2.0
**Date:** 2026-04-14
**Status:** Target state after the storage-plugin architecture refactor (Plans 1–5). See `docs/superpowers/specs/2026-04-13-storage-plugin-architecture-design.md` for the refactor plan.

---

## 1. Product Vision and Target Use Case

Cyoda-Go is an Entity Database Management System (EDBMS) — a database engine where the first-class abstraction is not a row or document but a *stateful entity* with schema, lifecycle, temporal history, and transactional integrity. Storage is provided by pluggable backends; the stock binary ships with in-memory (default) and PostgreSQL plugins, and third-party plugins can be compiled in to target other storage engines.

### Target Applications

High-complexity, high-consistency enterprise domains where correctness is non-negotiable:

- **Financial ledgers** — double-entry bookkeeping with strict state machine enforcement on journal entries
- **Order management** — multi-stage order lifecycles with automated and manual state transitions, external processor callouts for validation/enrichment
- **Regulatory compliance** — auditable entity histories with point-in-time retrieval for regulatory reporting windows
- **Digital twin orchestration** — behavioral clones of production systems for scenario testing at volumes exceeding production limits

### Scale Profile

Small compute clusters (3-10 stateless Go nodes) with a shared PostgreSQL instance. Active-active high availability — any node can serve any request. Moderate to high throughput bounded by PostgreSQL's write capacity.

> **Storage plugin architecture.** Cyoda-Go's storage layer is a plugin system defined by the stable `cyoda-go-spi` module (stdlib-only Go interfaces and value types). A running binary has exactly one active plugin, selected at startup via `CYODA_STORAGE_BACKEND`. The stock `cyoda-go` binary ships with the `memory` plugin (default, zero external dependencies) and the `postgres` plugin (durable storage with `SERIALIZABLE` isolation). A proprietary `cyoda-go-cassandra` plugin ships as a separate binary for deployments that need horizontal write scalability. Third-party plugins (Redis, ScyllaDB, FoundationDB, etc.) can be authored against `cyoda-go-spi` and compiled into a custom binary via a blank import. See `docs/ARCHITECTURE.md` Section 2 for the plugin contract and Section 9 for configuration.

### Core Value Proposition

Zero-compromise transactional safety. Each transaction holds a `pgx.Tx` handle in a single node's process memory, executing under PostgreSQL's `SERIALIZABLE` isolation. This guarantees:

- **Strict read-your-own-writes** — within a transaction, reads always reflect prior writes from that transaction, even before commit
- **Snapshot isolation** — concurrent transactions see a consistent snapshot, with conflict detection at commit time
- **No distributed transaction coordinator** — PostgreSQL is the single source of truth; no two-phase commit, no Paxos, no Raft

### Cost Model

- A standard HA PostgreSQL instance (managed or self-hosted)
- A small number of stateless Go binaries behind a load balancer
- No ZooKeeper, no etcd, no Kafka, no distributed cache infrastructure

---

## 2. EDBMS Core

### Entities as State Machines

Every entity in Cyoda-Go is a JSON document governed by a finite state machine. An entity is not inert data — it has a current state, follows defined transitions, and enforces lifecycle rules through the workflow engine.

| Property | Description |
|----------|-------------|
| **Identity** | UUID, assigned on creation |
| **Model** | Schema reference (name + version) — entities must conform to a locked model |
| **State** | Current FSM state, managed by the workflow engine |
| **Data** | Arbitrary JSON payload, validated against the model schema |
| **Temporal History** | Append-only version chain — every mutation creates an immutable version |
| **Soft Delete** | Default deletion mode — marks the entity as DELETED, preserving full version history for audit and temporal queries. Reversible via undeletion (planned: [#66](../../issues/66)). |
| **Physical Delete** | A separate process that permanently removes soft-deleted entities and all associated data (versions, audit events), giving fine control to suit compliance requirements. (Planned: [#65](../../issues/65)) |

### Entity Models

Models define the structural schema that entities conform to. They are discovered from sample data, not declared upfront.

**Discovery:** Import sample JSON (or XML) data. The engine infers a tree-structured schema — field names, types, nesting, arrays. Successive imports merge via union: fields accumulate, types widen.

**Lifecycle:**

```
UNLOCKED ──lock──► LOCKED ──unlock──► UNLOCKED
                     │
                     ▼
              Entities may be created
```

- Models must be locked before entities can be created against them
- Locked models cannot be unlocked while entities exist
- Models can only be deleted when unlocked and empty

**Change Levels** — dynamic model extension on ingestion (post-lock):

| Level | Allows |
|-------|--------|
| `STRUCTURAL` | New fields added to schema |
| `TYPE` | Leaf type widening (e.g., int to float) |
| `ARRAY_ELEMENTS` | Array element type widening |
| `ARRAY_LENGTH` | Array width changes only |

**Export Formats:** `JSON_SCHEMA` (standard JSON Schema) and `SIMPLE_VIEW` (lossless internal representation).

### Temporal Integrity

The persistence layer maintains bi-temporal entity versioning:

| Dimension | Semantics |
|-----------|-----------|
| `valid_time` | When the entity version became the "current" truth |
| `transaction_time` | When the version was committed to the database |
| `wall_clock_time` | Physical wall clock at version creation |

**Point-in-time retrieval:** Any entity (or collection) can be queried as it existed at a specific timestamp via `?pointInTime=<ISO8601>`. This applies to single-entity reads, collection reads, and search operations.

**Version history:** Every mutation (create, update, delete) appends a new immutable version. The full history is queryable through the audit trail.

---

## 3. Workflow Engine

The workflow engine is the core differentiator. It enforces that entities are not passive documents but active state machines with auditable, deterministic lifecycle behavior.

### Finite State Machine

Each entity follows a workflow — a directed graph of states connected by transitions. On entity creation, the engine:

1. Selects a matching workflow by evaluating workflow-level selection criteria
2. Places the entity in the workflow's `initialState`
3. Cascades through automated transitions until a stable state is reached (no further automated transition criteria are satisfied)

### Transition Types

| Type | Trigger | Gating |
|------|---------|--------|
| **Automated** | Fires on state entry when criteria are met | Criteria evaluation |
| **Manual** | Explicit API call (`POST /entity/{entityId}/{transition}`) | Criteria evaluation |
| **Loopback** | Self-transition that re-triggers automation from the current state | Criteria evaluation |

### Criteria Types

| Type | Evaluation | Description |
|------|-----------|-------------|
| **Simple** | Local | JSONPath expression + operator + value against entity data |
| **Lifecycle** | Local | Predicate on entity metadata (state, creationDate, previousTransition) |
| **Group** | Local | AND/OR composition with arbitrary nesting depth |
| **Array** | Local | Positional matching against array elements |
| **Function** | Remote | Delegated to external compute node via gRPC CloudEvents |

### Processor Execution Modes

Processors execute during transitions — they transform entity data, perform side effects, or create/modify other entities.

| Mode | Transaction Scope | On Failure | Use Case |
|------|-------------------|------------|----------|
| `SYNC` | Caller's transaction | Rollback all | Validation, enrichment |
| `ASYNC_SAME_TX` | Caller's transaction | Rollback all | Multi-step processing that must be atomic |
| `ASYNC_NEW_TX` | Independent transaction | Caller unaffected | Fire-and-forget side effects |

### Cascade Behavior

Processors may create or mutate other entities, triggering further workflow traversals within the same (or new) transaction as appropriate. Loop protection is enforced via configurable maximum state visits per entity per cascade.

### Workflow Management

- **Import/Export** via REST API
- **Modes:** `MERGE` (additive), `REPLACE` (full swap), `ACTIVATE` (replace + activate)
- **Multiple workflows per model** — workflow-level selection criteria determine which workflow applies to a given entity

### Audit Trail

13 event types track the full state machine narrative:

- Workflow selection (selected, not found)
- Transition attempts (attempted, made, denied, failed)
- Processor execution (dispatched, succeeded, failed)
- Criteria evaluation results
- Cancellation events

Filterable by event type, severity, time range, transaction ID. Cursor-based pagination.

---

## 4. Transaction Model

### ACID Guarantees

Cyoda-Go provides full ACID transactions. Each storage plugin supplies its own `TransactionManager` matching its storage engine's semantics:

| Plugin | Isolation | Conflict Detection | Handle |
|--------|-----------|-------------------|--------|
| **memory** | Serializable Snapshot Isolation (SSI) | Entity-level read set + write set tracking | In-process `Transaction` struct |
| **postgres** | `SERIALIZABLE` (SSI via predicate locks) | PostgreSQL-native (error `40001` mapped to `CONFLICT`) | In-process lifecycle tracker; `pgx.Tx` held per-statement inside stores |
| **cassandra** (proprietary plugin, separate binary) | SSI via custom coordinator + 2PC over a message broker | Per-entity version checks, HLC fencing, shard-epoch LWT | Coordinator-managed, multi-phase |

### Transaction Lifecycle

```
BEGIN ──► READ/WRITE ──► COMMIT
  │                        │
  │         conflict ◄─────┘
  │            │
  └──► ROLLBACK ◄──── timeout (TTL reaper)
```

1. **Begin** — Snapshot time captured. In-memory: empty read/write sets. PostgreSQL: `BEGIN SERIALIZABLE`.
2. **Read** — Check transaction buffer first (read-your-own-writes), then snapshot view.
3. **Write** — Buffered in transaction. Not visible to other transactions until commit.
4. **Commit** — Conflict detection. In-memory: check concurrent writes against read/write sets. PostgreSQL: native SSI validation. Atomic flush on success.
5. **Rollback** — Discard buffer (in-memory) or `ROLLBACK` (PostgreSQL).

### Read-Your-Own-Writes

Within a transaction, all reads reflect prior writes from that transaction, even before commit. This is critical for processor cascade correctness — a processor that creates entity B must be able to read entity B in the same transaction.

**Implementation:** The transaction maintains a write buffer. Read operations check the buffer before the underlying store. This holds for both in-memory and PostgreSQL backends (PostgreSQL's `SERIALIZABLE` isolation provides this natively; the in-memory backend replicates it via explicit buffering).

### Conflict Detection

**In-Memory SSI:** On commit, scan the committed transaction log for transactions committed after our snapshot time. If any committed transaction's write set intersects our read set or write set, abort with `CONFLICT` (409, `retryable: true`).

**PostgreSQL:** Native SSI predicate locking. PostgreSQL raises a serialization failure if concurrent transactions conflict. The application catches `40001` and returns `CONFLICT`.

### Transaction Timeout and Reaper

Transactions have a configurable TTL (default: 30 seconds). A background reaper goroutine periodically scans for expired transactions and rolls them back. The TTL aligns with PostgreSQL's `idle_in_transaction_session_timeout`.

### Multi-Node Transaction Affinity

In a multi-node cluster, the `pgx.Tx` handle lives in a single node's process memory. The transaction token encodes which node owns the transaction (see Section 9). All subsequent requests for that transaction are routed to the owning node — there is no distributed transaction protocol.

If the owning node dies, the transaction dies. PostgreSQL automatically rolls back the connection. The client receives `TRANSACTION_NODE_UNAVAILABLE` and must retry from scratch.

---

## 5. Multi-Tenancy

Tenant isolation in Cyoda-Go is structural, not conventional. There is no code path where tenant is absent, optional, or bypassable.

### Design Principles

- `TenantID` is a named Go type (not a bare `string`), providing compile-time safety
- Every request carries a resolved `UserContext` with tenant identity, injected by auth middleware
- The `StoreFactory` extracts tenant from context and returns a tenant-scoped view
- Persistence implementations partition by tenant at the storage level — not via application-level filtering

### Tenant Hierarchy

| Tenant | Access |
|--------|--------|
| **Regular tenant** | Own data only, plus SYSTEM-owned shared data (read-only) |
| **SYSTEM tenant** | Read/write own data; read all tenants' data for administration |

Cross-tenant data access is impossible for regular tenants, even with knowledge of entity IDs.

### gRPC Member Isolation

Calculation members (external compute nodes) are scoped to their authenticating tenant. A member connected under tenant A cannot receive dispatch requests for tenant B's entities.

---

## 6. Search

### Synchronous (Direct) Search

`POST /search/{entityName}/{modelVersion}` — Evaluates predicate conditions against entity data and returns results immediately.

### Asynchronous (Snapshot) Search

Snapshot search provides a job-based lifecycle for longer-running queries:

```
SUBMIT ──► RUNNING ──► SUCCESSFUL ──► (retrieve results) ──► DELETE
                  │
                  └──► FAILED / CANCELLED
```

- `POST /search/snapshot` — Submit search, receive job ID
- `GET /search/async/{jobId}` — Poll status
- Retrieve results with pagination
- `DELETE /search/async/{jobId}` — Cancel/cleanup

**Multi-node persistence:** In PostgreSQL mode, snapshot jobs and results are stored in `search_jobs` and `search_job_results` tables. Any node can create or retrieve snapshots. In memory mode, snapshots are node-local.

**Point-in-time:** The job captures `pointInTime` at submission (caller-specified or wall clock). Result entity IDs are stored; entity data is re-fetched at the job's point-in-time on retrieval.

### Predicate Operators (23)

| Category | Operators |
|----------|-----------|
| **Equality** | `EQUALS`, `NOT_EQUAL`, `IS_NULL`, `NOT_NULL` |
| **Comparison** | `GREATER_THAN`, `LESS_THAN`, `GREATER_OR_EQUAL`, `LESS_OR_EQUAL`, `BETWEEN`, `BETWEEN_INCLUSIVE` |
| **String** | `CONTAINS`, `NOT_CONTAINS`, `STARTS_WITH`, `NOT_STARTS_WITH`, `ENDS_WITH`, `NOT_ENDS_WITH`, `MATCHES_PATTERN`, `LIKE` |
| **Case-Insensitive** | `IEQUALS`, `INOT_EQUAL`, `ICONTAINS`, `INOT_CONTAINS`, `ISTARTS_WITH`, `INOT_STARTS_WITH`, `IENDS_WITH`, `INOT_ENDS_WITH` |

### Condition Types

| Type | Description |
|------|-------------|
| **Simple** | JSONPath + operator + value against entity data |
| **Lifecycle** | Predicate on entity metadata (state, creationDate, previousTransition) |
| **Group** | AND/OR composition with arbitrary nesting |
| **Array** | Positional matching against array elements |
| **Function** | Delegated to external compute node via gRPC |

---

## 7. Externalized Processing

### Protocol: gRPC CloudEvents

External computation nodes (calculation members) connect via bidirectional gRPC streaming using a CloudEvent envelope protocol.

### Connection Lifecycle

```
Client ──JoinEvent──► Server
Server ──GreetEvent──► Client (assigns memberId)
         ──keep-alive loop──
Server ──ProcessorRequest──► Client
Client ──ProcessorResponse──► Server
Server ──CriteriaRequest──► Client
Client ──CriteriaResponse──► Server
```

### CloudEvent Types

| Event Type | Direction | Purpose |
|------------|-----------|---------|
| `CalculationMemberJoinEvent` | Client to Server | Register with capability tags |
| `CalculationMemberGreetEvent` | Server to Client | Acknowledge, assign memberId |
| `CalculationMemberKeepAliveEvent` | Bidirectional | Liveness monitoring |
| `EntityProcessorCalculationRequest` | Server to Client | Dispatch processor execution |
| `EntityProcessorCalculationResponse` | Client to Server | Return processed entity |
| `EntityCriteriaCalculationRequest` | Server to Client | Dispatch criterion evaluation |
| `EntityCriteriaCalculationResponse` | Client to Server | Return evaluation result |
| `EventAckResponse` | Bidirectional | Acknowledge receipt |

### Tag-Based Routing

Processors and criteria define required tags in their configuration. Calculation members declare capability tags on join. The dispatcher routes requests only to members whose tags match. If no member matches, the request fails with `NO_COMPUTE_MEMBER_FOR_TAG`.

### Computation Member Lifecycle

- **Join:** Member connects, sends `JoinEvent` with tags and tenant credentials
- **Greet:** Server validates credentials, assigns `memberId`, sends `GreetEvent`
- **Active:** Member receives dispatch requests, sends responses
- **Keep-alive:** Server sends periodic keep-alive (configurable interval/timeout). Missed keep-alive marks member offline.
- **Disconnect:** Member disconnects or times out. Pending dispatches fail with `COMPUTE_MEMBER_DISCONNECTED`.

### Transaction Context Propagation

Processor callbacks (CRUD operations performed by the processor) carry the transaction token. In a multi-node cluster, callbacks may arrive at any node — the router forwards them to the transaction-owning node (see Section 9).

---

## 8. Authentication and Authorization

### Modes

| Mode | Configuration | Behavior |
|------|--------------|----------|
| **Mock** | `CYODA_IAM_MODE=mock` (default) | All requests auto-authenticated as a default user. Zero setup. |
| **JWT** | `CYODA_IAM_MODE=jwt` | Real OAuth 2.0 with RS256 JWT tokens |

### JWT Mode Capabilities

| Capability | Endpoint | Description |
|------------|----------|-------------|
| **Token issuance** | `POST /oauth/token` | `client_credentials` grant |
| **OBO exchange** | `POST /oauth/token` | RFC 8693 token exchange — a service acting on behalf of a user |
| **JWKS** | `GET /.well-known/jwks.json` | Public key discovery for token verification |
| **M2M clients** | `POST/DELETE /auth/m2m/...` | Create, delete, reset secret for machine-to-machine clients |
| **Key management** | `POST/DELETE /auth/keys/...` | Issue, invalidate, reactivate, delete signing key pairs |
| **Trusted keys** | `POST/DELETE /auth/trusted/...` | Register external signing keys for cross-system trust |
| **Bootstrap client** | `CYODA_BOOTSTRAP_CLIENT_ID` | Pre-configured M2M client at startup (solves chicken-and-egg) |

### Token Claims

```json
{
  "iss": "cyoda",
  "sub": "<client_id>",
  "jti": "<unique_id>",
  "iat": 1711700000,
  "exp": 1711703600,
  "caas_user_id": "<user_id>",
  "caas_org_id": "<tenant_id>",
  "scopes": "ROLE_ADMIN,ROLE_M2M",
  "caas_tier": "unlimited"
}

```

### Delegating Authenticator

The authenticator routes by `iss` (issuer) claim:
- **Local tokens** (issuer matches configured `CYODA_JWT_ISSUER`): Extract roles from `scopes` claim
- **External tokens** (issuer is a trusted external key): Extract roles from `user_roles` claim

### gRPC Authentication

gRPC calls authenticate via `Authorization` metadata. The same JWT validation applies. Calculation members must authenticate with `ROLE_M2M` tokens.

---

## 9. Multi-Node Cluster Architecture

### Overview

Cyoda-Go operates as a cluster of 3-10 stateless Go nodes behind a load balancer (nginx). Every node is identical — no leader election, no shard ownership. PostgreSQL is the single coordination layer.

### Node Discovery: Gossip (SWIM Protocol)

Nodes discover each other using HashiCorp memberlist (embedded, pure Go). No external service discovery infrastructure (no etcd, no Consul, no ZooKeeper).

| Property | Value |
|----------|-------|
| **Protocol** | SWIM (Scalable Weakly-consistent Infection-style Membership) |
| **Convergence** | O(log N) — failure detection scales logarithmically |
| **Bandwidth** | Constant per node — pings a small random subset |
| **Failure detection** | Automatic; dead nodes evicted within seconds |
| **Bootstrap** | Seed nodes via configuration. Exponential backoff with jitter. |

**Startup sequence:**

1. Filter self from seed list
2. `list.Join(seeds)` with exponential backoff (500ms initial, 10s max, 2min total)
3. Poll membership until stable for 2-second window (no changes)
4. Open gRPC/HTTP servers and mark node ready

### Transaction Routing Tokens

When a node begins a PostgreSQL transaction, it generates an opaque HMAC-signed token:

```
Token payload:
  nodeID    — ID of the node holding the pgx.Tx
  txRef     — UUID key into the node's local transaction map
  expiresAt — Unix timestamp (TTL)
```

The token is base64url-encoded with HMAC-SHA256 signature. Clients cannot forge or tamper with tokens. The router decodes the token locally (no network call) to determine the owning node.

**Wire transport:**
- HTTP: `X-Tx-Token` header
- gRPC: `tx-token` metadata key

**Separate from transaction ID:** The `transactionId` (UUID) remains the logical identifier for storage, audit, and temporal queries. The `txToken` is a routing-only concern. Single-node deployments never see `txToken` if cluster mode is disabled.

### Request Routing

Every node acts as a transparent HTTP proxy for requests that belong to another node's transaction:

```
1. Extract txToken from header/metadata
2. Verify HMAC signature
3. If nodeID == self → serve locally
4. If nodeID != self → resolve address from local memberlist → forward request
5. If nodeID not found in membership → TRANSACTION_NODE_UNAVAILABLE (node is dead, tx is gone)
```

Address resolution is a local scan over `list.Members()` — no I/O, O(N) over node count (N is small by design).

### Cluster-Aware Compute Dispatch

In a multi-node cluster, a calculation member's gRPC stream terminates at one node, but the workflow engine may need to dispatch to that member from any node. Dispatch requests are forwarded to the node hosting the target member's stream.

### Failure Semantics

| Failure | Consequence | Client Action |
|---------|------------|---------------|
| Node holding `pgx.Tx` dies | PostgreSQL auto-rollbacks the connection | Retry from scratch |
| Node holding gRPC stream dies | Member disconnects, pending dispatches fail | Reconnect member |
| PostgreSQL unreachable | All transactions on all nodes fail | Wait for recovery |
| Network partition (node-to-node) | Affected node's transactions unreachable from other nodes | Retry when partition heals |

**Key safety property:** The `pgx.Tx` handle lives exclusively in one node's process memory. No other node can commit, rollback, or interact with that transaction. There is no competing-commit scenario.

---

## 10. API Surface

### REST API (OpenAPI 3.1)

| Area | Endpoints | Description |
|------|-----------|-------------|
| **Health** | `GET /health` | Readiness probe |
| **Entity CRUD** | `POST/GET/PUT/DELETE /entity/...` | Create, read, update, delete (single and batch) |
| **Entity Stats** | `GET /entity/stats/...` | Count, state distribution per model |
| **Model Management** | `POST/GET/DELETE /model/...` | Import, export, lock, unlock, delete, validate, changeLevel |
| **Workflow** | `GET/POST /model/.../workflow/{export,import}` | Import/export workflow definitions |
| **Search** | `POST /search/{direct,async}/...` | Synchronous and snapshot search |
| **Audit** | `GET /audit/entity/{entityId}` | Entity change history, SM audit trail |
| **Messaging** | `POST/GET/DELETE /message/...` | Edge message store |
| **Auth** | `POST /oauth/token`, `GET /.well-known/jwks.json`, key/trusted/M2M management | Authentication and key management |
| **Account** | `GET /account` | Account info, subscriptions |
| **Cluster** | `GET /cluster/members/calculation/...` | Connected member registry |
| **Admin** | `GET/POST /admin/log-level` | Runtime log level control |

All REST responses follow RFC 9457 (Problem Details) for errors.

### gRPC API (CloudEventsService)

```protobuf
service CloudEventsService {
  rpc startStreaming(stream CloudEvent) returns (stream CloudEvent);
  rpc entityModelManage(CloudEvent) returns (CloudEvent);
  rpc entityManage(CloudEvent) returns (CloudEvent);
  rpc entityManageCollection(CloudEvent) returns (stream CloudEvent);
  rpc entitySearch(CloudEvent) returns (CloudEvent);
  rpc entitySearchCollection(CloudEvent) returns (stream CloudEvent);
}
```

All operations use CloudEvent envelopes with JSON payloads, matching the Cyoda Cloud protocol.

---

## 11. Deployment

### Single Binary

Cyoda-Go compiles to a single Go binary. No runtime dependencies beyond the binary itself (in memory mode) or PostgreSQL (in postgres mode).

**Container image:** Distroless base (`gcr.io/distroless/static-debian12`). Minimal attack surface.

### Deployment Modes

| Mode | Dependencies | Use Case |
|------|-------------|----------|
| **Standalone (memory)** | None | Development, testing, CI |
| **Standalone (PostgreSQL)** | PostgreSQL 17+ | Single-node production |
| **Multi-node cluster** | PostgreSQL 17+, nginx LB | HA production |

### Multi-Node Docker Deployment

```
┌─────────┐
│  nginx   │ ← Load balancer (port 8123)
│  (LB)   │
├─────────┤
│ Node 1  │ ← HTTP + gRPC + gossip
│ Node 2  │
│ Node 3  │
├─────────┤
│PostgreSQL│ ← Shared, SERIALIZABLE isolation
└─────────┘
```

Provisioned via `start-cluster.sh` with configurable `--nodes` flag.

---

## 12. Storage Plugin Architecture

A running binary has exactly one active storage plugin. Per-store routing (mixing backends in a single instance) is not supported — every store in a given binary uses the same plugin. Plugins are selected at startup via `CYODA_STORAGE_BACKEND`.

### Stock Plugins (shipped with `cyoda-go`)

| Plugin | Dependencies | Use case |
|--------|--------------|----------|
| **memory** (default) | None — in-process Go maps | Rapid development, agent-driven application engineering, embedded/test usage. Single-node only; data is lost on restart. |
| **postgres** | PostgreSQL 17+ | Production durability; single-node or multi-node clusters (3–10 nodes) behind a load balancer. All cluster state flows through PostgreSQL as the consistency authority. |

### Proprietary Plugin (separate binary: `cyoda-go-cassandra`)

| Plugin | Dependencies | Use case |
|--------|--------------|----------|
| **cassandra** | Apache Cassandra 4.x+, message broker (Kafka/Redpanda) | Deployments that outgrow a single PostgreSQL instance and need horizontal write scalability. SSI guarantees preserved via a custom 2-phase commit coordinator protocol. Higher operational complexity. |

### Writing a Third-Party Plugin

Plugin authors depend only on `github.com/cyoda-platform/cyoda-go-spi` (stdlib-only Go interfaces). A plugin implements the `Plugin` interface and registers itself from `init()`:

```go
import spi "github.com/cyoda-platform/cyoda-go-spi"

func init() { spi.Register(&myPlugin{}) }
```

Users compose a custom binary by blank-importing plugins alongside `cyoda-go`:

```go
import (
    _ "github.com/cyoda-platform/cyoda-go/plugins/memory"
    _ "github.com/cyoda-platform/cyoda-go/plugins/postgres"
    _ "example.com/my-redis-plugin"
)
```

The `memory` and `postgres` plugins serve as reference implementations. See `docs/ARCHITECTURE.md` Section 2 for the full contract.

---

## 13. Observability

### Structured Logging

All logging via Go's `log/slog`. Runtime-switchable via `POST /api/admin/log-level`.

| Level | Purpose |
|-------|---------|
| **ERROR** | Failures requiring investigation (stream send failed, commit failed, panic recovery) |
| **WARN** | Unexpected but recoverable (unknown CloudEvent type, auth failure) |
| **INFO** | High-level flow milestones (member joined, entity created, server started) |
| **DEBUG** | Detailed flow tracing with payload previews (first 200 chars, truncated) |

Structured context fields: `pkg`, `memberId`, `entityId`, `eventType`, `transactionId`.

### Error Classification

Three-tier error model:

| Tier | HTTP Status | Response Content | Server Action |
|------|-------------|-----------------|---------------|
| **Client Error** | 4xx | Full domain error detail with error code | Log at WARN |
| **Server Error** | 5xx | Generic message + correlation ticket UUID | Log at ERROR with full detail |
| **Fatal Error** | 5xx | Generic message + correlation ticket UUID | Log at ERROR, mark health unhealthy |

Error responses follow RFC 9457 (Problem Details). Error detail level controlled by `CYODA_ERROR_RESPONSE_MODE` (`sanitized` or `verbose`).

### Error Code Taxonomy

**Domain errors:**

| Code | HTTP Status | Retryable | Description |
|------|-------------|-----------|-------------|
| `MODEL_NOT_FOUND` | 404 | No | Referenced model does not exist |
| `MODEL_NOT_LOCKED` | 409 | No | Operation requires a locked model |
| `ENTITY_NOT_FOUND` | 404 | No | Referenced entity does not exist |
| `VALIDATION_FAILED` | 400 | No | Entity data does not conform to model schema |
| `TRANSITION_NOT_FOUND` | 404 | No | Named transition does not exist on current state |
| `WORKFLOW_FAILED` | 500 | No | Workflow engine encountered an unrecoverable error |
| `CONFLICT` | 409 | Yes | Transaction serialization conflict |
| `BAD_REQUEST` | 400 | No | Malformed input |
| `UNAUTHORIZED` | 401 | No | Missing or invalid credentials |
| `FORBIDDEN` | 403 | No | Insufficient permissions |
| `SERVER_ERROR` | 500 | No | Internal server error |

**Cluster/transaction errors:**

| Code | HTTP Status | Retryable | Description |
|------|-------------|-----------|-------------|
| `TRANSACTION_NODE_UNAVAILABLE` | 503 | Yes | Owning node is dead or unreachable |
| `TRANSACTION_EXPIRED` | 410 | No | Transaction TTL elapsed |
| `TRANSACTION_NOT_FOUND` | 404 | No | Transaction reference not found on owning node |
| `IDEMPOTENCY_CONFLICT` | 409 | No | Duplicate idempotency key |
| `CLUSTER_NODE_NOT_REGISTERED` | 503 | Yes | Target node not in membership list |

**Compute dispatch errors:**

| Code | HTTP Status | Retryable | Description |
|------|-------------|-----------|-------------|
| `NO_COMPUTE_MEMBER_FOR_TAG` | 503 | Yes | No connected member matches required tags |
| `DISPATCH_FORWARD_FAILED` | 502 | Yes | Failed to forward dispatch to member's host node |
| `DISPATCH_TIMEOUT` | 504 | Yes | Processor/criteria execution timed out |
| `COMPUTE_MEMBER_DISCONNECTED` | 503 | Yes | Member disconnected during dispatch |

### OpenTelemetry

OpenTelemetry instrumentation is implemented end-to-end. Traces, metrics, and log correlation use the OTel SDK with OTLP HTTP exporters. HTTP requests are auto-traced via `otelhttp`. Transaction lifecycle operations (`tx.begin`, `tx.commit`, `tx.rollback`, `tx.savepoint`) produce spans with duration/active/conflict metrics regardless of which plugin is active (the core wraps the plugin's `TransactionManager` with a tracing decorator). Workflow engine and externalized processor dispatch are traced. Plugins may add their own instrumentation — the `cyoda-go-cassandra` plugin, for example, emits per-CQL timing histograms, batch execution metrics, concurrency limiter metrics, and commit-protocol phase spans. A Grafana / Prometheus / Tempo dashboard ships with the bundled docker environment. Standard OTel environment variables (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `OTEL_TRACES_SAMPLER`) configure export and sampling.

---

## 14. Scale Profile and Operational Boundaries

### Target Operating Envelope

| Dimension | Sweet Spot | Upper Bound | Notes |
|-----------|-----------|-------------|-------|
| Cluster size | 3–5 nodes | 10–20 nodes | Beyond 10, gossip metadata grows and proxy hop probability increases |
| Concurrent transactions | 50–250 | ~750 (3 nodes × 25 PG connections) | Bounded by PG connection pool × node count |
| Entity volume | Up to millions per model | Bounded by PG storage | Version history grows monotonically (append-only, no compaction) |
| Transaction duration | < 1 second (ideal) | 60 seconds (default TTL) | Each second of transaction duration consumes one PG connection |
| Write throughput | 50–200 entity creates/s per node | Bounded by PG SERIALIZABLE | Contention on same entities triggers serialization failures + retries |
| Compute processor latency | < 500 ms | 30s (default timeout) | Processor duration dominates transaction duration |

### Where This Design Excels (PostgreSQL plugin)

- **Transactional correctness:** Zero-compromise SERIALIZABLE isolation. No eventual consistency, no conflict windows, no split-brain. PostgreSQL is the single source of truth.
- **Operational simplicity:** One PostgreSQL instance plus stateless Go binaries. No ZooKeeper, no Kafka, no external service discovery.
- **Development velocity:** The `memory` plugin runs with zero dependencies. Same code, same tests, same APIs as production.
- **Small-to-medium data volumes:** For financial ledgers, regulatory records, order management — datasets that fit comfortably in a single PostgreSQL instance (terabytes, not petabytes).

### Where This Design Has Limits (PostgreSQL plugin)

- **Write-heavy workloads at scale:** All writes go through a single PostgreSQL instance. The PostgreSQL plugin cannot shard writes across storage nodes. The `cassandra` plugin (separate `cyoda-go-cassandra` binary) is the answer for deployments that outgrow a single PostgreSQL instance and need horizontal write scalability.
- **Long-running transactions:** A 10-second processor holds a PG connection for 10+ seconds, limiting concurrency.
- **Large cluster sizes:** Beyond 10 nodes, the benefits of adding nodes diminish — compute capacity scales, but write capacity does not.
- **Unbounded version histories:** Append-only entity versioning has no built-in archival. Long-lived entities with frequent updates accumulate version chains that slow point-in-time queries.

### When to Switch Plugins

| Symptom | Signal to switch | Destination |
|---------|------------------|-------------|
| Transactions per second saturating a single PG instance, or growth trajectory will exceed PG single-node capacity within 12 months | Write throughput ceiling | `cyoda-go-cassandra` binary (cassandra plugin) |
| Cluster size consistently above 10 nodes with write bottleneck | Scale-out ceiling | `cyoda-go-cassandra` binary |
| Storage engine has no match in the stock lineup (e.g. cloud-native KV store, specialized time-series engine) | Plugin fit | Third-party plugin (author against `cyoda-go-spi`) |

See [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) Section 14 for detailed technical limits, latency expectations, and sizing guidance.

---

## 15. Planned Features

Items carried forward from the `cyoda-light-go` predecessor repository that are not yet implemented against the refactored architecture. Issue numbers will be re-opened in the `cyoda-go` repository when each item is scheduled.

| Title | Category | Description |
|-------|----------|-------------|
| Commit marker for transaction commit ambiguity resolution | HA Safety (PostgreSQL plugin) | Resolve ambiguity when a node dies after PostgreSQL COMMIT but before responding to the client. Write a commit marker to a separate table inside the transaction; clients can query the marker to determine if their transaction actually committed. |
| Strict context deadline propagation across flow chain | Performance, HA Safety | Propagate `context.Context` deadlines through the entire flow chain (workflow cascade, processor dispatch, gRPC callbacks). Prevent unbounded execution when upstream has already timed out. |
| Multi-node cluster E2E test with proxy routing | Test Coverage | End-to-end test exercising the full multi-node flow: client creates entity on node A, processor callback lands on node B, node B proxies to node A, commit succeeds. Validates transaction routing under realistic conditions. |
| Batch SaveResults with pgx.CopyFrom | Performance (PostgreSQL plugin) | Replace row-by-row INSERT in `SaveResults` with `pgx.CopyFrom` for bulk loading of search job result IDs into PostgreSQL. |
| Idempotency keys | HA Safety | Client-provided keys to prevent duplicate operations on retry (addresses the client-side commit-ambiguity path). |
| Conformance test suite (`cyoda-go-spi/spitest/`) | Plugin ecosystem | Shared behavioral conformance harness for any plugin to run against its own `StoreFactory`. Scheduled for after the cassandra plugin extraction. |

---

## References

- **Architecture:** [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)
- **Architecture Decision Records:** [`docs/adr/`](adr/)
- **OpenAPI Specification:** `api/openapi.yaml`
- **Storage Plugin SPI module:** [github.com/cyoda-platform/cyoda-go-spi](https://github.com/cyoda-platform/cyoda-go-spi)
- **Proprietary Cassandra plugin:** [github.com/cyoda-platform/cyoda-go-cassandra](https://github.com/cyoda-platform/cyoda-go-cassandra) (private)
