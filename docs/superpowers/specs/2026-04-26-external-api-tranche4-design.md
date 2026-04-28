# External API Scenario Suite — Tranche 4 Design

**Issue:** [#121](https://github.com/Cyoda-platform/cyoda-go/issues/121) — numeric types & polymorphism
**Predecessor:** Tranche 3 (#135) on `release/v0.6.3`
**Branch:** `feat/issue-121-external-api-tranche4`
**Author:** Paul Schleger / Claude
**Date:** 2026-04-26

## 1. Goal

Add the externally-reachable subset of dictionary files **13-numeric-types.yaml** and **14-polymorphism.yaml** to the parity test suite — 14 new parity scenarios — plus the async-search client primitives required by 4 of those scenarios. Bundle into one PR targeting `release/v0.6.3`.

## 2. Scope

### In scope

| Source | Scenarios | Result |
|---|---|---|
| File 13 — numeric-types | 9 implementable | `numeric/{01, 04, 05ext, 06, 07, 08, 09, 10, 11}` |
| File 13 — recorded skips | 3 | `numeric/{02 cross-ref, 03 internal_only, 05 internal_only}` |
| File 14 — polymorphism | 5 implementable | `poly/{01, 03, 04, 05 REST-half, 06}` |
| File 14 — recorded skips | 2 | `poly/{02 internal_only, 07 shape_only}` |
| Async-search client primitives | 4 methods | `SubmitAsyncSearch`, `GetAsyncSearchStatus`, `GetAsyncSearchResults`, `AwaitAsyncSearchResults`, `CancelAsyncSearch` |
| Driver pass-throughs | 5 methods | symmetric with above |
| Mapping doc | 14 rows + skip annotations | `e2e/externalapi/dictionary-mapping.md` |

### Out of scope

- Internal-only scenarios (`numeric/03`, `numeric/05`, `poly/02`, `poly/07`) — surface gap is intentional, not a bug.
- Upstream dictionary contributions for cyoda-go's existing `NumericClassification*` scenarios — described in #121 as "once this tranche lands"; deferred to a follow-up.
- Production code changes — pure test-infrastructure additions.

## 3. Pre-implementation gate

### Phase 0.1 — async-search wire probe (REQUIRED before Phase 1)

The dictionary's async-search scenarios assume `POST /api/search/async/{name}/{v}` returns `{jobId}` and `GET /api/search/async/{jobId}/status` reports `SUCCESSFUL` when complete. cyoda-go's OpenAPI confirms the routes exist (`api/openapi.yaml:5001-5274`), but the exact response shapes — particularly the result-page envelope at `GET /api/search/async/{jobId}` — must be verified before writing the helpers.

**Probe:** spin up the standard parity fixture, call the three routes manually with `curl` or a one-off test, capture the response bodies, and pin the helper types to the observed shapes. No commit — outcome is recorded inline in the implementation plan.

**Findings to record:**
- Submit response: `{jobId: "..."}` or richer envelope?
- Status response: `{status: "SUCCESSFUL"|"RUNNING"|"FAILED"|"CANCELLED"|"PENDING"}` or different enum?
- Results response: `{content: [...], pageable: {...}, ...}` or simpler array?

### Phase 0.2 / 0.3 — deferred (per brainstorm)

Decimal byte-identity (0.2) and polymorphic readback (0.3) trust the existing internal unit tests (`internal/domain/model/schema/numeric_test.go`, `internal/domain/model/exporter/simple_view.go`'s polymorphic format). Discoverable per-scenario during Phase 2/3.

## 4. Architecture

### 4.1 Async-search client surface

`e2e/parity/client/http.go` gains four primitives + one wrapper:

```go
// SubmitAsyncSearch issues POST /api/search/async/{name}/{version} with the
// given condition body. Returns the jobId.
func (c *Client) SubmitAsyncSearch(t *testing.T, name string, version int, condition string) (string, error)

// GetAsyncSearchStatus issues GET /api/search/async/{jobId}/status.
// Returns the current job status (SUCCESSFUL, RUNNING, FAILED, CANCELLED, PENDING).
func (c *Client) GetAsyncSearchStatus(t *testing.T, jobId string) (string, error)

// GetAsyncSearchResults issues GET /api/search/async/{jobId} and returns
// the result page (content + pagination metadata).
type AsyncSearchPage struct {
    Content  []EntityResult `json:"content"`
    // Additional fields populated post-Phase-0.1 probe.
}
func (c *Client) GetAsyncSearchResults(t *testing.T, jobId string) (AsyncSearchPage, error)

// CancelAsyncSearch issues POST /api/search/async/{jobId}/cancel.
func (c *Client) CancelAsyncSearch(t *testing.T, jobId string) error

// AwaitAsyncSearchResults submits an async search, polls for completion, and
// returns the result page. Polls every 100ms; fails the test after timeout.
// Treats SUCCESSFUL as terminal-success; FAILED/CANCELLED as terminal-error.
func (c *Client) AwaitAsyncSearchResults(t *testing.T, name string, version int, condition string, timeout time.Duration) (AsyncSearchPage, error)
```

`e2e/externalapi/driver/driver.go` gains symmetric pass-throughs (`SubmitAsyncSearch`, `GetAsyncSearchStatus`, `GetAsyncSearchResults`, `AwaitAsyncSearchResults`, `CancelAsyncSearch`).

### 4.2 New parity scenario files

Two new files in `e2e/parity/externalapi/`:

- **`numeric_types.go`** — 9 `Run*` for file 13 + `init()` registration.
- **`polymorphism.go`** — 5 `Run*` for file 14 + `init()` registration.

Both follow the existing tranche-3 pattern (e.g. `workflow_import_export.go`): each scenario uses `driver.NewInProcess(t, fixture)` for happy-path, `*Raw` helpers + `errorcontract.Match` for negative paths.

### 4.3 Discover-and-compare on file-14 negative path

`poly/06` (wrong-type condition against DOUBLE field) follows the protocol from tranches 2+3:

1. Capture cyoda-go's `properties.errorCode` via temporary `t.Logf("DISCOVER ...")`.
2. Classify against dictionary's `InvalidTypesInClientConditionException` @400:
   - `equiv_or_better` → tighten with `ErrorCode: "<observed>"` + comment "matches/exceeds cloud's X".
   - `worse` → discuss with controller; potentially file standalone v0.7.0 issue + `t.Skip`.
3. Remove `t.Logf` before commit.

No new GitHub issues filed without controller confirmation (per memory rule).

## 5. Per-scenario notes

### File 13

- **13/01 SimpleIntegerLandsInDoubleField:** model with `{"price": 13.111}` → DOUBLE; POST `{"price": 13}` accepted; readback yields 1 entity.
- **13/04 DefaultIntegerScopeINTEGER:** sample `{"key1":"abc","key2":123}` → SIMPLE_VIEW reports `$.key1=STRING`, `$.key2=INTEGER`. Asserts cyoda-go's default scope.
- **13/05ext PolymorphicMergeWithDefaultScopes:** two-sample merge → polymorphic field types `[INTEGER, STRING]`, `[INTEGER, BOOLEAN]`, `[BOOLEAN]`.
- **13/06 DoubleAtMaxBoundary:** `{"v": 1.7976931348623157E308}` round-trip via entity GET. Trust cyoda-go's float64 fidelity (already unit-tested).
- **13/07 BigDecimal20Plus18:** 38-significant-digit decimal; assert `field_equals BIG_DECIMAL` on export and round-trip via `stripTrailingZeros` numeric comparison.
- **13/08 UnboundDecimal>18Frac:** 19-fractional-digit; assert `field_equals UNBOUND_DECIMAL` and `toPlainString` numeric comparison.
- **13/09 BigInteger38Digit:** assert `field_equals BIG_INTEGER` and entity round-trip.
- **13/10 UnboundInteger40Digit:** assert `field_equals UNBOUND_INTEGER` and round-trip.
- **13/11 SearchIntegerAgainstDouble:** ingest 4 records with various `$.price` doubles ≥ 70; async + direct search both return 4. Uses async-search helpers.

### File 14

- **14/01 MixedObjectOrStringAtSamePath:** ingest sample with mixed `some-object` shapes; assert async + direct each return non-empty for both branches (object-key condition + string-equals condition).
- **14/03 PolymorphicValueArray:** register `AllFieldsModel/1` from a sample on the fly; ingest entity with `polymorphicArray` of 4 variants; readback verbatim (`json.RawMessage` equality) + `field_polymorphic [STRING, DOUBLE, BOOLEAN, UUID]`.
- **14/04 PolymorphicTimestampArray:** `objectArray[*].timestamp` of 3 temporal types; readback verbatim + `[LOCAL_DATE, YEAR_MONTH, ZONED_DATE_TIME]`. May need to verify cyoda-go classifies these temporal subtypes — discoverable mid-implementation.
- **14/05 (REST half) TrinoSearchOnPolymorphicScalar:** dictionary's RSocket leg is unreachable; REST-equivalent direct-search returns 2 records. Async leg is the parity test; record `(skipped)` on the RSocket-only step.
- **14/06 RejectWrongTypeCondition:** `$.price` (DOUBLE) with condition value `"abc"`; assert HTTP 400 + discover-and-compare on errorCode.

## 6. Testing strategy

- TDD red/green per client/Driver method (Phase 1).
- Each new scenario file is registered via `init()` → registered scenario produces 0 PASS before its `Run*` body lands → file IS the test contract.
- All scenarios run across memory + sqlite + postgres single-node fixtures (~14 × 3 = 42 PASS expected, plus skips).
- Backend divergence (a scenario PASSing on memory but FAILing on postgres) is a real bug — STOP and surface.
- `go vet` silent at every commit; `make test-short-all` clean before PR; `go test -race ./e2e/parity/...` clean once before PR.

## 7. Acceptance

- All externally-reachable scenarios in 13/14 landed as parity entries (14 implemented).
- Decimal round-trips assert via `stripTrailingZeros` / `toPlainString` per dictionary; integer round-trips byte-identical.
- `dictionary-mapping.md` rows updated for all 14 + 4 internal/shape-only skips with explanatory notes.
- `go test ./e2e/parity/...` 0 FAIL across single-node backends.
- Code review + security review approved.
- PR opened against `release/v0.6.3`.

## 8. Cross-repo coordination

No cross-repo changes expected — async-search is a single-node concept; no multinode infrastructure added. Cassandra picks these scenarios up automatically on its next dependency bump.

## 9. Out of plan, follow-up candidates

Recorded for visibility; not in tranche 4:

- Upstream dictionary contributions for cyoda-go's `NumericClassification18DigitDecimal`, `NumericClassification20DigitDecimal`, `NumericClassificationLargeInteger`, `NumericClassificationIntegerSchemaAcceptsInteger`, `NumericClassificationIntegerSchemaRejectsDecimal` (per #121 "once this tranche lands"). Becomes a separate effort against `cyoda/.ai/plans/external-api-scenarios/`.
- Tranche 5 candidates (cloud smoke test reconciliation, additional dictionary files if any remain).
