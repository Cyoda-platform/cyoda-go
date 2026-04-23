# Sub-project B — Handoff State (updated 2026-04-22, end-of-cyoda-go)

**Purpose:** Captures the state after cyoda-go completion. A fresh session can pick up the remaining cyoda-go-cassandra work without re-reading the conversation transcript.

## Status summary

| Repo | Worktree | Branch | Origin | Progress |
|---|---|---|---|---|
| cyoda-go-spi | `/Users/paul/go-projects/cyoda-light/cyoda-go-spi/` | `main` (merged) | **`v0.6.0` tagged + pushed** | ✅ Complete |
| cyoda-go | `/Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence/` | `feat/subproject-b-persistence` | Pushed, tracking `origin/feat/subproject-b-persistence` | ✅ **31 / 31 tasks + 3 bugfixes** |
| cyoda-go-cassandra | `/Users/paul/go-projects/cyoda-light/cyoda-go-cassandra/.worktrees/subproject-b-persistence/` | `feat/subproject-b-persistence` | Not yet pushed | **0 / 15 tasks** |

---

## cyoda-go: COMPLETE

All 31 plan tasks done across Phases 1-10. All pushed. Branch `feat/subproject-b-persistence` tracks origin and is clean.

### Gate 5 verification results (end-of-deliverable)

| Check | Result |
|---|---|
| `go vet ./...` root + all 3 plugin submodules | clean |
| `go test ./... -count=1` root module (37 pkgs) | PASS |
| `go test ./... -count=1` plugins/memory | PASS |
| `go test ./... -count=1` plugins/postgres | PASS |
| `go test ./... -count=1` plugins/sqlite | PASS |
| `go test -race ./... -count=1` root | PASS, no races |
| `go test -race ./... -count=1` per plugin | PASS, no races |

### Phase-by-phase summary

| Phase | Tasks | Deliverable |
|---|---|---|
| 1 | 1-3 | cyoda-go-spi v0.6.0 (`ErrRetryExhausted`, ExtendSchema godoc, tag) |
| 2 | 4-5 | Plugin config (postgres + sqlite: `SchemaSavepointInterval`, `SchemaExtendMaxRetries`) |
| 3 | 6-7 | Memory plugin B tests (B-I6 + B-I7) |
| 4 | 8-14 | Postgres B implementation + tests (interval refactor, save-on-lock, 4 new tests, self-wrap tx) |
| 5 | 15-20 | Sqlite conversion: apply-in-place → append-to-log with savepoints, BUSY retry, upgrade-path, local B tests |
| 6 | 21 | modelcache B-I8 verification test (stronger assertion than existing) |
| 7 | 22-28 | Five named parity entries + property harness (50 seeds × 3 backends) + runtime budget meta-test |
| 8 | 29 | Docs: `printHelp()`, `README.md`, overview §6 invariant table |
| 9 | 30 | go.mod bumps to cyoda-go-spi v0.6.0 across all modules |
| 10 | 31 | Gate 5 verification (✓ all green) |

### Bugs surfaced + fixed during Phase 7

The property-based parity test (Task 27) surfaced three real bugs en route — all fixed with TDD before Task 27 was allowed to land. This is exactly what B was designed to catch.

| Commit | Bug | Fix |
|---|---|---|
| `2b43009` | `schema.Extend`/`Merge` silently accepted kind-mismatched subtrees, producing OBJECT-with-primitive-types TypeSets that violate the Apply invariant | `checkAndExtend` now rejects kind mismatches explicitly with a `"kind mismatch at PATH: EXISTING vs INCOMING"` error (4xx at handler) |
| `f4a7728` | postgres and sqlite persisted delta rows without running Apply — malformed deltas only failed at fold-on-read time | Added pre-persist `applyFunc(current, delta)` check in both plugins, matching memory's apply-inline behavior. Symmetric contract across plugins. |
| `c965f23` | handler service layer blanket-mapped every `modelStore.Get` error to 404 MODEL_NOT_FOUND — fold/apply failures looked identical to missing rows | `CreateEntity` and `ExportModel` (+ 6 other sites) now do `errors.Is(err, spi.ErrNotFound)` → 404, everything else → 5xx with standard `common.Internal` ticket |

### Commit range (cyoda-go)

The B work is on branch `feat/subproject-b-persistence`. Top commits (newest first):

