# Architecture + PRD documentation sync — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `docs/ARCHITECTURE.md` and `docs/PRD.md` into perfect sync with the codebase at HEAD, restructure storage content into `docs/ARCHITECTURE.md` + per-plugin `docs/plugins/*.md`, and sanitize Cassandra plugin references to capability-only framing.

**Architecture:** Three phases. Phase A runs eight parallel reconciliation subagents, one per section scope, each returning a structured discrepancies list. Phase B applies the structural split and all reconciliation findings. Phase C verifies via six deterministic checks (mechanism grep-zero, rename-residue grep-zero, per-plugin template conformance, cross-link resolution, env-var cross-reference, frozen-section intactness).

**Tech Stack:** Markdown docs only (no code changes). Edits via `Edit`/`Write` tools. Verification via `grep`/`rg`. Parallel agent dispatch via the `Agent` tool with `Explore` subagent type.

**Spec:** `docs/superpowers/specs/2026-04-18-architecture-prd-sync-design.md`.

---

## Prerequisites

Before starting Task 1, verify:

- [ ] Working tree at repo root is clean: `git status` shows only untracked `.claude/scheduled_tasks.lock`, `cyoda-go` (stray binary), or similar harness/build artefacts — not the three target files.
- [ ] `docs/ARCHITECTURE.md` SHA matches the one referenced in the spec: `git log -1 --format=%H -- docs/ARCHITECTURE.md` returns a SHA whose content has the "Version: 2.0 / Date: 2026-04-14" header.
- [ ] `docs/PRD.md` is the 712-line version with the same header.
- [ ] `docs/plugins/` directory does not yet exist.

If any is off, stop and realign with the spec.

---

## File structure

### Docs touched

```
docs/
  ARCHITECTURE.md              (MODIFY — trim §2 to index+§2.4, apply reconciliation findings, update header)
  PRD.md                       (MODIFY — rewrite §1 storage callout, apply reconciliation findings, update header)
  plugins/
    README.md                  (CREATE — one-page index)
    IN_MEMORY.md               (CREATE — from current §2.1 + §3.2 + §9 memory content + reconciliation)
    SQLITE.md                  (CREATE — new; draws on plugins/sqlite/ and the sqlite design spec)
    POSTGRES.md                (CREATE — from current §2.2 + §3.3 + §3.5 + §9 postgres content + reconciliation)
```

### Code references

No code changes. All edits are in `docs/`. The reconciliation agents read code (`cmd/`, `app/`, `internal/`, `plugins/`, `api/`) but do not modify it.

---

## Phase A — Reconciliation (Tasks 1–8)

Eight parallel subagents. Each returns a **structured discrepancies list** in a single agent response. Dispatch in parallel — no cross-dependencies between agents.

Before dispatching, set shared baseline context that every agent gets:

- Spec: `docs/superpowers/specs/2026-04-18-architecture-prd-sync-design.md`
- Commit SHA of the source of truth: `git rev-parse HEAD` (capture this value; pass it to every agent)
- Output format required from each agent: Critical / Significant / Minor, each with `file:line`, what the doc says, what the code does, recommended fix.

### Task 1: Reconcile ARCHITECTURE.md §1 System Overview + §3 Transaction Model (storage-agnostic parts)

**Files:**
- Read: `docs/ARCHITECTURE.md` §1 (lines 32–210) and §3 excluding §3.2 and §3.3 (lines 369–473)
- Read: `cyoda-go-spi/` module (fetched via `go env GOMODCACHE` or local clone if available; otherwise from `go.mod` + `go.sum` to locate it in the module cache)
- Read: `internal/contract/` (all files)
- Read: `app/app.go`, `app/config.go`
- Read: `internal/cluster/lifecycle/`

- [ ] **Step 1: Dispatch Agent 1**

Use Agent tool with `subagent_type: Explore`. Description: "Reconcile ARCH §1+§3 vs code". Prompt includes:

```
Thorough reconciliation task against the cyoda-go codebase at commit <SHA>.

Read these sections of docs/ARCHITECTURE.md:
- §1 System Overview (lines 32–210)
- §3 Transaction Model, excluding §3.2 (In-Memory SSI) and §3.3 (PostgreSQL SERIALIZABLE)
  which are out of scope — they're being extracted to per-plugin files.
  §3.1 (TransactionManager SPI), §3.4 (TX Lifecycle Manager), §3.5 (pgx.Tx property),
  §3.6 (Plugin-Specific TMs) are in scope.

Cross-check against:
- The cyoda-go-spi module (located at the module cache path reported by `go list -m -f '{{.Dir}}' github.com/cyoda-platform/cyoda-go-spi`).
- internal/contract/ (every *.go file)
- app/app.go (startup sequence, plugin resolution)
- app/config.go (config types referenced in narrative)
- internal/cluster/lifecycle/ (Manager type referenced by TX Lifecycle narrative)

Verify every concrete claim — interface names, method signatures, file paths, package paths,
module paths, the blank-import pattern for custom binaries. Flag every drift.

Output format:
- Critical: doc claim contradicted by code.
- Significant: doc omits material change or describes since-refactored impl.
- Minor: stylistic drift, stale cross-reference, outdated example.
Each item: file:line, what doc says, what code does, recommended fix.
Return findings as text; do not edit files.
```

- [ ] **Step 2: Capture findings**

Save the agent's response verbatim to `/tmp/arch-sync-agent1.md`. No synthesis yet — raw agent output.

- [ ] **Step 3: Verify coverage**

Confirm the agent did not refuse or error. Re-dispatch if empty response.

### Task 2: Reconcile ARCHITECTURE.md §4 Multi-Node Routing

**Files:**
- Read: `docs/ARCHITECTURE.md` §4 (lines 475–875)
- Read: `internal/cluster/registry/gossip.go`
- Read: `internal/cluster/dispatch/` (all files)
- Read: `internal/cluster/proxy/` (all files)
- Read: `internal/cluster/token/` (all files)
- Read: `internal/domain/search/` (focus on persistent-snapshot claims in §4.6)

- [ ] **Step 1: Dispatch Agent 2**

```
Thorough reconciliation against cyoda-go codebase at commit <SHA>.

Read docs/ARCHITECTURE.md §4 (Multi-Node Routing, lines 475–875):
- §4.1 Cluster Discovery (SWIM gossip)
- §4.2 Transaction Routing
- §4.3 Compute Dispatch Routing
- §4.4 Transaction Flow Swimlane
- §4.5 Network Partition Analysis
- §4.6 Persistent Search Snapshots

Cross-check against:
- internal/cluster/registry/gossip.go (memberlist wiring, seed list, filterSelf)
- internal/cluster/dispatch/ (forwarder, dispatch handler, HMAC authn)
- internal/cluster/proxy/ (transaction routing proxy)
- internal/cluster/token/ (token-claim-based member affinity)
- internal/domain/search/ (snapshot TTL, reap interval, cross-cluster coherence)

Specifically verify:
- The seed-list + filterSelf behavior against the chart-side seed-list change (the ConfigMap
  now emits every pod DNS, not just pod-0).
- HMAC secret shape expected by dispatch handler against what the chart generates
  (the hex-over-b64 HMAC generation chain in templates/secret-hmac.yaml).
- Swimlane diagram accuracy against actual request flow.
- §4.6 search-snapshot claims against current search-snapshot code.

Output format as in Agent 1. Return findings as text.
```

- [ ] **Step 2: Capture findings to `/tmp/arch-sync-agent2.md`.**

### Task 3: Reconcile ARCHITECTURE.md §5 Workflow + §6 gRPC / Externalized Processing

