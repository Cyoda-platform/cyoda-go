# Design Review — Sub-project A.1, Revision 2

**Date:** 2026-04-21
**Reviewing:** `2026-04-21-data-ingestion-qa-subproject-a1-design.md` (Revision 2)
**Prior review:** `2026-04-21-data-ingestion-qa-subproject-a1-review-01.md`

---

## 1. Status of prior review items

All four blockers from Review 01 are resolved:

- **§3.1 `"1.200"` StripTrailingZeros row.** Fixed in §6.1 to `unscaled=12, scale=1, value=1.2`, with explicit Java-BigDecimal citation.
- **§3.2 `"1.00"` branching contradiction.** Dissolved by the §2.3 decision to classify by value — `"1.00"`, `"1e0"`, `"10e-1"` all route to `ClassifyInteger` with value 1. §4.4 now describes the rule consistently.
- **§3.3 `CollapseNumeric` interface.** Unified across §4.2 signature comment, §4.3 TypeSet.Add steps 4–5, and §5 formal spec: non-empty numeric-only input, returns a single `DataType`, caller filters NULL and non-numerics.
- **§3.4 `float64` fallback.** Deleted outright in §4.4 rather than retained as a lossy shim. Additionally, the revision identifies and scopes a concrete production bug at `internal/grpc/entity.go:35` where `json.Unmarshal` into `interface{}` was silently truncating integers above 2^53.

All specification gaps (§4.1–4.7) and minor items (§7) from Review 01 are addressed.

The BYTE/SHORT drop decision (Review 01 §5.1) was made in favor of dropping, and applied consistently throughout: enum entries removed, `PromoteToScope` eliminated, widening lattice simplified, test matrices updated.

## 2. What the revision did well beyond the review

**The gRPC precision-loss fix is a real find.** The review only asked you to resolve the `float64` fallback framing; you came back with a concrete production bug at `entity.go:35`, scoped the fix into A.1 with its own implementation step (§7 step 5), dedicated tests (§6.8), and a success criterion (§9) requiring an end-to-end gRPC test that proves the fix is live. This is a stronger outcome than the review asked for.

**Divergence consolidation in §2.3.** The four intentional cyoda-go divergences — cross-plugin schema parity, polymorphic numeric output shape, FLOAT/BYTE/SHORT drop, value-based classification — plus the `findCommonDataType`-STRING-fallback-is-a-bug-fix framing are now grouped in one section. §5.7 ties the two classifier-layer divergences together explicitly. A reader coming in cold can see the full set in one place.

**Search predicate semantics are now a follow-on.** §10 explicitly calls out that `{BIG_DECIMAL, STRING}` fields need defined search behavior, parked for whichever sub-project owns search. Scoping this out of A.1 is correct.

## 3. New issues in Revision 2

Five issues, all concentrated in the test specification and one prose walkthrough. The implementer will likely catch most during TDD, but since §6 is the executable contract, catching them here saves RED/GREEN cycles.

### 3.1 §4.4 `"1.00"` walkthrough has the same class of bug as the old `"1.200"` row

> `"1.00"` → `StripTrailingZeros` → unscaled=1, scale=-2 → whole number → `ClassifyInteger(100)` → `INTEGER`.

Java's `stripTrailingZeros` decrements scale by the number of zeros stripped. `"1.00"` starts at scale=2; stripping two zeros gives scale=0, not scale=-2. The intermediate should be `unscaled=1, scale=0`, and the extracted `*big.Int` is `1 × 10^0 = 1`, not 100.

The example accidentally describes what happens to `"100"`, not `"1.00"`:
- `"100"`: starts at scale=0, strip 2 zeros → unscaled=1, scale=-2, big.Int=100.
- `"1.00"`: starts at scale=2, strip 2 zeros → unscaled=1, scale=0, big.Int=1.

Final classification (`INTEGER`) is correct; `ClassifyInteger(1)` and `ClassifyInteger(100)` both return `INTEGER`. But the mechanics are wrong and will mislead any reader trying to understand the algorithm from the walkthrough.

**Fix:** Change §4.4's `"1.00"` row to `unscaled=1, scale=0 → ClassifyInteger(1) → INTEGER`. §6.7 already states the correct outcome and needs no change.

### 3.2 `"3.14159265358979323846"` does not classify as `BIG_DECIMAL`

This value is used as the canonical `BIG_DECIMAL` test case in §4.4 prose, §6.3 test table, §6.7 walker tests, and §6.8 gRPC tests. Under the rules in §4.2 it classifies as `UNBOUND_DECIMAL`, not `BIG_DECIMAL`:

- Digits: `3` + 20 fractional = precision 21, scale 20, exp 1.
- `BIG_DECIMAL` definite fit requires `scale ≤ 18`. 20 > 18. Fails.
- `BIG_DECIMAL` loose fit requires `scale ≤ 18`. Same failure.
- → `UNBOUND_DECIMAL`.

The analysis doc §6 hedges exactly this case as "at least `BIG_DECIMAL`, often `UNBOUND_DECIMAL` if Trino bounds fail" — precisely because 20 fractional digits exceeds the scale-18 bound.

