# Sub-project A.1 — Numeric Classifier Parity with Cyoda Cloud

**Date:** 2026-04-21
**Parent initiative:** Data-ingestion QA (Option 1 decomposition: sub-projects A, B, C, D)
**Predecessor context:** `docs/numeric-type-classification-analysis.md` — authoritative algorithm spec for Cyoda Cloud's numeric classifier.

## 1. Purpose

Port Cyoda Cloud's numeric classification algorithm to cyoda-go with behavioral parity sufficient to satisfy two hard requirements:

1. **Faithful reconstruction.** Every ingested numeric value round-trips through the schema/storage boundary without silent precision loss.
2. **Mathematically rigorous comparisons.** Search predicates on numeric fields (`=`, `<`, `<=`, `>`, `>=`) give exact answers regardless of the numeric family or precision.

cyoda-go does **not** aim for bit-identical schema output with Cyoda Cloud on every input. It aims for equivalent information content under a simpler model: one collapsed numeric type per field instead of a polymorphic numeric set. The collapse is lossless — every value still representable, every comparison still exact.

## 2. Scope

### 2.1 In scope

- Port the raw classifier (`ParserFunctions.kt:118-170` equivalent) to Go: Jackson-node-kind-analog discriminator, precision/scale envelopes for decimals, Int128 predicates for `BIG_INTEGER` / `BIG_DECIMAL`.
- Port scope promotion (`JacksonParser.parseLeaf` `intScope` / `decimalScope` defaults — `INTEGER` and `DOUBLE`).
- Port the widening lattice (`DataType.wideningConversionMap`) as a reference for `IsAssignableTo` checks used in validation.
- Introduce `CollapseNumeric(set)` — cyoda-go's design choice that reduces any numeric sub-set of a `TypeSet` to exactly one `DataType`. Cross-family promotion to `BigDecimal` or `UnboundDecimal` as specified by the collapse table.
- Introduce a minimal hand-rolled `Decimal` type (`internal/domain/model/schema/decimal.go`) using `math/big.Int` + `int32 scale`. Supports parse, compare, serialize, `stripTrailingZeros`, `precision`, `scale`, `setScale(n).isInt128`. No arithmetic. Arbitrary precision via `math/big`.
- Asymmetric validation compatibility: integer values accepted against decimal schemas; decimal values rejected against integer schemas. Natural consequence of comparing classified type to schema type through the widening lattice.
- Preserve cross-kind polymorphism (e.g., `{INTEGER, STRING}` for a field that has seen both numeric and non-numeric values).
- Port `NULL` merge semantics: `NULL` disappears when merged with any concrete type; remains when alone.
- Drive the port via red/green TDD. Tests derived from Cyoda Cloud's test classes and the analysis-doc edge-case table become the RED spec.

### 2.2 Out of scope

- `Polymorphic`, `ComparableDataType`, `findCommonDataType` equivalents. Replaced by `CollapseNumeric`.
- Trino serialization details for `BIG_DECIMAL` / `UNBOUND_DECIMAL`. The Cassandra plugin or a future Trino integration layer owns that concern.
- Arithmetic on `Decimal` values. All arithmetic delegated to Trino / downstream consumers.
- Changes to `ChangeLevel` enforcement or the extension operator — that's Sub-project A.2.
- Changes to any storage plugin. The classifier produces the DataType; plugins store opaque bytes with a type tag. Plugins never re-classify.

### 2.3 Non-requirements (explicitly)

- **Schema parity across storage engines.** A user migrating between plugins performs a data migration; the migration may re-classify. cyoda-go does not guarantee byte-identical schemas across plugins.
- **Compatibility with Cyoda Cloud's polymorphic numeric output.** A field Cyoda Cloud represents as `{FLOAT, DOUBLE, BIG_DECIMAL}` becomes `BIG_DECIMAL` in cyoda-go. Lossless, smaller, equally searchable.

## 3. Hard requirements, restated as invariants

