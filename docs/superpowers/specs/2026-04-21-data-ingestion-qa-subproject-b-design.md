# Sub-project B — Plugin Persistence + Fold Correctness

**Date:** 2026-04-21
**Revision:** 1
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
- A transparent-retry SPI contract that hides backend-specific conflict semantics from callers.
- Single-node cross-storage atomicity: a rejected `ExtendSchema` leaves no persisted trace anywhere in the backend's storage surface.

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
- **Plugin-internal savepoint policy.** Service layer does not count ops or decide savepoint placement. Each plugin reads the interval config at construction and owns its own counter.
- **Transparent retry at the SPI.** Callers see `ExtendSchema` as atomic-or-fail. Retry logic, conflict-detection idioms, and backoff live inside the plugin.

## 3. Invariants

B's contract. Each invariant is asserted across at least one of the three test tracks in §7. Memory is exempt from savepoint-specific invariants (B-I2/B-I3/B-I4) because it has no extension log.

| ID | Name | Backends | Summary |
|---|---|---|---|
| **B-I1** | Cross-plugin byte-identical fold | all four | `schema.Marshal(Load(modelRef))` byte-identical across memory/postgres/sqlite/cassandra for identical extension-call history. Memory's in-place state and log-backend folds produce the same bytes. |
| **B-I2** | Savepoint transparency | log-backends | Adding a savepoint to a log does not change `Marshal(Load(log))`. Savepoints are a load-time optimization, not a semantic operator. |
| **B-I3** | Save-on-lock atomicity | log-backends | `Lock(model)` commits lock-state + savepoint atomically, or neither. No split state under any failure. |
| **B-I4** | Save-on-size-threshold atomicity | log-backends | When the Nth op since last savepoint commits (N = `CYODA_SCHEMA_SAVEPOINT_INTERVAL`, default 64), a savepoint is written atomically with that op in the same backend-native commit. |
| **B-I5** | Causal-order preservation | all four | Extensions apply in the order they committed — memory via mutex-serialized in-place update, log-backends via clustering/seq order after the most recent savepoint. |
| **B-I6** | Cross-storage atomicity on rejection | all four | Rejected `ExtendSchema` (ChangeLevel violation, invariant breach, validation failure) leaves no persisted trace — no partial savepoint, no orphaned op, no torn write. Cross-storage extension of A.2's I7 (in-memory atomicity on rejection). |
| **B-I7** | Concurrent-extension convergence | all four | Under N concurrent `ExtendSchema` calls on same `(model,version)`, all succeed or exhaust `CYODA_SCHEMA_EXTEND_MAX_RETRIES` (default 8) via transparent retry inside the plugin; final fold is order-independent (follows from A.2 I5 — permutation invariance). |

"log-backends" ≡ postgres, sqlite, cassandra. Memory is exempt from B-I2/B-I3/B-I4.

**B-I1 note.** Byte identity is asserted on `schema.Marshal(Load(...))`, not on raw storage bytes. Backends are free to serialize at rest however (JSONB, opaque blob, partitioned rows). The invariant is on the observable output of the fold.

**B-I7 note.** Convergence follows from A.2's I5 (N-permutation invariance): any permutation of a set of deltas against a shared base yields the same final schema. Transparent retry re-reads the fresh base after a conflict and re-applies the delta against it. Idempotence (A.2 I4) guarantees that retrying a delta whose effect was already committed by a winner produces the same state.

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

**Postgres, sqlite, cassandra** — each plugin's `Config` struct gains two fields:

```go
type Config struct {
    // ... existing fields ...
    SchemaSavepointInterval int // default 64; read from CYODA_SCHEMA_SAVEPOINT_INTERVAL
    SchemaExtendMaxRetries  int // default 8;  read from CYODA_SCHEMA_EXTEND_MAX_RETRIES
}
```

`DefaultConfig()` populates both fields with the defaults. `FromEnv()` (or the equivalent env-reading helper per plugin) reads the env vars with defaults-preserved semantics (invalid/unset → default).

