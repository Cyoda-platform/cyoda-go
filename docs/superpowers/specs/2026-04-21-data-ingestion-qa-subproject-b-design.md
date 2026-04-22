# Sub-project B — Plugin Persistence + Fold Correctness

**Date:** 2026-04-21 (rev 2 on 2026-04-22)
**Revision:** 2 — post fresh-context review 2026-04-22 (file paths corrected; postgres retry dropped after verification of commutative-append; B-I4 semantics clarified as "since last savepoint" with query reuse; B-I8 added for local cache invalidation; property-test harness relocated to `e2e/parity/`; sqlite scope expanded with upgrade-path test; B-I3/B-I4 de-dup rule stated; "vacuously satisfies" replaces "exempt")
**Parent initiative:** Data-Ingestion QA (`docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md`)
**Prior work:**
- `docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md` — the ExtendSchema pipeline.
- `docs/superpowers/specs/2026-04-15-postgres-si-first-committer-wins-design.md` — postgres FCW semantics.
- `docs/superpowers/specs/2026-04-15-sqlite-storage-plugin-design.md` — sqlite plugin baseline.
- `docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a2-design.md` — in-memory schema-transformation invariants I1–I7 (the contract B extends across storage).

**Sister spec:**
- `cyoda-go-cassandra/docs/superpowers/specs/2026-04-22-data-ingestion-qa-subproject-b-cassandra-design.md` — Cassandra plugin realization. Conforms to the invariants and SPI contract established here.

## 1. Purpose

Extend the schema-transformation invariants established in A.2 across the storage boundary.

A.2 asserted

> `Apply(old, Diff(old, Extend(old, Walk(data), level))) ≡ Extend(old, Walk(data), level)` byte-for-byte

in-process via `schema.Marshal`. B asserts that for the same extension-call sequence, every persistent backend (memory, postgres, sqlite, cassandra) produces byte-identical `schema.Marshal(Load(...))` output, with:

- Full audit retained via append-only extension logs (never compacted, never rewritten).
- Fold cost bounded by a configurable savepoint interval.
- A plugin-layer retry contract: each backend handles its *native* conflict surface internally with bounded retry. Postgres and memory have no conflict surface on schema writes (see §5 for verification); the retry budget fires only on sqlite `SQLITE_BUSY` and Cassandra LWT `applied: false`.
- Single-node cross-storage atomicity: a rejected `ExtendSchema` leaves no persisted trace anywhere in the backend's storage surface.
- Local cache invalidation on extension commit: after a successful `ExtendSchema`, subsequent reads on the same node return post-extension state.

## 2. Scope

### 2.1 In scope

**Backends covered:**

- **memory** — in-process, mutex-serialized, single folded `[]byte` in the model descriptor. Exempt from savepoint invariants (no log).
- **postgres** — server-grade relational. Already has most of the machinery; B completes the contract (save-on-lock, transparent retry, config-driven interval).
- **sqlite** — single-node file-based. **Converted in B** from apply-in-place to log-based, mirroring postgres's algorithm adapted for `BEGIN IMMEDIATE` + busy_timeout.
- **cassandra** — out-of-repo sister plugin. Implementation is concurrent with B's public work; lands in the Cassandra repo on its own PR. Parity tests added here are picked up by Cassandra on its next `go.mod` refresh.

**SPI additions:**

- `ErrRetryExhausted` error type for retry-budget exhaustion (distinct from `ErrConflict`).
- Contract hardening on `ExtendSchema` — bounded transparent retry is the plugin's responsibility; success means durably committed and visible, error means no persisted effect.
- Config knobs read at plugin construction time:
  - `CYODA_SCHEMA_SAVEPOINT_INTERVAL` (default `64`)
  - `CYODA_SCHEMA_EXTEND_MAX_RETRIES` (default `8`)

### 2.2 Out of scope

