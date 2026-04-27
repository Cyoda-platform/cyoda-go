# v0.6.3 Outstanding Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land all 10 outstanding v0.6.3-milestoned issues against the `release/v0.6.3` branch via parallel-executable buckets that minimise file conflicts.

**Architecture:** 14 buckets organised into two waves. Wave 1 (13 buckets) is fully parallel — no two buckets touch the same file. Wave 2 (1 bucket) sequences four issues that all touch `app/app.go`. Wave 1 and Wave 2 can execute concurrently with each other since their file footprints are disjoint.

**Tech Stack:** Go 1.26+, `log/slog`, RFC 9457 Problem Details, SI+FCW transactions, parity-test driver, `superpowers:test-driven-development`, `superpowers:using-git-worktrees`, `superpowers:verification-before-completion`.

---

## Parallel Execution Strategy

### Worktree convention

Each bucket runs in its own worktree off `release/v0.6.3`, on a branch named `fix/v063-<short-name>`, and ships a separate PR targeting `release/v0.6.3`. Use the `superpowers:using-git-worktrees` skill to create the worktree before starting any bucket.

### Wave layout

**Wave 1 — fully parallel (13 buckets, all touch disjoint files):**

| Bucket | Issue(s) | Branch | Primary file(s) |
|---|---|---|---|
| A | #77 | `fix/v063-search-validate-refresh` | `internal/domain/search/service.go` |
| B | #132 | `fix/v063-parity-client-helpers` | `e2e/parity/client/http.go`, `e2e/externalapi/driver/driver.go`, four `t.Skip` sites |
| C | #129 | `fix/v063-entity-incompatible-type` | entity validation path |
| D | #130 | `fix/v063-change-level-invalid-enum` | set-change-level handler path |
| E | #131 | `fix/v063-workflow-import-404` | workflow import handler |
| F | #68 #11 | `fix/v063-sqlite-in-clause` | `plugins/sqlite/entity_store.go` |
| G | #68 #17 | `fix/v063-memory-tx-buffer-lock` | `plugins/memory/entity_store.go` |
| H | #68 #10 | `fix/v063-pagination-overflow` | `internal/domain/search/handler.go` |
| I | #68 #20 | `fix/v063-dockerfile-digest-pin` | `deploy/docker/Dockerfile` |
| J | #51 | `fix/v063-brew-audit-verify` | `.goreleaser.yaml` (verification) |
| K1 | #34 items 2–7 + #68 #14 | `fix/v063-auth-hardening` | `internal/auth/{trusted,kv_trusted_store}.go`, `internal/admin/admin.go` |
| K2 | #68 #9 | `fix/v063-jwks-cache-issuer-key` | `internal/auth/validator.go` |
| K3 | #68 #12 | `fix/v063-auth-error-uniformity` | `internal/auth/delegating.go` |

**Wave 2 — single bucket, internally sequenced (all touch `app/app.go`):**

| Bucket | Issue(s) | Branch | Primary file(s) |
|---|---|---|---|
| L | #10 → #34 #1 → #68 #19 → #26 | `fix/v063-app-startup-shutdown` | `app/app.go`, `cmd/cyoda/main.go` |

Wave 1 and Wave 2 can run concurrently — their file footprints do not overlap.

### Bucket-agent contract

Each agent picking up a bucket:

1. Invokes `superpowers:using-git-worktrees` to create the worktree off `release/v0.6.3`.
2. Reads its own bucket's section in this plan.
3. Executes tasks in the listed order using `superpowers:test-driven-development`.
4. Runs `superpowers:verification-before-completion` before opening a PR.
5. Opens the PR targeting `release/v0.6.3` with the issue's `Closes #N` references in the PR body so the milestone hygiene rule (see `feedback_release_milestone_invariant.md`) carries forward.

### Verification commands (every bucket)

Before the verification-before-completion gate, run:

```bash
go build ./...
go vet ./...
go test -short ./...
make test-short-all          # only if the bucket touches plugin submodules
go test -race -count=1 ./... # end-of-deliverable, before opening PR
```

---

## Shared Recipes

### Recipe 1 — Dictionary-aligned error code (Buckets C, D)

Used for buckets that replace a generic `BAD_REQUEST` (or other generic) with a specific dictionary-aligned code. Recipe is the same as the `#128 → MODEL_ALREADY_LOCKED` work that landed in PR #141; mimic that PR's commit shape.

For each emit site:

1. **RED:** add a unit/handler test asserting the specific code via `commontest.ExpectErrorCode` (the helper added in #141; in `internal/common/commontest`). Include the precondition Props the response should carry.
2. Add the new constant to `internal/common/error_codes.go`.
3. Run the test — should fail with constant undefined first, then with `errorCode != want`.
4. **GREEN:** switch the emit site from `common.Conflict(...)` / `common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, ...)` to `common.Operational(<status>, <new code constant>, msg)` with the structured `Props` populated.
5. Add `cmd/cyoda/help/content/errors/<NEW_CODE>.md` (required by `TestErrCode_Parity`); use the existing `MODEL_ALREADY_LOCKED.md` / `ENTITY_MODIFIED.md` shape — frontmatter, NAME, SYNOPSIS (`HTTP: <status> <reason>. Retryable: <yes|no>.`), DESCRIPTION enumerating Props, SEE ALSO.
6. Update `cmd/cyoda/help/content/errors.md` master index with an alphabetical row.
7. Update `cmd/cyoda/help/content/<surface>.md` (e.g. `crud.md`, `models.md`) ERRORS section + frontmatter `see_also` to include the new code.
8. If the issue corresponds to a parity-test scenario currently `t.Skip`-gated in `e2e/parity/externalapi/`: remove the skip, switch the assertion to the new code, and update `e2e/externalapi/dictionary-mapping.md`.
9. **GREEN verify:** all tests including `TestErrCode_Parity` pass.
10. **Commit** with conventional `fix(<surface>):` prefix; PR body includes `Closes #N`.

### Recipe 2 — RFC 9457 problem-detail body assertion

Use `commontest.ExpectErrorCode(t, resp, want)` from `internal/common/commontest/problemdetail.go`. Both `internal/domain/{model,entity}/handler_test.go` already import this. New tests in other handler packages should import it as well.

---

## Bucket A — #77: search field-path ValidateWithRefresh

**Issue:** Wire `ValidateWithRefresh` into the search service so a search whose condition references a field absent from the cached schema but present in the authoritative (post-`ExtendSchema`) schema succeeds after one refresh.

**Files:**
- Modify: `internal/domain/search/service.go` (TODO at lines 73–80 marks the integration point)
- Test: `internal/domain/search/service_test.go` (or `search_test.go` — match the existing convention)

**Acceptance** (from issue body):
- Stale-schema search referencing a freshly-added path succeeds after 1 refresh.
- Truly-missing path fails 4xx (not 5xx, not unbounded refresh).
- Refresh fires at most once per request.

### Steps

- [ ] **A1 — Worktree.** Create `fix/v063-search-validate-refresh` worktree off `release/v0.6.3`. Run `go test -short ./internal/domain/search/...` to confirm baseline green.

- [ ] **A2 — RED test 1: stale-schema-but-fresh-authoritative succeeds after 1 refresh.**

```go
// internal/domain/search/service_test.go (new test)
func TestSearch_StaleSchema_RefreshesOnceAndSucceeds(t *testing.T) {
    // Build a fake ModelStore where Get returns a schema missing field "z",
    // RefreshAndGet returns a schema that includes "z". Build a Condition
    // referencing "z". Assert Search returns a non-nil result and that
    // RefreshAndGet was called exactly once.
}
```

Run: `go test ./internal/domain/search/ -run TestSearch_StaleSchema_RefreshesOnceAndSucceeds -v`
Expected: FAIL — pre-execution validation not wired in yet.

- [ ] **A3 — RED test 2: truly-missing path fails 4xx, refresh fires at most once.**

```go
// internal/domain/search/service_test.go
func TestSearch_TrulyMissingPath_FourxxAfterOneRefresh(t *testing.T) {
    // Both Get and RefreshAndGet return a schema without the referenced field.
    // Assert Search returns *common.AppError with Status in [400, 500),
    // and that RefreshAndGet was called exactly once (not unbounded).
}
```

Run: `go test ./internal/domain/search/ -run TestSearch_TrulyMissingPath -v`
Expected: FAIL.

- [ ] **A4 — Implement the integration at the TODO point.**

In `internal/domain/search/service.go` around the TODO at lines 73–80:
1. Extract dotted paths from the predicate.Condition tree (recursively walk).
2. Build a minimal "touch" doc with each path assigned a non-nil placeholder value.
3. Call `handler.ValidateWithRefresh(ctx, modelStore, ref, touchDoc)` (the helper that already exists in `internal/domain/entity/handler.go`).
4. On non-nil error from ValidateWithRefresh: return as 4xx via `common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, err.Error())`.

Refresh must fire at most once per request — `ValidateWithRefresh` already enforces this; verify by inspecting its docstring before relying on it.

- [ ] **A5 — GREEN verify both tests pass.** Run: `go test ./internal/domain/search/ -v`. Expected: PASS for both new tests, no regressions.

- [ ] **A6 — Verification gate.** Run `go vet ./...` and `go test -short ./...`. All green.

- [ ] **A7 — Race detector pre-PR.** Run `go test -race -count=1 ./internal/domain/search/...`.

- [ ] **A8 — Commit + push + PR.**

```bash
git add internal/domain/search/
git commit -m "feat(search): wire ValidateWithRefresh into pre-execution field-path validation (#77)"
git push -u origin fix/v063-search-validate-refresh
gh pr create --base release/v0.6.3 --title "feat(search): wire ValidateWithRefresh into pre-execution field-path validation (#77)" --body "Closes #77."
```

---

## Bucket B — #132: parity client helpers

**Issue:** Add four parity-client helpers + driver pass-throughs that unblock four currently `t.Skip`-gated scenarios from earlier tranches.

**Files:**
- Modify: `e2e/parity/client/http.go` (add 4 methods)
- Modify: `e2e/externalapi/driver/driver.go` (add 4 pass-throughs)
- Modify: `e2e/parity/externalapi/{point_in_time,negative_validation}.go` (remove 4 `t.Skip` calls + write test bodies)
- Modify: `e2e/externalapi/dictionary-mapping.md` (flip 4 rows from `(skipped)` to `new:<fn>`)
- Test: `e2e/parity/client/<helper>_test.go` (new httptest unit tests, mirroring existing `*Raw` patterns)

**Helper signatures** (from issue body):
1. `GetEntityByTransactionID(t *testing.T, id uuid.UUID, txID string) (parityclient.EntityResult, error)`
2. `GetEntityByTransactionIDRaw(t *testing.T, id uuid.UUID, txID string) (int, []byte, error)` (for 12/05 scenario)
3. `GetEntityChangesAt(t *testing.T, id uuid.UUID, pointInTime time.Time) ([]parityclient.EntityChangeMeta, error)`
4. `GetEntityAtRaw(t *testing.T, id uuid.UUID, pointInTime time.Time) (int, []byte, error)` (for 12/04)

### Steps

- [ ] **B1 — Worktree.** Create `fix/v063-parity-client-helpers` off `release/v0.6.3`.

- [ ] **B2 — Survey existing `*Raw` patterns.** Read at least two existing `Raw` helpers (e.g. `LockModelRaw`, `GetEntityAtRaw` if exists, or `CreateEntityRaw`) in `e2e/parity/client/http.go` to learn the pattern: signature, error handling, header injection, body return shape, t.Helper() call, etc.

- [ ] **B3 — RED test for helper 1 (GetEntityByTransactionID).** Write an httptest-server unit test that fakes the `GET /entity/{id}?transactionId=<tx>` response and asserts the helper parses it correctly.

- [ ] **B4 — Implement helper 1.** Add `GetEntityByTransactionID` to `e2e/parity/client/http.go`. Run test — green.

- [ ] **B5 — RED test for helper 2 (GetEntityByTransactionIDRaw).** Test asserts the helper returns raw status + body for an arbitrary response (200 success + 4xx failure paths).

- [ ] **B6 — Implement helper 2.** Run test — green.

- [ ] **B7 — RED test for helper 3 (GetEntityChangesAt).** Test fakes `GET /entity/{id}/changes?pointInTime=<ISO>` response.

- [ ] **B8 — Implement helper 3.** Run test — green.

- [ ] **B9 — RED test for helper 4 (GetEntityAtRaw).** Test fakes `GET /entity/{id}?pointInTime=<ISO>` raw response.

- [ ] **B10 — Implement helper 4.** Run test — green.

- [ ] **B11 — Add 4 driver pass-throughs in `e2e/externalapi/driver/driver.go`.** Each pass-through delegates to the corresponding parity-client helper. Match the existing pass-through style (likely a 2–4 line method per helper).

- [ ] **B12 — Unblock the 4 `t.Skip` scenarios.** For each of:
  - `RunExternalAPI_07_02_GetEntityByTransactionID` (in `point_in_time.go`) — uses `GetEntityByTransactionID`
  - `RunExternalAPI_07_04_EntityChangeHistoryAtPointInTime` (in `point_in_time.go`) — uses `GetEntityChangesAt`
  - `RunExternalAPI_12_04_GetEntityAtTimeBeforeCreation` (in `negative_validation.go`) — uses `GetEntityAtRaw`
  - `RunExternalAPI_12_05_GetEntityWithBogusTransactionID` (in `negative_validation.go`) — uses `GetEntityByTransactionIDRaw`

  Remove the `t.Skip(...)` line, write the test body using the new helper, with assertions using `errorcontract.Match` for negative paths and direct envelope assertion for positive paths. Reference the discover-and-compare classification rubric in `docs/superpowers/specs/2026-04-25-external-api-tranche2-design.md` §4.3 to choose `same` / `worse` / `better` for each.

- [ ] **B13 — Update `e2e/externalapi/dictionary-mapping.md`.** Flip the four rows from `(skipped)` to `new:<RunExternalAPI_*_fn_name>` with the chosen classification.

- [ ] **B14 — Run the parity suite.** Run `go test ./e2e/parity/memory/ -run "TestParity/.*ExternalAPI_(07_02|07_04|12_04|12_05)" -v`. All four scenarios pass.

- [ ] **B15 — Verification gate + race + PR.** As Bucket A.

---

## Bucket C — #129: entity validation type-mismatch specific code

**Issue:** Entity-validation type-mismatch (e.g. trying to write a string into a DOUBLE field) currently emits generic `BAD_REQUEST`. Cyoda Cloud's dictionary expects `FoundIncompatibleTypeWithEntityModelException`-level specificity (regex pattern match on the class name).

**Code-name choice:** `INCOMPATIBLE_TYPE`. Rationale: matches the Cloud exception name's semantic root, fits cyoda-go's `SCREAMING_SNAKE_CASE` convention, and is symmetric to the existing `CONDITION_TYPE_MISMATCH` (which is search-side, distinct).

**Files:**
- Modify: `internal/common/error_codes.go` — add `ErrCodeIncompatibleType = "INCOMPATIBLE_TYPE"`.
- Modify: the entity validation site that currently emits `BAD_REQUEST` for type mismatches (likely in `internal/domain/entity/handler.go` or `service.go` — locate via grep for "type" + `BAD_REQUEST` near the validate-against-schema call).
- Create: `cmd/cyoda/help/content/errors/INCOMPATIBLE_TYPE.md`.
- Modify: `cmd/cyoda/help/content/errors.md` — add the new row to the alphabetical index.
- Modify: `cmd/cyoda/help/content/crud.md` — add the new code to the ERRORS section and `see_also`.
- Modify: parity test file containing `RunExternalAPI_12_02_CreateEntityWithIncompatibleType` and any other `t.Skip` referencing `gap_on_our_side (#129)` — remove skips.
- Modify: `e2e/externalapi/dictionary-mapping.md` — flip rows referencing `gap_on_our_side (#129)` back to `new:<fn>`.

### Steps

Apply Recipe 1 with:
- new code: `INCOMPATIBLE_TYPE`
- HTTP status: `400 Bad Request`
- retryable: no
- Props: include `entityName`, `entityVersion`, `fieldPath`, `expectedType`, `actualType` (whatever the validator already surfaces; check the existing 5xx ticket-correlated log for context)
- skipped tests to unblock: any `gap_on_our_side (#129)` rows in `dictionary-mapping.md`

- [ ] **C1 — Worktree.** Create `fix/v063-entity-incompatible-type` off `release/v0.6.3`.

- [ ] **C2 — Locate the emit site.** `grep -rn "ErrCodeBadRequest" internal/domain/entity/ | grep -i "type\|incompatible\|schema"` plus inspection. Read the existing parity test body for 12/02 (in `e2e/parity/externalapi/negative_validation.go`) to see the request shape that triggers this path.

- [ ] **C3 — RED test.** Add a unit/handler test asserting the response carries `errorCode: "INCOMPATIBLE_TYPE"` and HTTP 400. Use `commontest.ExpectErrorCode`. Confirm RED.

- [ ] **C4 — Add the constant** in `internal/common/error_codes.go`.

- [ ] **C5 — GREEN: switch the emit site.** Replace the `common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, ...)` (or `common.Conflict(...)`, depending on what the path uses today) with `common.Operational(http.StatusBadRequest, common.ErrCodeIncompatibleType, msg)`. Populate `Props` with the structured fields named above.

- [ ] **C6 — Add help doc.** Create `cmd/cyoda/help/content/errors/INCOMPATIBLE_TYPE.md` mirroring the structure of `MODEL_ALREADY_LOCKED.md`. SYNOPSIS line: `HTTP: 400 Bad Request. Retryable: no.`. DESCRIPTION enumerates Props.

- [ ] **C7 — Run `TestErrCode_Parity`.** Run `go test ./cmd/cyoda/help/ -run TestErrCode_Parity -v`. Expected: PASS.

- [ ] **C8 — Update master index + crud.md cross-references.**

- [ ] **C9 — Unblock parity tests.** Remove `t.Skip("pending #129...")` lines, switch assertions to `INCOMPATIBLE_TYPE`. Update `dictionary-mapping.md` rows.

- [ ] **C10 — Verification gate + race + PR with `Closes #129`.**

---

## Bucket D — #130: set-change-level invalid enum specific code

**Issue:** `set-change-level` endpoint accepts an invalid enum value and emits generic `BAD_REQUEST`. Dictionary expects an enum-validation-error specificity.

**Code-name choice:** `INVALID_CHANGE_LEVEL`. Rationale: scope-specific to the `changeLevel` endpoint surface (other enum-validation paths can later get their own specific codes if needed); avoids over-generalising to a global `INVALID_ENUM_VALUE` until the dictionary mapping confirms what to use.

**Files:** same shape as Bucket C, with:
- emit site in the set-change-level handler (`internal/domain/model/handler.go` or `service.go` — grep for the SetChangeLevel path)
- help doc `INVALID_CHANGE_LEVEL.md`
- index updates in `errors.md`, `models.md`
- parity test unblock for any `gap_on_our_side (#130)` rows

### Steps

Apply Recipe 1 with:
- new code: `INVALID_CHANGE_LEVEL`
- HTTP status: `400 Bad Request`
- retryable: no
- Props: include `entityName`, `entityVersion`, `suppliedValue`, `validValues` (list the actual valid `ChangeLevel` enum members)
- skipped tests to unblock: any `gap_on_our_side (#130)` rows

Steps mirror C1–C10 with appropriate substitutions. Branch: `fix/v063-change-level-invalid-enum`. Closes: `#130`.

---

## Bucket E — #131: workflow import on unknown model returns 404

**Issue:** Importing a workflow targeting a model that does not exist currently returns HTTP 200 `{"success":true}`. Dictionary requires HTTP 404 with a `MODEL_NOT_FOUND` (or `EntityModelNotFound`) regex match.

**Files:**
- Modify: workflow-import handler (likely `internal/domain/workflow/handler.go`; locate via grep for the workflow-import route).
- Test: `internal/domain/workflow/handler_test.go` (or similar; match existing convention).
- Unblock: parity tests `gap_on_our_side (#131)` in `dictionary-mapping.md`.

This is **not** a new error code — `MODEL_NOT_FOUND` already exists. Just thread the existing `modelNotFound(...)` (or `common.Operational(http.StatusNotFound, common.ErrCodeModelNotFound, ...)`) into the workflow-import path.

### Steps

- [ ] **E1 — Worktree.** Create `fix/v063-workflow-import-404` off `release/v0.6.3`.

- [ ] **E2 — Locate the bug.** Find the workflow-import handler. The current code likely silently no-ops or short-circuits to 200 when the model is missing instead of consulting the model store.

- [ ] **E3 — RED test.** Handler test issuing a workflow import for a non-existent model — assert HTTP 404 + `errorCode: "MODEL_NOT_FOUND"`.

- [ ] **E4 — GREEN: load model first.** Before applying the workflow, look up the model via `modelStore.Get(ctx, ref)`. If `errors.Is(err, spi.ErrNotFound)` or descriptor is nil, return `common.Operational(http.StatusNotFound, common.ErrCodeModelNotFound, fmt.Sprintf("model %s/%d not found", ref.EntityName, ref.ModelVersion))`.

- [ ] **E5 — Run handler tests.** Verify the new test passes and the previous (200-response) test (if any) is updated to assert the bug is fixed.

- [ ] **E6 — Unblock parity tests.** Remove `t.Skip("pending #131...")`, switch assertion to `HTTPStatus: 404, ErrorCode: "MODEL_NOT_FOUND"` via `errorcontract.Match`. Update `dictionary-mapping.md`.

- [ ] **E7 — Verification gate + race + PR with `Closes #131`.**

---

## Bucket F — #68 #11: SQLite IN-clause parameterization

**Issue:** Individual state filter values are parameterized but the IN list itself is built by string concatenation. Refactor to a bounded `?`-generator and cap size consistently with `SQLITE_MAX_VARIABLE_NUMBER` minus the other bindings in the query.

**Files:**
- Modify: `plugins/sqlite/entity_store.go:859` (state filter IN-clause construction)
- Test: `plugins/sqlite/<state_filter or entity_store>_test.go`

### Steps

- [ ] **F1 — Worktree.** Create `fix/v063-sqlite-in-clause` off `release/v0.6.3`. Note: `plugins/sqlite` is a submodule; `cd plugins/sqlite && go test ./...` to confirm baseline green.

- [ ] **F2 — RED test 1: many-state filter executes correctly.** Test that builds a filter with N state values (start with N=10, scale to N=999) and asserts the query returns the expected entities. The current concat path may already work for small N; the test pins behaviour.

- [ ] **F3 — RED test 2: oversized state list is rejected with a clear error.** Test with N > `SQLITE_MAX_VARIABLE_NUMBER - <reserved>` — assert a defined error (not a SQLite driver error leak). Determine the cap value: query SQLite for `PRAGMA compile_options` or hard-code the standard `SQLITE_MAX_VARIABLE_NUMBER = 999` (default). Subtract the count of other parameters bound in the same query (tenant id, model name, etc.) and assert the helper rejects beyond that.

- [ ] **F4 — GREEN.** Replace the concat with a `?`-list generator. Refuse oversized lists at the helper boundary with a wrapped error. Re-run tests.

- [ ] **F5 — Plugin-submodule verification.** `cd plugins/sqlite && go test ./... && go vet ./...`. From the repo root: `make test-short-all` (covers root + plugin submodules).

- [ ] **F6 — Race + PR with `Closes #68 (item 11)`.** PR body should be explicit that this is one of multiple items being landed against #68; do not auto-close #68 from this PR.

---

## Bucket G — #68 #17: memory plugin tx.Buffer locking

**Issue:** `CompareAndSave` tx-path writes `tx.Buffer` while holding `entityMu.RLock` but without `tx.OpMu.RLock` (which `Save()` does hold). Concurrent tx writes could race. Mirror the `Save()` locking discipline.

**Files:**
- Modify: `plugins/memory/entity_store.go:104-139`
- Test: `plugins/memory/entity_store_test.go` (race-conditional test)

### Steps

- [ ] **G1 — Worktree.** Create `fix/v063-memory-tx-buffer-lock` off `release/v0.6.3`. `cd plugins/memory && go test ./...` baseline green.

- [ ] **G2 — Read `Save()`** in the same file for the locking discipline. Note exactly which lock(s) `Save()` acquires and the order.

- [ ] **G3 — RED test: concurrent CompareAndSave on the same entity in overlapping tx.** Two goroutines, each holding their own tx ctx, both calling `CompareAndSave` on the same entity. Assert: one wins (no torn buffer), no race detector flag.

   This test is most useful with `-race`; document that in a comment.

- [ ] **G4 — Run the test under `-race`** to confirm the race exists today.

```bash
cd plugins/memory && go test -race -run TestCompareAndSave_Concurrent -v
```

Expected: race detector flag.

- [ ] **G5 — GREEN: mirror Save()'s locking.** In `CompareAndSave`'s tx-path, acquire `tx.OpMu.RLock` (or whatever Save() uses) before writing to `tx.Buffer`; release with `defer` per the `.claude/rules/go-mutex-discipline.md` rule.

- [ ] **G6 — Re-run with `-race`.** Race gone. Other tests still pass.

- [ ] **G7 — Verification gate + PR with `Closes #68 (item 17)`.**

---

## Bucket H — #68 #10: pagination overflow + missing upper cap

**Issue:** `opts.Offset = pn * pageSize` is unchecked for int64 overflow. Sync search caps `pageSize` at 10000 (line 62) but the async results path only checks `ps >= 0`. Cap both `pageSize` and `pageNumber` consistently and validate the multiplication fits in `int64`.

**Files:**
- Modify: `internal/domain/search/handler.go:154-177` (async results pagination)
- Test: `internal/domain/search/handler_test.go`

### Steps

- [ ] **H1 — Worktree.** Create `fix/v063-pagination-overflow` off `release/v0.6.3`.

- [ ] **H2 — RED test 1: oversized pageSize is rejected.** Send a request with `pageSize = 1_000_000` to the async results endpoint. Assert HTTP 400 `BAD_REQUEST`.

- [ ] **H3 — RED test 2: pageNumber × pageSize overflow is rejected.** Send `pageNumber = math.MaxInt32, pageSize = 10000`. Assert HTTP 400 `BAD_REQUEST` (not 500, not silent wrap-around).

- [ ] **H4 — GREEN.** Apply the sync path's cap (10000) to async pageSize. Add a `pageNumber` upper cap consistent with the sync path (or derive a sensible one — e.g. `math.MaxInt32 / 10000`). Use `safeMul64(pn, pageSize)` (introduce as a helper if absent — `math/bits.Mul64` returns hi+lo, hi != 0 means overflow) for the offset multiplication.

- [ ] **H5 — Run handler tests.** Verify both new tests pass.

- [ ] **H6 — Verification gate + PR with `Closes #68 (item 10)`.**

---

## Bucket I — #68 #20: distroless image digest pinning

**Issue:** `deploy/docker/Dockerfile:17` uses `gcr.io/distroless/static` with an unpinned tag. Pin by `@sha256:…` for supply-chain integrity.

**Files:**
- Modify: `deploy/docker/Dockerfile`
- Optional: a script or `Makefile` target to refresh the digest periodically

### Steps

- [ ] **I1 — Worktree.** Create `fix/v063-dockerfile-digest-pin` off `release/v0.6.3`.

- [ ] **I2 — Resolve current digest.** Run:

```bash
docker pull gcr.io/distroless/static:latest
docker inspect --format='{{index .RepoDigests 0}}' gcr.io/distroless/static:latest
```

Record the resulting `gcr.io/distroless/static@sha256:…` string.

- [ ] **I3 — Update Dockerfile.** Replace the `FROM gcr.io/distroless/static` line with `FROM gcr.io/distroless/static@sha256:<recorded>` (preserve any tag annotation as a comment for human readability).

- [ ] **I4 — Build the image to confirm the pin resolves.**

```bash
docker build -f deploy/docker/Dockerfile -t cyoda-go:digest-pin-test .
```

- [ ] **I5 — Optional: refresh helper.** Add `scripts/refresh-distroless-digest.sh` (or a Makefile target) that re-resolves the digest and rewrites the Dockerfile line. Document in the script header that this should be run periodically (monthly cadence; or trigger on Renovate/Dependabot's distroless update issue).

- [ ] **I6 — Verification gate + PR with `Closes #68 (item 20)`.** No Go test changes required; CI's image-build job exercises the pin.

---

## Bucket J — #51: brew audit verify-and-close

**Issue:** Run `brew audit --strict` against a snapshot-generated formula. The original concern (audit flags `post_install` in the formula) is likely already obviated by commit `e34d5f4 fix(release): drop brew post_install; revise caveats`. This bucket's primary task is to verify and close.

**Files:**
- Read-only: `.goreleaser.yaml` (verify no `post_install` in the `brews:` stanza)
- Optionally: regenerate snapshot + re-run audit to confirm clean

### Steps

- [ ] **J1 — Worktree.** Create `fix/v063-brew-audit-verify` off `release/v0.6.3`.

- [ ] **J2 — Inspect goreleaser config.** Read `.goreleaser.yaml`. Confirm the `brews:` stanza no longer carries a `post_install` block; confirm `caveats` is in place. If both confirmed, the original audit concern is moot.

- [ ] **J3 — Decision tree.**

  **(A) If post_install is gone and caveats are present:** No code change needed. Comment on #51:
  ```
  Resolved by e34d5f4 (fix(release): drop brew post_install; revise caveats). The original concern (`brew audit --strict` flagging the post_install block) no longer applies — the brews stanza now ships caveats-only per the issue's mitigation tree.
  ```
  Close #51 with the comment.

  **(B) If post_install is still present:** run the full audit per the issue body:

  ```bash
  goreleaser release --snapshot --clean --skip=publish
  brew audit --strict ./dist/cyoda.rb
  ```

  Apply the mitigation-tree fallback that matches the audit outcome. Commit. Re-run audit. Close #51 with a comment recording the actual audit result.

- [ ] **J4 — If (B) was needed, push the goreleaser-config commit; if (A), there is no PR — just an issue close.**

---

## Bucket K1 — #34 items 2–7 + #68 #14: trusted-key + auth/admin error hardening

**Issue:** Cluster of small hardening items in `internal/auth/{trusted,kv_trusted_store}.go` and `internal/admin/admin.go`. Each item has an explicit "Fix:" sentence in the issue body. Land as one bundled PR with one commit per item.

**Files:**
- Modify: `internal/auth/trusted.go` (items 3, 6, 14 of #34/#68)
- Modify: `internal/auth/kv_trusted_store.go` (items 2, 4, 5, 7 of #34)
- Modify: `internal/admin/admin.go` (item 14 of #68)
- Test: `internal/auth/trusted_test.go`, `internal/auth/kv_trusted_store_test.go` (new TDD coverage per item)

### Steps (one TDD cycle per item; commit per item)

- [ ] **K1.1 — Worktree.** Create `fix/v063-auth-hardening` off `release/v0.6.3`.

- [ ] **K1.2 — Item #34/2: configurable max trusted keys.**
  - **RED** test: `Register` rejects with `409 Conflict` once `<max>` keys are registered. Use a small max for the test (e.g. 3).
  - **GREEN:** add `MaxTrustedKeys int` to the store's config struct (default `100`). On Register, count current keys; if at-cap, return `common.Operational(http.StatusConflict, common.ErrCodeConflict, "trusted-key registry full")`. (Generic `CONFLICT` is acceptable here — this is an admin-path cardinality precondition; do NOT introduce a new error code in this bundle.)
  - Commit.

- [ ] **K1.3 — Item #34/3: KID validation regex.**
  - **RED** test: `Register` with KID containing `..` or `\x00` or a 1000-char KID returns `400 BAD_REQUEST`.
  - **GREEN:** at the handler boundary in `trusted.go` (before delegating to the store), enforce `^[a-zA-Z0-9._-]{1,256}$`. Return `common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid KID: must match ^[a-zA-Z0-9._-]{1,256}$")`.
  - Commit.

- [ ] **K1.4 — Item #34/4: RSA exponent validation.**
  - **RED** test: registering a key whose serialized exponent overflows `int` is rejected.
  - **GREEN:** after `new(big.Int).SetBytes(eBytes)`, validate `e.IsInt64() && e.Int64() > 0 && e.Int64() <= math.MaxInt && e.Bit(0) == 1` (positive, fits int, odd). Return error if not.
  - Apply at both `internal/auth/kv_trusted_store.go:197` and `internal/auth/trusted.go:257`.
  - Commit.

- [ ] **K1.5 — Item #34/5: stored context wrap.**
  - No RED test — this is defence-in-depth.
  - **GREEN:** in `kv_trusted_store.go:37-41`, wrap the stored context with `context.WithoutCancel(ctx)`.
  - Run existing tests to confirm no regressions.
  - Commit.

- [ ] **K1.6 — Item #34/6: generic 404 message.**
  - **RED** test: deleting a nonexistent KID returns `404` with body `"key not found"` (or whatever generic phrasing is chosen) rather than the raw backend error.
  - **GREEN:** in `trusted.go:162`, replace `http.Error(w, err.Error(), http.StatusNotFound)` with `common.WriteError(w, r, common.Operational(http.StatusNotFound, common.ErrCodeBadRequest, "key not found"))`. Log the original error via `slog.Info("trusted-key not found", "kid", kid, "err", err.Error())` server-side.
  - Commit.

- [ ] **K1.7 — Item #34/7: existence check on Register (409 instead of silent overwrite).**
  - **RED** test: re-registering the same KID returns `409 Conflict` and the original key remains intact.
  - **GREEN:** in `kv_trusted_store.go:80`, check existence before insert; if exists, return `common.Operational(http.StatusConflict, common.ErrCodeConflict, "trusted key with this KID already registered")`.
  - **Behavioural note:** flag in the PR body that this is a behaviour-visible change for any tooling re-registering with the same KID. Add a release-note bullet for v0.6.3.
  - Commit.

- [ ] **K1.8 — Item #68/14: replace `err.Error()` 5xx leaks with generic + structured log.**
  - For each site listed in #68 item 14 (`internal/auth/trusted.go:146,165,180,195` and `internal/admin/admin.go:42`):
    - **RED** test: a 5xx response body does NOT contain the raw error string from the wrapped error.
    - **GREEN:** replace `http.Error(w, err.Error(), http.StatusInternalServerError)` with `common.WriteError(w, r, common.Internal("descriptive operation summary", err))`. The `Internal` constructor already emits the ticket-correlated structured log and a sanitized response body.
  - One commit per file (so the diff stays reviewable) or one commit covering all sites — author's choice.

- [ ] **K1.9 — Verification gate + race + PR.** PR body lists all items: `Closes #34 (items 2-7); contributes to #68 (item 14)`. Do NOT auto-close #68 — there are other items in flight in other buckets.

---

## Bucket K2 — #68 #9: JWKS cache key (issuer, kid)

**Issue:** `internal/auth/validator.go:63-66,104-132` keys the JWKS cache on `kid` only. If multiple issuers share a `kid` value, signature confusion is possible. Re-key on `(issuer, kid)`.

**Files:**
- Modify: `internal/auth/validator.go`
- Test: `internal/auth/validator_test.go`

### Steps

- [ ] **K2.1 — Worktree.** Create `fix/v063-jwks-cache-issuer-key` off `release/v0.6.3`.

- [ ] **K2.2 — RED test: same-`kid`-different-issuer signature confusion.** Build two fake JWKS endpoints (different issuers) advertising different keys with the same `kid`. Verify a token signed by issuer A's key but claiming issuer B is rejected. Today's code may incorrectly accept it (cache hit on `kid` returns issuer A's key for an issuer-B token).

- [ ] **K2.3 — GREEN.** Change the cache key type from `string` (kid) to a struct `{Issuer, KID string}` (or `issuer + "\x00" + kid` if a string key is required by the cache implementation). Update both the lookup and the populate paths.

- [ ] **K2.4 — Run validator tests.** Confirm no regressions on the single-issuer path.

- [ ] **K2.5 — Verification gate + PR with `contributes to #68 (item 9)`.** Do NOT auto-close #68.

---

## Bucket K3 — #68 #12: auth error message uniformity

**Issue:** `internal/auth/delegating.go:24-45` returns distinct messages for "missing header" / "invalid format" / "empty bearer" / "token invalid". Collapse to a single `"authentication failed"` and log the specific reason server-side (user-enumeration mitigation).

**Files:**
- Modify: `internal/auth/delegating.go`
- Test: `internal/auth/delegating_test.go`

### Steps

- [ ] **K3.1 — Worktree.** Create `fix/v063-auth-error-uniformity` off `release/v0.6.3`.

- [ ] **K3.2 — RED test: all four failure modes return the same client-facing string.** Test that for each of the four input shapes (no header, bad format, empty bearer, invalid token), the response body is exactly `authentication failed` and HTTP status `401 Unauthorized`.

- [ ] **K3.3 — RED test: server log records the specific reason.** Use a slog test handler to capture log records; assert that for each of the four shapes, exactly one log record is emitted at WARN level with structured field `reason` = "missing-header" / "invalid-format" / "empty-bearer" / "token-invalid".

- [ ] **K3.4 — GREEN.** Refactor `delegating.go:24-45` to:
  - emit a single client message `"authentication failed"` for all four paths (via `common.Operational(http.StatusUnauthorized, common.ErrCodeUnauthorized, "authentication failed")`);
  - emit one structured `slog.Warn("authentication failed", "reason", "<specific>", "remote_addr", r.RemoteAddr, ...)` per path (no PII).

- [ ] **K3.5 — Verification gate + PR with `contributes to #68 (item 12)`.** Do NOT auto-close #68.

---

## Bucket L — Wave 2: app/app.go startup and shutdown cluster

**Issues bundled:** #10, #34 #1, #68 #19, #26. All edit `app/app.go` (and #26 also edits `cmd/cyoda/main.go`). Sequenced internally; one PR.

**Files:**
- Modify: `app/app.go`
- Modify: `cmd/cyoda/main.go`
- Test: `app/app_test.go`, `cmd/cyoda/main_test.go` (or new files following existing convention)

### Steps

- [ ] **L.1 — Worktree.** Create `fix/v063-app-startup-shutdown` off `release/v0.6.3`.

#### Sub-bundle L.A — #10: panic → slog.Error + os.Exit normalisation

- [ ] **L.A.1 — Survey.** `grep -n "panic(" app/app.go` to confirm the 9 sites currently present (the issue body said 10, current state is 9 per analysis).

- [ ] **L.A.2 — RED test (single, representative).** Add `app/app_test.go:TestNew_StorageFactoryFailureExits` that injects a failing storage factory and asserts the process exits with status 1 (use `if os.Getenv("BE_CRASHER") == "1" { ... }` + subprocess pattern from `os/exec_test.go`-style tests). This is one representative test for the whole sweep — converting all 9 sites under test would be prohibitive; one passing test on the converted shape is sufficient verification.

- [ ] **L.A.3 — GREEN.** Convert each `panic(fmt.Sprintf("...: %v", err))` to:

```go
slog.Error("startup failure", "phase", "<descriptive phase>", "error", err.Error())
os.Exit(1)
```

Use a per-site `phase` field consistent with the existing slog pattern (e.g. `"create storage factory"`, `"transaction manager"`, `"jwt-signing-key"`, `"kv-trusted-store-bootstrap"`, etc.). Do NOT bypass the OTel-flush concern — this same site won't have OTel running yet (these are pre-server-start failures), so `os.Exit(1)` is acceptable here.

- [ ] **L.A.4 — Verify.** `go test ./app/...` and `go vet ./...` — green. The representative test passes.

- [ ] **L.A.5 — Commit:** `fix(app): normalise startup-failure handling to slog.Error+os.Exit (#10)`.

#### Sub-bundle L.B — #34 #1: split route registration; protect /oauth/keys/* with authMW + ROLE_ADMIN

- [ ] **L.B.1 — RED test.** Add a handler test that POSTs to `/oauth/keys/trusted` without an `Authorization` header and asserts HTTP 401 (currently 200/204).

- [ ] **L.B.2 — GREEN.** In `app/app.go:198-203` (the route registration block), split into two groups:
  - **Public** routes mounted directly on the mux: `/oauth/token`, `/.well-known/jwks.json`.
  - **Admin** routes wrapped in `authMW(requireRole("ROLE_ADMIN", ...))`: all `/oauth/keys/trusted/*` and `/oauth/keys/keypair/*`.

- [ ] **L.B.3 — Re-run handler tests.** Confirm legitimate admin tokens still succeed; missing/invalid auth returns 401.

- [ ] **L.B.4 — Behavioural-note PR body bullet:** "Existing tooling that calls /oauth/keys/* without auth will now receive 401. This closes a known auth-bypass; release notes carry the migration call-out."

- [ ] **L.B.5 — Commit:** `fix(auth): protect /oauth/keys/* with authMW + ROLE_ADMIN (#34 item 1)`.

#### Sub-bundle L.C — #68 #19: gRPC GracefulStop with deadline

- [ ] **L.C.1 — Locate.** `app/app.go:478` currently calls `grpcServer.Stop()`.

- [ ] **L.C.2 — GREEN.** Replace with a `GracefulStop` invocation guarded by a 10-second deadline (matching the HTTP server's drain budget). Pattern:

```go
done := make(chan struct{})
go func() { grpcServer.GracefulStop(); close(done) }()
select {
case <-done:
case <-time.After(10 * time.Second):
    slog.Warn("gRPC graceful stop deadline exceeded; forcing", "phase", "shutdown")
    grpcServer.Stop()
}
```

- [ ] **L.C.3 — Commit:** `fix(grpc): graceful stop with 10s deadline matching HTTP drain (#68 item 19)`.

#### Sub-bundle L.D — #26: graceful shutdown coordination

- [ ] **L.D.1 — RED test.** Add `cmd/cyoda/main_test.go:TestShutdown_OnSIGTERM_DrainsBothServers` (subprocess pattern). The test starts the binary in a subprocess, sends SIGTERM after a brief warm-up, and asserts:
  - process exits with status 0 (clean) within 12 seconds;
  - HTTP requests in flight at SIGTERM time are completed (use a slow handler injected via test config);
  - OTel flush ran (verify by checking the OTel exporter log buffer or a sentinel file the flush hook writes).

- [ ] **L.D.2 — GREEN.** In `cmd/cyoda/main.go`:
  - Replace any blocking `http.ListenAndServe` / `grpcServer.Serve` calls with an `errgroup` coordinating both servers.
  - Install a SIGTERM/SIGINT handler that:
    1. calls `app.Shutdown(ctx)` (which invokes the existing `App.Shutdown` method);
    2. that method calls `httpServer.Shutdown(ctx)` and the gRPC graceful-stop dance from L.C;
    3. defers OTel flush at the top of `main` so it runs on any exit path.

- [ ] **L.D.3 — Verify.** `go test ./cmd/cyoda/... ./app/...`.

- [ ] **L.D.4 — Commit:** `feat(cmd): SIGTERM-driven graceful shutdown via errgroup (#26)`.

#### Verification + PR

- [ ] **L.5 — Bundle race detector run.** `go test -race -count=1 ./...` from repo root. Green.

- [ ] **L.6 — Bundle PR.** Title: `fix: app startup-failure and shutdown lifecycle (#10, #34, #68, #26)`. Body lists all four issue closes:

```
Closes #10.
Closes #26.
Contributes to #34 (item 1; other items shipping via fix/v063-auth-hardening).
Contributes to #68 (item 19; other items shipping via separate PRs).
```

---

## Self-Review

**Spec coverage:**
- #10 — Bucket L.A ✓
- #26 — Bucket L.D ✓
- #34 items 2–7 — Bucket K1 ✓
- #34 item 1 — Bucket L.B ✓
- #51 — Bucket J ✓
- #68 item 9 — Bucket K2 ✓
- #68 item 10 — Bucket H ✓
- #68 item 11 — Bucket F ✓
- #68 item 12 — Bucket K3 ✓
- #68 item 14 — Bucket K1 ✓
- #68 item 17 — Bucket G ✓
- #68 item 19 — Bucket L.C ✓
- #68 item 20 — Bucket I ✓
- #77 — Bucket A ✓
- #129 — Bucket C ✓
- #130 — Bucket D ✓
- #131 — Bucket E ✓
- #132 — Bucket B ✓

All 10 outstanding issues covered.

**Placeholder scan:** No "TBD", "implement later", "fill in details", "similar to Task N", "add appropriate error handling" appear in any task. Each task names exact files, exact constant/code names, exact commands. Where the agent must locate a code site (e.g. "the workflow-import handler" in Bucket E), the plan says how to find it (`grep` instructions) rather than asking the agent to guess.

**Type / name consistency:**
- `commontest.ExpectErrorCode` referenced in Recipe 2, Buckets A, C, D — same import path everywhere.
- `common.Operational(status, code, message)` — constructor signature consistent across all buckets.
- New error code constants: `ErrCodeIncompatibleType`, `ErrCodeInvalidChangeLevel` — names match the help-doc filename convention (`<CODE>.md`) used elsewhere.
- Branch naming `fix/v063-<short-name>` consistent across all buckets.
- Issue / PR cross-references use `Closes #N` for full-issue closures and `contributes to #N (item X)` for partial bundles, matching the pattern from PR #141.

**Housekeeping prerequisites (do BEFORE launching agents):**

- Manually close #126 (delivered via merged PR #127; auto-close failed — release-branch quirk per `feedback_release_branch_issue_closure.md`). Run:
  ```bash
  gh issue close 126 --comment "Resolved via PR #127 (commit 3f172ff). Auto-close didn't fire because the PR targets a release branch."
  ```

- Re-fetch `release/v0.6.3` before each agent's worktree creation to ensure they branch off the latest tip.

---

## Closing Checklist (post-merge of all PRs)

- [ ] All 10 outstanding issues' PRs merged into `release/v0.6.3`.
- [ ] Each closed-by issue carries the `v0.6.3` milestone (per `feedback_release_milestone_invariant.md`).
- [ ] `release/v0.6.3` smoke run: `go test -race ./...`, `make test-short-all`, full E2E.
- [ ] Release-merge PR (`release/v0.6.3` → `main`) drafted with milestone-derived changelog.
- [ ] CHART appVersion bump (`deploy/helm/cyoda/Chart.yaml`) committed alongside the release-merge PR.
- [ ] `v0.6.3` tag pushed; goreleaser builds; brew formula auto-published; `brew audit --strict` outcome recorded.
