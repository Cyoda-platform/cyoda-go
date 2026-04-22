# Sub-project B — Handoff State (2026-04-22)

**Purpose:** This document captures the state at the Phase-4 checkpoint. A fresh session can pick up from here without re-reading the conversation transcript.

## Status summary

| Plan | Worktree | Branch | Origin | Progress |
|---|---|---|---|---|
| cyoda-go-spi | `/Users/paul/go-projects/cyoda-light/cyoda-go-spi/` | `main` (merged) | **`v0.6.0` tagged + pushed** | ✅ Complete |
| cyoda-go | `/Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence/` | `feat/subproject-b-persistence` | Pushed, tracking `origin/feat/subproject-b-persistence` | **14 / 31 tasks done** |
| cyoda-go-cassandra | `/Users/paul/go-projects/cyoda-light/cyoda-go-cassandra/.worktrees/subproject-b-persistence/` | `feat/subproject-b-persistence` | Not yet pushed | 0 / 15 tasks |

## What was completed (14 tasks)

Tasks **1 through 14** of the cyoda-go plan plus the implied Cassandra Task 1 *coordination* (cyoda-go-spi v0.6.0 exists and can now be consumed by a Cassandra go.mod bump).

### Phase 1 — cyoda-go-spi SPI additions (Tasks 1-3)
- Task 1: `ErrRetryExhausted` error type added to `errors.go`. Distinct from `ErrConflict`.
- Task 2: `ExtendSchema` method godoc on `ModelStore` interface clarified: retry contract, ctx-cancellation semantics, no-persisted-effect-on-error, empty-delta no-op.
- Task 3: Released as `cyoda-go-spi@v0.6.0`. The tag also carries the previously-unmerged `feat/model-schema-extensions` commit (`a89759d` — `ExtendSchema` method addition). One coordinated release.

### Phase 2 — plugin Config (Tasks 4-5)
- Task 4: `plugins/postgres/config.go` gains `SchemaSavepointInterval` field (default 64, env `CYODA_SCHEMA_SAVEPOINT_INTERVAL`). New helper `envIntMin1` clamps values < 1 to default with a slog warning.
- Task 5: `plugins/sqlite/config.go` gains BOTH `SchemaSavepointInterval` and `SchemaExtendMaxRetries` (default 8). Sqlite's existing helpers use an `Fn` suffix convention, so the new helper is named `envIntMin1Fn` for local consistency.

### Phase 3 — memory plugin B tests (Tasks 6-7)
- Task 6: `TestMemory_ExtendSchema_RejectionLeavesDescriptorUnmutated` (B-I6).
- Task 7: `TestMemory_ExtendSchema_ConvergenceUnderConcurrency` (B-I7). No production-code change in memory.

### Phase 4 — postgres B implementation + tests (Tasks 8-14)
- Task 8: `lastSavepointSeq(ctx, ref) (int64, error)` method added to `modelStore`. Plus internal-package test file `model_extensions_internal_test.go` with fixture `newPGFixture(t)`.
- Task 9: Savepoint trigger in `ExtendSchema` refactored from hardcoded `newSeq%64==0` to `(newSeq - lastSavepointSeq) >= cfg.SchemaSavepointInterval`. Config threaded through `StoreFactory` and `DBConfig.toInternal()`. Existing `TestExtendSchema_SavepointEvery64` stays green at default interval=64.
- Task 10: Save-on-lock implemented in `Lock`. Lock is now idempotent (skips savepoint on re-lock) and handles nil-schema models. Unlock's dev-mode "operator-contract violation" check narrowed to **delta rows only** — lifecycle savepoints from save-on-lock are silently drained. This is a real behavior change for Unlock and deserves reviewer attention; see note below.
- Task 11: `TestExtendSchema_CommutativeAppend_ConvergesUnderConcurrency` (B-I7 local).
- Task 12: `TestExtendSchema_ContextCancellation_ReturnsCtxErr`.
- Task 13: `TestExtendSchema_FoldAcrossSavepointBoundary_ByteIdentical` (B-I2 local).
- Task 14: `TestExtendSchema_RejectionLeavesNoSavepointOrOp` (B-I6 tightening). **Added production code:** `ExtendSchema` now self-wraps in a pgx transaction when no ambient tx is present, so a failing savepoint-fold path rolls back the just-inserted delta row. Uses an `extendSchemaBody(ctx, q Querier, ...)` helper with a shadow `modelStore` for the tx-bound querier (subagent-chosen pattern).