For every ingested scalar value `v`:

**(I1) Classification is deterministic.** Two identical inputs classify to the same `DataType` regardless of the order of ingestion or the current schema state.

**(I2) Reconstruction is lossless.** For any value `v` classified as `DataType T`, the storage layer can reconstruct a value byte-equal to `v`'s canonical form (after `stripTrailingZeros` for decimals).

**(I3) Comparisons are exact.** For any two values `v1`, `v2` classified into the same numeric family (post-collapse, same `DataType`), `compare(v1, v2)` returns the mathematically correct result: negative, zero, or positive.

**(I4) The collapsed `DataType` of a field's `TypeSet` can losslessly represent every value ever observed at that field.**

**(I5) Polymorphism across kinds is preserved.** A field that has seen both numeric and string values carries a `TypeSet` with one collapsed numeric entry plus any non-numeric entries.

## 4. Architecture

Three new units, one modified unit. All within `internal/domain/model/schema/` and `internal/domain/model/importer/`.

### 4.1 `internal/domain/model/schema/decimal.go` — minimal decimal type

```go
// Decimal is a fixed-scale arbitrary-precision decimal.
// Value = unscaled × 10^(-scale).
// No arithmetic — cyoda-go delegates arithmetic to Trino.
type Decimal struct {
    unscaled *big.Int
    scale    int32
}

func ParseDecimal(s string) (Decimal, error)
func (d Decimal) IsZero() bool
func (d Decimal) Sign() int
func (d Decimal) StripTrailingZeros() Decimal
func (d Decimal) Precision() int          // digit count of unscaled (excluding sign)
func (d Decimal) Scale() int32
func (d Decimal) SetScale(newScale int32) (Decimal, bool)  // ok=false on negative-scale-with-precision-loss
func (d Decimal) Unscaled() *big.Int      // defensive copy
func (d Decimal) IsInt128() bool          // unscaled fits signed Int128 (-2^127 .. 2^127-1)
func (d Decimal) Cmp(other Decimal) int   // exact comparison
func (d Decimal) Canonical() string       // round-trippable string form
func (d Decimal) MarshalJSON() ([]byte, error)
func (d *Decimal) UnmarshalJSON([]byte) error
```

Implementation notes:

- `ParseDecimal` accepts `"123"`, `"123.456"`, `"1.5e2"`, `"1.5E-2"`, `"-.5"`, `".5"`, with optional leading `+`/`-`. Rejects `NaN`, `Infinity`, empty string, malformed.
- `StripTrailingZeros` normalizes `"1.200"` → unscaled=12, scale=-1 (value 120). Matches Java `BigDecimal.stripTrailingZeros()`.
- `SetScale(n)` for `n > scale` multiplies unscaled by `10^(n-scale)`. For `n < scale`, only succeeds if unscaled is divisible by `10^(scale-n)`.
- `IsInt128` checks the unscaled `big.Int` is within the signed Int128 range `[-2^127, 2^127-1]`. Because `big.Int.BitLen` ignores sign, the implementation compares against pre-computed `big.Int` boundaries rather than relying on `BitLen() <= 127` alone (which would incorrectly exclude `-2^127`).
- `Cmp` normalizes to common scale (larger of the two), then `big.Int.Cmp`.
- `Canonical` formats as plain decimal (no scientific notation) — matches `ParserFunctions.kt`'s expectation for canonicalized form.

### 4.2 `internal/domain/model/schema/numeric.go` — classifier + collapse

