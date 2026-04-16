# Issue #24 — Domain Layer: Statistics Scalability and Search Precision

**Issue:** [#24](https://github.com/Cyoda-platform/cyoda-go/issues/24)
**Date:** 2026-04-16
**Status:** Design — pending user review

## Overview

Resolve three independent domain-layer correctness/scalability bugs consolidated under issue #24:

1. **Stats scalability** — `Handler.GetStatisticsByState` and `GetStatisticsByStateForModel` call `EntityStore.GetAll(...)` and group by state in Go. Loads full entity payloads into memory just to count.
2. **XML number precision** — `inferXMLValue` in the model importer casts `int64` → `float64`, losing precision beyond `2^53`.
3. **`matchArray` numeric comparison** — array-element equality uses `fmt.Sprintf("%v")` string compare, diverging from scalar `EQUALS` which has a numeric-aware path.

The three fixes are bundled in one design but ship as **three sequential PRs** (Section 5). PR-1 spans repos (SPI bump, cassandra companion PR, then cyoda-go consumer); PR-2 and PR-3 are pure in-module fixes.

### Source locations

- `internal/domain/entity/service.go:316` (`GetStatisticsByState`), `:370` (`GetStatisticsByStateForModel`)
- `internal/domain/model/importer/parser.go:101` (`inferXMLValue`)
- `internal/match/match.go:150` (`matchArray`), with helper `opEquals` at `internal/match/operators.go:86`
- SPI: `github.com/cyoda-platform/cyoda-go-spi` (currently `v0.4.0`, will bump to `v0.5.0`)
- External cassandra plugin: `https://github.com/Cyoda-platform/cyoda-go-cassandra` (local checkout: `../cyoda-go-cassandra`)

---

## Section 1 — Rollout Sequencing

Three sequential PRs in cyoda-go, plus two upstream PRs in dependent repos:

| Step | Repo | Change | Result |
|------|------|--------|--------|
| 1 | `cyoda-go-spi` | Add `CountByState` to `EntityStore` + spitest conformance test | Tag `v0.5.0` |
| 2 | `cyoda-go-cassandra` | Bump SPI to `v0.5.0`, implement `CountByState` | Tag a release |
| 3 | `cyoda-go` PR-1 | Bump SPI + cassandra deps, implement in memory/sqlite/postgres, switch handler | Closes "stats" portion |
| 4 | `cyoda-go` PR-2 | XML `json.Number` fix in `inferXMLValue` | Closes "XML precision" portion |
| 5 | `cyoda-go` PR-3 | `matchArray` delegates to `opEquals` | Closes issue #24 |

PR-2 and PR-3 don't depend on PR-1 and could land in parallel. Kept sequential to minimize review load per PR.

---

## Section 2 — Stats: SPI Change + Plugin Implementations

### 2.1 SPI change (`cyoda-go-spi`, `v0.4.0` → `v0.5.0`)

Add to `EntityStore` interface in `persistence.go`:

```go
// CountByState returns the count of non-deleted entities grouped by state
// for the given model. If states is non-nil, only the listed states are
// included in the result. If states is nil, all states are returned.
// An empty (non-nil) states slice returns an empty map without querying
// the storage layer.
//
// Unknown model: returns an empty map with no error, matching Count's
// behavior (no model-registry check at this layer).
//
// Implementations MUST push the state filter down to the storage layer
// when feasible. Callers may invoke this from inside a transaction; the
// returned counts MUST reflect the transactional view (uncommitted writes
// from the current tx are visible, writes from other in-flight txs are not),
// matching the semantics of Count.
CountByState(ctx context.Context, modelRef ModelRef, states []string) (map[string]int64, error)
```

### 2.2 Conformance test (`spitest/entity.go`)

Add `testEntityCountByState` covering:

- Empty model → empty map.
- Mixed states (e.g., 3 in `"new"`, 2 in `"approved"`, 1 in `"rejected"`) → correct grouping.
- `states = []string{"approved"}` → only `"approved"` returned.
- `states = []string{"approved", "missing"}` → only `"approved"` returned (missing states omitted, not zero-valued).
- `states = nil` → all states returned.
- `states = []string{}` → empty map returned (and the implementation does not hit storage — early return).
- Unknown model (`modelRef` not registered, no entities saved) → empty map, no error.
- Deleted entities excluded.
- Tenant isolation: entities in another tenant don't appear.
- Transactional visibility: uncommitted save in current tx is visible; uncommitted save in another tx is not.

The test must run against memory, sqlite, postgres, and cassandra (cassandra picks it up automatically via the parity-test registry).

### 2.3 Plugin implementations

**Common early-exit (all plugins):** `if states != nil && len(states) == 0 { return map[string]int64{}, nil }` before any storage call. Makes the empty-slice contract explicit and driver-independent (no reliance on how a Go empty slice is serialized into a postgres `ANY` array, an sqlite `IN ()` clause, etc.).

**postgres** (`plugins/postgres/entity_store.go`):
```sql
SELECT state, count(*) FROM entities
WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND NOT deleted
  [AND state = ANY($4)]   -- only when states != nil (and len > 0; len == 0 returns early)
GROUP BY state
```
Single round-trip. State filter via `state = ANY($4)` with a `[]string` parameter.

**sqlite** (`plugins/sqlite/entity_store.go`):
Same query semantics. SQLite has no array type, so the filter expands to `IN (?, ?, ...)` with one placeholder per state. In-tx path mirrors existing `Count`: fall back to `GetAll` then group in Go (the merged-view logic already lives there).

**memory** (`plugins/memory/entity_store.go`):
Iterate `entityData[tenant]`, group `latest.entity.Meta.State` for non-deleted entities matching `modelRef`, apply optional filter. In-tx: same fallback as existing `Count`.

**cassandra** (external repo):
Mirror existing `Count` shard-scan. Iterate shards, scan `(eid, deleted, state)`, group in Go. State filter applied in Go (cassandra has no efficient cross-partition `GROUP BY`).

> **Cassandra performance note:** `CountByState` reads the `state` column for every (non-deleted) entity in every shard, in addition to what `Count` already reads (`eid, deleted`). It is strictly more expensive than `Count` per row, and asymptotically the same scan cost. The existing `Count` is documented in the cassandra plugin as O(n) with the comment "acceptable for the current phase," so `CountByState` inherits that posture. We do not have calibrated entity-count estimates per tenant in this repo; the cassandra plugin's own performance tracking is the source of truth for whether the scan is acceptable at production scale.

> **Open follow-up (out of scope):** state-indexed materialized view for cassandra if scan performance proves insufficient. Track as a separate issue post-merge.

### 2.4 Handler changes (`internal/domain/entity/service.go`)

Replace lines 332-364 (`GetStatisticsByState`) and 386-411 (`GetStatisticsByStateForModel`):

- Dereference `*[]string` → `[]string` (or `nil`):
  ```go
  var filterStates []string
  if states != nil {
      filterStates = *states  // pointer-to-empty-slice intentionally preserved as []string{},
                              // which the SPI contract handles as "empty map, no storage call"
  }
  ```
  This handles both nil-pointer (no filter) and pointer-to-empty-slice (empty map result) cases distinctly.
- Call `entityStore.CountByState(ctx, ref, filterStates)`.
- Flatten the returned `map[string]int64` into the existing `[]EntityStatByState` shape.
- Per-model loop in `GetStatisticsByState` is unchanged (still iterates `modelStore.GetAll` and aggregates per model).

The existing `[]EntityStatByState` struct, the HTTP/gRPC response shapes, and route signatures are unchanged — only the handler implementation.

> **Known limitation (follow-up):** `GetStatisticsByState` still loads every model definition via `modelStore.GetAll(ctx)` and issues one `CountByState` call per model. With the entity-loading bottleneck removed, the per-model fan-out becomes the next pressure point for tenants with many models. Acceptable at current scale; track as a separate follow-up issue with two natural directions: (a) batch — extend the SPI with `CountByStateAll(ctx, states) (map[ModelRef]map[string]int64, error)` so postgres/sqlite can do one `GROUP BY (model_name, model_version, state)` query; (b) parallelize the per-model loop with bounded concurrency. Out of scope for this issue.

### 2.5 In-tx fallback (documented limitation)

`CountByState` inside a long transaction can still load entities into memory in sqlite/memory plugins, because their merged-view logic requires it. This matches the existing `Count` behavior — not a regression. Documented in the SPI godoc.

---

## Section 3 — XML Precision (`internal/domain/model/importer/parser.go`)

### 3.1 Change

Replace `inferXMLValue` (lines 101-112):

```go
func inferXMLValue(s string) any {
    // Defer numeric coercion: keep numbers as json.Number so callers
    // can choose Int64() vs Float64() vs string preservation.
    // Mirrors ParseJSON's UseNumber() — XML and JSON imports produce
    // structurally identical trees for numeric leaves.
    if isJSONNumber(s) {
        return json.Number(s)
    }
    if b, err := strconv.ParseBool(s); err == nil {
        return b
    }
    return s
}
```

`isJSONNumber(s)` validates strictly against the JSON number grammar (RFC 8259 §6):
`-? (0 | [1-9][0-9]*) (\.[0-9]+)? ([eE][+-]?[0-9]+)?` — anchored at both ends.

To eliminate any risk of regex/grammar drift, the **canonical implementation** is:

```go
func isJSONNumber(s string) bool {
    var n json.Number
    return json.Unmarshal([]byte(s), &n) == nil
}
```

This delegates the exact validation rules to `encoding/json`, which is the authority that downstream code will use to round-trip the value. Anything `json.Unmarshal` accepts as a `json.Number` is round-trip-safe; anything it rejects would have produced a broken tree. Implementation cost is one byte-slice allocation per call — acceptable for an XML import path that is not in any hot loop.

Test cases below MUST exercise the JSON-grammar edge cases directly so the test suite catches any future implementation switch (e.g., to a hand-rolled scanner for performance) that diverges from `encoding/json`'s behavior.

### 3.2 Tests (`parser_xml_value_test.go`, new file)

- `<x>9007199254740993</x>` → `json.Number("9007199254740993")` (currently rounds to `9007199254740992`).
- `<x>123.456</x>` → `json.Number("123.456")`, `.Float64()` succeeds.
- `<x>-0.0</x>`, `<x>1e10</x>`, `<x>1.5e-5</x>` → all `json.Number`.
- `<x>true</x>`, `<x>false</x>` → `bool`.
- `<x>NaN</x>`, `<x>Inf</x>`, `<x>0x1a</x>` → `string` (rejected by `isJSONNumber`).
- **JSON-grammar edge cases (must be `string`, not `json.Number`):**
  - `<x>007</x>` → `string` (leading zeros not allowed by JSON grammar)
  - `<x>00</x>` → `string`
  - `<x>-</x>` → `string` (lone minus)
  - `<x>+1</x>` → `string` (leading plus not allowed)
  - `<x>1.</x>` → `string` (trailing dot)
  - `<x>.5</x>` → `string` (no integer part)
  - `<x>1e</x>`, `<x>1e+</x>` → `string` (incomplete exponent)
- **Edge cases that ARE valid:**
  - `<x>0</x>` → `json.Number("0")`
  - `<x>-0</x>` → `json.Number("-0")`
  - `<x>1E2</x>` → `json.Number("1E2")` (uppercase E allowed)
- `<x>hello</x>`, `<x></x>` → `string`.
- `<x>  42  </x>` → confirms existing trim behavior still produces `json.Number("42")`.

**Cross-format symmetry test:** parse the same numeric leaves as JSON and as XML, compare trees. Must match.

### 3.3 Downstream consumer audit

Grep for `ParseXML(`, `importer.ParseXML(`, `inferXMLValue(`. Find any consumer that does a type switch or assertion on `int64`/`float64` from XML output. Update to handle `json.Number` (call `.Int64()` / `.Float64()` and propagate errors). Most consumers likely already handle `json.Number` since `ParseJSON` produces it. Per Gate 6 (resolve, don't defer): any consumer found is fixed in PR-2.

---

## Section 4 — `matchArray` (`internal/match/match.go`)

### 4.1 Change

Replace lines 158-164 of `matchArray`:

```go
expStr := fmt.Sprintf("%v", expected)
if !result.Exists() || result.String() != expStr {
    return false, nil
}
```

with:

```go
if !result.Exists() || !opEquals(result, expected) {
    return false, nil
}
```

`opEquals` (defined at `operators.go:86`) already handles the numeric-aware path: `actual.Type == gjson.Number` → `toFloat64(expected)` compare; otherwise string compare. Identical semantics to scalar `EQUALS`. One-line comment at the call site documents the deliberate delegation.

### 4.2 Tests (additions to `internal/match/match_test.go`)

> Verified: `predicate.ArrayCondition` (defined in SPI at `predicate/condition.go:36`) has fields `JsonPath string` and `Values []any`. Test cases below use those exact field names.

- **Numeric across Go types:** entity `"scores": [1, 2, 3]`. Predicate `Values: []any{1, 2, 3}` (`int`) → match. Same with `int64(1), int64(2), int64(3)` → match. Same with `1.0, 2.0, 3.0` (`float64`) → match.
- **`json.Number` expected:** entity `"scores": [1.5]`. Predicate `Values: []any{json.Number("1.5")}` → match. (Important because PR-2 makes XML produce `json.Number`, so XML-imported predicates feed `json.Number` here.)
- **Type mismatch:** entity `"tags": ["go"]` (string). Predicate `Values: []any{42}` (number) → no match.
- **Existing nil-skip:** entity `"tags": ["go", "rust", "python"]`. Predicate `Values: []any{"go", nil, "python"}` → match (existing test, kept).
- **Existence:** entity has 2 elements at path, predicate requires 3 → no match (existing behavior).

### 4.3 `toFloat64` extension for `json.Number` (required)

`toFloat64` at `internal/match/operators.go:243` currently switches on: `float64`, `float32`, `int`, `int64`, `string`, default. **Audit confirms it does NOT handle `json.Number`** — `json.Number` has underlying type `string` but Go type switches use exact type, so `json.Number` falls to the `default` branch and returns `cannot convert json.Number to float64`.

**Extension (lands in PR-3):**

```go
case json.Number:
    return n.Float64()
```

Add to the switch in `toFloat64`. Benefits every numeric operator (`opEquals`, `opCompare`, `opBetween`, `matchArray`) uniformly — not just the array case. Without this extension, predicates built from XML-imported documents (which produce `json.Number` after PR-2) would break across the board, not only in `matchArray`.

Add a unit test covering `toFloat64(json.Number("1.5")) → 1.5`, and an integration-level test that runs an `EQUALS` predicate with `json.Number` expected against a numeric entity field — proving the fix propagates through `opEquals`.

---

## Section 5 — Cross-Cutting

### 5.1 Testing strategy by gate

- **Gate 1 (TDD):** Each PR follows red→green→refactor. Conformance tests in `spitest` written first; plugin implementations follow.
- **Gate 2 (E2E):** PR-1 adds an E2E test exercising the statistics-by-state HTTP endpoint with a state filter against a real postgres. PR-2 and PR-3 are below the HTTP layer; existing E2E coverage that exercises XML import / array predicates verifies integration.
- **Gate 3 (security):** No credential/secret surface touched. Tenant isolation: `CountByState` uses the same `tenant_id` filter as `Count`, verified by the conformance test.
- **Gate 4 (docs):** No env vars, no CLI flags, no config. SPI godoc on `CountByState` documents in-tx fallback. No README/CONTRIBUTING/printHelp changes required.
- **Gate 5 (verify):** `go test ./...` and `go vet ./...` green on every PR. Cassandra plugin tests run in its own repo's CI.
- **Gate 6 (resolve, don't defer):** XML consumer audit (Section 3.3), `toFloat64` extension (Section 4.3) — both fixed in their respective PRs, not deferred.

### 5.2 Risks

| Risk | Mitigation |
|------|-----------|
| Cassandra repo PR not merged when cyoda-go PR-1 is ready | Sequence step 2 → step 3 explicitly. Temporary `replace` directive only if absolutely needed. |
| XML consumer audit misses a downstream type assertion | Full test suite run is the safety net; Gate 6 says any breakage is fixed in PR-2. |
| In-tx `CountByState` still loads entities (sqlite/memory) | Matches existing `Count` behavior. Documented in SPI godoc. Not a regression. |
| `toFloat64` doesn't handle `json.Number` | Confirmed missing (Section 4.3). Extension lands in PR-3. |
| Per-model fan-out in `GetStatisticsByState` becomes new bottleneck | Documented as known limitation in Section 2.4. Follow-up issue post-merge. |

### 5.3 Out of scope

- State-indexed materialized view for cassandra `CountByState` (separate follow-up if needed).
- Per-model fan-out optimization for `GetStatisticsByState` — batched `CountByStateAll` SPI or parallelized loop (Section 2.4 documents this as a known limitation; track as separate follow-up).
- Capability-interface refactor of the SPI (separate spec if/when we go there).
- `matchArray` operators beyond equality. The bug is about equality; we don't extend to `LESS_THAN`-per-element or similar.
- Backwards-compatibility shims for the SPI bump. Per CLAUDE.md project conventions, we don't add fallback paths for hypothetical un-migrated plugins; cassandra is migrated as part of the rollout.