```
6427fa3 chore: bump cyoda-go-spi to v0.6.0
79fa20d docs: env vars in printHelp/README + overview §6 invariant table
c636f7f test(parity): runtime-budget meta-test for property suite (§7.3)
33750e7 test(parity): property-based B-I1 with in-memory oracle, 50 seeds
c965f23 fix(service): distinguish ErrNotFound from other Get errors         # bugfix 3
f4a7728 fix(postgres,sqlite): pre-persist Apply check on ExtendSchema       # bugfix 2
2b43009 fix(schema): Extend rejects kind mismatches                          # bugfix 1
9131796 test(parity): B-I8 — local cache invalidation on extension commit
82faff7 test(parity): B-I2/B-I3 — savepoint-on-lock fold equivalence
1af824b test(parity): B-I7 — concurrent schema-extension convergence
65e34e4 test(parity): B-I6 — schema extension atomic rejection
c67c90a test(parity): B-I1 — cross-backend byte identity via 20-field widening
f4097e8 test(modelcache): B-I8 stronger assertion — post-extend Get returns new schema
354981c test(sqlite): rejection atomicity (B-I6) + fold savepoint-equivalence (B-I2)
71174ca test(sqlite): deterministic retry-exhaustion test (B-I7)
9c450c7 feat(sqlite): SQLITE_BUSY transparent retry (B-I7) + ctx cancellation
95fa7a2 docs(sqlite): correct Unlock-asymmetry test comment
98390a2 test(sqlite): upgrade-path from pre-B deployment
3f816a2 docs(sqlite): clarify SchemaSavepointInterval=0 semantics
e611115 feat(sqlite): savepoint triggering (interval + on-lock) [B-I3/B-I4]
5dba22c feat(sqlite): convert ExtendSchema from apply-in-place to log-based
8b9bd92 feat(sqlite): add foldLocked + lastSavepointSeq (B-I1 infrastructure)
10de5f1 docs: Phase-4 handoff state (this doc, pre-update)
3a6addd test(postgres): B-I6 — rejection at savepoint boundary leaves no row
c0118a1 test(postgres): B-I2 — fold across savepoint boundary byte-identical
1e7bced test(postgres): ctx cancellation returns ctx.Err(), not ErrRetryExhausted
94f369a test(postgres): B-I7 — commutative-append convergence
... [Phase 1-3 commits below]
```

Full range: from `a473163` (first B commit — spec rev 1) through `6427fa3` (final chore: cyoda-go-spi bump). All pushed to `origin/feat/subproject-b-persistence`.

### Notable non-plan commits you should know about

The three `fix(...)` commits (`2b43009`, `f4a7728`, `c965f23`) are scope expansion driven by the Task 27 property test. They are SMALL, SURGICAL fixes — the largest is `c965f23` with the `classifyGetErr` helper added to `internal/domain/model/service.go` covering 6 call sites plus 3 inline fixes in `internal/domain/entity/service.go`. They are Gate 6 responses ("resolve, don't defer") that the user explicitly approved ("We're not going to go forward with bugs like that").

---

## cyoda-go-cassandra: PENDING (15 tasks)

### Location

```
/Users/paul/go-projects/cyoda-light/cyoda-go-cassandra/.worktrees/subproject-b-persistence/
```

Branch: `feat/subproject-b-persistence` (local only, not yet pushed).

### Plan

`docs/superpowers/plans/2026-04-22-data-ingestion-qa-subproject-b-cassandra.md` (1540 lines, rev 3).

Spec: `docs/superpowers/specs/2026-04-22-data-ingestion-qa-subproject-b-cassandra-design.md` (rev 3).

### Task list

| Task | Title | Phase |
|---|---|---|
| 1 | Bump dependencies — cyoda-go-spi v0.6.0 + cyoda-go plugin modules | Setup |
| 2 | Migration — add `model_schema_extensions` table | Setup |
| 3 | Cassandra Config — `SchemaSavepointInterval` + `SchemaExtendMaxRetries` | Setup |
| 4 | ExtendSchema — append-only delta row via LWT (Lightweight Transaction) | Core impl |
| 5 | Fold-on-read — update Get to invoke `foldLocked` | Core impl |
| 6 | Savepoint-on-size-threshold via LoggedBatch | Core impl |
| 7 | Save-on-lock via LoggedBatch | Core impl |
| 8 | B-I7 — LWT retry convergence test | Tests |
| 9 | B-I6 — rejection leaves no log row | Tests |
| 10 | B-I2 — savepoint transparency | Tests |
| 11 | B-I8 — local cache invalidation | Tests |
| 12 | ApplyFunc wiring in the plugin bootstrap | Wiring |
| 13 | Consume cyoda-go parity registry entries (via go.mod bump) | Wiring |
| 14 | Documentation — `CASSANDRA_BACKEND_DESIGN.md` update | Docs |
| 15 | Gate 5 — Full verification | Verify |