```go
// Numeric envelopes from Cyoda Cloud's ParserFunctions.kt:33-59.
const (
    floatMaxPrecision       = 6
    floatMaxAbsScale        = 31
    doubleMaxPrecision      = 15
    doubleMaxAbsScale       = 292
    bigDecimalMaxScale      = 18
    bigDecimalDefinitePrec  = 38
    bigDecimalDefiniteExp   = 20
    bigDecimalLoosePrec     = 39
    bigDecimalLooseExp      = 21
)

// NumericFamily returns 1 for integer-family, 2 for decimal-family, 0 otherwise.
func NumericFamily(dt DataType) int

// NumericRank is the widening-order position within a family.
func NumericRank(dt DataType) int

// IsNumeric reports whether dt is in either numeric family.
func IsNumeric(dt DataType) bool

// ClassifyDecimal classifies a parsed decimal value into FLOAT, DOUBLE,
// BIG_DECIMAL, or UNBOUND_DECIMAL. Matches ParserFunctions.kt:121-130.
func ClassifyDecimal(d Decimal) DataType

// ClassifyInteger classifies a parsed integer value into BYTE, SHORT,
// INTEGER, LONG, BIG_INTEGER, or UNBOUND_INTEGER. Matches
// ParserFunctions.kt:133-155.
func ClassifyInteger(v *big.Int) DataType

// PromoteToScope widens a freshly classified type to at least the
// configured minimum scope. Matches JacksonParser.parseLeaf:352-392.
func PromoteToScope(raw DataType, intScope, decimalScope DataType) DataType

// IsAssignableTo reports whether dataT can losslessly assign into schemaT
// per the widening lattice from DataType.kt:239-272. Used by Validate.
func IsAssignableTo(dataT, schemaT DataType) bool

// CollapseNumeric takes a set of DataTypes (numeric and non-numeric
// intermixed) and returns a set where all numeric members have been
// reduced to exactly one DataType per the collapse rule (see §5).
// Non-numeric members pass through untouched. NULL is dropped if any
// concrete type is present. Preserves cross-kind polymorphism.
func CollapseNumeric(types []DataType) []DataType
```

### 4.3 `internal/domain/model/schema/types.go` — `TypeSet.Add` updated

Current `TypeSet.Add` collapses same-family-by-rank. Update:

- Add the new member as before.
- After adding, if more than one numeric member is present, call `CollapseNumeric(numericMembers)` and replace them with the single result.
- Non-numeric members are never touched.
- If `NULL` is present alongside a concrete type, `NULL` is removed (matches Cyoda Cloud `ModelDataType.merge`).

This preserves the invariant: a `TypeSet` has at most one numeric entry at any time.

### 4.4 `internal/domain/model/importer/walker.go` — replace `inferNumericType*`

- Remove `inferNumericType` (float64 path, value-ranged) and `inferNumericTypeFromString` (current `strconv.ParseFloat`-based).
- New path: receive `json.Number`, parse via `ParseDecimal`, split on scale (`scale <= 0` and no fractional part → integer branch; else decimal branch).
- Integer branch: extract `big.Int` from `Decimal.Unscaled * 10^(-scale)`, call `ClassifyInteger`, `PromoteToScope`.
- Decimal branch: apply `StripTrailingZeros`, call `ClassifyDecimal`, `PromoteToScope`.
- `float64` legacy path preserved as a safety fallback when callers bypass `UseNumber()`; documented as lossy but behaves identically to Cyoda Cloud's "pre-UseNumber" shape (Double classification for fractional, value-ranged for whole-number floats).

### 4.5 `internal/domain/model/schema/validate.go` — assignability check

Replace `isCompatible(dataT, modelT)` that returns `true` for any numeric pair, with a call to `IsAssignableTo(dataT, modelT)`. This realizes Cyoda Cloud's asymmetric behavior:

- `"13"` (classified `INTEGER`) against a `DOUBLE` schema: `IsAssignableTo(INTEGER, DOUBLE)` = true per the lattice. Accepted.
- `"13.111"` (classified `DOUBLE`) against an `INTEGER` schema: `IsAssignableTo(DOUBLE, INTEGER)` = false. Rejected at strict validation.