**Memory** — memory uses functional options (`Option`), not a `Config` struct, and consumes neither knob (no savepoints; no retry loop ever retries because the mutex never produces a conflict surface). The retry wrapper is kept for SPI-contract uniformity; it reads no config. No functional-option additions are required for B.

## 5. Per-plugin design

### 5.1 Memory plugin

**Current state** (per survey): no extension log. Schema stored as a single folded `[]byte` in `spi.ModelDescriptor.Schema`. `ExtendSchema` acquires `modelMu`, calls the injected `applyFunc`, replaces the descriptor's schema bytes in place. Mutex serializes all writes; rejection (applyFunc error) leaves the descriptor untouched.

**B changes:**

1. **Add transparent retry wrapper** — memory's mutex never produces a conflict surface, so the retry wrapper is a no-op loop that always succeeds on first attempt. Kept to honor the SPI contract uniformly; simplifies the test harness (every plugin behaves the same way at the surface).
2. **Add explicit B-I6 test** — ChangeLevel-violating extension fails, descriptor schema bytes unchanged (currently asserted implicitly via "applyFunc error → no mutation"; B promotes to an explicit named test).
3. **Add B-I7 local concurrency stress test** — N goroutines call `ExtendSchema` on the same model; assert mutex serialization produces deterministic final state and no torn writes.

**No migration, no data-model change.**

### 5.2 Postgres plugin

**Current state** (per survey): `model_schema_extensions` table exists with `(tenant_id, model_name, model_version, seq, kind IN ('delta','savepoint'), payload JSONB, tx_id, created_at)`. Savepoint every **64** deltas is already implemented (hardcoded at `plugins/postgres/model_extensions.go:276`). Fold-on-read = reverse-scan for the latest savepoint, forward-apply deltas after it. Uses native postgres `SAVEPOINT` for transaction-level rollback visibility. FCW via row-level version validation (readSet tracked, `FOR SHARE` at commit, `ErrConflict` on mismatch). No internal retry — `ErrConflict` surfaces to caller.

**B changes:**

1. **Refactor hardcoded `savepointEveryN = 64`** at `model_extensions.go:276` to read `cfg.SchemaSavepointInterval`. Default 64 preserved.
2. **Add save-on-lock** — on the `Lock` transition (unlocked → locked), write a savepoint row atomically with the lock-state change in the same postgres transaction. Asserts B-I3.
3. **Add transparent retry wrapper** — on `ErrConflict` from the FCW machinery, re-read schema, re-apply delta against fresh base, retry up to `cfg.SchemaExtendMaxRetries`. Exhaustion surfaces `ErrRetryExhausted`.
4. **Parameterize `TestExtendSchema_SavepointEvery64`** by the config value. Existing tests stay green at default 64.

**Added tests (per-plugin):**
- `TestPostgres_ExtendSchema_SaveOnLock` (B-I3).
- `TestPostgres_ExtendSchema_TransparentRetry_ConvergesUnderContention` (B-I7 local).
- `TestPostgres_ExtendSchema_FoldAcrossSavepointBoundary_UnderConcurrentReads` (B-I1/B-I2 local).
- `TestPostgres_ExtendSchema_RejectionLeavesNoSavepointOrOp` (B-I6 tightening).

**No migration** — existing table schema supports both kinds already.

### 5.3 SQLite plugin

**Current state** (per survey): `model_schema_extensions` table exists in migrations but is **unused**. `ExtendSchema` does apply-in-place read-modify-write under `BEGIN IMMEDIATE`. Comment in migration: "SQLite is single-node by design; this table exists for SPI parity and for the conformance tests. Fold is trivial since there is only one writer."

**B converts sqlite to log-based.** The unused table becomes the active log, mirroring postgres's algorithm.

**B changes:**

1. **Replace apply-in-place read-modify-write** in `plugins/sqlite/model_store.go` with log-based implementation. Populate `model_schema_extensions` with `(kind='delta')` rows on extension, `(kind='savepoint')` rows on the size-threshold trigger and on lock.
2. **Transaction shape:** `BEGIN IMMEDIATE`, append delta row + (conditionally) savepoint row, commit. Sqlite's single-writer model serializes concurrent extensions via `SQLITE_BUSY`. The plugin catches `SQLITE_BUSY`, waits up to `busy_timeout`, and retries transparently. Exhaustion after `cfg.SchemaExtendMaxRetries` surfaces `ErrRetryExhausted`.
3. **Fold-on-read** matches postgres: reverse-scan for most recent savepoint, forward-apply ops after it. Same injected `applyFunc`.
4. **Save-on-lock:** on `Lock` transition, write savepoint row atomically with the lock-state change in the same SQLite transaction.