**Files:**
- Read: `docs/ARCHITECTURE.md` §5 (lines 877–939) and §6 (lines 941–999)
- Read: `internal/domain/workflow/` (all files)
- Read: `api/grpc/events/` (all files)
- Read: `internal/grpc/` (all files)
- Read: `internal/domain/messaging/`

- [ ] **Step 1: Dispatch Agent 3**

```
Thorough reconciliation against cyoda-go codebase at commit <SHA>.

Read docs/ARCHITECTURE.md:
- §5 Workflow Engine (lines 877–939): FSM model, execution modes, cascade logic,
  processor execution, audit trail.
- §6 gRPC & Externalized Processing (lines 941–999): CloudEventsService,
  member lifecycle, tag-based selection, response correlation, CloudEvent types.

Cross-check against:
- internal/domain/workflow/ (Engine, State, Transition, Processor, Criteria)
- api/grpc/events/ (proto + generated CloudEventsService stubs)
- internal/grpc/ (server, member registry, dispatch)
- internal/domain/messaging/ (any messaging contracts surfaced)

Verify every FSM concept name, every CloudEvent type name, every gRPC method
signature, every lifecycle event name, every tag-selection rule.

Output format as in Agent 1.
```

- [ ] **Step 2: Capture findings to `/tmp/arch-sync-agent3.md`.**

### Task 4: Reconcile ARCHITECTURE.md §7 Auth + §8 Error Model

**Files:**
- Read: `docs/ARCHITECTURE.md` §7 (lines 1001–1047) and §8 (lines 1048–1133)
- Read: `internal/auth/` (all files)
- Read: `internal/admin/auth.go`, `internal/admin/admin.go`
- Read: `app/app.go` (validateBootstrapConfig, validateMetricsAuth)
- Read: `internal/common/error_codes.go`
- Read: `internal/api/middleware/`
- Read: `internal/common/apperror.go` or equivalent error type

- [ ] **Step 1: Dispatch Agent 4**

```
Thorough reconciliation against cyoda-go codebase at commit <SHA>.

Read docs/ARCHITECTURE.md:
- §7 Authentication & Authorization: §7.1 Mock Mode, §7.2 JWT Mode, §7.3 Authorization.
- §8 Error Model: §8.1 Three-Tier Classification, §8.2 RFC 9457 Problem Details,
  §8.3 Error Code Taxonomy, §8.4 Warning/Error Accumulation.

Cross-check against:
- internal/auth/service.go (adminMux, OAuth endpoints, JWT mint/verify)
- internal/admin/admin.go + internal/admin/auth.go (probe endpoints, metrics bearer
  middleware — this is NEW since the doc was written; flag as missing if §7 doesn't cover it)
- app/app.go (validateBootstrapConfig coupled-predicate, validateMetricsAuth coupled-predicate —
  both are recent additions)
- internal/common/error_codes.go (verify the error code taxonomy enumerated in §8.3 matches;
  flag additions/removals)
- internal/api/middleware/ (Problem Details middleware, Auth middleware)

Specifically flag:
- /metrics bearer auth mechanism (CYODA_METRICS_BEARER, CYODA_METRICS_REQUIRE_AUTH) —
  confirm §7 covers it or flag as missing.
- Bootstrap coupled-predicate (both CYODA_BOOTSTRAP_CLIENT_ID and CLIENT_SECRET
  must be set-or-both-empty in jwt mode) — confirm §7 covers it or flag as missing.
- _FILE suffix support for credential env vars — confirm §7/§9 covers it or flag.

Output format as in Agent 1.
```

- [ ] **Step 2: Capture findings to `/tmp/arch-sync-agent4.md`.**

### Task 5: Reconcile ARCHITECTURE.md §9 Configuration Reference

**Files:**
- Read: `docs/ARCHITECTURE.md` §9 (lines 1135–1240)
- Read: `app/config.go` (entire file, focus on `DefaultConfig`)
- Read: `cmd/cyoda/main.go` (focus on `printHelp`)
- Read: `README.md` (config sections)
- Read: each plugin's `ConfigVars()`: `plugins/memory/plugin.go`, `plugins/sqlite/plugin.go`, `plugins/postgres/plugin.go`

- [ ] **Step 1: Dispatch Agent 5 (highest-drift agent)**

```
Thorough reconciliation against cyoda-go codebase at commit <SHA>.

Read docs/ARCHITECTURE.md §9 Configuration Reference (lines 1135–1240):
- Server (ports, bind, context path, error mode, log level, etc.)
- Storage — plugin selection
- PostgreSQL plugin subsection (lines 1157–1168)
- Cassandra plugin subsection (lines 1169–1186) — will be REMOVED in Phase B;
  flag this to confirm the removal is correct, but don't evaluate content accuracy.
- IAM
- Bootstrap
- gRPC
- Cluster
- Search

Cross-check every CYODA_* env var mentioned in §9 against:
- app/config.go DefaultConfig (every env var has a call site there for non-plugin vars)
- cmd/cyoda/main.go printHelp (every env var should appear in help text)
- README.md (env vars are documented in the Configuration section)
- plugins/memory/plugin.go ConfigVars(), plugins/sqlite/plugin.go ConfigVars(),
  plugins/postgres/plugin.go ConfigVars() — the plugin-local env vars.

Flag:
- Env vars mentioned in §9 that do not exist in code (orphans in doc).
- Env vars in code that do not appear in §9 (orphans in code, missed in doc).
  Highest risk: CYODA_METRICS_REQUIRE_AUTH, CYODA_METRICS_BEARER (+ _FILE variant),
  CYODA_ADMIN_BIND_ADDRESS, CYODA_SUPPRESS_BANNER, CYODA_REQUIRE_JWT,
  _FILE variants for credentials, CYODA_SEED_NODES, CYODA_NODE_ADDR, CYODA_GOSSIP_ADDR,
  CYODA_PROFILES, CYODA_SQLITE_*.
- Default-value drift: §9 says default X, code says default Y.
- Env vars in §9 that are plugin-scoped (should move to the per-plugin files during
  Phase B restructuring). Tag these with "EXTRACT TO plugins/<plugin>.md".

Output format as in Agent 1. This agent's output drives the largest volume of edits
in Phase B; be exhaustive.
```

- [ ] **Step 2: Capture findings to `/tmp/arch-sync-agent5.md`.**

### Task 6: Reconcile ARCHITECTURE.md §10 Deployment + §11 Observability + §14 Limits

**Files:**
- Read: `docs/ARCHITECTURE.md` §10 (lines 1242–1298), §11 (lines 1300–1320), §14 (if present, else skip)
- Read: `deploy/helm/cyoda/` (chart overview)
- Read: `deploy/docker/` (Dockerfile, compose.yaml)
- Read: `internal/observability/` (metrics, tracing)
- Read: `internal/admin/admin.go` (probe endpoints)

- [ ] **Step 1: Dispatch Agent 6**

```
Thorough reconciliation against cyoda-go codebase at commit <SHA>.

Read docs/ARCHITECTURE.md:
- §10 Deployment Architecture: §10.1 Single-Node, §10.2 Multi-Node Cluster
- §11 Observability
- §14 Non-Functional Limits and Design Boundaries (if present)

Cross-check against:
- deploy/helm/cyoda/Chart.yaml, values.yaml, templates/ (shipping shape — the Helm spec
  at docs/superpowers/specs/2026-04-17-provisioning-helm-design.md is authoritative for
  the chart's current state)
- deploy/docker/Dockerfile, deploy/docker/compose.yaml (canonical compose)
- internal/observability/ (metrics registry, tracing setup, sampler, OTel exporters)
- internal/admin/admin.go (probe endpoints, bearer-gated /metrics)
- .goreleaser.yaml (release artifacts — desktop, Docker)

Specifically flag:
- §11 observability: any Cassandra-specific span/metric name (cyoda.cassandra.*) — MUST
  be removed; generalise to plugin-namespaced bullet.
- §11 mention of "trace context propagation through the cassandra plugin's broker messages" —
  MUST be removed.
- §10 single-node / multi-node shapes — confirm against the current helm + compose.
- §14 limits — confirm numeric limits still hold (search snapshot TTL, tx TTL, stability
  window, etc.) against app/config.go defaults.

Output format as in Agent 1.
```