Extension behavior at non-empty `ChangeLevel` — e.g., whether `INTEGER` schema plus `"13.111"` input at `ChangeLevel = Type` produces a widened `BIG_DECIMAL` schema — is downstream of this sub-project. A.2 owns the Extend → Diff → Apply pipeline that uses the collapse rule to widen schemas during ingestion. A.1 produces only the strict-validate semantics.

## 5. `CollapseNumeric` — the cyoda-go design choice

Formal specification. Given a slice of `DataType` values, all in a numeric family:

### 5.1 Same-family collapse

If all members are in the integer family: return the single member with the highest `NumericRank`.
If all members are in the decimal family: return the single member with the highest `NumericRank`.

### 5.2 Cross-family collapse

If members span both families: the result is the narrowest decimal-family type that losslessly contains every observed integer-family member:

| Widest integer observed | Widest decimal observed | Collapse |
|---|---|---|
| `BYTE` / `SHORT` / `INTEGER` / `LONG` | `FLOAT` / `DOUBLE` / `BIG_DECIMAL` | `BIG_DECIMAL` |
| `BYTE` / `SHORT` / `INTEGER` / `LONG` | `UNBOUND_DECIMAL` | `UNBOUND_DECIMAL` |
| `BIG_INTEGER` | any `<= BIG_DECIMAL` | `BIG_DECIMAL` |
| `BIG_INTEGER` | `UNBOUND_DECIMAL` | `UNBOUND_DECIMAL` |
| `UNBOUND_INTEGER` | any | `UNBOUND_DECIMAL` |

### 5.3 NULL handling

If the input contains `NULL` and any other type (numeric or not), `NULL` is dropped. If input is `{NULL}` alone, `NULL` is returned.

### 5.4 Rationale — why cross-family promotes into decimal

- Every integer value of any observed integer-family type is exactly representable in the target decimal type (`BIG_DECIMAL` holds integer values up to 2^127-1 at scale 0; `UNBOUND_DECIMAL` arbitrary).
- The reverse (promoting into integer) cannot preserve fractional values.
- Search-predicate semantics on the collapsed decimal type remain mathematically rigorous — `big.Int.Cmp` after scale alignment gives exact answers.
- Storage cost increases moderately (decimal-family values use `Decimal`; integer-family values use `big.Int`). Acceptable for the precision gain.

### 5.5 Cross-kind polymorphism

Non-numeric members (`STRING`, `BOOLEAN`, `BYTE_ARRAY`, date/time types, `UUID`, `UNKNOWN`) are not affected by `CollapseNumeric`. They remain in the `TypeSet` alongside the single collapsed numeric member.

A field whose history is `{INTEGER, DOUBLE, STRING, NULL}` ends up as `{BIG_DECIMAL, STRING}`.

## 6. Test specification

Every test case below becomes a named Go test. The test suite acts as the executable contract for this sub-project.

### 6.1 `Decimal` type tests (`decimal_test.go`)

- `ParseDecimal`:
  - `"0"`, `"0.0"`, `"-0"`, `"-0.0"`, `"0.1"`, `"123.456"`, `"1.5e2"`, `"1.5E-2"`, `"-.5"`, `".5"`, `"1e0"`, `"1.0"`, `"1.00"` all parse correctly with expected `(unscaled, scale)`.
  - `"NaN"`, `"Infinity"`, `"+Infinity"`, `"-Infinity"`, `""`, `"abc"`, `"1.2.3"`, `"1e"`, `"1..2"` all error.
- `StripTrailingZeros`:
  - `"1.200"` → unscaled=12, scale=-1 (value 120, one-significant-digit form per `stripTrailingZeros`). Verify both unscaled and scale.
  - `"100"` → unscaled=1, scale=-2.
  - `"0"` and `"0.0"` both → unscaled=0, scale=0.
  - `"1.5"` → unchanged.
- `Precision`:
  - Matches Java `BigDecimal.precision()`: always at least 1, counts significant digits of unscaled.
- `Scale`:
  - Matches Java `BigDecimal.scale()`: number of digits after decimal point (negative allowed for `1e2` form).