**Fix:** Either change the expected classification to `UNBOUND_DECIMAL` wherever this value appears, or change the test input to a value that actually classifies as `BIG_DECIMAL`. Recommended: use two distinct pi-derived values so the test suite covers both sides of the scale-18 cliff (the most important envelope transition on the decimal side):

| Test input | Classification | Rationale |
|---|---|---|
| `"3.141592653589793238"` (18 fractional digits) | `BIG_DECIMAL` | precision=19, scale=18, exp=1 — clean definite fit |
| `"3.14159265358979323846"` (20 fractional digits) | `UNBOUND_DECIMAL` | scale=20 > 18, both definite and loose fit fail |

Same replacement needed in §6.8 (gRPC) and §9 success criteria.

### 3.3 Digit-count arithmetic off by one on pi

§4.4 says "20-digit unscaled, scale=19." §6.3 says "20 / 19" (precision / scale). The actual value `"3.14159265358979323846"` has:

- 1 digit before the decimal point (`3`)
- 20 digits after
- Total: 21 digits unscaled, scale=20

If the test input were intended to have scale=19 and 20-digit unscaled (matching the doc's labels), the string should be `"3.1415926535897932384"` — one fewer fractional digit.

This matters in combination with issue 3.2: even pi-to-19-fractional-digits (scale=19) still fails the `BIG_DECIMAL` scale ≤ 18 bound. Shaving one digit does not rescue the classification; you need to get to scale ≤ 18, which means pi-to-18-fractional-digits or fewer.

**Fix:** Pick a consistent specific value per the recommendation in 3.2, and make all digit counts match it.

### 3.4 §6.3 boundary row "38-digit unscaled, scale=18, exp > 20" is self-contradictory

With `precision=38` and `scale=18`, `exp = precision - scale = 20`. It cannot be "> 20" given those dimensions. The row's goal appears to be covering the case where definite fit fails on exp but loose fit also fails on exp, which requires `precision - scale > 21` — a different shape than `precision=38, scale=18`.

The boundary tests are better expressed as concrete values than as dimensional descriptions. Dimensional descriptions hide the "is this combination even possible?" question; concrete values don't.

**Fix:** Replace the dimensional table with concrete boundary values:

| Specific input | Expected | Why |
|---|---|---|
| unscaled = `10^37` (38 digits, leading 1), scale=18 | `BIG_DECIMAL` | precision=38, exp=20 — definite fit |
| unscaled = `2^127 - 1` (≈39 digits), scale=18, `IsInt128()` → true | `BIG_DECIMAL` | precision=39, exp=21 — loose fit passes Int128 |
| unscaled = `2^127` (≈39 digits), scale=18, `IsInt128()` → false | `UNBOUND_DECIMAL` | loose fails Int128 |
| unscaled = `10^39` (40 digits), scale=18 | `UNBOUND_DECIMAL` | precision exceeds loose cap 39 |
| unscaled = `1`, scale=-22 (exp=23) | `UNBOUND_DECIMAL` | both definite (exp>20) and loose (exp>21) fail on exp |

### 3.5 §6.8 gRPC test payload has quoted-string issue

The decimal row in §6.8:

> CloudEvent payload with `{"x": "3.14159265358979323846"}` (decimal literal, 20 digits)

The value is JSON-quoted — it's a string, not a numeric literal. Under cyoda-go's §2.1 rule (no numeric autodiscovery from strings), this classifies as `STRING` regardless of whether `UseNumber` is in effect. The `float64` truncation path never engages on a quoted value. The test as written does not exercise the bug the fix is supposed to prevent.

Contrast with the two integer rows in §6.8 (`9007199254740993` and `12345678901234567890`) which are correctly unquoted and therefore do exercise the numeric decode path.

**Fix:** Drop the quotes: `{"x": 3.14159265358979323846}`. Combined with issue 3.2, pick a value that also classifies cleanly as `BIG_DECIMAL` if that's the desired test assertion — e.g., `{"x": 3.141592653589793238}`.

## 4. Unaddressed but probably intentional

**A.1 / A.2 shipping coordination** (Review 01 §5.2) is not explicitly resolved. §8's mention that "the old lenient behavior was a latent bug" and "release notes must call this out" implicitly commits to shipping A.1 independently and accepting the brief window where clients relying on the old lenient integer-schema-accepting-decimal behavior get rejections instead. If that's the team's call, fine — but a single sentence in §8 making the decision explicit rather than inferrable would close the loop.

## 5. Summary

Five new issues, all in the test/prose layer, none architectural. Four are test-spec precision issues (wrong classification expectations, off-by-one digit counts, self-contradictory boundary descriptions, a JSON-quoting slip); one is a prose walkthrough bug in §4.4 that doesn't affect the tests but will mislead readers.

Fix all five and the doc is ready for §7 step 1.

The revision's handling of Review 01 items is strong, and the `entity.go:35` find during revision is a genuine improvement on what the review asked for.
