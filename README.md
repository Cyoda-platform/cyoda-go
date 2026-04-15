# Cyoda-Go

A Go [EDBMS](https://medium.com/@paul_42036/whats-an-entity-database-11f8538b631a) (Entity Database Management System) — a database engine where the first-class abstraction is not a row or document but a *stateful entity* with schema, lifecycle, temporal history, and transactional integrity. Cyoda-Go replicates the functional behavior of the [Cyoda](https://cyoda.com) platform's APIs, gRPC integrations, entity lifecycle management, and workflow engine.

Cyoda-Go operates in two modes:

- **In-Memory mode** — a single-process, zero-dependency instance. Sub-millisecond latencies. Data is lost on restart.
  - **Local development target** — build and test Cyoda applications without a full distributed deployment
  - **Functional test harness** — verify API inputs/outputs and gRPC integration contracts
  - **Digital Twin Universe component** — validate at volumes and rates far exceeding production limits, run thousands of scenarios per hour without rate limits or API costs
- **PostgreSQL mode** — durable storage with `SERIALIZABLE` isolation for production workloads.
  - **Zero-compromise transactional safety** — full ACID with PostgreSQL-native SSI; no eventual consistency, no conflict windows, no split-brain
  - **Active-active high availability** — 3-10 stateless Go nodes behind a load balancer, any node serves any request, no leader election
  - **Operational simplicity** — PostgreSQL is the only infrastructure dependency; no ZooKeeper, no etcd, no Kafka

## Target Applications

High-complexity, high-consistency enterprise domains where correctness is non-negotiable:

- **Financial ledgers** — double-entry bookkeeping with strict state machine enforcement on journal entries
- **Order management** — multi-stage order lifecycles with automated and manual state transitions, external processor callouts for validation/enrichment
- **Regulatory compliance** — auditable entity histories with point-in-time retrieval for regulatory reporting windows
- **Digital twin orchestration** — behavioral clones of production systems for scenario testing at volumes exceeding production limits

## EDBMS Features

- **Entity Management** — store, retrieve, update, and delete JSON entities with full version history and the ability to query any past state
- **Entity Models** — discover schemas automatically from sample data, evolve them over time, and validate incoming entities against them
- **Workflow Engine** — define how entities move through states with rules that fire automatically or on demand, including callouts to external processors
- **Search** — find entities by field values, metadata, or compound conditions, with both immediate and background query modes
- **Externalized Processing** — connect external computation nodes over gRPC that receive work, transform entities, and evaluate conditions
- **Transactions** — all-or-nothing operations with conflict detection so concurrent writers never silently corrupt data
- **Multi-Tenancy** — each tenant's data is fully isolated; no API path can cross tenant boundaries
- **Authentication** — OAuth 2.0 token issuance, machine-to-machine credentials, on-behalf-of token exchange, and external key trust
- **Temporal Queries** — retrieve entities and search results as they existed at any point in the past
- **Edge Messaging** — store and retrieve messages with headers, metadata, and arbitrarily large payloads
- **Audit Trail** — every entity change and workflow decision is recorded and queryable
- **Pluggable Persistence** — run entirely in memory for speed, or switch to PostgreSQL for durability, or mix both per data type

See [OVERVIEW.md](OVERVIEW.md) for the full architecture and feature details.

## Requirements

- **Go 1.26+**
- **Docker** (optional, for PostgreSQL backend and container builds)
- **PostgreSQL 17+** (required for PostgreSQL mode)

## Quick Start

### Local Development (recommended)

```bash
./cyoda-go.sh
```

This runs Cyoda-Go with the `local` profile (`.env.local`): in-memory storage, JWT auth, debug logging on port **8123** (HTTP) and **9123** (gRPC). Copy `.env.local.example` to `.env.local` to customize.

```bash
# Get a token (bootstrap credentials are printed at startup):
TOKEN=$(curl -s -X POST http://localhost:8123/api/oauth/token \
  -u "m2m.user:<secret-from-startup-log>" \
  -d "grant_type=client_credentials" | jq -r .access_token)

# Use it:
curl -H "Authorization: Bearer $TOKEN" http://localhost:8123/api/health
# {"status":"UP"}
```

### In-Memory Mode (no dependencies, no auth)

```bash
go run ./cmd/cyoda-go
```

Starts on port **8080** (HTTP) and **9090** (gRPC) with mock auth (no tokens needed). All data lives in memory and is lost on restart. This is the simplest way to get started, but doesn't reflect production auth behavior.

```bash
curl http://localhost:8080/api/health
# {"status":"UP"}
```

### Docker with PostgreSQL (single node)

```bash
./cyoda-go-docker.sh
```

This generates a `.env.docker` with a fresh JWT signing key and starts both Cyoda-Go and PostgreSQL via `docker compose`. Data is persisted to a Docker volume.

```bash
# Get a token (credentials printed at startup):
TOKEN=$(curl -s -X POST http://localhost:8123/api/oauth/token \
  -u "m2m.user:78f647e3..." -d "grant_type=client_credentials" | jq -r .access_token)

# Use it:
curl -H "Authorization: Bearer $TOKEN" http://localhost:8123/api/health
```

### Multi-Node Cluster

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

Provisioned via `start-cluster.sh` with configurable `--nodes` flag. All nodes are stateless and identical — no leader election, no shard ownership. PostgreSQL is the single coordination layer. Nodes discover each other using gossip (SWIM protocol) with no external service discovery infrastructure.

When a node begins a PostgreSQL transaction, it generates a signed routing token encoding which node owns the `pgx.Tx` handle. All subsequent requests for that transaction are routed to the owning node. If the owning node dies, PostgreSQL auto-rolls back the connection and the client retries from scratch.

## Storage Modes

Cyoda-Go supports two storage backends, switchable via environment variables without code changes.

### In-Memory (default)

All data lives in process memory. Fast, zero dependencies, ideal for development and testing. Data is lost on restart. Transactions use snapshot isolation with SSI conflict detection. Single-node only.

```bash
CYODA_STORAGE_BACKEND=memory   # this is the default
```

### PostgreSQL

Durable storage with `SERIALIZABLE` isolation. Includes automatic schema migrations, bi-temporal entity versioning, and row-level security policies. Supports single-node and multi-node cluster deployments. Requires a PostgreSQL 17+ instance.

```bash
CYODA_STORAGE_BACKEND=postgres
CYODA_POSTGRES_URL=postgres://user:pass@localhost:5432/minicyoda?sslmode=disable
CYODA_POSTGRES_AUTO_MIGRATE=true
```

## Scale Profile

| Dimension | Sweet Spot | Upper Bound |
|-----------|-----------|-------------|
| Cluster size | 3-5 nodes | 10-20 nodes |
| Concurrent transactions | 50-250 | ~750 (3 nodes x 25 PG connections) |
| Entity volume | Up to millions per model | Bounded by PG storage |
| Write throughput | 50-200 entity creates/s per node | Bounded by PG SERIALIZABLE |

Cyoda-Go excels at transactional correctness and operational simplicity for small-to-medium data volumes (terabytes, not petabytes). It trades away horizontal write scalability — all writes go through a single PostgreSQL instance.

## Configuration

All configuration is via environment variables with the `CYODA_` prefix. Run `cyoda-go --help` for the complete reference.

### Profiles

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_PROFILES` | *(none)* | Comma-separated profile names. Loads `.env` then `.env.{profile}` for each profile. Shell env vars always win. |

Profiles load `.env` files in order — later profiles override earlier ones. Example files: `.env.local`, `.env.postgres`, `.env.otel`. Copy the corresponding `.example` file to get started.

```bash
# Combine profiles:
CYODA_PROFILES=postgres,otel go run ./cmd/cyoda-go
```

The `./cyoda-go.sh` script is a convenience wrapper that sets `CYODA_PROFILES=local` by default.

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_HTTP_PORT` | `8080` | HTTP listen port |
| `CYODA_GRPC_PORT` | `9090` | gRPC listen port |
| `CYODA_CONTEXT_PATH` | `/api` | URL prefix for all routes |
| `CYODA_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `CYODA_ERROR_RESPONSE_MODE` | `sanitized` | Error detail: `sanitized` (production) or `verbose` (development) |

### Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_IAM_MODE` | `mock` | `mock` (no auth) or `jwt` (OAuth 2.0 with JWT) |
| `CYODA_IAM_MOCK_ROLES` | `ROLE_ADMIN,ROLE_M2M` | Comma-separated roles granted to the default mock user. `ROLE_M2M` is required for the gRPC streaming endpoint; `ROLE_ADMIN` for admin HTTP endpoints. |
| `CYODA_JWT_SIGNING_KEY` | — | RSA private key in PEM format. Required for `jwt` mode. |
| `CYODA_JWT_ISSUER` | `cyoda-go` | JWT issuer claim |
| `CYODA_JWT_EXPIRY_SECONDS` | `3600` | Token lifetime |

### Bootstrap (jwt mode)

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_BOOTSTRAP_CLIENT_ID` | — | Creates an M2M client at startup. Solves the chicken-and-egg of needing a token to create tokens. |
| `CYODA_BOOTSTRAP_CLIENT_SECRET` | *(generated)* | Fixed secret, or omit to auto-generate |
| `CYODA_BOOTSTRAP_TENANT_ID` | `default-tenant` | Tenant for the bootstrap client |
| `CYODA_BOOTSTRAP_ROLES` | `ROLE_ADMIN,ROLE_M2M` | Comma-separated roles |

### PostgreSQL

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_POSTGRES_URL` | — | Connection string. Required when any store uses `postgres`. |
| `CYODA_POSTGRES_MAX_CONNS` | `25` | Connection pool maximum |
| `CYODA_POSTGRES_MIN_CONNS` | `5` | Connection pool minimum |
| `CYODA_POSTGRES_AUTO_MIGRATE` | `true` | Run schema migrations on startup |

### gRPC

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_KEEPALIVE_INTERVAL` | `10` | Keep-alive send interval (seconds) |
| `CYODA_KEEPALIVE_TIMEOUT` | `30` | Keep-alive timeout before disconnect (seconds) |

### Observability

| Variable | Default | Description |
|----------|---------|-------------|
| `CYODA_OTEL_ENABLED` | `false` | Enable OpenTelemetry tracing and metrics |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://localhost:4318` | OTLP endpoint (standard OTel env var) |

Enable via the `otel` profile: `CYODA_PROFILES=local,otel go run ./cmd/cyoda-go`. Copy `.env.otel.example` to `.env.otel` to customize.

### Admin Endpoints

#### Log Level (`/api/admin/log-level`)

Runtime-switchable log level. Requires `ROLE_ADMIN` on the JWT.

**GET `/api/admin/log-level`** — returns the current log level as JSON:

```json
{"level": "info"}
```

**POST `/api/admin/log-level`** — changes the log level atomically:

```bash
curl -X POST -H 'Authorization: Bearer $TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"level":"debug"}' \
  http://localhost:8080/api/admin/log-level
```

#### Trace Sampler (`/api/admin/trace-sampler`)

Runtime-switchable OTel trace sampler. Requires `ROLE_ADMIN` on the
JWT. Matches the `/api/admin/log-level` pattern.

**GET `/api/admin/trace-sampler`** — returns the current sampler
configuration as JSON:

```json
{"sampler": "ratio", "ratio": 0.1, "parent_based": true}
```

**POST `/api/admin/trace-sampler`** — changes the sampler
configuration atomically. Body shape is symmetric with the GET
response:

```bash
# Sample every trace (default)
curl -X POST -H 'Authorization: Bearer $TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"sampler":"always"}' \
  http://localhost:8080/api/admin/trace-sampler

# Sample 10% of traces
curl -X POST -H 'Authorization: Bearer $TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"sampler":"ratio","ratio":0.1}' \
  http://localhost:8080/api/admin/trace-sampler

# Disable tracing entirely
curl -X POST -H 'Authorization: Bearer $TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"sampler":"never"}' \
  http://localhost:8080/api/admin/trace-sampler
```

Valid `sampler` values: `always`, `never`, `ratio`. When `sampler` is
`ratio`, `ratio` must be a float in `[0, 1]`.

**`parent_based` interaction with upstream sampling decisions.** When
`parent_based` is `true` (the default), the sampler respects the
upstream trace's sampling decision from the `traceparent` header. If
an upstream service or load balancer decided "do not sample", this
node honors that decision, even with `sampler: always`. This is
standard OTel `ParentBased` behavior and is usually what operators
want for distributed-trace correctness.

To force 100% sampling on this node regardless of upstream, set
`parent_based: false`:

```json
{"sampler": "always", "parent_based": false}
```

**Initial sampler.** At startup, the sampler is seeded from the
standard OTel env vars `OTEL_TRACES_SAMPLER` and
`OTEL_TRACES_SAMPLER_ARG`. Supported values are the six standard
combinations from the OTel spec (`always_on`, `always_off`,
`traceidratio`, and their `parentbased_` variants). The admin endpoint
is a runtime override, not a replacement.

**Process-local.** Each node has its own sampler; multi-node
deployments need to hit each node's admin endpoint separately, same
as `/admin/log-level`.