- **Polymorphic-slot kind-conflict implementation** — deferred to A.3 (issue #85).
- **Concurrency under load, multi-node schema propagation, gossip loss, cross-node cache staleness** — deferred to C.
- **Input boundary hardening** (malformed JSON, Unicode edge cases, size limits, fuzz at HTTP layer) — deferred to D.
- **Unbound-data ingestion mode** — filed as issue #86. Independent feature request surfaced during B's brainstorm; not addressed here.
- **Unified model+entity log** — filed as issue #87. Architectural evolution, not B's scope.
- **New `SchemaOp` kinds** — B uses the existing A.2 op catalog; no new delta kinds are introduced.
- **Fold-performance tripwire benchmark** — advisory, not a hard invariant. May appear as a future bench if B's property-test budget proves insufficient signal on pathological log growth.

### 2.3 Principles

- **Append-only, never compacted.** Extension logs grow. Savepoints are additive checkpoints, never replacements. Full audit retained.
- **Byte equality is the cross-backend contract.** Not semantic equality — the bytes produced by `schema.Marshal(Load(...))` must be byte-identical across backends for identical extension-call history. Backends that produce divergent bytes are a failing test, not a tolerated variation.
- **Plugin-internal savepoint policy.** Service layer does not count ops or decide savepoint placement. Each plugin reads the interval config at construction. The "ops since last savepoint" count is derived from `lastSavepointSeq` — a value the plugin already reads inside `foldLocked` on the savepoint path, so no extra query is needed.
- **Retry where a conflict surface exists.** `ExtendSchema` is atomic-or-fail at the SPI boundary. Plugins with a native conflict surface (sqlite, cassandra) implement bounded retry internally. Plugins without one (memory, postgres) never retry because they never conflict — concurrent writers succeed via mutex serialization (memory) or commutative append under REPEATABLE READ (postgres). The retry wrapper is not a uniform abstraction; it exists only where it does useful work (Gate 6).

## 3. Invariants

B's contract. Each invariant is asserted across at least one of the three test tracks in §7. Memory is exempt from savepoint-specific invariants (B-I2/B-I3/B-I4) because it has no extension log.

| ID | Name | Backends | Summary |
|---|---|---|---|
| **B-I1** | Cross-plugin byte-identical fold | all four | `schema.Marshal(Load(modelRef))` byte-identical across memory/postgres/sqlite/cassandra for identical extension-call history. Memory's in-place state and log-backend folds produce the same bytes. |
| **B-I2** | Savepoint transparency | log-backends | Adding a savepoint to a log does not change `Marshal(Load(log))`. Savepoints are a load-time optimization, not a semantic operator. |
| **B-I3** | Save-on-lock atomicity | log-backends | `Lock(model)` commits lock-state + savepoint atomically, or neither. No split state under any failure. |
| **B-I4** | Save-on-size-threshold atomicity | log-backends | When `(newSeq - lastSavepointSeq) >= interval` (interval = `CYODA_SCHEMA_SAVEPOINT_INTERVAL`, default 64), a savepoint is written atomically with the committing op in the same backend-native commit. "Since last savepoint" rather than "global seq modulo" so interval reconfiguration doesn't produce irregular placement. `lastSavepointSeq` is already read by `foldLocked` on the savepoint path — no extra query introduced. |
| **B-I5** | Causal-order preservation | all four | Extensions apply in the order they committed — memory via mutex-serialized in-place update, log-backends via clustering/seq order after the most recent savepoint. |
| **B-I6** | Cross-storage atomicity on rejection | all four | Rejected `ExtendSchema` (ChangeLevel violation, invariant breach, validation failure) leaves no persisted trace — no partial savepoint, no orphaned op, no torn write. Cross-storage extension of A.2's I7 (in-memory atomicity on rejection). |
| **B-I7** | Concurrent-extension convergence | all four | Under N concurrent `ExtendSchema` calls on same `(model,version)`, all succeed (exhaustion paths below) and the final fold `schema.Marshal(Load(...))` is independent of which call won any individual commit race. |
| **B-I8** | Local cache invalidation on commit | all four | After a successful `ExtendSchema` commit, subsequent `Get(ref)` calls on the same node return post-extension state. The `modelcache` layer is invalidated at extension-commit time; no stale-cache window is observable to local callers. |

"log-backends" ≡ postgres, sqlite, cassandra. Memory vacuously satisfies B-I2/B-I3/B-I4 (no extension log; "adding a savepoint doesn't change fold" is vacuous, "save on lock/interval" is vacuous).

**B-I1 note.** Byte identity is asserted on `schema.Marshal(Load(...))`, not on raw storage bytes. Backends are free to serialize at rest however (JSONB, opaque blob, partitioned rows). The invariant is on the observable output of the fold.

**B-I3 + B-I4 de-dup rule.** If a Lock transition triggers at exactly `(newSeq - lastSavepointSeq) >= interval`, only one savepoint row is written — the lock-triggered savepoint supersedes the interval-triggered one. Plugins check lock transition first; when writing a lock-savepoint, the interval check is skipped for the same commit.

**B-I7 note — which backends retry.** Retry fires only where a backend produces a native conflict surface:
- **Sqlite:** `SQLITE_BUSY` on `BEGIN IMMEDIATE` under contention → retry up to `CYODA_SCHEMA_EXTEND_MAX_RETRIES` (default 8), immediate retry (no backoff; sqlite's own `busy_timeout` already provides a wait). Exhaustion → `ErrRetryExhausted`.
- **Cassandra:** LWT `applied: false` on delta_seq collision → HLC bump + retry, same budget. Exhaustion → `ErrRetryExhausted`.
- **Postgres:** no retry. `ExtendSchema` is a bare `INSERT ... RETURNING seq` on an append-only table; under REPEATABLE READ, two concurrent writers both commit. Correctness follows from A.2's I2 (commutativity) — the fold of `[d1, d2]` is equivalent to fold of `[d2, d1]` at the `schema.Marshal` level.
- **Memory:** no retry. `modelMu` serializes; concurrent callers queue but never conflict.

**B-I7 justification.** For memory and postgres (commutative-append), convergence is A.2's I2 (commutativity) + I5 (permutation invariance) applied directly — N deltas against a shared base yield the same fold in any order. For sqlite and Cassandra (rebase-on-retry), each retry re-reads the freshest state and recomputes its delta against it; convergence follows from A.2's I1 (round-trip) + I3 (monotonicity) + I4 (idempotence): the final fold is a fixed point of the extend lattice regardless of interleaving. The observable B-I7 property (final fold identical across race outcomes) is a consequence in both cases.

## 4. SPI changes

Landed in a new `cyoda-go-spi` tag (provisionally `v0.6.0`; exact version assigned at ship per `feedback_go_module_tags_immutable` — no tag force-moves).

### 4.1 Error types

```go
// ErrRetryExhausted is returned by ExtendSchema when the plugin's
// transparent retry budget has been consumed without success. The caller
// may choose to retry after backoff, or to surface the condition to the
// end user as "try again later".
//
// Distinct from ErrConflict: ErrConflict means a single attempt hit a
// conflict; ErrRetryExhausted means the plugin ran its configured number
// of retries and none succeeded.
var ErrRetryExhausted = errors.New("schema extension retry budget exhausted")
```

### 4.2 `ExtendSchema` contract

Method signature unchanged:

```go
ExtendSchema(ctx context.Context, ref ModelRef, delta SchemaDelta) error
```

**Contract (new wording in godoc):**

- Implementations handle backend-native conflict detection internally with bounded transparent retry.
- Success (`nil` return) means the extension is durably committed and visible to subsequent reads on this and other nodes.
- Non-nil error means **no persisted effect** — no log entry, no savepoint, no partial state.
- `ErrRetryExhausted` surfaces only when all configured retries have been consumed. `ErrConflict` is reserved for the single-attempt case.

### 4.3 Plugin-construction config

**Sqlite, cassandra** — each plugin's `Config` struct gains two fields:

```go
type Config struct {
    // ... existing fields ...
    SchemaSavepointInterval int // default 64; read from CYODA_SCHEMA_SAVEPOINT_INTERVAL; min 1
    SchemaExtendMaxRetries  int // default 8;  read from CYODA_SCHEMA_EXTEND_MAX_RETRIES;  min 1
}
```

**Postgres** — gains only `SchemaSavepointInterval`. Postgres has no conflict surface on `ExtendSchema` (commutative append; see §5.2); `SchemaExtendMaxRetries` is not consumed.

**Memory** — uses functional options (`Option`), not a `Config` struct. Consumes neither knob (no savepoints, no conflict surface). No functional-option additions are required for B.

`DefaultConfig()` populates declared fields with the defaults. `FromEnv()` (or the equivalent env-reading helper per plugin) reads the env vars with defaults-preserved semantics (invalid/unset/≤0 → default, logged warning). No upper-bound clamp; operators picking pathological values (e.g., interval=1 for a savepoint every op) own the consequences.

## 5. Per-plugin design

### 5.1 Memory plugin

**Current state** (per survey): no extension log. Schema stored as a single folded `[]byte` in `spi.ModelDescriptor.Schema`. `ExtendSchema` acquires `modelMu`, calls the injected `applyFunc`, replaces the descriptor's schema bytes in place. Mutex serializes all writes; rejection (applyFunc error) leaves the descriptor untouched.

**B changes:**

1. **Add explicit B-I6 test** — ChangeLevel-violating extension fails, descriptor schema bytes unchanged (currently asserted implicitly via "applyFunc error → no mutation"; B promotes to an explicit named test).
2. **Add B-I7 local convergence test** — N goroutines call `ExtendSchema` on the same model; assert the final folded schema is byte-identical to a single-goroutine replay of the same deltas in any serial order (I2 commutativity). Avoid circular assertions — the test must check state equivalence across orderings, not just "no torn writes" (which mutex makes impossible by construction).
3. **No retry wrapper** — memory has no conflict surface. Per Gate 6, no dead code "for uniformity."

**No migration, no data-model change, no config additions.**

### 5.2 Postgres plugin

**Current state** (per survey, verified against `plugins/postgres/model_store.go:246-294` and `plugins/postgres/model_extensions.go:26-79`): `model_schema_extensions` table exists with `(tenant_id, model_name, model_version, seq, kind IN ('delta','savepoint'), payload JSONB, tx_id, created_at)`. `ExtendSchema` performs a bare `INSERT ... RETURNING seq` against that table; no version check, no FCW, no conflict detection on schema writes. Savepoint emission at `newSeq % 64 == 0` is hardcoded at `plugins/postgres/model_store.go:276` (not `model_extensions.go`). Fold-on-read (`foldLocked`) reverse-scans for the latest savepoint, forward-applies deltas after it via the injected `applyFunc`.

The entity-store FCW machinery (`commit_validator.go`, `transaction_manager.go`) tracks entity-level readSet; it does not apply to schema writes. Under concurrent `ExtendSchema` calls within REPEATABLE READ transactions, both writers append their delta row and both commit — the fold result is well-defined by A.2's I2 (commutativity).

**B changes:**

1. **Refactor hardcoded `64`** at `model_store.go:276` (the `newSeq % 64 == 0` literal; there is no named constant today) to a config-driven check. Per B-I4's "since-last-savepoint" semantics, the trigger becomes `(newSeq - lastSavepointSeq) >= cfg.SchemaSavepointInterval`. `lastSavepointSeq` is already queried inside `foldLocked` (line 29-33 of `model_extensions.go`) on the savepoint path — the check lifts that value with no extra query.
2. **Add save-on-lock** — on the `Lock` transition (unlocked → locked), write a savepoint row atomically with the lock-state change in the same postgres transaction. Implements B-I3. The B-I3/B-I4 de-dup rule applies.
3. **No retry wrapper.** Postgres's `ExtendSchema` has no conflict surface on schema writes (verified above); `CYODA_SCHEMA_EXTEND_MAX_RETRIES` is not consumed by postgres. The spec's retry contract applies *where a conflict surface exists* — postgres simply never conflicts.
4. **Test adjustments.** `TestExtendSchema_SavepointEvery64` is renamed and parameterized by `cfg.SchemaSavepointInterval`; existing assertions at default 64 pass unchanged. Existing `TestExtendSchema_RolledBack_NotVisible` continues to cover atomicity of append within a transaction.

**Added tests (per-plugin):**
- `TestPostgres_ExtendSchema_SaveOnLock` (B-I3).
- `TestPostgres_ExtendSchema_CommutativeAppend_ConvergesUnderConcurrency` (B-I7 local — asserts both writers commit and final fold is byte-identical to any serial ordering; no retry involved).
- `TestPostgres_ExtendSchema_FoldAcrossSavepointBoundary_ByteIdentical` (B-I1/B-I2 local).
- `TestPostgres_ExtendSchema_RejectionLeavesNoSavepointOrOp` (B-I6 tightening).
- `TestPostgres_ExtendSchema_SavepointTriggerRespectsIntervalChange` (B-I4 — start with interval 64, commit past the first savepoint, change interval to 128, confirm next savepoint fires at `(newSeq - lastSavepointSeq) >= 128` rather than at the next multiple of 128).

**No migration** — existing table schema supports both kinds already.

### 5.3 SQLite plugin

**Current state** (per survey): `model_schema_extensions` table exists in `migrations/000001_initial_schema.up.sql:141-154` but is **unused**. `ExtendSchema` does apply-in-place read-modify-write (SELECT → unmarshal → apply → UPDATE) under connection-level `_txlock=immediate`. Migration comment: "SQLite is single-node by design; this table exists for SPI parity and for the conformance tests. Fold is trivial since there is only one writer." No `applyFunc` is required at Get time because the schema is always in a post-fold state.

**B converts sqlite to log-based.** This is the single largest structural change in B's code path. The unused table becomes the active log; the fold path is introduced; `applyFunc` becomes required (as in postgres).

**B changes:**

1. **Log-based `ExtendSchema`.** Replace apply-in-place in `plugins/sqlite/model_store.go`:238–285 with `INSERT INTO model_schema_extensions (kind='delta', ...)` under `BEGIN IMMEDIATE`. On savepoint-trigger (same formula as postgres: `(newSeq - lastSavepointSeq) >= cfg.SchemaSavepointInterval`), append a `(kind='savepoint', payload=<folded>)` row in the same transaction.
2. **Fold-on-read.** New `foldLocked` implementation mirroring postgres's algorithm adapted for SQLite dialect (same reverse-scan-for-savepoint + forward-apply pattern; no backend-specific quirks).
3. **Save-on-lock.** On `Lock` transition, write savepoint row atomically with the lock state change in the same SQLite transaction. The B-I3/B-I4 de-dup rule applies: if the lock transition coincides with the interval threshold, only the lock savepoint is written.
4. **Transparent retry on `SQLITE_BUSY`.** sqlite's single-writer lock produces `SQLITE_BUSY` under contention; the plugin's existing error mapping at `errors.go:22-50` classifies this as `spi.ErrConflict`. B adds retry *inside* `ExtendSchema`: catch the classified conflict, reopen the transaction, re-read current state, re-append, up to `cfg.SchemaExtendMaxRetries`. No explicit Go-level backoff — sqlite's own `busy_timeout` provides serialization delay.
5. **Base-schema handling on upgrade.** Pre-B sqlite deployments have populated `models.doc.schema` (the folded state) and an empty `model_schema_extensions` table. Post-B `foldLocked` must treat the populated `models.doc.schema` as the base and the empty extension table as "no deltas yet" — same semantics as a fresh post-B model with no extensions. Verified: postgres's `foldLocked` already does this (queries `baseSchema` when no savepoint exists, returns base verbatim when no deltas — `plugins/postgres/model_extensions.go:34-78`). SQLite inherits the same pattern, so existing sqlite deployments open cleanly post-upgrade without a data migration.
6. **Upgrade-path test.** New test that seeds a sqlite database in the pre-B state (populated `models.doc.schema`, empty extension table), opens it with the post-B plugin, and asserts `Get` returns the base schema verbatim (zero deltas → identity fold).
7. **Test rewrites** (existing tests adapted for log semantics; same assertions, different mechanism):
   - `TestSQLite_ExtendSchema_AppliesInPlace` → `TestSQLite_ExtendSchema_AppendsToLog`.
   - `TestSQLite_ExtendSchema_MultiDeltaFold` → asserts fold via log replay.
   - `TestSQLite_ExtendSchema_CrossTenantIsolation` → mechanics change, assertion unchanged.
8. **Test additions** (per-plugin, mirroring postgres):
   - `TestSQLite_ExtendSchema_SavepointAtConfigInterval` (B-I4).
   - `TestSQLite_ExtendSchema_SaveOnLock` (B-I3).
   - `TestSQLite_ExtendSchema_TransparentRetry_ConvergesUnderBusy` (B-I7 local).
   - `TestSQLite_ExtendSchema_RejectionLeavesNoPersistedTrace` (B-I6).
   - `TestSQLite_ExtendSchema_UpgradeFromPreBDeployment` (the upgrade-path case).

**No migration needed** — `model_schema_extensions` already in `000001_initial_schema.up.sql`. Existing deployments open cleanly via the base-schema fallback in (5).

**Scope acknowledgment.** This is not a simple "mirror postgres" change. It introduces ~200-300 lines of new code, requires the plugin's existing `applyFunc` wiring to become mandatory (models with no extensions keep the current fast path), and rewrites most of `model_extensions_test.go`. Sqlite is the largest single piece of implementation in B.

### 5.4 Cassandra plugin (summary; full design in sister spec)

Cassandra realization lives in `cyoda-go-cassandra/docs/superpowers/specs/2026-04-22-data-ingestion-qa-subproject-b-cassandra-design.md`. Summary of what it delivers:

- Full implementation of the existing 2026-04-20 Cassandra extension-log design, adjusted to match B's SPI contract (transparent retry, configurable interval, save-on-lock).
- HLC-sequenced `delta_seq` for conflict-free ordering across writers.
- LWT (`IF NOT EXISTS` / version CAS) for conflict detection on pathological collisions, with transparent retry on `applied: false` (HLC bump + re-apply).
- `LoggedBatch` for save-on-lock atomicity and save-on-size-threshold atomicity.
- go.mod bump to the new `cyoda-go-spi` tag.
- Migration: `model_schema_extensions` table added to `000001_initial_schema.up.cql`.
- Parity tests added here in cyoda-go are picked up automatically by Cassandra on its next dep refresh.

## 6. SPI version coordination

The Cassandra plugin's `go.mod` pins older cyoda-go plugin-module versions (`plugins/memory@v0.1.0`, `plugins/postgres@v0.1.0`) that predate the SPI addition of `CountByState`. This surfaces today as a build failure in the Cassandra worktree — `*EntityStore does not implement spi.EntityStore (missing method CountByState)` — because Cassandra consumes the plugin modules as dependencies. The drift is in Cassandra's go.mod pins, not in the Cassandra code itself; Cassandra has a `CountByState` implementation (see its `cql.go`). The coordinated spi-tag bump resolves the pin mismatch.

**Coordinated sequence:**

1. Cut `cyoda-go-spi@v0.6.0` (or next version) including:
   - `ErrRetryExhausted`.
   - `ExtendSchema` contract godoc updates.
2. Itemize pre-existing cross-plugin SPI drift (not B's additions) by diffing each plugin's go.mod against the head-of-tree spi interface. Known at spec-write time: Cassandra pins plugin modules lacking `CountByState`. Any additional drift surfaced by the audit is landed as part of B's coordinated bump; drift discovered *after* the audit is a separate PR.
3. cyoda-go plugin `go.mod` bumps land alongside B's implementation commits.
4. Cassandra's sister PR bumps its go.mod to the new spi tag and the B-tagged plugin module versions, landing its realization and resolving the pre-existing drift in one go.

During iteration, a `replace github.com/cyoda-platform/cyoda-go-spi => ../cyoda-go-spi` directive (or equivalent local-path override) may be used; the release commits pin to the tagged version.

## 7. Test architecture

Three observational styles, coverage map below.

### 7.1 Track 1 — Parity (black-box, HTTP-layer)

Added to `e2e/parity/registry.go`. Driven via the existing per-backend fixtures (`e2e/parity/memory`, `e2e/parity/postgres`, `e2e/parity/sqlite`). Cassandra picks these up automatically on its next `go.mod` refresh.

| Parity test | Asserts | Shape |
|---|---|---|
| `SchemaExtensionCrossBackendByteIdentity` | B-I1 | Seed a deterministic 20-extension sequence via `gentree` + drive through HTTP. Fetch folded schema. Compare `schema.Marshal` bytes against the `memory` backend's output in the same parity run as the canonical baseline. |
| `SchemaExtensionAtomicRejection` | B-I6 | Attempt a ChangeLevel-violating extension. Assert (a) HTTP 4xx with expected error code, (b) `GetModel` returns byte-identical pre-call schema bytes, (c) backend log-tail count unchanged (via backend-specific introspection hook exposed in the test helper). |
| `SchemaExtensionConcurrentConvergence` | B-I7 | 10 goroutines extend the same (model,version) with overlapping and disjoint shapes. Assert all eventually succeed within the retry budget, final `schema.Marshal` is identical regardless of race winner, no persisted torn writes. |
| `SchemaExtensionSavepointOnLockFoldEquivalence` | B-I2, B-I3 | Extend × N < interval, Lock, (attempt-permitted-post-lock extensions × M), fetch fold. Assert byte-identical against a second backend running the same sequence. Cross-backend check subsumes per-backend B-I2. |
| `SchemaExtensionLocalCacheInvalidationOnCommit` | B-I8 | Warm the `modelcache` by calling `GetModel`, submit an `ExtendSchema`, immediately call `GetModel` again on the same node. Assert the returned schema reflects the new extension (not the stale cached result). |

The existing `SchemaExtensionsSequentialFoldAcrossRequests` entry is kept unchanged.

### 7.2 Track 2 — Per-plugin gray-box

Backend-specific log introspection — each plugin uses its native query idiom. Tests live in each plugin's own `model_extensions_test.go` (per-plugin `go.mod` submodule).

- **Memory** (`plugins/memory/model_extensions_test.go`): rejection atomicity (B-I6), convergence under concurrent extend (B-I7 — assert final fold identical to a single-goroutine serial replay; not "no torn writes", which is circular given the mutex), cache-invalidation hook fires on commit (B-I8). B-I2/B-I3/B-I4 vacuously satisfied (no log).
- **Postgres** (`plugins/postgres/model_extensions_test.go`): savepoint row at `(newSeq - lastSavepointSeq) ≥ interval` (B-I4), savepoint row at lock transition (B-I3), forward-scan clustering order after savepoint (B-I5), no savepoint on rejection (B-I6 tightening), both writers commit on concurrent extend (B-I7 commutative-append; no retry path to assert), cache-invalidation on commit (B-I8).
- **SQLite** (`plugins/sqlite/model_extensions_test.go`): same assertions as postgres for B-I3/B-I4/B-I5/B-I6/B-I8, plus `SQLITE_BUSY` → retry → convergence for B-I7.
- **Cassandra** (in sister spec): LWT collision → HLC-bump retry → convergence for B-I7, save-on-lock in `LoggedBatch` for B-I3, savepoint-trigger at `(delta_seq - last_savepoint_delta_seq) ≥ interval` for B-I4, cache-invalidation on commit for B-I8.

### 7.3 Track 3 — Property-based (seed-driven, HTTP/parity-layer)

Property tests live inside the existing `e2e/parity/` package — no new top-level package. This avoids the import-cycle trap of putting property tests under `internal/domain/model/schema/` (which would need to import plugin submodules that have their own `go.mod`, requiring cross-module replace directives or a fragmented per-submodule harness). The parity package already imports plugin fixtures via its per-backend subdirectories (`e2e/parity/memory`, `e2e/parity/postgres`, `e2e/parity/sqlite`) and drives them through the in-process HTTP test server.

**Implementation shape.** New file `e2e/parity/schema_extension_property.go` defines one entry point:

```go
// RunSchemaExtensionByteIdentityProperty drives a seeded sequence of
// extensions through the fixture backend and asserts that the final
// schema.Marshal bytes equal the bytes produced by the deterministic
// in-memory oracle for the same seed. Iterates over a table of seeds;
// each seed runs as a subtest for replay ergonomics.
func RunSchemaExtensionByteIdentityProperty(t *testing.T, fixture BackendFixture)
```

Registered as a single entry in `e2e/parity/registry.go` (`SchemaExtensionByteIdentityProperty`). When the registry runs against a backend fixture, this entry fans out to 50 seeded subtests.

**Oracle.** Expected bytes are computed per-seed from A.2's existing `schema` package — the deterministic in-memory `Extend` / `Marshal` path. Each fixture backend asserts its output bytes equal the oracle's. No cross-backend communication needed; each backend runs independently and verifies against the same pure-function oracle.

**Sample budget.** 50 seeds × (B-I1 + B-I6) × 3 in-repo backends = 300 subtests. (B-I2/B-I5 are asserted indirectly via B-I1 at the seed-output level — if byte identity holds for every seed, the internal order-preserving and savepoint-transparency conditions are necessarily holding too.) Cassandra participates via its own CI.

**Per-sample cost.** Each seed averages ~8 extensions + 1 Get. At parity harness HTTP-layer cost (~20 ms per call), per-seed wall-clock ≈ 180 ms. 300 subtests × 180 ms ≈ 54 s across the three in-repo backends, within budget.

**Fixture reuse.** Parity's per-backend test processes each start one testcontainers instance (postgres, sqlite via in-proc) and reuse it across all parity entries including this one. Container startup is amortized across ~40 parity entries; property tests add marginal cost. The fixture reuse is automatic — no per-sample container restart.

**Runtime budget: 90 s local / 120 s CI hard fail** for the property entry specifically (not the whole parity suite). Meta-test `TestParity_SchemaExtensionProperty_Budget` enforces the 120 s ceiling.

**Long-test gating.** Property tests do not run under `go test -short`. The entry is skipped when `testing.Short()` is true — keeps fast local iteration cheap. Full CI runs use `go test ./...` (no `-short`), so the property budget is enforced every CI run.

Determinism discipline (inherited from A.2):
- `math/rand/v2` PCG seeding, explicit and logged on failure.
- Seed replay via `-seed=<N>` flag.
- No `range` over Go maps in generator paths.

### 7.4 Coverage map

| Invariant | Track 1 (parity fixed) | Track 2 (gray-box) | Track 3 (property seeded) |
|---|---|---|---|
| B-I1 | ✓ | — | ✓ |
| B-I2 | ✓ (subsumed by B-I1 cross-backend) | ✓ postgres/sqlite/cassandra | ✓ (via B-I1 seed coverage) |
| B-I3 | ✓ | ✓ postgres/sqlite/cassandra | — |
| B-I4 | — | ✓ postgres/sqlite/cassandra | — |
| B-I5 | ✓ (via B-I1) | ✓ postgres/sqlite/cassandra | ✓ (via B-I1 seed coverage) |
| B-I6 | ✓ | ✓ all four | ✓ |
| B-I7 | ✓ | ✓ all four | — |
| B-I8 | ✓ (warm cache + Get-after-extend parity test) | ✓ all four (assert modelcache invalidation hook fires) | — |

### 7.5 Race detector

`go test -race ./...` is a one-shot sanity pass before PR creation per `.claude/rules/race-testing.md`. Not a per-step gate.

## 8. Config + documentation

### 8.1 New env vars

| Env var | Default | Type | Honored by |
|---|---|---|---|
| `CYODA_SCHEMA_SAVEPOINT_INTERVAL` | `64` | Integer, ≥ 1 | postgres, sqlite, cassandra (ignored by memory — no log) |
| `CYODA_SCHEMA_EXTEND_MAX_RETRIES` | `8` | Integer, ≥ 1 | sqlite, cassandra (ignored by memory and postgres — no conflict surface on schema writes) |

Invalid values (≤ 0, non-integer) fall back to the default with a logged warning at plugin construction. No upper-bound clamp — operators picking pathological values (e.g., interval=1) own the consequences.

### 8.2 Documentation updates (Gate 4)

Updated together in a single documentation commit:

- `cmd/cyoda/main.go` `printHelp()` — add both env vars under the plugin-configuration reference section.
- `README.md` — add both env vars to the configuration table, with an explicit "Honored by" column noting which plugins consume them (same as §8.1 table). Memory is documented as "does not consume these knobs — uses functional options, and has no savepoints or conflict surface."
- `DefaultConfig()` in each plugin with a `Config` struct — `plugins/postgres/config.go` gets `SchemaSavepointInterval` only, `plugins/sqlite/config.go` gets both, the Cassandra plugin's `config.go` in its repo gets both. Memory is N/A.
- `CONTRIBUTING.md` — note the `CYODA_SCHEMA_*` config knobs in the testing section if relevant.

### 8.3 Overview invariant table update

`docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md` §6 currently lists:

```
| TBD | B | Cross-plugin byte-identical fold |
| TBD | B | Single-node cross-storage atomicity |
```

These TBDs are replaced at ship with the eight concrete B invariants from §3 above, in one edit. Done as part of B's final commit, not in advance.

## 9. Execution order

1. **SPI additions in `cyoda-go-spi`.** `ErrRetryExhausted`, contract godoc updates. Fresh tag.
2. **Config struct additions + env var plumbing** in each cyoda-go plugin. Default-preserving (all existing behavior unchanged).
3. **Memory plugin** — test additions (B-I6 explicit, B-I7 local concurrency stress). No behavior change.
4. **Postgres plugin** — interval-config wiring (trigger rewritten as "since last savepoint", lifting `lastSavepointSeq` from the existing `foldLocked` query), save-on-lock atomicity. No retry wrapper (no conflict surface). Per-plugin tests for B-I3, B-I4-since-last-savepoint, B-I6 tightening, B-I7 commutative-append, B-I8 cache invalidation.
5. **SQLite plugin** — convert from apply-in-place to log-based. New code path mirroring postgres adapted for `BEGIN IMMEDIATE`. Add `SQLITE_BUSY` retry wrapper. Include the upgrade-path test for pre-B deployments. Full B-I1…B-I8 sqlite-local test coverage.
6. **Parity registry additions** — four new `SchemaExtension*` entries in `e2e/parity/registry.go`.
7. **Property-test harness** — `internal/domain/model/schema/persistence/` package with `BackendFixture` abstraction and property test files (one per primary invariant that admits property-based coverage: B-I1, B-I2, B-I5, B-I6).
8. **Runtime-budget meta-test** — `TestBBPropertySuiteBudget` enforces 120 s CI ceiling.
9. **Docs pass** — `printHelp()`, `README.md`, `DefaultConfig()`, `CONTRIBUTING.md`.
10. **SPI bump in cyoda-go** — `go.mod` updated to the new spi tag. Sanity: existing tests stay green.
11. **Overview §6 invariant-table update** — replace TBDs with concrete B-I1…B-I8.
12. **Cassandra execution** — in its sister repo, on its own PR cadence. Consumes cyoda-go's new spi tag via go.mod bump; picks up new parity tests automatically.
13. **Full verification (Gate 5).**
    - `go test ./... -v` green (Docker running).
    - Per-plugin submodule tests: `cd plugins/memory && go test ./... -v`, same for postgres, sqlite. Per `feedback_plugin_submodule_tests`.
    - `go vet ./...` clean.
    - `go test -race ./...` clean (one-shot before PR).
    - `go test ./internal/e2e/... -v` green.

## 10. Success criteria

- All B-I1…B-I8 assertions pass on memory/postgres/sqlite. Cassandra passes in its sister repo against the new parity registry.
- Property-test suite completes in ≤ 90 s local / ≤ 120 s CI. Meta-test enforces 120 s ceiling as hard-fail.
- Parity registry exercises the four new `SchemaExtension*` scenarios against memory/postgres/sqlite.
- Per-plugin gray-box tests pass in each of memory/postgres/sqlite.
- `go test ./... -v` green in cyoda-go (with Docker).
- Per-plugin submodule tests green: memory, postgres, sqlite (each has its own `go.mod`).
- `go vet ./...` clean.
- `go test -race ./...` clean (one-shot before PR creation).
- E2E regression absent: `go test ./internal/e2e/... -v` green.
- Documentation reflects the two new env vars in `printHelp`, `README.md`, `DefaultConfig()`, and `CONTRIBUTING.md` (where applicable).
- Overview §6 invariant table updated with B-I1…B-I8.
- Cassandra go.mod bumps to the new spi tag and CountByState drift resolved.
- Issues #86 (unbound-data mode) and #87 (unified model+entity log) remain open, linked from this spec as tracked follow-ons.

## 11. Risks

**R-1. SQLite log-conversion scope.** Converting sqlite from apply-in-place to log-based is the largest single code-path change in B (~200-300 LOC new, most of `model_extensions_test.go` rewritten). Edge cases: (a) `BEGIN IMMEDIATE` / `SQLITE_BUSY` retry interaction with backend-native timeouts; (b) operators upgrading an existing sqlite file with populated `models.doc.schema` but empty extension log; (c) `applyFunc` wiring becoming mandatory for models with any extension history.

*Mitigation:* Mirror postgres's algorithm exactly — no novel sqlite-specific optimizations in B. The upgrade-path test (§5.3 point 6) explicitly covers the pre-B-state-open case. Track 1 parity tests catch cross-backend divergence by construction. Track 3 seeded property coverage amplifies.

**R-2. Cross-repo version bump coordination.** The new `cyoda-go-spi` tag, cyoda-go plugin go.mod bumps, and Cassandra's go.mod bump must land in the right order. A premature ship in one repo can leave the other uncompilable.

*Mitigation:* Execution order steps 1 + 10 + 12 explicitly sequence this. During iteration, `replace` directives are used so cross-repo work proceeds in parallel without tag thrash.

**R-3. Property-test wall-clock flakiness under real backends.** Testcontainers startup varies by host. Property suite wall-clock budget is 120 s; on slow CI hardware the budget may be tight.

*Mitigation:* Reuse the existing testcontainers singletons from the parity harness rather than spinning fresh containers per sample. Budget headroom (120 s vs ~80 s realistic on reference hardware). If CI flakes, the meta-test's message surfaces the exact wall-clock time so budget can be re-tuned with evidence, not guesswork.

**R-4. Pre-existing cross-plugin `go.mod` pin drift.** The Cassandra plugin's `go.mod` pins `plugins/memory@v0.1.0` and `plugins/postgres@v0.1.0`, which predate the SPI addition of `CountByState` on `EntityStore`. This surfaces today as a build failure in the Cassandra worktree. The drift is in the pins, not in Cassandra's own code (`CountByState` is implemented there). Blocks Cassandra builds until resolved.

*Mitigation:* Audit every plugin's go.mod at spi-bump time; surface all SPI-consumer drift (not just B's additions). Cassandra's B execution includes bumping the pinned plugin-module versions alongside the spi version.

## 12. Follow-on work

- **Sub-project A.3** — polymorphic-slot kind conflicts (issue #85).
- **Sub-project C** — concurrency + multi-node correctness.
- **Sub-project D** — input boundary hardening.
- **Issue #86** — unbound-data ingestion mode (filed 2026-04-22).
- **Issue #87** — unified model+entity log (filed 2026-04-22).
- **Fold-performance tripwire benchmark** — advisory, not a hard invariant. Appears as a future bench if B's property budget proves insufficient signal on pathological log growth.

## 13. References

- `docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md` — initiative overview.
- `docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a2-design.md` — in-memory invariants I1–I7.
- `docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md` — `ExtendSchema` pipeline.
- `docs/superpowers/specs/2026-04-15-postgres-si-first-committer-wins-design.md` — postgres FCW.
- `docs/superpowers/specs/2026-04-15-sqlite-storage-plugin-design.md` — sqlite plugin baseline.
- `cyoda-go-cassandra/docs/superpowers/specs/2026-04-22-data-ingestion-qa-subproject-b-cassandra-design.md` — Cassandra sister spec.
- `CLAUDE.md` gates 1–6 — TDD, E2E, security, docs, verify, resolve-don't-defer.
- `.claude/rules/race-testing.md` — one-shot race sanity pass before PR.
- `.claude/rules/documentation-hygiene.md` — env var / help-text / README sync.
- GitHub issue #86 — unbound-data ingestion mode.
- GitHub issue #87 — unified model+entity log.