- [ ] **Step 2: Capture findings to `/tmp/arch-sync-agent6.md`.**

### Task 7: Reconcile PRD.md §1–§4

**Files:**
- Read: `docs/PRD.md` §1 (Vision, lines 9–42), §2 (EDBMS Core, lines 44–105), §3 (Workflow Engine, lines 106–169), §4 (Transaction Model, lines 170–221)
- Read: `internal/domain/entity/` (entity type, lifecycle, soft delete)
- Read: `internal/domain/model/` (model discovery, change levels, lock lifecycle)
- Read: `internal/domain/workflow/` (FSM, transition types, criteria, processor modes, cascade, audit)
- Read: `app/app.go` + transaction-related code (ACID guarantees, lifecycle, read-your-own-writes, conflict detection, tx timeout/reaper, multi-node affinity)

- [ ] **Step 1: Dispatch Agent 7**

```
Thorough reconciliation of docs/PRD.md §1–§4 against cyoda-go codebase at commit <SHA>.

Read PRD sections:
- §1 Product Vision and Target Use Case (lines 9–42) — will have storage callout rewritten
  in Phase B per the spec, so focus on the rest: target apps, scale profile, cost model.
- §2 EDBMS Core (lines 44–105): entities as state machines, entity models, temporal integrity.
- §3 Workflow Engine (lines 106–169): FSM, transition types, criteria types, processor
  execution modes, cascade behaviour, workflow management, audit trail.
- §4 Transaction Model (lines 170–221): ACID guarantees, tx lifecycle, read-your-own-writes,
  conflict detection, tx timeout and reaper, multi-node tx affinity.

Cross-check against:
- internal/domain/entity/ (UUID, Model, State, Data, Temporal History, Soft Delete,
  Physical Delete — the PRD mentions planned issues #65 and #66; verify if they are
  still planned or landed)
- internal/domain/model/ (discovery via import, lock lifecycle UNLOCKED↔LOCKED, change levels)
- internal/domain/workflow/ (finite state machine, transition types, criteria types,
  processor execution modes sync/async, cascade, audit trail)
- app/app.go and lifecycle.Manager (ACID guarantees, tx timeout, reaper)

Flag:
- Planned feature references (#65, #66, etc.) that have since landed and should move
  out of "planned" context.
- Processor mode names that drifted.
- Transition type names that drifted.
- Criteria operator names (PRD lists 23 search predicates — verify count against
  internal/match/).
- ACID claim specificity (spec-level, not implementation-specific).

Output format as in Agent 1.
```

- [ ] **Step 2: Capture findings to `/tmp/arch-sync-agent7.md`.**

### Task 8: Reconcile PRD.md §5–§12 (excluding frozen sections)

**Files:**
- Read: `docs/PRD.md` §5 onward (lines 222 through end, stopping at the first frozen version-history/decision-log block)
- Read: `internal/domain/account/` (tenants, tenant hierarchy)
- Read: `internal/match/` (search predicates)
- Read: `internal/domain/search/` (snapshot mechanics referenced in §6 Search)
- Read: `api/grpc/events/` (gRPC CloudEvents for §7 Externalized Processing)

- [ ] **Step 1: Dispatch Agent 8**

```
Thorough reconciliation of docs/PRD.md §5 through last non-frozen section against
cyoda-go codebase at commit <SHA>.

Read PRD sections (stop at any version history / change log / frozen block):
- §5 Multi-Tenancy (lines 222–247): design principles, tenant hierarchy, gRPC member isolation.
- §6 Search (lines 248–294): synchronous search, asynchronous (snapshot) search,
  predicate operators (23 listed), condition types.
- §7 Externalized Processing (lines 295–347): protocol gRPC CloudEvents, connection lifecycle,
  CloudEvent types, tag-based routing, computation member lifecycle, transaction context propagation.
- §8 onward (if present, until frozen content).

Cross-check against:
- internal/domain/account/ (tenant model, hierarchy if any)
- internal/match/ (predicate operators — enumerate what exists; compare to the 23 listed in §6)
- internal/domain/search/ (synchronous direct search, async snapshot search, snapshot TTL)
- api/grpc/events/ (CloudEvent type names, correlation, routing tags)
- internal/grpc/ (member lifecycle, tag-based selection)

Flag:
- Count mismatches (e.g. "23 predicate operators" — is it still 23? List actual count).
- Predicate operator name drift (PRD says X, code says Y).
- CloudEvent type name drift.
- Connection-lifecycle state names.
- Tenant hierarchy claims vs actual account model.

Output format as in Agent 1. Do NOT evaluate content inside any "version history",
"decision log", or similarly-labelled frozen block.
```

- [ ] **Step 2: Capture findings to `/tmp/arch-sync-agent8.md`.**

### Task 9: Synthesize all reconciliation findings

**Files:**
- Read: `/tmp/arch-sync-agent1.md` through `/tmp/arch-sync-agent8.md`
- Create: `/tmp/arch-sync-findings.md` (consolidated fix list)

- [ ] **Step 1: Read all eight agent outputs.**

- [ ] **Step 2: Consolidate into a single fix-list structured as follows:**

```markdown
# Reconciliation findings — consolidated

## A. Critical (must-fix, doc contradicts code)
### A.1 ARCHITECTURE.md
- line XX: [claim] → [code truth at file:line] → [fix]
...
### A.2 PRD.md
...

## B. Significant (material omissions or since-refactored)
...

## C. Minor (stylistic drift)
...

## D. Content extracted to per-plugin files (from Agent 5)
### D.1 memory plugin
- env vars: ...
- §3.2 content extracted verbatim
### D.2 sqlite plugin
- env vars: ...
### D.3 postgres plugin
- env vars: ...
- §3.3 content extracted verbatim
- §3.5 pgx.Tx single-owner extracted verbatim
```

- [ ] **Step 3: Commit the findings to the repo for audit trail (not a permanent doc, but useful for review).**

```bash
# Don't commit /tmp files. Instead, copy the synthesis to a working location:
mkdir -p .worktree-scratch
cp /tmp/arch-sync-findings.md .worktree-scratch/reconciliation-findings.md
# Add .worktree-scratch/ to .gitignore if not already
echo ".worktree-scratch/" >> .gitignore
```

(This keeps the findings accessible during Phase B without polluting main. Delete after Phase C passes.)

---

## Phase B — Apply fixes and restructure (Tasks 10–18)

### Task 10: Create `docs/plugins/` directory with README.md

**Files:**
- Create: `docs/plugins/README.md`

- [ ] **Step 1: Create the directory and index file.**

```bash
mkdir -p docs/plugins
```

- [ ] **Step 2: Write `docs/plugins/README.md`:**

