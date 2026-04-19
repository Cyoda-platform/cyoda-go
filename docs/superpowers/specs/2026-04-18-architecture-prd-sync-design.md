# Architecture + PRD documentation sync — Design Specification

**Status:** draft · **Date:** 2026-04-18 · **Target:** perfect alignment between `docs/ARCHITECTURE.md` + `docs/PRD.md` and the current codebase at HEAD (commit `31435d2` at spec-write time)

---

## 1. Scope and goals

`docs/ARCHITECTURE.md` (1555 lines) and `docs/PRD.md` (712 lines) are the canonical technical and product references for cyoda-go. Both documents are stamped **Version 2.0, Date 2026-04-14** with a status line claiming "Target state after the storage-plugin architecture refactor (Plans 1–5)". Since that stamp the storage-plugin refactor shipped, the SQLite plugin landed as a first-class stock plugin, the desktop/Docker/Helm provisioning track shipped (PR #45, #54, #60), and many env-var defaults / endpoints have evolved.

### Goals

1. **Every statement in `ARCHITECTURE.md` and `PRD.md` is grounded in a verifiable code reference** at spec-write commit. Drift is the exception that gets flagged and fixed, not the baseline.
2. **Structural split:** `ARCHITECTURE.md` becomes storage-agnostic. Per-plugin depth moves to `docs/plugins/IN_MEMORY.md`, `docs/plugins/SQLITE.md` (new), `docs/plugins/POSTGRES.md`.
3. **SQLite gets first-class treatment** parallel to the other stock plugins — currently missing entirely from both documents.
4. **Cassandra plugin is presented as a commercial capability**, not a technical design. No mechanism leakage, no repository reference, no design-doc pointer — just capability envelope, value proposition, and a "contact Cyoda" call to action.
5. **Legacy rename residue** (`cmd/cyoda-go/`, pre-rename chart tags, etc.) swept from both documents.

### Frozen — not touched in this pass

- `ARCHITECTURE.md §13 Design Decisions Log` — historical record, immutable by convention.
- `PRD.md` version history / version-2.0 status line content prior to the Version bump. The version bump itself is allowed; prior version entries are not re-edited.
- Any `docs/adr/` file.
- Any `docs/superpowers/` file other than this spec and its downstream plan.

### Non-goals

- Rewriting git history to scrub pre-sanitization commits. Considered and rejected (see §6).
- Expanding `PRD.md` scope into new product domains.
- Adding feature sections that are planned-but-not-implemented — those stay in `§12 Planned Features`.
- Revising the `cyoda-go-spi` module documentation.
- Touching the per-plugin `doc.go` files (they're reference implementations for plugin authors; they can drift with the code).

---

## 2. Target file structure

```
docs/
  ARCHITECTURE.md              # storage-agnostic core — see §3 for content
  PRD.md                       # single file; updated per §5
  plugins/
    README.md                  # one-page index + "authoring a plugin" pointer
    IN_MEMORY.md               # memory plugin
    SQLITE.md                  # NEW — sqlite plugin
    POSTGRES.md                # postgres plugin
  superpowers/
    specs/
      2026-04-18-architecture-prd-sync-design.md   # this spec
      ...
```

The `cassandra` plugin is not represented as a file — it surfaces only as a capability paragraph in `ARCHITECTURE.md §2.4` and a mention in `PRD.md §1`.

---

## 3. `ARCHITECTURE.md` — target shape

After the split, the storage-agnostic core retains the same top-level section numbering as today (so cross-references from other docs still hold), but the contents of §2 shrink dramatically.

### §2. Storage Architecture (≈30 lines, down from ≈155)

- Opening storage-agnostic paragraphs (SPI contract, blank-import binary pattern, plugin-resolution at startup) — **kept**.
- **§2.1 The `memory` plugin** — capability one-liner + link to `docs/plugins/IN_MEMORY.md`.
- **§2.2 The `sqlite` plugin** — capability one-liner + link to `docs/plugins/SQLITE.md`.
- **§2.3 The `postgres` plugin** — capability one-liner + link to `docs/plugins/POSTGRES.md`.
- **§2.4 The `cassandra` plugin (commercial)** — three-paragraph block:
  1. One-sentence framing ("A Cassandra-backed storage plugin is available as a commercial offering from Cyoda. It slots into cyoda-go through the same `spi.Plugin` contract as the open-source plugins — operators select it at runtime via `CYODA_STORAGE_BACKEND=cassandra`.")
  2. Capability envelope bullet list: horizontal write scalability · snapshot isolation with first-committer-wins · append-only point-in-time storage with full historical reads · no single points of failure · multi-node consistency.
  3. Value-proposition sentence plus contact CTA pointing to `https://www.cyoda.com` with direction to use its contact page.

No mention of: the proprietary repository, the `CASSANDRA_BACKEND_DESIGN.md` document, HLC, shard epochs, 2PC phases, transaction reaper, `ClusterBroadcaster` adapter, message-broker specifics (Kafka / Redpanda), consistency-clock details, LWT, `USING TIMESTAMP`, any table name, any env-var prefix specific to the plugin.

### Other §§: reconciliation only

All other sections (§1, §3–§14) stay in `ARCHITECTURE.md` at their current section numbers. Content changes come from the Phase A reconciliation pass (§7 of this spec), not from structural edits.

Plugin-specific subsections currently embedded in general sections (e.g., `§3.2 In-Memory SSI Conflict Detection`, `§3.3 PostgreSQL SERIALIZABLE + Error Code 40001`) are **excised and moved** to their respective `docs/plugins/*.md` files. Their place in `ARCHITECTURE.md` is taken by one-sentence summaries linking out.

Specific Cassandra leakage points to sanitize across the full document (beyond §2.3):

| Current location | Current content | Action |
|---|---|---|
| Repositories table (§1) | `cyoda-go-cassandra` row | Remove row |
| §1 SPI lifecycle note | "(e.g. cassandra's shard-rebalance wait)" parenthetical | Remove parenthetical; generic point stays |
| §3.6 Plugin-Specific Transaction Managers | "cassandra plugin — a coordinator-managed 2PC over a message broker, with HLC fencing, per-shard epoch ownership..." | Replace with neutral bullet: "Commercial plugins implement their own `TransactionManager` against their underlying store's primitives." |
| §9 Cassandra config subsection | Full `CYODA_CASSANDRA_*` + `CYODA_REDPANDA_*` env-var listing | Remove entirely. §9 is storage-agnostic after the split; per-plugin config lives with each plugin. |
| §11 Observability | Cassandra-specific span/metric examples and broker-propagation known-gap | Generalised; plugin-namespaced instrumentation point kept without naming |
| §12 Planned Features | "cyoda-go-cassandra for cassandra plugin" | Remove. |

---

## 4. `docs/plugins/*.md` — per-plugin file template

Every per-plugin file follows the same seven-section shape so readers can compare plugins quickly:

```
# <plugin name> storage plugin

## Capabilities                          — one paragraph, headline first
## Concurrency model                     — how SSI is achieved; who owns isolation
## Transaction manager                   — plugin-specific TM details
## Data model and schema                 — persistence layout + migrations
## Configuration (env vars)              — exhaustive per-plugin env-var reference
## Operational notes and limits          — what to watch out for in prod
## When to use / when not to use         — one sentence each, targeted
```

### 4.1 `docs/plugins/IN_MEMORY.md`

**Capabilities framing** (this is the money-shot positioning; worth writing carefully):

> Ephemeral, in-process state — no disk I/O, no network round-trips, no query planner on the hot path. The memory plugin's latency profile sits an order of magnitude ahead of any persistent backend: a full SSI transaction (begin → read-modify-write → commit) completes in the low microseconds rather than the milliseconds a Postgres round-trip takes. That performance envelope makes the memory plugin particularly effective as the **state-backing for high-throughput digital-twin workloads** — an agentic software factory where an agent swarm drives thousands of scenario executions per second against a behavioural twin of a production entity, or a simulation that replays weeks of production state-machine behaviour in seconds. Same workflow semantics, same FSM engine, same SSI guarantees as the persistent backends — without the durability trade-off.

Other sections extracted from current `§2.1` + `§3.2` + `§9` memory content, updated for any drift found during reconciliation.

### 4.2 `docs/plugins/SQLITE.md` (new)

Pulled from `docs/superpowers/specs/2026-04-15-sqlite-storage-plugin-design.md` (the plugin's own design spec) and verified against `plugins/sqlite/` code:

- **Capabilities:** persistent, zero-ops single-node storage; embedded in-process via a pure-Go (WASM) SQLite driver; ideal for desktop binary, edge deployments, containerised single-node production; search predicate pushdown to SQL.
- **Concurrency:** application-layer SSI ported from memory plugin (SQLite's database-level write lock doesn't provide the semantics cyoda needs). Exclusive `flock` at startup — second process against the same file fails fast.
- **Transaction manager:** same SSI engine as memory; SQLite is the durability layer, not the concurrency controller.
- **Data model:** mirrors PostgreSQL logical schema with SQLite optimisations (JSONB via BLOB, `STRICT` + `WITHOUT ROWID` where beneficial, INTEGER timestamps). Migrations via `golang-migrate` embedded SQL files.
- **Config:** `CYODA_SQLITE_PATH` (default `$XDG_DATA_HOME/cyoda/cyoda.db`, Windows `%LocalAppData%\cyoda\cyoda.db`), `CYODA_SQLITE_AUTO_MIGRATE`, `CYODA_SQLITE_BUSY_TIMEOUT`, `CYODA_SQLITE_CACHE_SIZE`, `CYODA_SQLITE_SEARCH_SCAN_LIMIT`.
- **Operational notes:** no CGO (WASM driver `ncruces/go-sqlite3`, ≈2-3× slower than native C; traded for clean cross-compile + sqlite-vec roadmap); NFS unsupported; tenant isolation application-layer only (no RLS).
- **Limits:** single-process, single-node, no horizontal scale.
- **When to use:** desktop binary users, containerised single-node production, embedded deployments.
- **When not to use:** multi-node, multi-process, NFS-mounted storage.

### 4.3 `docs/plugins/POSTGRES.md`

Pulled from current `§2.2` + `§3.3` + `§3.5` (pgx.Tx single-owner) + `§9` postgres content, updated for any drift found during reconciliation.

**Capabilities framing** (with managed-platform story):

> Durable multi-node storage using PostgreSQL as the single source of truth for transaction isolation. Each transaction holds a `pgx.Tx` handle in one cyoda node's process memory, executing under PostgreSQL's `SERIALIZABLE` isolation — cyoda's multi-node architecture pins each transaction to its owning node via `txID → pgx.Tx` affinity, giving active-active HA without distributed-transaction overhead. **Works against any managed PostgreSQL 14+ platform:** AWS RDS, Google Cloud SQL, Azure Database for PostgreSQL, Supabase, Neon, Aiven, Crunchy Bridge, Render, Fly.io Postgres, and self-hosted.

**Managed-platform operational note:** platforms that front PostgreSQL with **PgBouncer in transaction pooling mode** (Supabase port 6543, Neon pooled endpoint) strip prepared-statement caching mid-session. `pgx`'s default extended-query protocol uses prepared statements. Options: (a) use the platform's direct-connection endpoint (Supabase 5432, Neon direct), or (b) set `default_query_exec_mode=exec` on the pgx pool to force simple-query mode. cyoda uses transaction-scoped `SET LOCAL` only for RLS — no session-level state fights the pool.

### 4.4 `docs/plugins/README.md`

One-page index. Bullet list linking to each of the three open-source plugin docs with their one-line capability summaries. Pointer to `plugins/memory/doc.go` + `plugins/postgres/doc.go` + `plugins/sqlite/doc.go` (if present) as reference implementations for plugin authors, and to the `cyoda-go-spi` module for the contract. Note that the `cassandra` plugin is a commercial Cyoda offering documented by Cyoda elsewhere.

---

## 5. `PRD.md` — target shape

Stays a single file. Changes are narrative, not structural.

### 5.1 Storage-plugin callout in §1

Replaces the current blockquote (lines 23–26):

> **Storage plugin architecture.** Cyoda-Go's storage layer is a plugin system defined by the stable `cyoda-go-spi` module (stdlib-only Go interfaces and value types). A running binary has exactly one active plugin, selected at startup via `CYODA_STORAGE_BACKEND`. The stock `cyoda-go` binary ships with three open-source plugins:
>
> - **`memory`** (default) — ephemeral, microsecond-latency SSI for tests and high-throughput digital-twin workloads.
> - **`sqlite`** — persistent, zero-ops single-node storage for desktop, edge, and containerised single-node production.
> - **`postgres`** — durable multi-node storage with SSI via PostgreSQL `SERIALIZABLE`; works against any managed PostgreSQL platform.
>
> A commercial `cassandra` plugin is also available from Cyoda for deployments that need horizontal write scalability beyond what a single-primary PostgreSQL can provide — see [cyoda.com](https://www.cyoda.com) and use its contact page.
>
> Third-party plugins (Redis, ScyllaDB, FoundationDB, etc.) can be authored against `cyoda-go-spi` and compiled into a custom binary via a blank import. See `docs/ARCHITECTURE.md` for the plugin contract and `docs/plugins/` for per-plugin specifics.

### 5.2 Other sections

- **§1 Scale Profile:** rewritten so it acknowledges all four backends and their scale envelopes without mechanism leak.
- **§1 Cost Model:** generalised so it doesn't imply Postgres is the only production path.
- **§4 Transaction Model:** one sentence added: "SQLite plugin uses application-layer SSI; the commercial Cassandra plugin implements SSI against its own primitives."
- **All other §§:** reconciled section-by-section against the code (see §7 below). No new structural content — just drift correction.

---

## 6. Git-history rewrite — considered and rejected

Rewriting commit history to scrub the pre-sanitization §2.3 content was considered. Decision: not worth the disruption.

Rationale:
- The content has already been on the public `Cyoda-platform/cyoda-go` repository since commit `31435d2` (my recent merge) and earlier. Forks, archive.org / Software Heritage / Common Crawl snapshots, and potential LLM training corpora retain the unredacted version. A force-push cleans the canonical remote but does not restore exclusivity.
- The leaked content names **publicly-known distributed-systems primitives** (HLC, Cassandra LWT, 2PC phases, transaction reapers, SWIM gossip) applied to Cassandra. The proprietary synthesis is "how they compose into a coordinator protocol", and even that is recoverable from the Cassandra design doc's publicly-stated Goals.
- A history rewrite invalidates every contributor's local clone, breaks open PRs, and requires a GitHub Support ticket to purge dangling-commit access — significant coordination cost for a best-effort partial cleanup.

**Fix-forward policy:** the sanitised docs become the canonical reference. No rewrite.

---

## 7. Reconciliation methodology — Phase A

The structural split (§3–§5 of this spec) covers **known** drift. The reconciliation pass covers **unknown** drift — the sections of `ARCHITECTURE.md` and `PRD.md` that haven't been explicitly flagged but may have silently diverged from the code over the months since the 2.0 stamp.

### 7.1 Parallel Explore-agent dispatch

Eight agents, one per scope cluster. Each agent reads its section(s), cross-checks against the code, returns a structured discrepancies list (Critical / Significant / Minor) using the same format as the recent provisioning-spec reconciliation.

| Agent | Scope | Primary code references |
|---|---|---|
| 1 | `ARCHITECTURE §1` (System Overview) + `§3` (Transaction Model, storage-agnostic parts) | `cyoda-go-spi` module, `internal/contract/`, `app/app.go`, `internal/cluster/lifecycle/` |
| 2 | `ARCHITECTURE §4` (Multi-Node Routing) | `internal/cluster/registry/gossip.go`, `internal/cluster/dispatch/`, `internal/cluster/proxy/`, `internal/cluster/token/`, `internal/domain/search/` |
| 3 | `ARCHITECTURE §5` (Workflow) + `§6` (gRPC & Externalized Processing) | `internal/domain/workflow/`, `api/grpc/events/`, `internal/grpc/`, `internal/domain/messaging/` |
| 4 | `ARCHITECTURE §7` (Auth) + `§8` (Error Model) | `internal/auth/`, `internal/admin/` (metrics bearer), `app/app.go validateBootstrapConfig` + `validateMetricsAuth`, `internal/common/error_codes.go`, `internal/api/middleware/` |
| 5 | `ARCHITECTURE §9` (Configuration Reference) — **highest drift risk** | `app/config.go DefaultConfig`, `cmd/cyoda/main.go printHelp`, `README.md`, each plugin's `ConfigVars()` |
| 6 | `ARCHITECTURE §10` (Deployment) + `§11` (Observability) + `§14` (Limits) | `deploy/helm/cyoda/`, `deploy/docker/`, `internal/observability/`, `internal/admin/` |
| 7 | `PRD §1–§4` (Vision / EDBMS Core / Workflow / Transaction Model) | `internal/domain/entity/`, `internal/domain/model/`, `internal/domain/workflow/`, `app/app.go`, migration plugin |
| 8 | `PRD §5–§12` (Multi-Tenancy / Search / Externalized Processing / rest, **excluding** version history and frozen sections) | `internal/domain/account/`, `internal/match/`, `internal/domain/search/`, `api/grpc/events/` |

Frozen sections (§13 Design Decisions Log on ARCHITECTURE, and version-history blocks on PRD) are **excluded from the reconciliation pass** and untouched.

### 7.2 Synthesis

Discrepancies from all eight agents are collated into a single fix-list. Each item is categorised:

- **Critical:** doc claims something contradicted by code (mislead readers into wrong behaviour expectations).
- **Significant:** doc omits a material change or describes an implementation that has since been refactored.
- **Minor:** stylistic drift, stale cross-reference, outdated example.

All three categories get fixed in this pass — "perfectly in sync" is strict.

---

## 8. Incidental cleanup

Bundled into the restructuring commit:

| File | Stale content | Fix |
|---|---|---|
| `ARCHITECTURE.md` package-layout diagram | `cmd/cyoda-go/main.go` | → `cmd/cyoda/main.go` |
| `ARCHITECTURE.md` status line | "Target state after the storage-plugin architecture refactor (Plans 1–5)" | → "Current as of 2026-04-18 (Helm provisioning shipped in PR #60)" |
| `ARCHITECTURE.md` + `PRD.md` version | `2.0`, dated `2026-04-14` | → `2.1`, dated `2026-04-18` |

---

## 9. Verification — Phase C

Before claiming done:

1. **Cassandra mechanism grep:** `grep -rEi 'HLC|hybrid logical clock|Redpanda|shard.epoch|ClusterBroadcaster|USING TIMESTAMP|transaction_log_idx|2PC|two.phase commit|LWT|last.writer.wins|cyoda-go-cassandra|CASSANDRA_BACKEND_DESIGN' docs/ARCHITECTURE.md docs/PRD.md docs/plugins/` — expected hits: zero. Any hit is a leak.
2. **Rename-residue grep:** `grep -rE 'cmd/cyoda-go|deploy/helm/cyoda-go|cyoda-go-0\.' docs/ARCHITECTURE.md docs/PRD.md docs/plugins/` — expected hits: zero.
3. **Per-plugin template conformance:** each of `docs/plugins/IN_MEMORY.md`, `SQLITE.md`, `POSTGRES.md` has all seven template sections in the prescribed order.
4. **Cross-link resolution:** every `[text](path)` link from `ARCHITECTURE.md` to a `plugins/*.md` file resolves to an existing file. Script-checkable.
5. **Env-var cross-reference:** every `CYODA_*` env var documented in `ARCHITECTURE.md §9` or a plugin file appears in `app/config.go` + `cmd/cyoda/main.go printHelp` + `README.md`, and vice-versa. Orphans flagged.
6. **Frozen sections intact:** `git diff` on `ARCHITECTURE.md §13` range and `PRD.md` version-history block shows only the version-line bump, no content edits.

---

## 10. Out of scope

- Rewriting git history.
- Revising `cyoda-go-spi` module documentation.
- Revising per-plugin `doc.go` files.
- Expanding `PRD.md` scope into new product domains.
- Adding content for planned-but-unimplemented features (they stay in `§12 Planned Features`).
- Touching `docs/adr/` or any other `docs/superpowers/` spec.
- Any code change to `cyoda-go` proper. This is a documentation-only deliverable.

---

## 11. Open questions

None remaining at spec-writing time. The reconciliation pass may surface items that warrant human judgement (e.g., "spec claims X but code does Y — which is the canonical behaviour?"); those are handled inline during Phase B synthesis with a decision log kept in the implementation plan.