- `SetScale`:
  - Upward scale (adding zeros) always succeeds.
  - Downward scale with non-zero trailing digits reports `ok=false`.
- `IsInt128`:
  - `2^127 - 1` → true.
  - `2^127` → false.
  - `-2^127` → true.
  - `-2^127 - 1` → false.
- `Cmp`:
  - `"1.5" == "1.50"` → 0.
  - `"1.5" < "1.6"` → -1.
  - `"1e10" > "9e9"` → 1.
  - `"0.0" == "-0.0"` → 0.
  - Cross-scale: `"1.5000000001" vs "1.5"` → 1.
- `Canonical`:
  - Round-trips through `ParseDecimal` for every test value.
  - No scientific notation.

### 6.2 `ClassifyInteger` tests (`numeric_test.go`)

From the Cyoda Cloud edge-case table (§6 of the analysis doc):

| Input `big.Int` | Classified as |
|---|---|
| `0`, `-1`, `127`, `128`, `-128`, `-129` | `BYTE` for `[-128, 127]`, `SHORT` for `[-32768, 32767] \ [-128, 127]`, `INTEGER` otherwise |
| `32767`, `32768`, `-32768`, `-32769` | `SHORT` / `INTEGER` boundary |
| `2^31 - 1`, `2^31` | `INTEGER` / `LONG` boundary |
| `2^63 - 1` | `LONG` |
| `2^63` | `BIG_INTEGER` |
| `2^127 - 1` | `BIG_INTEGER` |
| `2^127` | `UNBOUND_INTEGER` |
| `10^30` | `UNBOUND_INTEGER` (outside Int128) |

### 6.3 `ClassifyDecimal` tests

| Input `Decimal` | stripTrailingZeros'd | precision | scale | Classified as |
|---|---|---|---|---|
| `"0.1"` | `0.1` | 1 | 1 | `FLOAT` |
| `"1.5"` | `1.5` | 2 | 1 | `FLOAT` |
| `"0.123456"` | `0.123456` | 6 | 6 | `FLOAT` |
| `"0.1234567"` | `0.1234567` | 7 | 7 | `DOUBLE` (precision > 6) |
| `"0.123456789012345"` | 15 | 15 | `DOUBLE` |
| `"0.1234567890123456"` | 16 | 16 | `BIG_DECIMAL` (precision > 15) |
| `"3.14159265358979323846"` (20 digits) | 20 | 20 | `BIG_DECIMAL` |
| `"1e-400"` | exponent exceeds | — | — | `UNBOUND_DECIMAL` |
| `"1e400"` | exponent exceeds | — | — | `UNBOUND_DECIMAL` |
| `"1.00"` (stripTrailingZeros to "1") | 1 | 0 | `FLOAT` → `DOUBLE` after scope |
| `"0"` (no fractional) | — | — | Not reached; integer branch |

Precision/scale envelopes verified against Cyoda Cloud constants at `ParserFunctions.kt:33-59`.

### 6.4 `CollapseNumeric` tests

| Input set | Expected |
|---|---|
| `{BYTE}` | `{BYTE}` |
| `{BYTE, SHORT, INTEGER, LONG}` | `{LONG}` |
| `{LONG, BIG_INTEGER}` | `{BIG_INTEGER}` |
| `{FLOAT, DOUBLE, BIG_DECIMAL}` | `{BIG_DECIMAL}` (the named example from brainstorming) |
| `{FLOAT, UNBOUND_DECIMAL}` | `{UNBOUND_DECIMAL}` |
| `{INTEGER, DOUBLE}` | `{BIG_DECIMAL}` (cross-family, all values fit) |
| `{LONG, FLOAT}` | `{BIG_DECIMAL}` |
| `{BIG_INTEGER, DOUBLE}` | `{BIG_DECIMAL}` |
| `{UNBOUND_INTEGER, DOUBLE}` | `{UNBOUND_DECIMAL}` |
| `{LONG, UNBOUND_DECIMAL}` | `{UNBOUND_DECIMAL}` |
| `{NULL}` | `{NULL}` |
| `{NULL, INTEGER}` | `{INTEGER}` |
| `{INTEGER, STRING}` | `{INTEGER, STRING}` (cross-kind preserved) |
| `{INTEGER, DOUBLE, STRING}` | `{BIG_DECIMAL, STRING}` |
| `{INTEGER, DOUBLE, STRING, NULL}` | `{BIG_DECIMAL, STRING}` |