```markdown
# Storage plugins

cyoda-go's storage layer is a plugin system defined by the stable
[`cyoda-go-spi`](https://github.com/cyoda-platform/cyoda-go-spi) module (stdlib-only
Go interfaces and value types). A running binary has exactly one active plugin,
selected at startup via `CYODA_STORAGE_BACKEND`.

## Open-source plugins shipped with the stock binary

- **[`memory`](IN_MEMORY.md)** (default) — ephemeral, microsecond-latency SSI for
  tests and high-throughput digital-twin workloads.
- **[`sqlite`](SQLITE.md)** — persistent, zero-ops single-node storage for desktop,
  edge, and containerised single-node production.
- **[`postgres`](POSTGRES.md)** — durable multi-node storage with SSI via PostgreSQL
  `SERIALIZABLE`; works against any managed PostgreSQL 14+ platform.

## Commercial plugin

A **`cassandra`** plugin is available as a commercial offering from Cyoda for
deployments that need horizontal write scalability beyond a single-primary
PostgreSQL. See [cyoda.com](https://www.cyoda.com) and use its contact page.

## Authoring your own plugin

Third-party plugins (Redis, ScyllaDB, FoundationDB, etc.) can be authored against
`cyoda-go-spi` and compiled into a custom binary via a blank import in a local
`main.go`. The stock plugins serve as reference implementations:

- [`plugins/memory/doc.go`](../../plugins/memory/doc.go) — the simplest possible
  reference implementation; start here.
- [`plugins/postgres/doc.go`](../../plugins/postgres/doc.go) — the fully-featured
  reference implementation with migrations, connection pooling, and multi-node
  wiring.

See [`../ARCHITECTURE.md`](../ARCHITECTURE.md) §2 for the plugin contract.
```

- [ ] **Step 3: Commit.**

```bash
git add docs/plugins/README.md
git commit -m "docs(plugins): create plugins/ directory with index README"
```

### Task 11: Create `docs/plugins/IN_MEMORY.md`

**Files:**
- Create: `docs/plugins/IN_MEMORY.md`

- [ ] **Step 1: Extract content from the current ARCHITECTURE.md §2.1 and §3.2.**

Read `docs/ARCHITECTURE.md` around lines 244–284 (current §2.1) and lines 389–412 (current §3.2).

- [ ] **Step 2: Apply reconciliation-findings items tagged for the memory plugin from `/tmp/arch-sync-findings.md` §D.1.**

- [ ] **Step 3: Write `docs/plugins/IN_MEMORY.md` with the seven-section template:**

```markdown
# `memory` storage plugin

## Capabilities

Ephemeral, in-process state — no disk I/O, no network round-trips, no query
planner on the hot path. The memory plugin's latency profile sits an order of
magnitude ahead of any persistent backend: a full SSI transaction (begin →
read-modify-write → commit) completes in the low microseconds rather than the
milliseconds a Postgres round-trip takes.

That performance envelope makes the memory plugin particularly effective as
the **state-backing for high-throughput digital-twin workloads** — an agentic
software factory where an agent swarm drives thousands of scenario executions
per second against a behavioural twin of a production entity, or a simulation
that replays weeks of production state-machine behaviour in seconds. Same
workflow semantics, same FSM engine, same SSI guarantees as the persistent
backends — without the durability trade-off.

## Concurrency model

<!--
IMPLEMENTER NOTE: This section is extracted verbatim from the current
docs/ARCHITECTURE.md §3.2 (In-Memory SSI Conflict Detection, approximately
lines 389–412). Read that section, copy its content here, and apply any
reconciliation-finding deltas tagged §D.1 in /tmp/arch-sync-findings.md.
The final content describes: the committedLog structure, active-transaction
tracking, read-set and write-set recording, and first-committer-wins
conflict detection at commit time.
-->

## Transaction manager

<!--
IMPLEMENTER NOTE: Extract from current §3.2 tail + §3.6 memory bullet.
Reference plugins/memory/txmanager.go for the concrete type names.
-->
See `plugins/memory/txmanager.go` for the `TransactionManager` implementation.
The manager owns the per-transaction read/write sets and the committed-log
window used for conflict detection.

## Data model and schema

No persistence. All state lives in Go data structures inside the process.
Restart loses everything. Data structures mirror the entity/model/workflow/KV/
message/audit/search boundaries defined by the SPI — see
`plugins/memory/<type>_store.go` for each.

## Configuration (env vars)

The memory plugin has no plugin-specific env vars. It is the default backend when
`CYODA_STORAGE_BACKEND` is unset or set to `memory`.

## Operational notes and limits

- Process-local; data lost on restart.
- Single-process only — multiple cyoda processes against the "same" memory plugin
  would have independent state (there is no shared store).
- No persistence snapshots. Pair with periodic exports to a durable backend if
  agent/simulation results matter beyond the session.
- No tenant isolation beyond application-layer.

## When to use / when not to use

**Use:** tests, short-lived local dev, parity baselines, high-throughput digital-twin
simulations where durability is delegated to an external snapshot mechanism.

**Don't use:** production where any restart would lose data; multi-process
deployments; anywhere durable storage is a functional requirement.
```

- [ ] **Step 4: Run the verification grep for this file (Cassandra mechanism leak):**

```bash
grep -rEi 'HLC|hybrid logical clock|Redpanda|shard.epoch|ClusterBroadcaster|USING TIMESTAMP|transaction_log_idx|2PC|two.phase commit|LWT|last.writer.wins|cyoda-go-cassandra|CASSANDRA_BACKEND_DESIGN' docs/plugins/IN_MEMORY.md
```

Expected: no output.

- [ ] **Step 5: Commit.**

```bash
git add docs/plugins/IN_MEMORY.md
git commit -m "docs(plugins): extract memory plugin docs to IN_MEMORY.md"
```

### Task 12: Create `docs/plugins/SQLITE.md`

**Files:**
- Create: `docs/plugins/SQLITE.md`

- [ ] **Step 1: Gather source material.**