## Adaptations from the plan

Noted where the subagents deviated from the plan text and why:

| Task | Deviation | Rationale |
|---|---|---|
| 1 | Used unqualified `ErrRetryExhausted` / `ErrConflict` in `errors_test.go` instead of the plan's `spi.ErrRetryExhausted`. | The test file is internal (`package spi`), not external; unqualified matches the file's existing style. |
| 1 (post-implementer) | Branch rebased onto `feat/model-schema-extensions` before Task 2. | `main` did not have `ExtendSchema` yet; the plan assumed it did. Rebase brought the method into scope for Task 2's godoc edit. |
| 5 | Sqlite helper named `envIntMin1Fn` (not `envIntMin1`). | Local consistency with sqlite's existing `envIntFn`/`envBoolFn` naming. |
| 6-7 | Plan referenced `newTestFactory(t)` / `withTenant` / `factory.SetApplyFunc`. Memory's real helpers are `memory.NewStoreFactory(memory.WithApplyFunc(fn))` and `extTestCtx("t1")`. Plan text's `spi.ModelDraft` doesn't exist — used `spi.ModelUnlocked` + `Lock()`. | Use real helpers that exist in the tree, per the plan's "Before You Begin" note allowing this. |
| 8 | Tests placed in new internal-package test file `model_extensions_internal_test.go`. Hardcoded `want 128` from the plan's test replaced with SQL cross-check. | Unexported method access + `seq` is a shared `BIGSERIAL` that both deltas and savepoints consume; hardcoded seq arithmetic was wrong. |
| 9 | Test rewritten to assert savepoint-count / seq-distance invariants rather than exact seq values. | Same BIGSERIAL reality as Task 8. |
| 10 | Lock idempotence + nil-schema guard + Unlock dev-mode narrowing. | Save-on-lock as-specified broke pre-existing `testModelLockIdempotent` and `TestUnlock_*` tests. Fixes are load-bearing and correctness-preserving; Gate 6 "resolve, don't defer." |
| 11-13 | Delta payloads JSON-encoded; `setUnionApplyFunc` adapted to handle JSON strings; `sortNewlineTokens` reshaped accordingly. | Postgres's `payload` column is `JSONB`, not raw bytes. Plain strings fail with `invalid input syntax for type json`. |
| 14 | `ExtendSchema` self-wraps in tx when no ambient tx present; test + production change combined into one commit. | Without self-wrap, the delta INSERT auto-commits before the savepoint-fold fails, violating B-I6. Combined commit reflects the atomicity linkage. |

## Behavior changes worth reviewer attention