**Rewritten tests** (existing tests adapted for log semantics, not new tests):
- `TestSQLite_ExtendSchema_AppliesInPlace` → `TestSQLite_ExtendSchema_AppendsToLog`.
- `TestSQLite_ExtendSchema_MultiDeltaFold` → asserts fold via log replay, not in-place accumulation.
- `TestSQLite_ExtendSchema_CrossTenantIsolation` → unchanged semantically.

**Added tests (per-plugin, mirroring postgres):**
- `TestSQLite_ExtendSchema_SavepointAtConfigInterval` (B-I4).
- `TestSQLite_ExtendSchema_SaveOnLock` (B-I3).
- `TestSQLite_ExtendSchema_TransparentRetry_ConvergesUnderBusy` (B-I7 local).
- `TestSQLite_ExtendSchema_RejectionLeavesNoPersistedTrace` (B-I6).

**No migration needed** — the `model_schema_extensions` table already exists.

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

Cassandra currently pins `cyoda-go-spi v0.5.3` and has pre-existing SPI drift unrelated to B (missing `CountByState` on `EntityStore` — build failure in the Cassandra worktree today). That drift must be resolved in the same coordinated SPI bump.

**Coordinated sequence:**

1. Cut `cyoda-go-spi@v0.6.0` (or next version) including:
   - `ErrRetryExhausted`.
   - `ExtendSchema` contract godoc updates.
   - Any other SPI methods the other plugins already depend on (audit against each plugin's go.mod at ship time).
2. cyoda-go plugin `go.mod` bumps land alongside B's implementation commits.
3. Cassandra's sister PR bumps its go.mod to the new spi tag and lands its realization.

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

The existing `SchemaExtensionsSequentialFoldAcrossRequests` entry is kept unchanged.

### 7.2 Track 2 — Per-plugin gray-box

Backend-specific log introspection — each plugin uses its native query idiom. Tests live in each plugin's own `model_extensions_test.go` (per-plugin `go.mod` submodule).

- **Memory** (`plugins/memory/model_extensions_test.go`): rejection atomicity (B-I6), mutex serialization under concurrent extend (B-I7 local). No log; B-I2/B-I3/B-I4 N/A.
- **Postgres** (`plugins/postgres/model_extensions_test.go`): savepoint row at `seq = N × interval` (B-I4), savepoint row at lock transition (B-I3), forward-scan clustering order after savepoint (B-I5), no savepoint on rejection (B-I6 tightening), FCW loser → retry → convergence (B-I7 local).
- **SQLite** (`plugins/sqlite/model_extensions_test.go`): same assertions as postgres, adapted for `BEGIN IMMEDIATE` + busy_timeout retry.
- **Cassandra** (in sister spec): LWT collision → HLC-bump retry → convergence, save-on-lock in `LoggedBatch`, savepoint at `delta_seq mod interval == 0`.

### 7.3 Track 3 — Property-based (seed-driven, SPI-layer)

New package `internal/domain/model/schema/persistence/`. Reuses A.2's `gentree` generator (no modifications; add a new import). Parameterizes by a `BackendFixture` abstraction that exposes a real `spi.ModelStore` backed by each plugin in sequence.

```go
// BackendFixture is the persistence-layer contract for property-based
// testing against real plugin stores. Tests iterate over fixture
// implementations (memory, postgres, sqlite) and run the same property
// body against each.
type BackendFixture interface {
    Store() spi.ModelStore
    Reset(t *testing.T) // fresh tenant + model per sample
    Name() string
}
```

Property tests iterate `gentree` seeds, drive each backend through an extension sequence, and assert B-I1/B-I2/B-I5/B-I6 byte-identity and convergence across backends.

**Sample budget:** 50 seeded samples per invariant per applicable backend. Applicability = property-testable invariants (B-I1, B-I2, B-I5, B-I6) against in-repo backends where the invariant is not exempt. B-I1/B-I5/B-I6 run against memory + postgres + sqlite (3 backends); B-I2 runs against postgres + sqlite only (memory exempt). Total ≈ 50 × (3+2+3+3) = 550 samples local. Cassandra participates via its own CI on its own cadence.

**Runtime budget: 90 s local / 120 s CI hard fail.** Meta-test `TestBBPropertySuiteBudget` in the property-test package enforces the 120 s ceiling — hard-fails if wall-clock time exceeds it.

Determinism discipline (inherited from A.2):
- `math/rand/v2` PCG seeding, explicit and logged on failure.
- Seed replay via `-seed=<N>` flag.
- No `range` over Go maps in generator paths.

### 7.4 Coverage map

| Invariant | Track 1 (parity) | Track 2 (gray-box) | Track 3 (property) |
|---|---|---|---|
| B-I1 | ✓ | — | ✓ |
| B-I2 | ✓ (subsumed by B-I1 cross-backend) | ✓ postgres/sqlite/cassandra | ✓ |
| B-I3 | ✓ | ✓ postgres/sqlite/cassandra | — |
| B-I4 | — | ✓ postgres/sqlite/cassandra | — |
| B-I5 | ✓ (via B-I1) | ✓ postgres/sqlite/cassandra | ✓ |
| B-I6 | ✓ | ✓ all four | ✓ |
| B-I7 | ✓ | ✓ all four | — |

### 7.5 Race detector

`go test -race ./...` is a one-shot sanity pass before PR creation per `.claude/rules/race-testing.md`. Not a per-step gate.

## 8. Config + documentation

### 8.1 New env vars

| Env var | Default | Type | Scope |
|---|---|---|---|
| `CYODA_SCHEMA_SAVEPOINT_INTERVAL` | `64` | Integer, ≥ 1 | Per-plugin (read at construction) |
| `CYODA_SCHEMA_EXTEND_MAX_RETRIES` | `8` | Integer, ≥ 1 | Per-plugin (read at construction; no-op for memory) |

Invalid values (≤ 0, non-integer) fall back to the default with a logged warning at plugin construction.

### 8.2 Documentation updates (Gate 4)

Updated together in a single documentation commit:

- `cmd/cyoda/main.go` `printHelp()` — add both env vars under the plugin-configuration reference section.
- `README.md` — add both env vars to the configuration table.
- `DefaultConfig()` in each plugin with a `Config` struct — `plugins/postgres/config.go`, `plugins/sqlite/config.go`, and the Cassandra plugin's `config.go` in its repo. Memory is N/A (no `Config`, uses functional options; does not consume these knobs).
- `CONTRIBUTING.md` — note the `CYODA_SCHEMA_*` config knobs in the testing section if relevant.

### 8.3 Overview invariant table update

`docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md` §6 currently lists:

```
| TBD | B | Cross-plugin byte-identical fold |
| TBD | B | Single-node cross-storage atomicity |
```

These TBDs are replaced at ship with the seven concrete B invariants from §3 above, in one edit. Done as part of B's final commit, not in advance.

## 9. Execution order

1. **SPI additions in `cyoda-go-spi`.** `ErrRetryExhausted`, contract godoc updates. Fresh tag.
2. **Config struct additions + env var plumbing** in each cyoda-go plugin. Default-preserving (all existing behavior unchanged).
3. **Memory plugin** — test additions (B-I6 explicit, B-I7 local concurrency stress). No behavior change.
4. **Postgres plugin** — interval-config wiring, save-on-lock, transparent retry wrapper. Per-plugin tests for B-I3/B-I4-parameterized/B-I7.
5. **SQLite plugin** — convert from apply-in-place to log-based. New code path mirroring postgres adapted for `BEGIN IMMEDIATE`. Full B-I1…B-I7 sqlite-local test coverage.
6. **Parity registry additions** — four new `SchemaExtension*` entries in `e2e/parity/registry.go`.
7. **Property-test harness** — `internal/domain/model/schema/persistence/` package with `BackendFixture` abstraction and property test files (one per primary invariant that admits property-based coverage: B-I1, B-I2, B-I5, B-I6).
8. **Runtime-budget meta-test** — `TestBBPropertySuiteBudget` enforces 120 s CI ceiling.
9. **Docs pass** — `printHelp()`, `README.md`, `DefaultConfig()`, `CONTRIBUTING.md`.
10. **SPI bump in cyoda-go** — `go.mod` updated to the new spi tag. Sanity: existing tests stay green.
11. **Overview §6 invariant-table update** — replace TBDs with concrete B-I1…B-I7.
12. **Cassandra execution** — in its sister repo, on its own PR cadence. Consumes cyoda-go's new spi tag via go.mod bump; picks up new parity tests automatically.
13. **Full verification (Gate 5).**
    - `go test ./... -v` green (Docker running).
    - Per-plugin submodule tests: `cd plugins/memory && go test ./... -v`, same for postgres, sqlite. Per `feedback_plugin_submodule_tests`.
    - `go vet ./...` clean.
    - `go test -race ./...` clean (one-shot before PR).
    - `go test ./internal/e2e/... -v` green.

## 10. Success criteria

- All B-I1…B-I7 assertions pass on memory/postgres/sqlite. Cassandra passes in its sister repo against the new parity registry.
- Property-test suite completes in ≤ 90 s local / ≤ 120 s CI. Meta-test enforces 120 s ceiling as hard-fail.
- Parity registry exercises the four new `SchemaExtension*` scenarios against memory/postgres/sqlite.
- Per-plugin gray-box tests pass in each of memory/postgres/sqlite.
- `go test ./... -v` green in cyoda-go (with Docker).
- Per-plugin submodule tests green: memory, postgres, sqlite (each has its own `go.mod`).
- `go vet ./...` clean.
- `go test -race ./...` clean (one-shot before PR creation).
- E2E regression absent: `go test ./internal/e2e/... -v` green.
- Documentation reflects the two new env vars in `printHelp`, `README.md`, `DefaultConfig()`, and `CONTRIBUTING.md` (where applicable).
- Overview §6 invariant table updated with B-I1…B-I7.
- Cassandra go.mod bumps to the new spi tag and CountByState drift resolved.
- Issues #86 (unbound-data mode) and #87 (unified model+entity log) remain open, linked from this spec as tracked follow-ons.

## 11. Risks

**R-1. SQLite log-conversion scope creep.** Converting sqlite from apply-in-place to log-based is the largest single code-path change in B. The risk is that edge cases in `BEGIN IMMEDIATE` semantics or busy-timeout retry interaction create subtle divergence from postgres's behavior.

*Mitigation:* Mirror postgres's algorithm exactly. No novel sqlite-specific optimizations in B. Track 1 parity tests catch cross-backend divergence by construction. Property-based Track 3 amplifies coverage.

**R-2. Cross-repo version bump coordination.** The new `cyoda-go-spi` tag, cyoda-go plugin go.mod bumps, and Cassandra's go.mod bump must land in the right order. A premature ship in one repo can leave the other uncompilable.

*Mitigation:* Execution order steps 1 + 10 + 12 explicitly sequence this. During iteration, `replace` directives are used so cross-repo work proceeds in parallel without tag thrash.

**R-3. Property-test wall-clock flakiness under real backends.** Testcontainers startup varies by host. Property suite wall-clock budget is 120 s; on slow CI hardware the budget may be tight.

*Mitigation:* Reuse the existing testcontainers singletons from the parity harness rather than spinning fresh containers per sample. Budget headroom (120 s vs ~80 s realistic on reference hardware). If CI flakes, the meta-test's message surfaces the exact wall-clock time so budget can be re-tuned with evidence, not guesswork.

**R-4. Pre-existing Cassandra SPI drift (`CountByState`).** Unrelated to B but blocks Cassandra builds today. Must be resolved in the same coordinated spi bump.

*Mitigation:* Audit every plugin's go.mod at spi-bump time; surface all SPI drift (not just B's additions). Cassandra's B execution includes fixing this drift.

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