### 6.5 `TypeSet.Add` integration tests

- Adding `BYTE` then `LONG` → `{LONG}`.
- Adding `INTEGER` then `DOUBLE` → `{BIG_DECIMAL}`.
- Adding `NULL` then `INTEGER` → `{INTEGER}`.
- Adding `INTEGER` then `STRING` → `{INTEGER, STRING}`.
- Adding `INTEGER`, `DOUBLE`, `STRING`, `NULL` in any order → `{BIG_DECIMAL, STRING}`.

### 6.6 `IsAssignableTo` tests (widening lattice)

From Cyoda Cloud's `DataType.kt:239-272`:

- `BYTE → SHORT, INTEGER, LONG, FLOAT, DOUBLE, BIG_INTEGER, BIG_DECIMAL, UNBOUND_INTEGER, UNBOUND_DECIMAL` all true.
- `INTEGER → FLOAT` — false (precision).
- `INTEGER → DOUBLE` — true.
- `LONG → DOUBLE` — false (precision).
- `LONG → BIG_DECIMAL` — true.
- `DOUBLE → UNBOUND_DECIMAL` — true; `DOUBLE → BIG_DECIMAL` — false.
- `FLOAT → DOUBLE, BIG_DECIMAL, UNBOUND_DECIMAL` — all true.
- Self-assignment: every type assigns to itself.
- `NULL → anything` — true.

### 6.7 Walker integration tests (`walker_test.go` additions)

- JSON `{"x": "3.14159265358979323846"}` → field `x` classified as `BIG_DECIMAL`. (Regression: current code returns `DOUBLE`.)
- JSON `{"x": 42}` with default `intScope=INTEGER` → `INTEGER`. (Matches current.)
- JSON `{"x": 42}` with `intScope=BYTE` → `BYTE`. (Matches current after port.)
- JSON `{"x": 9223372036854775808}` (2^63) → `BIG_INTEGER`.
- JSON `{"x": 1e400}` → `UNBOUND_DECIMAL`.
- JSON `{"x": null}` → `NULL`.
- JSON `{"x": "42"}` (string) → `STRING`. (No numeric autodiscovery from strings.)
- Successive walks merging observations: `{"x": 1}` then `{"x": "hello"}` → `{INTEGER, STRING}`.
- Successive walks merging numerics: `{"x": 1}` then `{"x": 1.5}` → `{BIG_DECIMAL}`.

### 6.8 Validation tests (`validate_test.go` additions)

- Schema: `INTEGER`. Data: `13` → accepts. Data: `13.111` → rejects with path + type info.
- Schema: `DOUBLE`. Data: `13` → accepts (integer fits decimal). Data: `13.111` → accepts. Data: `"hello"` → rejects.
- Schema: `BIG_DECIMAL`. Data: `13` → accepts. Data: `13.111` → accepts. Data: `3.14159265358979323846` → accepts.
- Schema: `UNBOUND_DECIMAL`. Data: any numeric → accepts.
- Schema: `{INTEGER, STRING}` (polymorphic). Data: `13` → accepts. Data: `"hello"` → accepts. Data: `13.111` → rejects (neither INTEGER nor STRING).
- Schema: `NULL`. Data: `null` → accepts. Data: `13` → rejects.

### 6.9 Test cases cross-referencing Cyoda Cloud sources