1. **Unlock drains the extension log wholesale** (deltas + savepoints) in both dev and prod. Previously the dev-mode "operator-contract violation" check errored on ANY remaining row; it now counts only `kind='delta'` rows and silently drains savepoints. Rationale: after save-on-lock, the lifecycle legitimately produces savepoint rows. The operator-contract warning should fire only on stale deltas (evidence of a writer that didn't drain). This is an intentional narrowing; see commit `50cf24f` diff for the full change.

2. **`ExtendSchema` now self-wraps in a pgx transaction** when no ambient entity tx is present. This was added to honor B-I6 atomicity on the savepoint-fold-error path. Existing behavior (when an ambient tx IS present) is unchanged — still participates in the ambient tx's visibility. Commit `3a6addd`.

3. **`modelStore` gained a `pool *pgxpool.Pool` field** to support the self-wrap path. Wired through `store_factory.go`. Commit `3a6addd`.

4. **Shadow `modelStore` copy pattern** for threading the tx querier through the existing fold/baseSchema helpers. Works because no field carries per-call mutable state today; if that changes in future, the pattern must be revisited.

## Test suite state

```
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
go vet ./...                  # clean
cd plugins/memory && go test ./...   # ok
cd ../postgres && go test ./...      # ok  (includes 5 new tests)
cd ../sqlite && go test ./...        # ok  (sqlite has B config but no B behavior changes yet)
```

Root-level `go test ./...` not run yet — deferred until after Phase 7 (parity registry) so the e2e suite isn't exercised against half-built infrastructure. Gate 5 verification is Task 31.

## Known pre-existing flakes (NOT caused by B)

- `TestEntityStore_GetAsAt` and `TestEntityStore_GetAllAsAt` in `plugins/postgres` flake ~1-in-6 under full-suite load. They use `time.Sleep(2*time.Millisecond)` between writes. Pre-existing; intermittent; consistently pass in isolation and on repeated runs.

Out of scope for B. Consider tracking separately.

## Remaining work — cyoda-go plan

### Phase 5 — sqlite conversion (Tasks 15-20) — LARGEST REMAINING CHUNK
- Task 15: Create `plugins/sqlite/model_extensions.go` with `foldLocked` + `lastSavepointSeq`. TDD scaffolds.
- Task 16: Rewrite `ExtendSchema` from apply-in-place to log-based. Populate `model_schema_extensions`. Make `applyFunc` wiring mandatory for models with pending deltas. Update `Get` to call `foldLocked`.
- Task 17: Savepoint triggering (interval + on-lock). Reuse postgres's pattern via `lastSavepointSeq*InTx` helpers inside `BEGIN IMMEDIATE` transactions.
- Task 18: Upgrade-path test (pre-B sqlite file with populated `models.doc.schema` and empty extension log → zero-deltas fold returns base verbatim).
- Task 19: `SQLITE_BUSY` retry wrapper around `extendSchemaAttempt`. `CYODA_SCHEMA_EXTEND_MAX_RETRIES=8` default; ctx cancellation returns `ctx.Err()` wrapped with attempt count.
- Task 20: Remaining sqlite-local tests (B-I2, B-I6) mirroring postgres.

Estimated size: ~200-300 LOC of new production code + ~200 LOC of tests. The largest single task bundle.

### Phase 6 — modelcache B-I8 test (Task 21)
- Add `TestCachingModelStore_ExtendSchema_InvalidatesCache` to `internal/cluster/modelcache/cache_test.go`. The production implementation already exists at `cache.go:178-184`; the test promotes the existing invalidation to an executable contract.

### Phase 7 — parity registry + property harness (Tasks 22-28)
- Tasks 22-26: Five new named parity entries (byte identity, atomic rejection, concurrent convergence, save-on-lock fold equivalence, cache invalidation). Each is a top-level `Run*` function in `e2e/parity/` registered in `registry.go`.
- Task 27: Property-based parity entry + `e2e/parity/oracle.go` with the deterministic in-memory oracle helper. Requires wiring the actual schema/importer signatures (verified in the plan: `schema.Extend(existing, incoming, level)`, `importer.Walk(data any)`, `schema.Marshal(n)`).
- Task 28: Runtime-budget meta-test (`TestParity_SchemaExtensionProperty_Budget_CI`) enforcing 120s CI ceiling.

**Property-test oracle is cross-package:** lives in `e2e/parity/oracle.go`, imports `internal/domain/model/{schema,importer}`. Make sure `e2e/parity/` doesn't become a cyclic module.

### Phase 8 — documentation (Task 29)
- `cmd/cyoda/main.go` `printHelp()` adds both env vars.
- `README.md` config table adds both env vars with an "Honored by" column.
- `docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md` §6 invariant table replaces the two `TBD | B | ...` rows with concrete B-I1..B-I8.

### Phase 9 — plugin go.mod bumps (Task 30)
- Bump `cyoda-go-spi` from current (`v0.5.3`?) to `v0.6.0` in each plugin's `go.mod` and at the repo root.
- Bump `plugins/memory`, `plugins/postgres`, `plugins/sqlite` in the root `go.mod` if they're consumed as modules externally.
- Run `go mod tidy` per module. Verify builds cleanly.

### Phase 10 — Gate 5 verification (Task 31)
- `go test ./... -v` (Docker running) — full suite including e2e and parity.
- Per-plugin submodule tests.
- `go vet ./...`.
- `go test -race ./...` one-shot before PR creation.

## Remaining work — Cassandra plan (all 15 tasks)

Located at `cyoda-go-cassandra/.worktrees/subproject-b-persistence/docs/superpowers/plans/2026-04-22-data-ingestion-qa-subproject-b-cassandra.md`. Summary of what's pending there:

- **Task 1:** bump cyoda-go-spi to v0.6.0 + plugin-module pins (resolves pre-existing `CountByState` drift + picks up B's additions). Can be done immediately.
- **Task 2:** migration — append `model_schema_extensions` table to `migrations/000001_initial_schema.up.cql`.
- **Task 3:** Cassandra `config.go` gains `SchemaSavepointInterval` + `SchemaExtendMaxRetries`.
- **Tasks 4-7:** core implementation. `model_extensions.go` with LWT-gated insert + HLC-sequenced `delta_seq` + retry loop + savepoint-on-size-threshold via `LoggedBatch`. `Get` wired to `foldLocked`. `Lock` wraps state change + savepoint in a single `LoggedBatch`.
- **Tasks 8-11:** per-plugin gray-box tests (B-I7 LWT retry convergence, ctx cancellation, B-I6 rejection, B-I2 fold across savepoint, B-I8 post-extend Get reflects state).
- **Task 12:** wire `schema.Apply` as the plugin's injected `ApplyFunc` at construction time.
- **Tasks 13-14:** consume cyoda-go's new parity entries via go.mod refresh; doc update in `CASSANDRA_BACKEND_DESIGN.md`.
- **Task 15:** Gate 5 verification (Cassandra testcontainer suite).

**Critical dependency:** Cassandra tests require a running Cassandra container. The existing test harness bootstraps it. Ensure Docker is available before attempting.

## Known open questions / TODOs for the resumer

1. **Task 10 unlock-behavior commit message is vague** about the dev-mode narrowing (commit `50cf24f`). Consider amending when convenient, or document in the PR description instead.

2. **Pre-existing flakes** (`TestEntityStore_GetAsAt*`) — worth filing an issue to fix independently. Not in scope for B.

3. **Postgres self-wrap uses `shadow := *s`** copy pattern in Task 14. Works today but would break if `modelStore` ever gains mutable per-instance state. Low-risk forward note.

4. **Cassandra plan's Task 4+ uses `replace` directive** during development. Once cyoda-go Task 30 lands (plugin go.mod bumps to published versions), Cassandra can consume the real tags.

5. **Parity oracle implementation detail (Task 27):** the plan specifies `schema.Extend(existing, incoming, level)` where `level` is `spi.ChangeLevelStructural`. Confirm this is the right level for oracle generation — it should permit the widest set of deltas the property tests generate.

## How to resume

**In a fresh session, run:**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git pull   # confirm local is current
git log --oneline -15   # sanity check — should show 3a6addd at top
```

Read this handoff doc, the plan, and the spec (rev 3). The next task is **Task 15** (sqlite conversion).

For Cassandra, a parallel path is viable — **cyoda-go-cassandra Task 1** can be done immediately (bump deps, picks up v0.6.0) and then Tasks 2-11 can proceed against the current local state of cyoda-go via a `replace` directive.

## Commit range for review (cyoda-go)

The B work starts at `a473163` (spec rev 1) and currently ends at `3a6addd` (Task 14). Commits `a473163`..`3a6addd` are all B's work on `feat/subproject-b-persistence`.

```
git log --oneline a473163..3a6addd
```

yields 15 commits: 4 docs (spec rev 1/2/3 + plan) and 11 code/test commits (Tasks 4-14; Tasks 6-7 are two tests in one area).

## Commit range for review (cyoda-go-spi)

Tasks 1-3. Published as `v0.6.0` on the main branch. Commits: `a56f603` (ErrRetryExhausted) + `2993ad0` (ExtendSchema godoc).