### Cassandra-specific notes

- **LWT (Lightweight Transaction) IF NOT EXISTS** is used for delta insert collision detection (Cassandra has no native auto-increment; Paxos-based CAS detects conflicts).
- **LoggedBatch** provides atomicity for save-on-lock and save-on-size-threshold (delta+savepoint written atomically). Cassandra has no ACID transactions.
- **HLC-based seq allocation** instead of auto-increment BIGSERIAL.
- **Testcontainers-go** bootstraps a real Cassandra container for tests — needs Docker.
- The Cassandra plugin is in a separate repo with its own `go.mod`. Its dependency on cyoda-go is via Go module path `github.com/cyoda-platform/cyoda-go`.

### Critical: Task 13 inherits the parity tests automatically

Once Task 13 bumps cyoda-go to the latest version (after merge to cyoda-go `main`), the parity registry's 5 named entries + property harness + budget meta-test are picked up **without code changes** in the Cassandra plugin. The Cassandra fixture just registers itself with the parity runner, and all cyoda-go parity entries run against it. This is the cross-plugin verification pattern.

**This means Cassandra's Task 13 will transitively verify B-I1 through B-I8 against a real Cassandra backend.** If any bug surfaces, it's likely plugin-local (LWT retry budget, HLC skew, LoggedBatch ordering) — fix Cassandra-side only.

---

## Key references

### Specs

- **Overview:** `docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md` — §6 invariant table is now populated with B-I1..B-I8.
- **cyoda-go B design:** `docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-b-design.md` (rev 3).
- **Cassandra design:** `cyoda-go-cassandra/.worktrees/subproject-b-persistence/docs/superpowers/specs/2026-04-22-data-ingestion-qa-subproject-b-cassandra-design.md` (rev 3).

### Plans

- **cyoda-go (this repo):** `docs/superpowers/plans/2026-04-22-data-ingestion-qa-subproject-b.md` (3298 lines, 31 tasks) — all tasks done.
- **Cassandra:** `cyoda-go-cassandra/.worktrees/subproject-b-persistence/docs/superpowers/plans/2026-04-22-data-ingestion-qa-subproject-b-cassandra.md` (1540 lines, 15 tasks).

### Relevant project memory (Claude auto-memory)

Already contains (at `~/.claude/projects/-Users-paul-go-projects-cyoda-light-cyoda-go/memory/`):
- `feedback_cross_plugin_design_verification.md` — dispatch Explore agents per plugin BEFORE writing per-plugin spec sections
- `feedback_race_testing_discipline.md` — race detector only at end-of-deliverable
- `feedback_gate6_no_followups.md` — "file a follow-up issue" is never valid; fix now or surface
- `feedback_plugin_submodule_tests.md` — each plugin has its own go.mod; run per-plugin explicitly
- `feedback_worktree_before_plan.md` — worktree BEFORE brainstorming/plan
- `feedback_git_push_credential.md` — use `git -c "credential.helper=!f() { echo username=x-access-token; echo password=$GH_TOKEN; }; f" push ...` in sandbox

### Plan defects discovered (apply to Cassandra too)

Watch out for the same defect patterns in the Cassandra plan, based on what cyoda-go Phase 7 surfaced:

1. **Wrong client method names** — plan may reference `c.GetModelSchema(...)`. The real client uses `c.ExportModel(t, "SIMPLE_VIEW", name, version)`. NO `GetModelSchema` exists.
2. **Wrong enum values** — plan may use `"ArrayLength"`. Real enum is SCREAMING_SNAKE_CASE: `"ARRAY_LENGTH"`, `"STRUCTURAL"`, `"TYPE"`, `"ARRAY_ELEMENTS"`.
3. **Wrong oracle helper name** — plan may call `expectedFoldFromBodies(t, bodies)`. Real function is `expectedSimpleViewFromBodies(bodies, currentState) ([]byte, error)`, and the separate helper `expectedSimpleViewFromExtensions(extensions []any, currentState string) ([]byte, error)` exists for the property harness path.
4. **Missing `SetChangeLevel("STRUCTURAL")`** — plan test flows may omit this. Without it, `CreateEntity` rejects schema-widening bodies. Insert it after `LockModel`.
5. **Missing `ImportWorkflow` may or may not be needed** — Cassandra plan should mirror what worked in cyoda-go parity tests: most test flows don't need it.
6. **`fx.factory.SetApplyFunc(fn)` panics** on re-entry (fixture already installs one). Use `fx.store.applyFunc = fn` directly — matches the established cyoda-go pattern.
7. **BackendFixture has no `Name()` method** — use `t.Name()` in error messages.

### Test fixture naming convention

- `plugins/postgres/model_extensions_internal_test.go` — postgres reference
- `plugins/sqlite/model_extensions_internal_test.go` — sqlite reference
- For Cassandra, follow the same naming: internal-package test file for unexported-method access + `newCassandraFixture`/`newCassandraFixtureWithInterval` helpers mirroring sqlite's pattern.

---

## How to resume

### In a fresh session, read this first

1. This handoff doc.
2. The Cassandra plan: `docs/superpowers/plans/2026-04-22-data-ingestion-qa-subproject-b-cassandra.md` at the Cassandra worktree.
3. The Cassandra spec (rev 3): `docs/superpowers/specs/2026-04-22-data-ingestion-qa-subproject-b-cassandra-design.md` at the Cassandra worktree.

Do NOT read the conversation transcript — this doc is the authoritative resume state.

### Verify cyoda-go state

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git log --oneline -3   # should show 6427fa3 at top (chore: bump cyoda-go-spi to v0.6.0)
git status             # should be clean, tracking origin
```

### Start Cassandra work

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-cassandra/.worktrees/subproject-b-persistence
git log --oneline -5   # verify worktree state
git status
```

The Cassandra worktree was prepared ahead of time. It has the plan + spec committed. Start at Task 1.

### Workflow

Follow the same pattern used for cyoda-go:
1. Use `superpowers:subagent-driven-development` — dispatch one implementer subagent per task.
2. Per task: implementer writes test → runs RED → implements → runs GREEN → commits.
3. Spec reviewer + code quality reviewer for non-trivial tasks; inline review for test-only additions.
4. Mark TodoWrite tasks complete as you go.
5. Push commits incrementally (use the GH_TOKEN PAT pattern from memory).

### Estimated effort

- **Setup (Tasks 1-3):** ~1 hour — mechanical
- **Core impl (Tasks 4-7):** ~4-6 hours — LWT + LoggedBatch design is the hard part
- **Tests (Tasks 8-11):** ~2 hours — parallels cyoda-go patterns
- **Wiring (Tasks 12-13):** ~1 hour — mechanical, but Task 13 may need care with go.mod version pinning
- **Docs + Gate 5 (Tasks 14-15):** ~1 hour

Total: ~9-11 hours in a focused session. Can be done in a single session if Docker is available for the Cassandra testcontainer.

---

## Open questions for the resumer

1. **Task 1 go.mod pinning:** Cassandra's Task 1 needs to bump `cyoda-go` to a version that includes the B changes. Since `feat/subproject-b-persistence` is NOT yet merged to cyoda-go's `main`, Task 1 may need to:
   - Use a `replace` directive pointing at the local worktree (dev-mode), OR
   - Wait until cyoda-go's PR is merged and a new version is tagged.
   
   Decide which approach fits the current state of the cyoda-go PR. The plan (rev 3) proposes starting with `replace` for Tasks 4-12 and switching to published versions for Task 13.

2. **Cassandra container startup cost** for Gate 5 is ~30s per run. Budget accordingly.

3. **cyoda-go PR status:** the branch `feat/subproject-b-persistence` has NOT yet been opened as a PR. When to PR is a user decision — could be before or after Cassandra. Ask.

## Summary

**cyoda-go: DONE.** Branch `feat/subproject-b-persistence` on origin, ready for PR review whenever the user decides.

**cyoda-go-cassandra: 0/15 tasks.** Next session's focus.

**No blockers known.** All cross-repo coordination artifacts (cyoda-go-spi v0.6.0, parity registry, invariant spec) are in place.
