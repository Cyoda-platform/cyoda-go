# External API Scenario Suite — Tranche 2 Design

- **Issue:** [#119](https://github.com/Cyoda-platform/cyoda-go/issues/119) (tranche 2 of 5)
- **Date:** 2026-04-25
- **Target branch:** `release/v0.6.3`
- **Predecessor:** Tranche 1 (#118) merged as commit `6164b82` — establishes HTTPDriver, errorcontract, dictionary-mapping, parity.Register pattern.

## 1. Purpose

Implement the next four YAML files of cyoda-cloud's External API Scenario
Dictionary against cyoda-go: `02-change-level-governance` (7 scenarios),
`05-entity-update` (6), `07-point-in-time-and-changelog` (5),
`12-negative-validation` (10). 28 new scenarios total, plus a retroactive
revision of tranche-1's `01/07` under the new error-code discipline.

File 12 is the first heavy user of the `errorcontract.Match` matcher;
this tranche introduces the **discover-and-compare** discipline that
governs all subsequent negative-path assertions.

## 2. Scope

### 2.1 In scope

- 28 new `Run*` parity tests across four files (02 / 05 / 07 / 12).
- Driver vocabulary expansion (~8 helpers, add-as-needed).
- Client `*Raw` helpers (~5, add-as-needed) for negative-path body capture.
- Discover-and-compare error-code discipline applied to every negative
  scenario in this tranche, with `t.Skip` + tracking issue for any
  `worse` divergences.
- Retroactive revision of `01/07` from tranche 1 under the same rubric.
- Updates to `e2e/externalapi/dictionary-mapping.md` flipping all
  `pending:tranche-2` rows to status-of-record.

### 2.2 Out of scope

- Files 02, 05, 07, 12 scenarios that overlap with #124 (delete-by-
  condition + pointInTime) — recorded as `gap_on_our_side`.
- Tranche 3+ work (#120–#122).
- Server-side fixes for any `worse`-class divergences discovered —
  filed as standalone issues (target v0.7.0), not implemented here.

## 3. Architecture (delta from tranche 1)

No new infrastructure. Reuses verbatim:

- `e2e/externalapi/driver/Driver` (both constructors)
- `e2e/externalapi/errorcontract.Match` + `ExpectedError`
- `e2e/parity/registry.go:Register(...)` runtime helper
- `e2e/externalapi/dictionary-mapping.md`
- Per-backend blank-import discipline (memory/sqlite/postgres in tree;
  cyoda-go-cassandra coordination tracked in
  Cyoda-platform/cyoda-go-cassandra#34)

New files:

```
e2e/parity/externalapi/
├── change_level_governance.go    # file 02 — 7 Run* + init() Register
├── entity_update.go              # file 05 — 6 Run* + init() Register
├── point_in_time.go              # file 07 — 5 Run* + init() Register
└── negative_validation.go        # file 12 — 10 Run* + init() Register
```

## 4. Components

### 4.1 Driver vocabulary additions

All thin pass-throughs to existing `e2e/parity/client.Client` methods.
Driver layer adds the dictionary-vocabulary names.

Actual shipped surface (reconciled after implementation — see note below):

| Driver method | Underlying client method | Source file |
|---|---|---|
| `SetChangeLevel(name, version, level string)` | `c.SetChangeLevel(t, ...)` | file 02 |
| `UpdateEntity(id uuid.UUID, transition, body string)` | `c.UpdateEntity(t, ...)` | file 05 |
| `UpdateEntityData(id uuid.UUID, body string)` | `c.UpdateEntityData(t, ...)` | file 05 (loopback) |
| `GetEntityAt(id uuid.UUID, pointInTime time.Time)` | `c.GetEntityAt(t, ...)` | file 07 |
| `GetEntityChanges(id uuid.UUID)` | `c.GetEntityChanges(t, ...)` | file 07 |
| `SetChangeLevelRaw(name, version, level string)` | new client `*Raw` | file 12 (12/03) |
| `ImportModelRaw(name, version, sample string)` | new client `*Raw` | file 12 (kept available for future) |
| `UpdateEntityRaw(id, transition, body string)` | new client `*Raw` | file 12 (12/08) |
| `GetEntityChangesRaw(id uuid.UUID)` | new client `*Raw` | file 12 (12/06), 07/05 |
| `ImportWorkflowRaw(name, version, body string)` | new client `*Raw` | file 12 (12/10) |

The set of `*Raw` helpers shipped during implementation differs slightly from the design table draft:
scenarios that ended up `t.Skip`-gated (12/04, 12/07) didn't need their planned `*Raw` helpers, and
12/06 + 12/10 surfaced the need for `GetEntityChangesRaw` + `ImportWorkflowRaw` that weren't
pre-listed. The actual 5+5 set fits the actual scenarios.

### 4.2 Discover-and-compare error-code discipline

Every negative-path scenario in tranche 2 (and 01/07 retroactively)
follows this protocol:

1. **Dictionary expectation.** Read the YAML scenario's `assertions:`
   or `expected_error:` block plus the `source_test:` Kotlin reference
   at `/Users/paul/dev/cyoda/.ai/integration-tests/...`. Capture the
   cloud-side error code (the dictionary's authoritative spec).

2. **Cyoda-go observation.** Run the scenario once with the assertion
   intentionally loose (e.g. `errorcontract.Match` on `HTTPStatus` only).
   Capture cyoda-go's actual `properties.errorCode`.

3. **Classify** against the dictionary:

   | Outcome | Action |
   |---|---|
   | `equiv_or_better` — cyoda-go emits the same code or strictly more specific | Tighten assertion to cyoda-go's code. Comment: `// matches cloud's <code>` or `// stricter than cloud's <code>; propose upstream`. |
   | `worse` — cyoda-go emits a less-specific code that loses information the dictionary preserves (e.g. `CONFLICT` vs `MODEL_ALREADY_LOCKED`) | `t.Skip("pending #<N> — cyoda-go emits <X>, dictionary requires <Y>")` + file a server-side issue + mark `gap_on_our_side` in mapping with the issue number. Test body stays in place; flipping the skip is the close-the-issue checklist item. |
   | `different_naming_same_level` — cyoda-go's code is at the same semantic level but uses different naming | Assert on cyoda-go's code. Comment: `// cyoda-go: <code>; cloud (per dictionary): <code>; semantically equivalent — reconcile in tranche-5 cloud smoke`. |

4. **Mapping update.** Each row in `dictionary-mapping.md` records the
   classification. `gap_on_our_side` rows always link to the tracking
   issue.

This builds a per-scenario catalogue of cyoda-go ↔ dictionary
divergences as a side effect of implementation.

### 4.3 01/07 retroactive revision

`01/07 lock-twice-is-rejected` currently asserts
`ErrorCode: "CONFLICT"`. Under discover-and-compare this is the
`worse` case — cyoda-go's generic `CONFLICT` (from `common.Conflict()`
in `internal/common/errors.go`) discards the specific failure mode
(`MODEL_ALREADY_LOCKED` per the source Kotlin test) that the
dictionary preserves. Note: this same constructor's
unconditional-retryable behaviour is the subject of #126.

Tranche-2 actions for 01/07:

- File a server-side issue against
  `internal/domain/model/service.go:221` to emit a more specific code
  on relock attempts. Target v0.7.0 (alongside #124 and #126 — the
  three lockstep `release/v0.7.0` items).
- Convert `RunExternalAPI_01_07_LockTwiceRejected` to begin with
  `t.Skip("pending #<N>")`. Test body stays — `LockModelRaw` and
  `errorcontract.Match` remain wired. Removing the skip is the close-
  the-issue checklist item.
- Update mapping row from
  `new:RunExternalAPI_01_07_LockTwiceRejected` to
  `gap_on_our_side` with the issue number.

### 4.4 Per-file scope notes

#### File 02 — change-level governance (7 scenarios)

Mostly happy paths exercising `setChangeLevel` STRUCTURAL/NULL/TYPE
transitions, idempotent re-imports, and the cross-cutting "updated
schema on unlocked then lock" lifecycle. The
"concurrent-extend-30-versions" stress scenario is the most novel;
bound to N=5–10 per tranche-1 precedent to keep test runtime
reasonable. Expected gap count: 0–1.

#### File 05 — entity update (6 scenarios)

Per-entity update via transition + loopback + nested-JSON. Tranche 1
already exercises the batch path through 04/02
`UpdateCollectionAge`; file 05 is the per-entity equivalents. Driver
gains `UpdateEntity` and `UpdateEntityData` (loopback). Expected gap
count: 0.

#### File 07 — pointInTime + changelog (5 scenarios)

Basic `pointInTime` reads (already exercised in tranche 1's 06/06
setup, where the helper works). New scenarios: `transactionId`-scoped
reads, `/entity/{id}/changes` history. One scenario ("delete by
condition at point-in-time") is the #124 gap → `gap_on_our_side`,
no implementation. Expected gap count: 1 (the #124 overlap).

#### File 12 — negative validation (10 scenarios)

The discover-and-compare workhorse. Each scenario likely yields one
of: green test using `errorcontract.Match` (probably 5–7), or
`t.Skip` + new issue (probably 3–5). Realistic outlook is ~70% green,
~30% gaps.

### 4.5 Expected gap budget

| Category | Estimate |
|---|---|
| `equiv_or_better` (green) | ~22 of 28 |
| `worse` → `t.Skip` + new issue | ~3–5 |
| `gap_on_our_side` (no test) — overlap with #124 / similar | ~1–2 |
| Retroactive: 01/07 → t.Skip | +1 |

Net mergeable count: ~22–24 green tranche-2 scenarios + 4–7 skipped
with tracking issues filed.

## 5. Error handling

- **Positive path:** as tranche 1. Driver methods return `error`,
  `t.Fatalf` on non-nil. 409-retry handled by parity client.
- **Negative path:** uses `*Raw` helper → `errorcontract.Match`.
  Discover-and-compare classifies the assertion strength.
- **Skipped scenarios:** `t.Skip` carries the tracking issue number
  in the message so the test runner output points at the gap.
- **Security (Gate 3):** the matcher echoes only typed fields, never
  raw body bytes. The `bodyPreview` helper from tranche 1 is
  available if any new diagnostic message needs to log a server
  body fragment.

## 6. Testing strategy

- TDD per Driver helper, per `*Raw` client helper, per `Run*`.
- Each scenario file lands as one commit (per tranche-1 precedent).
- `make test-all` green at end of tranche.
- `go test -race ./...` one-shot before PR per `.claude/rules/race-testing.md`.

## 7. Acceptance

From issue #119 plus this design:

- `go test ./e2e/parity/... -v` green across memory / sqlite / postgres
  (skipped scenarios count as green).
- Every new negative scenario asserts via `errorcontract.Match`, never
  ad-hoc regex on error message strings.
- `dictionary-mapping.md` fully up to date for files 02 / 05 / 07 / 12
  with classification per scenario.
- Every `t.Skip`-marked scenario references a tracked issue.
- 01/07 revisited under the new rubric.

## 8. Workflow

Per `CLAUDE.md` feature workflow:

1. Worktree on `feat/issue-119-external-api-tranche2` off
   `release/v0.6.3` ✓ done
2. Brainstorming ✓ done
3. This design doc
4. `superpowers:writing-plans` → executable plan
5. `superpowers:subagent-driven-development` → TDD implementation
6. `superpowers:verification-before-completion`
7. `superpowers:requesting-code-review`
8. `antigravity-bundle-security-developer:cc-skill-security-review`
9. PR targeting `release/v0.6.3` with `Closes #119` in body