For each of the following Cyoda Cloud test files, at least one equivalent Go test asserts the same observable classification (or the documented cyoda-go divergence):

- `ParserFunctionsKtTest.kt:182-355` — raw node classification, Trino bounds.
- `DataTypeParsingTest.kt:27-416` — string parsing round-trip. In cyoda-go this is `ParseDecimal` round-trip + classifier.
- `JacksonParserTest.kt:738-790` — end-to-end. In cyoda-go this is `walker_test.go`.
- `DataTypeTest.kt:275-304` — collapse-to-common-type. In cyoda-go, `CollapseNumeric` tests replace this; Cyoda Cloud's STRING-fallback result is replaced by cross-family promotion to decimal. The divergence is documented in §5.4.
- `EntityInteractorIT.kt:843-875` — asymmetric compatibility (integer into decimal OK; decimal into integer NOT OK). In cyoda-go, `IsAssignableTo` tests realize this same asymmetry.

## 7. Implementation sequence

1. `decimal.go` + `decimal_test.go`. Stand-alone — no schema dependency. All §6.1 tests pass.
2. `numeric.go` + `numeric_test.go`. Depends on `decimal.go`. All §6.2–§6.4, §6.6 tests pass.
3. `types.go` — modify `TypeSet.Add` + add migration test for existing callers. §6.5 tests pass.
4. `walker.go` — replace numeric-inference path. §6.7 tests pass.
5. `validate.go` — swap `isCompatible` for `IsAssignableTo`. §6.8 tests pass.
6. Cross-reference pass — run every equivalent from §6.9, confirm match or document the intended divergence.
7. `docs/numeric-classification.md` — cyoda-go's policy document, citing `docs/numeric-type-classification-analysis.md` as the raw-classifier source and §5 of this spec as the cyoda-go divergence.

Each step is a RED/GREEN TDD cycle. Each step ends with a commit. The PR assembles all seven commits.

## 8. Risks

- **Walker behavior change visible to API clients.** A value previously classified `DOUBLE` may now classify `BIG_DECIMAL`. This is intentional and documented, but existing integration tests may need updates. Audit `internal/e2e/` and `plugins/*/conformance_test.go` during implementation.
- **Asymmetric validation breaks clients that relied on lenient numeric matching.** `13.111` against an `INTEGER` schema previously validated; now rejected. Intentional, aligns with Cyoda Cloud. Ingestion clients that hit this path get a clear error; cyoda-go tests that asserted the old behavior get updated.
- **`math/big.Int` performance.** Not a concern for classification (one parse + one envelope check per value). Would be a concern if we did arithmetic — we don't.
- **No library ≠ no bugs.** Hand-rolled `Decimal` must have exhaustive test coverage on parse edge cases (leading zeros, scientific notation, negative zero, precision boundaries). §6.1 lists the critical ones; implementation must add any boundary cases the hand-roll exposes.

## 9. Success criteria

- All tests in §6 pass.
- `go vet ./...`, `go test -race -short ./...` clean.
- `docs/numeric-classification.md` committed.
- At least one end-to-end ingestion test (existing `internal/e2e/model_extension_test.go` style) confirms a 20-digit decimal value round-trips as `BIG_DECIMAL` through the full HTTP stack.
- No regressions in existing tests that aren't justified by an intentional divergence documented in §8.

## 10. Follow-on work (not in this sub-project)

- **Sub-project A.2:** schema-transformation round-trip coverage. Builds on A.1.
- **Trino integration layer:** serialization of `BIG_DECIMAL` / `UNBOUND_DECIMAL` to Trino-native types. Likely lives in a new package `internal/trino/` or inside the Cassandra plugin. Out of scope here.
- **Arithmetic for decimals in cyoda-go:** explicitly rejected. All arithmetic delegated to Trino.
- **Numeric schema tightening** (e.g., narrowing `BIG_DECIMAL` back to `DOUBLE` when all observed values fit): out of scope; the collapse rule is monotone-up-only.