Read:
- `docs/superpowers/specs/2026-04-15-sqlite-storage-plugin-design.md` (the plugin's own design spec)
- `plugins/sqlite/plugin.go` (ConfigVars)
- `plugins/sqlite/config.go` (OS-aware default DB path)
- `plugins/sqlite/store_factory.go` (flock, WASM driver)
- Agent 7/8 findings for any drift

- [ ] **Step 2: Write the file following the seven-section template.**

```markdown
# `sqlite` storage plugin

## Capabilities

Persistent, zero-ops single-node storage. Embedded in-process via a pure-Go
(WASM) SQLite driver — no CGO, clean cross-compilation, future
[sqlite-vec](https://github.com/asg017/sqlite-vec) support. The ideal backend
for desktop binary users, edge deployments, and containerised single-node
production.

Search predicate pushdown to SQL — the majority of entity search predicates
resolve in the SQL engine rather than post-filter in Go, matching the
PostgreSQL plugin's search shape.

## Concurrency model

Application-layer Serializable Snapshot Isolation (SSI), **ported from the
memory plugin**. SQLite provides only database-level write locking (zero write
concurrency); cyoda's SSI gives first-committer-wins entity-level conflict
detection on top.

An exclusive `flock` on the database file is acquired at startup and held for
the process lifetime. A second cyoda process against the same file fails fast
with a clear error. The flock is required because the SSI state (committed-log,
active-transaction set) is per-process — two processes sharing a file would
have independent SSI and silently corrupt each other's conflict detection.

`flock` does not work on NFS, but SQLite itself is unreliable on NFS, so the
restriction is implicit in choosing SQLite at all.

## Transaction manager

Same SSI engine as the memory plugin (`TransactionManager` ported verbatim;
SQLite is the durability layer, not the concurrency controller). Reference:
`plugins/sqlite/txmanager.go`.

## Data model and schema

Mirrors the PostgreSQL logical schema with SQLite optimisations:
- JSONB columns stored as `BLOB` with `jsonb()` / `json()` functions — 2-5×
  faster `json_extract()` than TEXT JSON. Plugin asserts
  `sqlite_version() >= 3.45.0` at startup.
- `STRICT` tables + `WITHOUT ROWID` on append-only tables (e.g. `entity_versions`).
- INTEGER timestamps (Unix nanoseconds) — 15-25% smaller, 15-30% faster point
  lookups than TEXT timestamps.

Migrations via `golang-migrate` with embedded SQL files — same pattern as the
postgres plugin. Runs automatically on startup when
`CYODA_SQLITE_AUTO_MIGRATE=true` (the default).

## Configuration (env vars)

| Var | Default | Purpose |
|---|---|---|
| `CYODA_SQLITE_PATH` | `$XDG_DATA_HOME/cyoda/cyoda.db` on Linux/macOS (fallback `~/.local/share/cyoda/cyoda.db`); `%LocalAppData%\cyoda\cyoda.db` on Windows | Database file path |
| `CYODA_SQLITE_AUTO_MIGRATE` | `true` | Run embedded SQL migrations on startup |
| `CYODA_SQLITE_BUSY_TIMEOUT` | `5s` | Wait time for write lock before returning `SQLITE_BUSY` |
| `CYODA_SQLITE_CACHE_SIZE` | `64000` (KiB) | Page cache size in KiB |
| `CYODA_SQLITE_SEARCH_SCAN_LIMIT` | `100000` | Max rows examined per search when a residual filter applies |

## Operational notes and limits

- **No CGO.** Uses [`ncruces/go-sqlite3`](https://github.com/ncruces/go-sqlite3)
  (WASM-based); ~2-3× slower on micro-benchmarks than native C SQLite. Accepted
  for clean cross-compile and the sqlite-vec roadmap.
- **Tenant isolation is application-layer only.** No RLS (SQLite has no native
  row-level security).
- **Single-process, single-node.** See concurrency model for the flock requirement.
- **NFS unsupported.**

## When to use / when not to use

**Use:** desktop binary users, containerised single-node production, embedded
deployments, edge devices, any scenario where "memory plugin but must survive
restart" is the requirement.

**Don't use:** multi-node deployments, multi-process deployments, NFS-mounted
storage, workloads that need horizontal write scale (go to postgres or cassandra).
```

- [ ] **Step 3: Apply any Agent 5/7/8 findings tagged for SQLite.**

- [ ] **Step 4: Run verification grep.**

```bash
grep -rEi 'HLC|hybrid logical clock|Redpanda|shard.epoch|ClusterBroadcaster|USING TIMESTAMP|transaction_log_idx|2PC|two.phase commit|LWT|last.writer.wins|cyoda-go-cassandra|CASSANDRA_BACKEND_DESIGN' docs/plugins/SQLITE.md
```

Expected: no output.

- [ ] **Step 5: Commit.**

```bash
git add docs/plugins/SQLITE.md
git commit -m "docs(plugins): add SQLITE.md — first-class sqlite plugin documentation"
```

### Task 13: Create `docs/plugins/POSTGRES.md`

**Files:**
- Create: `docs/plugins/POSTGRES.md`

- [ ] **Step 1: Extract content from current ARCHITECTURE.md §2.2, §3.3, §3.5, §9 postgres subsection.**

Read:
- `docs/ARCHITECTURE.md` lines 285–350 (current §2.2)
- `docs/ARCHITECTURE.md` lines 413–462 (current §3.3, §3.5)
- `docs/ARCHITECTURE.md` lines 1157–1168 (current §9 postgres subsection)
- Agent 1/5 findings tagged §D.3

- [ ] **Step 2: Write the file following the seven-section template.**

```markdown
# `postgres` storage plugin

## Capabilities

Durable multi-node storage using PostgreSQL as the single source of truth for
transaction isolation. Each transaction holds a `pgx.Tx` handle in one cyoda
node's process memory, executing under PostgreSQL's `SERIALIZABLE` isolation —
cyoda's multi-node architecture pins each transaction to its owning node via
`txID → pgx.Tx` affinity, giving active-active HA without distributed-transaction
overhead.

**Works against any managed PostgreSQL 14+ platform:** AWS RDS, Google Cloud SQL,
Azure Database for PostgreSQL, Supabase, Neon, Aiven, Crunchy Bridge, Render,
Fly.io Postgres, DigitalOcean Managed Databases, and self-hosted.

## Concurrency model

<!--
IMPLEMENTER NOTE: Extract verbatim from current docs/ARCHITECTURE.md §3.3
(PostgreSQL SERIALIZABLE + Error Code 40001, approximately lines 413–434).
Apply reconciliation-finding deltas tagged §D.3 in /tmp/arch-sync-findings.md.
Content covers: SERIALIZABLE isolation mode set per-transaction, the 40001
"could not serialize access due to concurrent update" retry semantics, and
cyoda's retry-bounded handling of the error class.
-->

## Transaction manager

Lightweight in-process lifecycle tracker. The real serialization guarantee comes
from PostgreSQL's `SERIALIZABLE` isolation inside the stores. The TM assigns IDs,
tracks active/committed sets with timestamps, and supports savepoints as a local
stack. See `plugins/postgres/txmanager.go`.

### `pgx.Tx` single-owner property

<!--
IMPLEMENTER NOTE: Extract verbatim from current docs/ARCHITECTURE.md §3.5
(pgx.Tx Single-Owner Property, approximately lines 452–462). Apply any
reconciliation-finding deltas tagged §D.3.
-->

## Data model and schema

<!--
IMPLEMENTER NOTE: Extract the postgres data-model description and migration
infrastructure from current docs/ARCHITECTURE.md §2.2 (approximately lines
285–350). Apply reconciliation-finding deltas tagged §D.3.
-->

Schema lives under `plugins/postgres/migrations/` (embedded via `embed.FS`).
Applied on startup via `golang-migrate` when `CYODA_POSTGRES_AUTO_MIGRATE=true`
(the default). Entities, models, KV, messages, workflows, and audit each have
their dedicated tables with appropriate JSONB columns and GIN indexes.

## Configuration (env vars)

<!--
IMPLEMENTER NOTE: The complete list of postgres env vars comes from
plugins/postgres/plugin.go ConfigVars(). Enumerate every entry from that
method into the table below. Cross-check against current §9 postgres
subsection of docs/ARCHITECTURE.md (approximately lines 1157–1168) and
apply Agent 5 findings tagged §D.3 for any default-value drift or
missing entries. The _FILE-suffix variant of CYODA_POSTGRES_URL is
implemented in plugins/postgres/config.go via resolveSecretWith.
-->

| Var | Default | Purpose |
|---|---|---|
| `CYODA_POSTGRES_URL` (or `_FILE`) | *(required)* | PostgreSQL connection string |
| `CYODA_POSTGRES_AUTO_MIGRATE` | `true` | Run embedded SQL migrations on startup |
| *(remaining rows enumerated from ConfigVars() during implementation)* | | |

### Managed-platform notes

Platforms that front PostgreSQL with **PgBouncer in transaction pooling mode**
(Supabase port 6543, Neon pooled endpoint) strip prepared-statement caching
mid-session. `pgx`'s default extended-query protocol uses prepared statements.

Options:
- Use the platform's **direct-connection endpoint** (Supabase 5432, Neon direct)
  — recommended for cyoda.
- Set `default_query_exec_mode=exec` on the `pgx` pool to force simple-query
  mode — accepts a small per-query overhead in exchange for pooler compatibility.

cyoda uses transaction-scoped `SET LOCAL` for RLS (tenant isolation) only — no
session-level state fights PgBouncer transaction mode beyond the prepared-statement
cache.

## Operational notes and limits

- Requires PostgreSQL 14+.
- Recommended HA mode: primary + streaming replica with automatic failover.
- Cluster-mode cyoda uses Postgres for transaction isolation and cyoda's own
  gossip registry for node discovery — the two are orthogonal.
- Scale-out is bounded by the PostgreSQL primary's write capacity. Read-replicas
  are not yet wired in to cyoda.

## When to use / when not to use

**Use:** clustered production, high consistency requirements, audit/compliance
workloads, any deployment where a managed PostgreSQL platform is the infrastructure
baseline.

**Don't use:** single-process desktop deployments (use sqlite), workloads whose
write volume exceeds what a single Postgres primary can sustain (consider the
commercial cassandra plugin).
```

- [ ] **Step 3: Apply Agent 1/5 reconciliation findings.**

- [ ] **Step 4: Run verification grep.**

```bash
grep -rEi 'HLC|hybrid logical clock|Redpanda|shard.epoch|ClusterBroadcaster|USING TIMESTAMP|transaction_log_idx|2PC|two.phase commit|LWT|last.writer.wins|cyoda-go-cassandra|CASSANDRA_BACKEND_DESIGN' docs/plugins/POSTGRES.md
```

Expected: no output.

- [ ] **Step 5: Commit.**

```bash
git add docs/plugins/POSTGRES.md
git commit -m "docs(plugins): extract postgres plugin docs to POSTGRES.md"
```

### Task 14: Restructure ARCHITECTURE.md §2 — trim to index + §2.4 Cassandra capabilities

**Files:**
- Modify: `docs/ARCHITECTURE.md` §2 (lines 211–366)

- [ ] **Step 1: Read current §2 (lines 211–366).**

- [ ] **Step 2: Replace §2 body with the target shape from the spec:**

<!--
IMPLEMENTER NOTE: The opening storage-agnostic paragraphs of §2 (approximately
lines 211–243 of the current docs/ARCHITECTURE.md — SPI contract, blank-import
binary pattern, plugin resolution at startup) are kept verbatim. Only the
per-plugin subsections (§2.1 through §2.3) are replaced with the abbreviated
forms shown below, and §2.4 is added.
-->

```markdown
## 2. Storage Architecture

<!-- keep existing opening paragraphs here -->

### 2.1 The `memory` plugin (`plugins/memory/`)

Ephemeral, in-process state with microsecond-latency SSI. Default for tests,
local development, and high-throughput digital-twin workloads where durability
is delegated elsewhere. Full detail in
[docs/plugins/IN_MEMORY.md](plugins/IN_MEMORY.md).

### 2.2 The `sqlite` plugin (`plugins/sqlite/`)

Persistent, zero-ops single-node storage. Embedded in-process via a pure-Go
(WASM) SQLite driver, exclusive file lock, application-layer SSI ported from
the memory plugin, search predicate pushdown to SQL. Default for desktop
binary, edge deployments, and containerised single-node production. Full
detail in [docs/plugins/SQLITE.md](plugins/SQLITE.md).

### 2.3 The `postgres` plugin (`plugins/postgres/`)

Durable multi-node storage with SSI via PostgreSQL `SERIALIZABLE`. Works
against any managed PostgreSQL 14+ platform (RDS, Cloud SQL, Azure, Supabase,
Neon, Aiven, Crunchy Bridge, self-hosted, etc.). Full detail in
[docs/plugins/POSTGRES.md](plugins/POSTGRES.md).

### 2.4 The `cassandra` plugin (commercial)

A Cassandra-backed storage plugin is available as a commercial offering from
Cyoda. It slots into cyoda-go through the same `spi.Plugin` contract as the
open-source plugins — operators select it at runtime via
`CYODA_STORAGE_BACKEND=cassandra`.

**Capability envelope:**

- Horizontal write scalability across a Cassandra cluster
- Snapshot isolation with first-committer-wins semantics
- Append-only point-in-time storage with full historical reads
- No single points of failure
- Multi-node consistency

**When it fits:** workloads whose write volume or availability requirements
outgrow a single-primary PostgreSQL deployment — while keeping the same EDBMS
semantics (entities, workflows, temporal history, SSI) that the open-source
binary provides on top of the in-memory / sqlite / postgres plugins.

**Interested?** Get in touch with Cyoda at [cyoda.com](https://www.cyoda.com)
and use its contact page.
```

- [ ] **Step 3: Run the Cassandra mechanism grep.**

```bash
grep -nEi 'HLC|hybrid logical clock|Redpanda|shard.epoch|ClusterBroadcaster|USING TIMESTAMP|transaction_log_idx|2PC|two.phase commit|LWT|last.writer.wins|cyoda-go-cassandra|CASSANDRA_BACKEND_DESIGN' docs/ARCHITECTURE.md
```

Expected: no output in §2 range (may still have hits elsewhere — those are cleaned in Task 15).

- [ ] **Step 4: Commit.**

```bash
git add docs/ARCHITECTURE.md
git commit -m "docs(architecture): trim §2 to plugins index + §2.4 Cassandra capabilities"
```

### Task 15: Sweep remaining Cassandra mechanism leaks from ARCHITECTURE.md

**Files:**
- Modify: `docs/ARCHITECTURE.md` — lines 44 (repositories table), ~231 (SPI lifecycle note), ~469 (§3.6 TM bullet), ~1169–1186 (§9 Cassandra subsection), ~1310–1316 (§11 observability), ~1322 (§12 cyoda-go-cassandra reference)

- [ ] **Step 1: Remove row from repositories table at line 44.**

Find: `| \`cyoda-go-cassandra\` | github.com/cyoda-platform/cyoda-go-cassandra | Proprietary Cassandra storage plugin + full binary | Proprietary |`

Delete that row.

- [ ] **Step 2: Remove parenthetical in SPI lifecycle note (~line 231).**

Find: `(e.g. cassandra's shard-rebalance wait)` and remove the parenthetical. Keep the generic SPI point.

- [ ] **Step 3: Replace §3.6 cassandra bullet.**

Find:
```
- **cassandra plugin** — a coordinator-managed 2PC over a message broker, with HLC fencing, per-shard epoch ownership, and committed-log replay for recovery (see Section 2.3). Owns its own TM because Cassandra has neither `SERIALIZABLE` nor native multi-partition transactions.
```

Replace with:
```
- **Commercial plugins** (e.g. the Cassandra plugin from Cyoda) implement their
  own `TransactionManager` against their underlying store's primitives. See
  §2.4 for the capability envelope of the commercial Cassandra plugin.
```

- [ ] **Step 4: Remove §9 Cassandra subsection entirely.**

Delete lines ~1169–1186 (the `### Cassandra plugin (CYODA_STORAGE_BACKEND=cassandra, available only in the cyoda-go-cassandra binary)` heading through the last env-var bullet of that subsection).

- [ ] **Step 5: Generalise §11 Observability.**

Find the paragraph with "cyoda.cassandra.cql.duration, cyoda.cassandra.batch.duration..." and rewrite:

```
**Plugin-level instrumentation:** plugins are free to add their own spans and
metrics under a plugin-specific namespace. The `memory` and `postgres` plugins
emit minimal plugin-level telemetry (their behaviour is well-captured by the
core transaction/workflow/dispatch spans); other plugins may add detailed
instrumentation scoped to their own namespace as their hot-path semantics warrant.
```

Also remove the paragraph that mentions "trace context propagation through the cassandra plugin's broker messages, search pipeline, and external-processor gRPC/CloudEvents is incomplete" — replace with a more generic known-gap note if §11 still has a Known-Gaps block, otherwise just delete.

- [ ] **Step 6: Remove cyoda-go-cassandra reference in §12.**

Find: `cyoda-go-cassandra for cassandra plugin` and remove. If the sentence becomes nonsensical after removal, rewrite it to reference only open-source repositories.

- [ ] **Step 7: Run the Cassandra mechanism grep across the whole file.**

```bash
grep -nEi 'HLC|hybrid logical clock|Redpanda|shard.epoch|ClusterBroadcaster|USING TIMESTAMP|transaction_log_idx|2PC|two.phase commit|LWT|last.writer.wins|cyoda-go-cassandra|CASSANDRA_BACKEND_DESIGN' docs/ARCHITECTURE.md
```

Expected: no output.

- [ ] **Step 8: Commit.**

```bash
git add docs/ARCHITECTURE.md
git commit -m "docs(architecture): sweep Cassandra mechanism leaks to capability-only"
```

### Task 16: Apply reconciliation findings to ARCHITECTURE.md non-storage sections

**Files:**
- Modify: `docs/ARCHITECTURE.md` — any non-§2 sections with findings in `/tmp/arch-sync-findings.md` §A, §B, §C

- [ ] **Step 1: Walk through `/tmp/arch-sync-findings.md` §A (Critical) for ARCHITECTURE.md items, applying each fix.**

For each item:
- Use `Read` to load the surrounding context.
- Use `Edit` to make the fix.
- Verify the edit landed.

- [ ] **Step 2: Walk through §B (Significant) for ARCHITECTURE.md items, applying each.**

- [ ] **Step 3: Walk through §C (Minor) for ARCHITECTURE.md items, applying each.**

- [ ] **Step 4: Fix the cross-references at lines 467–468** (flagged during spec self-review):

Find (current text):
```
- **memory plugin** — in-process SSI with entity-level read/write sets and a committed-transaction log (Section 3.2).
- **postgres plugin** — lightweight in-process lifecycle tracker. The real serialization guarantee comes from PostgreSQL's `SERIALIZABLE` isolation inside the stores (Section 3.3). ...
```

Replace with:
```
- **memory plugin** — in-process SSI with entity-level read/write sets and a committed-transaction log (see [docs/plugins/IN_MEMORY.md](plugins/IN_MEMORY.md) for the full implementation).
- **postgres plugin** — lightweight in-process lifecycle tracker. The real serialization guarantee comes from PostgreSQL's `SERIALIZABLE` isolation inside the stores (see [docs/plugins/POSTGRES.md](plugins/POSTGRES.md)). ...
```

- [ ] **Step 5: Fix the package-layout diagram** — `cmd/cyoda-go/main.go` → `cmd/cyoda/main.go`.

- [ ] **Step 6: Update the header metadata.**

Find:
```
**Version:** 2.0
**Date:** 2026-04-14
**Status:** Target state after the storage-plugin architecture refactor (Plans 1–5). See `docs/superpowers/specs/2026-04-13-storage-plugin-architecture-design.md` for the refactor plan.
```

Replace with:
```
**Version:** 2.1
**Date:** 2026-04-18
**Status:** Current as of the Helm provisioning drop (PR #60, commit 31435d2).
```

- [ ] **Step 7: Remove §3.2 and §3.3 from ARCHITECTURE.md (content extracted to plugin files).**

In their place, add a one-line pointer:

```
### 3.2 Memory plugin SSI

See [docs/plugins/IN_MEMORY.md](plugins/IN_MEMORY.md).

### 3.3 Postgres `SERIALIZABLE` + retry

See [docs/plugins/POSTGRES.md](plugins/POSTGRES.md).
```

(Keep the section numbers intact to avoid breaking any intra-document references.)

- [ ] **Step 8: Similarly, §3.5 pgx.Tx content — extract to POSTGRES.md, replace with pointer.**

- [ ] **Step 9: Commit.**

```bash
git add docs/ARCHITECTURE.md
git commit -m "docs(architecture): apply reconciliation findings; link out to plugin docs"
```

### Task 17: Rewrite PRD.md §1 storage callout + apply reconciliation findings

**Files:**
- Modify: `docs/PRD.md`

- [ ] **Step 1: Rewrite the §1 storage-plugin blockquote (currently lines 23–26).**

Find: the blockquote starting `> **Storage plugin architecture.** Cyoda-Go's storage layer is a plugin system...`

Replace with the spec-approved version (from `docs/superpowers/specs/2026-04-18-architecture-prd-sync-design.md` §5.1):

```markdown
> **Storage plugin architecture.** Cyoda-Go's storage layer is a plugin system
> defined by the stable `cyoda-go-spi` module (stdlib-only Go interfaces and
> value types). A running binary has exactly one active plugin, selected at
> startup via `CYODA_STORAGE_BACKEND`. The stock `cyoda-go` binary ships with
> three open-source plugins:
>
> - **`memory`** (default) — ephemeral, microsecond-latency SSI for tests and
>   high-throughput digital-twin workloads.
> - **`sqlite`** — persistent, zero-ops single-node storage for desktop, edge,
>   and containerised single-node production.
> - **`postgres`** — durable multi-node storage with SSI via PostgreSQL
>   `SERIALIZABLE`; works against any managed PostgreSQL platform.
>
> A commercial `cassandra` plugin is also available from Cyoda for deployments
> that need horizontal write scalability beyond what a single-primary
> PostgreSQL can provide — see [cyoda.com](https://www.cyoda.com) and use its
> contact page.
>
> Third-party plugins (Redis, ScyllaDB, FoundationDB, etc.) can be authored
> against `cyoda-go-spi` and compiled into a custom binary via a blank import.
> See `docs/ARCHITECTURE.md` for the plugin contract and `docs/plugins/` for
> per-plugin specifics.
```

- [ ] **Step 2: Generalise §1 Scale Profile** (currently line 25 area).

Find: `Small compute clusters (3-10 stateless Go nodes) with a shared PostgreSQL instance.`

Replace with:

```markdown
Scale envelope depends on storage plugin:

- **`memory`** — single process, bounded by host RAM.
- **`sqlite`** — single node, persistent; write throughput bounded by local disk.
- **`postgres`** — small compute clusters (3–10 stateless Go nodes) behind a
  load balancer, sharing a primary PostgreSQL. Active-active HA; any node
  serves any request.
- **`cassandra`** (commercial) — multi-cluster, horizontal write scale-out
  without a single-primary bottleneck.
```

- [ ] **Step 3: Generalise §1 Cost Model** similarly — acknowledge all four backends.

- [ ] **Step 4: Add one sentence to §4 Transaction Model:**

Find a place near the ACID discussion and add:

```
SQLite plugin uses application-layer SSI ported from the memory plugin; the
commercial Cassandra plugin implements SSI against its own primitives.
```

- [ ] **Step 5: Walk through `/tmp/arch-sync-findings.md` §A/§B/§C for PRD.md items, applying each fix.**

Follow the same walk-through pattern as Task 16.

- [ ] **Step 6: Update the PRD.md header metadata.**

Find:
```
**Version:** 2.0
**Date:** 2026-04-14
**Status:** Target state after the storage-plugin architecture refactor (Plans 1–5). See `docs/superpowers/specs/2026-04-13-storage-plugin-architecture-design.md` for the refactor plan.
```

Replace with:
```
**Version:** 2.1
**Date:** 2026-04-18
**Status:** Current as of the Helm provisioning drop (PR #60, commit 31435d2).
```

- [ ] **Step 7: Commit.**

```bash
git add docs/PRD.md
git commit -m "docs(prd): rewrite storage callout; apply reconciliation findings; refresh header"
```

### Task 18: Clean up working-scratch and spec artefacts

**Files:**
- Modify: `.gitignore` (if `.worktree-scratch/` was added in Task 9)
- Delete: `.worktree-scratch/reconciliation-findings.md`

- [ ] **Step 1: Delete the scratch directory.**

```bash
rm -rf .worktree-scratch/
```

- [ ] **Step 2: If `.gitignore` was modified to include `.worktree-scratch/`, leave that line in place** (future runs may use the same pattern).

- [ ] **Step 3: No commit needed unless `.gitignore` changed; if it did, commit the gitignore update:**

```bash
# Only if .gitignore was modified in Task 9:
git add .gitignore
git commit -m "chore(gitignore): add .worktree-scratch/ for temporary agent findings"
```

---

## Phase C — Verification (Tasks 19–20)

### Task 19: Run the six verification checks

**Files:**
- Read-only: `docs/ARCHITECTURE.md`, `docs/PRD.md`, `docs/plugins/*.md`

- [ ] **Step 1: Cassandra mechanism grep across all docs files.**

```bash
grep -rEi 'HLC|hybrid logical clock|Redpanda|shard.epoch|ClusterBroadcaster|USING TIMESTAMP|transaction_log_idx|2PC|two.phase commit|LWT|last.writer.wins|cyoda-go-cassandra|CASSANDRA_BACKEND_DESIGN' docs/ARCHITECTURE.md docs/PRD.md docs/plugins/
```

Expected: **no output**. Any hit is a leak — fix it before proceeding.

- [ ] **Step 2: Rename-residue grep.**

```bash
grep -rE 'cmd/cyoda-go|deploy/helm/cyoda-go|cyoda-go-0\.' docs/ARCHITECTURE.md docs/PRD.md docs/plugins/
```

Expected: **no output**.

- [ ] **Step 3: Per-plugin template conformance.**

Each of `docs/plugins/IN_MEMORY.md`, `docs/plugins/SQLITE.md`, `docs/plugins/POSTGRES.md` should have these seven section headings in order:

```
## Capabilities
## Concurrency model
## Transaction manager
## Data model and schema
## Configuration (env vars)
## Operational notes and limits
## When to use / when not to use
```

Check:
```bash
for f in docs/plugins/IN_MEMORY.md docs/plugins/SQLITE.md docs/plugins/POSTGRES.md; do
  echo "=== $f ==="
  grep -nE '^## ' "$f"
done
```

Expected: each file shows all seven headings in order.

- [ ] **Step 4: Cross-link resolution.**

```bash
grep -nE '\]\(plugins/' docs/ARCHITECTURE.md | while IFS=: read -r line_num line; do
  # Extract the link path
  path=$(echo "$line" | grep -oE '\]\(plugins/[^)]+\)' | sed 's/^](\(.*\))$/\1/')
  if [ -n "$path" ]; then
    full_path="docs/$path"
    if [ ! -f "$full_path" ]; then
      echo "BROKEN LINK at line $line_num: $path"
    fi
  fi
done
```

Expected: **no "BROKEN LINK" lines**.

- [ ] **Step 5: Env-var cross-reference.**

```bash
# Collect every CYODA_* env var documented in the docs tree
grep -rhoE 'CYODA_[A-Z0-9_]+' docs/ARCHITECTURE.md docs/plugins/ | sort -u > /tmp/env-docs.txt

# Collect every CYODA_* env var referenced in code
grep -rhoE 'CYODA_[A-Z0-9_]+' app/ cmd/ plugins/ internal/ | sort -u > /tmp/env-code.txt

# Orphans in doc (documented but not in code)
echo "=== Documented but not in code ==="
comm -23 /tmp/env-docs.txt /tmp/env-code.txt

# Orphans in code (in code but not documented)
echo "=== In code but not documented ==="
comm -13 /tmp/env-docs.txt /tmp/env-code.txt
```

Expected: both lists empty. Any orphan is a drift — fix or flag for the next reconciliation pass.

Some acceptable exceptions (if any remain): `_FILE` variants may only appear in READMEs, not in the plugin docs — that's intentional and can be excluded.

- [ ] **Step 6: Frozen-sections intactness.**

```bash
# Confirm §13 of ARCHITECTURE.md was not edited by us (should only have header-line drift)
git log --oneline -p HEAD~10..HEAD -- docs/ARCHITECTURE.md | grep -A 200 "Section 13" | head -50
```

Manual scan: the only change in the §13 range should be any version/date header drift from Task 16, not content.

- [ ] **Step 7: If any check fails, loop back to the relevant task. If all six pass, proceed to Task 20.**

### Task 20: Request code review + user review gate

**Files:**
- Read-only: all new and modified docs.

- [ ] **Step 1: Invoke the requesting-code-review skill** to dispatch a code-reviewer subagent over the commit range.

Prompt notes: this is a docs-only change; the reviewer should focus on clarity, consistency, completeness against the spec, and any Cassandra mechanism residue not caught by grep (e.g., rephrased but still revealing content).

- [ ] **Step 2: Apply any reviewer-flagged fixes.**

- [ ] **Step 3: Commit the fixes** (one commit per category of fix — Critical / Important / Minor — per the receiving-code-review pattern).

- [ ] **Step 4: Create a PR.**

```bash
# Use a feature branch if main is reserved
git checkout -b docs/architecture-prd-sync
git push -u origin docs/architecture-prd-sync
gh pr create --title "docs: sync ARCHITECTURE.md and PRD.md with codebase; split storage content to plugins/*" \
  --body "$(cat <<'EOF'
## Summary

Brings docs/ARCHITECTURE.md and docs/PRD.md into perfect sync with the codebase
at HEAD. Restructures storage-plugin content: ARCHITECTURE.md becomes
storage-agnostic, per-plugin depth moves to docs/plugins/{IN_MEMORY,SQLITE,POSTGRES}.md.
Sanitises Cassandra plugin references to capability-only framing.

## Changes

- NEW: docs/plugins/README.md, IN_MEMORY.md, SQLITE.md, POSTGRES.md
- MODIFIED: docs/ARCHITECTURE.md — §2 restructured, Cassandra mechanism leaks
  removed, per-plugin content extracted, reconciliation findings applied,
  header bumped to Version 2.1.
- MODIFIED: docs/PRD.md — §1 storage callout rewritten, scale/cost sections
  generalised, reconciliation findings applied, header bumped to Version 2.1.

## Spec

docs/superpowers/specs/2026-04-18-architecture-prd-sync-design.md

## Test plan

- [x] Cassandra mechanism grep returns zero hits
- [x] Rename-residue grep returns zero hits
- [x] Per-plugin template conformance
- [x] Cross-link resolution
- [x] Env-var cross-reference (documented ↔ code)
- [x] Frozen-sections intactness
EOF
)"
```

- [ ] **Step 5: User review gate.**

Ping the user: "PR open at <URL>. Please review and merge when ready."

---

## Rollback plan

If any task fails irrecoverably:
- Phase B tasks are small, single-commit units. Revert the offending commit with `git revert <sha>` and redo.
- Phase A agent outputs (`/tmp/arch-sync-agentN.md`) are write-only; re-running an agent simply overwrites.
- Phase C is non-destructive (grep-based checks).
- Spec (`docs/superpowers/specs/2026-04-18-architecture-prd-sync-design.md`) is the source of truth — if a design decision is in dispute, re-read the spec.
