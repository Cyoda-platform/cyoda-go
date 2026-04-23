# Sub-project A.1 — Numeric Classifier Parity with Cyoda Cloud

**Date:** 2026-04-21
**Revision:** 3 (post-review-02, 2026-04-21)
**Parent initiative:** Data-ingestion QA (Option 1 decomposition: sub-projects A, B, C, D)
**Predecessor context:** `docs/numeric-type-classification-analysis.md` — authoritative algorithm spec for Cyoda Cloud's numeric classifier.
**Reviews:**
- `docs/superpowers/reviews/2026-04-21-data-ingestion-qa-subproject-a1-review-01.md` (incorporated in rev 2)
- `docs/superpowers/reviews/2026-04-21-data-ingestion-qa-subproject-a1-review-02.md` (incorporated in rev 3)

## 1. Purpose

Port Cyoda Cloud's numeric classification algorithm to cyoda-go with sufficient behavioral parity to satisfy two hard requirements:

1. **Faithful reconstruction.** Every ingested numeric value round-trips through the schema/storage boundary without silent precision loss.
2. **Mathematically rigorous comparisons.** Search predicates on numeric fields (`=`, `<`, `<=`, `>`, `>=`) give exact answers regardless of the numeric family or precision.

cyoda-go does **not** aim for bit-identical schema output with Cyoda Cloud on every input. It aims for equivalent information content under a simpler and in places more principled model:
- One collapsed numeric type per field, not a polymorphic numeric set.
- Value-based integer/decimal classification, not syntactic (see §2.3).
- A smaller DataType enum — BYTE, SHORT, FLOAT dropped — because Cyoda Cloud's defaults promote past them and cyoda-go has no caller that configures narrower scopes.

The collapse is lossless — every value is still representable, every comparison still exact.

## 2. Scope

### 2.1 In scope

- Port the raw classifier (`ParserFunctions.kt:118-170` equivalent) to Go: value-based integer/decimal discriminator (see §2.3), precision/scale envelopes for decimals, Int128 predicates for `BIG_INTEGER` / `BIG_DECIMAL`.
- Port the widening lattice (`DataType.wideningConversionMap`) as a reference for `IsAssignableTo` checks used in validation, minus the dropped types.
- Introduce `CollapseNumeric(types []DataType) DataType` — cyoda-go's design choice that reduces any numeric-only set to exactly one `DataType`. Cross-family promotion to `BIG_DECIMAL` or `UNBOUND_DECIMAL` as specified by the collapse table in §5.
- Introduce a minimal hand-rolled `Decimal` type (`internal/domain/model/schema/decimal.go`) using `math/big.Int` + `int32 scale`. Supports parse, compare, serialize, `StripTrailingZeros`, `Precision`, `Scale`, `SetScale`, `IsInt128`. No arithmetic. Arbitrary precision via `math/big`.
- Asymmetric validation compatibility: integer values accepted against decimal schemas; decimal values rejected against integer schemas. Natural consequence of comparing classified type to schema type through the widening lattice.
- Preserve cross-kind polymorphism (e.g., `{INTEGER, STRING}` for a field that has seen both numeric and non-numeric values).
- Port `NULL` merge semantics: `NULL` disappears when merged with any concrete type; remains when alone.
- Fix the gRPC ingestion path at `internal/grpc/entity.go:35` so payload numerics preserve precision (currently decode through `json.Unmarshal` into `interface{}`, which routes numerics through `float64` and truncates above 2^53). Switch that site to a `json.UseNumber()` decoder. This fix is required for A.1 to satisfy its invariants.
- Drive the port via red/green TDD. Tests derived from Cyoda Cloud's test classes and the analysis-doc edge-case table become the RED spec.

### 2.2 Out of scope

- `Polymorphic`, `ComparableDataType`, `findCommonDataType` equivalents. Replaced by `CollapseNumeric`.
- Trino serialization details for `BIG_DECIMAL` / `UNBOUND_DECIMAL`. A future Trino integration layer or the Cassandra plugin owns that concern.
- Arithmetic on `Decimal` values. All arithmetic delegated to Trino / downstream consumers.
- Changes to `ChangeLevel` enforcement or the extension operator — that's Sub-project A.2.
- Changes to any storage plugin. The classifier produces the DataType; plugins store opaque bytes with a type tag. Plugins never re-classify.
- Cyoda Cloud's `ParsingSpec.parseStrings` and `alsoSaveInStrings` knobs. cyoda-go does not port these.

### 2.3 Non-requirements and intentional divergences

These are explicit cyoda-go divergences from Cyoda Cloud — decisions taken on principle, documented here so future readers can trace the intent.

**Schema parity across storage engines.** A user migrating between plugins performs a data migration; the migration may re-classify. cyoda-go does not guarantee byte-identical schemas across plugins.

**Compatibility with Cyoda Cloud's polymorphic numeric output.** A field Cyoda Cloud represents as `{FLOAT, DOUBLE, BIG_DECIMAL}` becomes `BIG_DECIMAL` in cyoda-go. Lossless, smaller, equally searchable. See §5 for the full collapse rule and rationale.

**FLOAT, BYTE, SHORT dropped from the DataType enum.** Cyoda Cloud's defaults (`decimalScope=DOUBLE`, `intScope=INTEGER`) promote past these types on every free-discovery path; cyoda-go has no caller that sets a narrower scope, so the types would be dead code. Removing them simplifies the classifier, the widening lattice, and the test matrix. Values Cyoda Cloud classifies as FLOAT classify as DOUBLE in cyoda-go; values Cyoda Cloud classifies as BYTE or SHORT classify as INTEGER. No `PromoteToScope` function is needed.

**Value-based integer/decimal split, not syntactic.** Cyoda Cloud routes on Jackson node kind (`IntNode` vs `DecimalNode`), which is a leaky abstraction over JSON grammar: `"1.0"` and `"1e0"` both denote the integer 1 but take different classifier branches because Jackson built different node types. cyoda-go classifies by value — any whole-number literal classifies via the integer branch regardless of source syntax. `"1"`, `"1.0"`, `"1.00"`, `"1e0"`, `"10e-1"` all classify as `INTEGER`. A literal with a non-zero fractional part — `"0.1"`, `"1.5"` — classifies via the decimal branch.

**`findCommonDataType` STRING fallback is a fix, not a neutral divergence.** Cyoda Cloud's `findCommonDataType` returns STRING when a polymorphic numeric set has no common type — silently corrupting user data into an unsortable, uncomparable representation. cyoda-go replaces this with `CollapseNumeric`'s cross-family promotion to BIG_DECIMAL or UNBOUND_DECIMAL. The cyoda-go behavior is strictly better: lossless, searchable, and mathematically correct.

## 3. Hard requirements, restated as invariants

For every ingested scalar value `v` (via any supported entry point — HTTP, gRPC, CloudEvent):

**(I1) Classification is deterministic.** Two identical inputs classify to the same `DataType` regardless of entry point, caller, or current schema state.

**(I2) Reconstruction is lossless in numerical value.** For any value `v` classified as `DataType T`, the storage layer can reconstruct a value **numerically equal** to `v`. Original textual representation (trailing zeros, exponent form) is not preserved — `"1.200"` round-trips as `"1.2"` — but the numeric value is exact.

**(I3) Comparisons are exact.** For any two values `v1`, `v2` classified into the same numeric DataType (post-collapse), `compare(v1, v2)` returns the mathematically correct result: negative, zero, or positive.

**(I4) The collapsed `DataType` of a field's `TypeSet` can losslessly represent every value ever observed at that field.**

**(I5) Polymorphism across kinds is preserved.** A field that has seen both numeric and string values carries a `TypeSet` with one collapsed numeric entry plus any non-numeric entries.

All invariants are stated unconditionally. A fallback path that punctures any of them is a bug to fix, not an exception to document. The gRPC ingestion fix (§2.1) is required because without it I1/I2 fail on the gRPC entry point.

## 4. Architecture

Four new/modified units. All within `internal/domain/model/schema/`, `internal/domain/model/importer/`, and one fix in `internal/grpc/entity.go`.

### 4.1 `internal/domain/model/schema/decimal.go` — minimal decimal type

```go
// Decimal is a fixed-scale arbitrary-precision decimal.
// Value = unscaled × 10^(-scale).
// Scale may be negative (e.g., 1e2 has unscaled=1, scale=-2).
// No arithmetic — cyoda-go delegates arithmetic to Trino.
type Decimal struct {
    unscaled *big.Int
    scale    int32
}

func ParseDecimal(s string) (Decimal, error)
func (d Decimal) IsZero() bool
func (d Decimal) Sign() int
func (d Decimal) StripTrailingZeros() Decimal
func (d Decimal) Precision() int                    // Java BigDecimal.precision: ≥ 1; returns 1 for zero
func (d Decimal) Scale() int32
func (d Decimal) SetScale(newScale int32) (Decimal, error)  // errors if downward scale would lose precision
func (d Decimal) Unscaled() *big.Int                // defensive copy
func (d Decimal) IsInt128() bool                    // unscaled fits signed Int128: [-2^127, 2^127-1]
func (d Decimal) Cmp(other Decimal) int             // exact comparison, no rounding
func (d Decimal) Canonical() string                 // round-trippable plain decimal, no scientific form
func (d Decimal) MarshalJSON() ([]byte, error)
func (d *Decimal) UnmarshalJSON([]byte) error
```

Implementation notes:

- `ParseDecimal` accepts `"123"`, `"123.456"`, `"1.5e2"`, `"1.5E-2"`, `"-.5"`, `".5"`, `"1e0"`, with optional leading `+`/`-`. Rejects `NaN`, `Infinity`, `+Infinity`, `-Infinity`, empty string, and malformed forms like `"1.2.3"`, `"1e"`, `"abc"`.
- `StripTrailingZeros` matches Java `BigDecimal.stripTrailingZeros()`:
  - `"1.200"` → unscaled=12, scale=1 (value 1.2).
  - `"100"` → unscaled=1, scale=-2 (value 100).
  - `"0"` and `"0.0"` both → unscaled=0, scale=0.
  - `"1.5"` → unchanged.
- `Precision` matches Java: returns 1 for zero (not 0). For nonzero values, equals the number of digits in `|unscaled|`.
- `Scale` matches Java `BigDecimal.scale()`: negative allowed for `1e2`-style inputs.
- `SetScale(n)`:
  - `n == scale` → no-op, returns `(d, nil)`.
  - `n > scale` → multiply unscaled by `10^(n-scale)`, returns `(d', nil)`. Always succeeds.
  - `n < scale` → succeeds only if unscaled is divisible by `10^(scale-n)`. Otherwise returns an error describing the precision-loss condition.
  - Negative `n` permitted (matches Java).
- `IsInt128` compares the unscaled `big.Int` against pre-computed signed Int128 bounds `[-2^127, 2^127-1]`. Does not rely on `BitLen() <= 127` — that check incorrectly rejects `-2^127` (whose `BitLen()` is 128).
- `Cmp` normalizes to a common scale (larger of the two) via upward `SetScale`, then `big.Int.Cmp`. Exact; no rounding modes.
- `Canonical` formats as plain decimal with no scientific notation. Round-trips through `ParseDecimal`.

### 4.2 `internal/domain/model/schema/numeric.go` — classifier + collapse

```go
// Numeric envelopes from Cyoda Cloud's ParserFunctions.kt:33-59.
// The "exp" constants refer to precision - scale — the decimal
// "characteristic," the magnitude of the unscaled-int-at-scale-18
// representation. This matches Trino's fixed-scale-18 BIG_DECIMAL
// storage format where the unscaled value must fit Int128.
const (
    doubleMaxPrecision     = 15
    doubleMaxAbsScale      = 292
    bigDecimalMaxScale     = 18
    bigDecimalDefinitePrec = 38   // precision ≤ 38 AND exp ≤ 20 → definite fit
    bigDecimalDefiniteExp  = 20   // exp = precision - scale
    bigDecimalLoosePrec    = 39   // precision ≤ 39 AND exp ≤ 21 → verify via setScale(18).unscaledValue().IsInt128()
    bigDecimalLooseExp     = 21
)

// NumericFamily returns 1 for integer-family, 2 for decimal-family, 0 otherwise.
// Post-drop DataTypes: INTEGER/LONG/BIG_INTEGER/UNBOUND_INTEGER (family 1);
// DOUBLE/BIG_DECIMAL/UNBOUND_DECIMAL (family 2).
func NumericFamily(dt DataType) int

// NumericRank is the widening-order position within a family.
func NumericRank(dt DataType) int

// IsNumeric reports whether dt is in either numeric family.
func IsNumeric(dt DataType) bool

// ClassifyInteger classifies a whole-number value into INTEGER, LONG,
// BIG_INTEGER, or UNBOUND_INTEGER by magnitude. Matches the Cyoda Cloud
// integer-family logic at ParserFunctions.kt:133-155 minus BYTE/SHORT.
func ClassifyInteger(v *big.Int) DataType

// ClassifyDecimal classifies a non-whole-number decimal value into
// DOUBLE, BIG_DECIMAL, or UNBOUND_DECIMAL. Input MUST be the result of
// StripTrailingZeros. Matches Cyoda Cloud's precision/scale envelope
// logic at ParserFunctions.kt:121-130, with the two-tier definite/loose
// BIG_DECIMAL fit:
//   - DOUBLE if precision ≤ 15 and |scale| ≤ 292.
//   - BIG_DECIMAL definite if precision ≤ 38 and (precision - scale) ≤ 20
//     AND scale ≤ 18.
//   - BIG_DECIMAL loose if precision ≤ 39 and (precision - scale) ≤ 21
//     AND scale ≤ 18 AND SetScale(18).Unscaled().IsInt128().
//   - Otherwise UNBOUND_DECIMAL.
func ClassifyDecimal(d Decimal) DataType

// IsAssignableTo reports whether dataT can losslessly assign into
// schemaT per the widening lattice (DataType.kt:239-272, minus dropped
// types). Used by Validate. NULL assigns to any type.
func IsAssignableTo(dataT, schemaT DataType) bool

// CollapseNumeric reduces a numeric-only set to a single DataType per
// the collapse rule (see §5). Caller is responsible for filtering out
// non-numeric members and NULL — see TypeSet.Add.
//
// Preconditions:
//   - types is non-empty.
//   - Every element of types has IsNumeric(t) == true.
func CollapseNumeric(types []DataType) DataType
```

### 4.3 `internal/domain/model/schema/types.go` — `DataType` enum + `TypeSet.Add`

**Enum changes.** Remove `Byte`, `Short`, `Float` constants. Remove any references (godoc, `dataTypeNames` map, `ParseDataType` fallthroughs). Renumber iota if necessary — this is acceptable because the enum is cyoda-go-internal (not SPI-exposed) and has no external consumers.

**`TypeSet.Add` behavior.**

On each `Add(dt)`:
1. If `dt == NULL` and the set already contains concrete types, skip (NULL drops).
2. If `dt` is a concrete type and the set contains only `NULL`, remove `NULL`.
3. Add `dt` as before.
4. Partition the set: numeric members `N`, non-numeric members `P`.
5. If `|N| >= 2`: replace `N` with `{CollapseNumeric(N)}`.
6. Non-numeric members are untouched.

Invariant after every `Add`: the set contains at most one numeric DataType, any number of non-numeric DataTypes, and `NULL` only if no concrete type is present.

### 4.4 `internal/domain/model/importer/walker.go` — classifier integration

The branching logic is value-based, not syntactic:

1. Receive `json.Number` via UseNumber-backed decoder.
2. Parse via `ParseDecimal`.
3. Apply `StripTrailingZeros`.
4. If the result has `scale <= 0` (i.e., no fractional component remains after stripping), the value is a whole number: extract `*big.Int` = unscaled × 10^(-scale), call `ClassifyInteger`.
5. Otherwise, the value has a non-zero fractional part: call `ClassifyDecimal`.

Under this rule:
- `"1.00"` → `StripTrailingZeros` starts at scale=2, strips two zeros → unscaled=1, scale=0 → whole number → `ClassifyInteger(1)` → `INTEGER`. **Not** a decimal branch.
- `"100"` → `StripTrailingZeros` starts at scale=0, strips two zeros → unscaled=1, scale=-2 → whole number → big.Int = 1 × 10^2 = 100 → `ClassifyInteger(100)` → `INTEGER`.
- `"1e0"` → unscaled=1, scale=0 → whole number → `INTEGER`.
- `"10e-1"` → stripped → unscaled=1, scale=0 → `INTEGER`.
- `"0.1"` → unscaled=1, scale=1 → fractional → `ClassifyDecimal` → `DOUBLE`.
- `"3.141592653589793238"` (18 fractional digits) → unscaled = 19-digit integer, scale=18 → fractional → `ClassifyDecimal` → precision=19, exp=1 — fails DOUBLE (precision > 15), definite fit passes (precision ≤ 38, exp ≤ 20, scale ≤ 18) → `BIG_DECIMAL`.
- `"3.14159265358979323846"` (20 fractional digits) → unscaled = 21-digit integer, scale=20 → fractional → `ClassifyDecimal` → scale=20 > 18 fails BIG_DECIMAL's scale bound (both definite and loose) → `UNBOUND_DECIMAL`.

No `PromoteToScope` call anywhere. No `float64` fallback. No `WalkConfig.IntScope` / `DecimalScope` fields — those configurations disappear with BYTE/SHORT/FLOAT.

The `float64` case in `walker.walkValue` is **deleted**, not replaced. If a caller somehow delivers raw `float64` values (bypassing UseNumber), the walker returns an error rather than silently guessing — this fails fast at the boundary rather than admitting a precision-lossy path.

### 4.5 `internal/grpc/entity.go:35` — UseNumber fix (scoped into A.1)

Current code at entity.go:35:
```go
var req events.EntityCreateRequestJson
if err := json.Unmarshal(payload, &req); err != nil { ... }
```

`events.EntityCreateRequestJson.Payload.Data` is `interface{}`, so `json.Unmarshal` decodes numerics as `float64` — truncating integers above 2^53. That data is then re-marshaled and passed to the entity handler, which uses UseNumber but has already lost precision.

Replacement:
```go
dec := json.NewDecoder(bytes.NewReader(payload))
dec.UseNumber()
var req events.EntityCreateRequestJson
if err := dec.Decode(&req); err != nil { ... }
```

Applies at every gRPC dispatch site that unmarshals a CloudEvent body whose payload eventually reaches the entity handler. Grep for `json.Unmarshal(payload, &req)` patterns in `internal/grpc/`; audit each for whether the decoded value crosses into ingestion.

### 4.6 `internal/domain/model/schema/validate.go` — assignability check

Replace `isCompatible(dataT, modelT)` that returns `true` for any numeric pair, with a call to `IsAssignableTo(dataT, modelT)`. This realizes Cyoda Cloud's asymmetric behavior:

- `"13"` (classified `INTEGER`) against a `DOUBLE` schema: `IsAssignableTo(INTEGER, DOUBLE)` = true per the lattice. Accepted.
- `"13.111"` (classified `DOUBLE`) against an `INTEGER` schema: `IsAssignableTo(DOUBLE, INTEGER)` = false. Rejected at strict validation.

Extension behavior at non-empty `ChangeLevel` — e.g., whether `INTEGER` schema plus `"13.111"` input at `ChangeLevel = Type` produces a widened `BIG_DECIMAL` schema — is downstream of this sub-project. A.2 owns the `Extend → Diff → Apply` pipeline that uses the collapse rule to widen schemas during ingestion. A.1 produces only the strict-validate semantics.

## 5. `CollapseNumeric` — the cyoda-go design choice

Formal specification. Given a **numeric-only, non-empty** slice of `DataType`:

### 5.1 Same-family collapse

If all members are in the integer family: return the single member with the highest `NumericRank`.
If all members are in the decimal family: return the single member with the highest `NumericRank`.

### 5.2 Cross-family collapse

If members span both families: the result is the narrowest decimal-family type that losslessly contains every observed integer-family member:

| Widest integer observed | Widest decimal observed | Collapse |
|---|---|---|
| `INTEGER` / `LONG` | `DOUBLE` / `BIG_DECIMAL` | `BIG_DECIMAL` |
| `INTEGER` / `LONG` | `UNBOUND_DECIMAL` | `UNBOUND_DECIMAL` |
| `BIG_INTEGER` | `DOUBLE` / `BIG_DECIMAL` | `BIG_DECIMAL` |
| `BIG_INTEGER` | `UNBOUND_DECIMAL` | `UNBOUND_DECIMAL` |
| `UNBOUND_INTEGER` | any | `UNBOUND_DECIMAL` |

Six cross-family cells (3 integer classes × 2 decimal classes, roughly — table collapses equivalent rows).

### 5.3 Precondition on input

`CollapseNumeric` requires non-empty numeric-only input. `NULL` and non-numeric members must be filtered by the caller. `TypeSet.Add` (§4.3) owns this responsibility; no other caller is expected.

### 5.4 Rationale — why cross-family promotes into decimal, not STRING

Cyoda Cloud's `findCommonDataType` falls back to `STRING` when no common widening target exists. That is a bug, not a neutral design choice: it silently converts numeric data into an unsortable, uncomparable representation. A field that sees `13` and `13.111` under Cyoda Cloud becomes STRING; a user who runs a range predicate on that field gets lexicographic comparison, not numeric.

cyoda-go replaces this with cross-family promotion to a decimal type:

- Every integer value of any observed integer-family type is exactly representable in the target decimal type (`BIG_DECIMAL` holds integer values up to 2^127-1 at scale 0; `UNBOUND_DECIMAL` is arbitrary precision).
- The reverse (promoting into integer) cannot preserve fractional values.
- Search-predicate semantics on the collapsed decimal type remain mathematically rigorous: `Decimal.Cmp` gives exact answers.
- Storage cost increases moderately (decimal-family values use `Decimal` representation; integer-family values use `big.Int`). Acceptable for the correctness gain.

### 5.5 Precision-envelope tags are not storage formats

`DOUBLE`, `BIG_DECIMAL`, and `UNBOUND_DECIMAL` are precision-envelope schema tags, not storage formats. All numeric values are held as `Decimal` regardless of tag — the tag describes which precision envelope the value falls into, not the binary representation used to store it. A value tagged `DOUBLE` in cyoda-go is not IEEE 754 binary — it is a `Decimal` with precision ≤ 15 and |scale| ≤ 292.

### 5.6 Cross-kind polymorphism preserved

Non-numeric members (`STRING`, `BOOLEAN`, `BYTE_ARRAY`, date/time types, `UUID`, `UNKNOWN`) are outside `CollapseNumeric`'s input. They remain in the `TypeSet` alongside the single collapsed numeric member.

A field whose history is `{INTEGER, DOUBLE, STRING, NULL}` ends up as `{BIG_DECIMAL, STRING}`.

### 5.7 Value-based classification — second intentional divergence

`CollapseNumeric`'s divergence from Cyoda Cloud's `findCommonDataType` is one of two documented cyoda-go divergences in this sub-project.

The second — value-based integer/decimal classification at the walker layer — is listed at §2.3. It is not a collapse-rule concern but an input-classification concern. Together they make cyoda-go's model strictly more principled than Cyoda Cloud's: identical values classify identically regardless of source syntax, and mixed-kind fields never silently degrade to STRING.

## 6. Test specification

Every test case below becomes a named Go test. The test suite is the executable contract for this sub-project.

### 6.1 `Decimal` type tests (`decimal_test.go`)

- `ParseDecimal`:
  - `"0"`, `"0.0"`, `"-0"`, `"-0.0"`, `"0.1"`, `"123.456"`, `"1.5e2"`, `"1.5E-2"`, `"-.5"`, `".5"`, `"1e0"`, `"1.0"`, `"1.00"` all parse correctly with expected `(unscaled, scale)`.
  - `"NaN"`, `"Infinity"`, `"+Infinity"`, `"-Infinity"`, `""`, `"abc"`, `"1.2.3"`, `"1e"`, `"1..2"` all error.
- `StripTrailingZeros`:
  - `"1.200"` → unscaled=12, scale=1 (value 1.2). (Matches Java `BigDecimal("1.200").stripTrailingZeros()`.)
  - `"100"` → unscaled=1, scale=-2 (value 100).
  - `"0"` and `"0.0"` both → unscaled=0, scale=0.
  - `"1.5"` → unchanged.
- `Precision`:
  - `ParseDecimal("0").Precision()` == 1. (Matches Java.)
  - `ParseDecimal("1").Precision()` == 1.
  - `ParseDecimal("10").Precision()` == 2.
  - `ParseDecimal("12345").Precision()` == 5.
  - `ParseDecimal("-12345").Precision()` == 5.
- `Scale`:
  - Matches Java `BigDecimal.scale()`; negative allowed for `"1e2"` form.
- `SetScale`:
  - `n == scale` → `(d, nil)`, no-op.
  - `n > scale` → multiply by `10^(n-scale)`, `(d', nil)`.
  - `n < scale` with unscaled divisible by `10^(scale-n)` → `(d', nil)`, unscaled divided.
  - `n < scale` with non-zero trailing digits → returns `error` describing the precision-loss.
  - Negative `n` allowed and handled like any other scale.
- `IsInt128`:
  - `2^127 - 1` → true.
  - `2^127` → false.
  - `-2^127` → true. *(critical boundary — the naive `BitLen() <= 127` check fails this; implementation must use signed Int128 range comparison.)*
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

Input `*big.Int` → Expected DataType:

| Input | Expected |
|---|---|
| `0`, `-1`, `127`, `128`, `-128`, `-129`, `32767`, `32768`, `2^31 - 1` | `INTEGER` (no BYTE/SHORT after drop) |
| `2^31` | `LONG` |
| `2^63 - 1` | `LONG` |
| `2^63` | `BIG_INTEGER` |
| `2^127 - 1` | `BIG_INTEGER` |
| `2^127` | `UNBOUND_INTEGER` |
| `10^30` | `UNBOUND_INTEGER` |

### 6.3 `ClassifyDecimal` tests

Input `Decimal` (assume already `StripTrailingZeros`'d and fractional — whole numbers route to `ClassifyInteger`):

| Source | unscaled / scale | precision / exp | Classified as |
|---|---|---|---|
| `"0.1"` | 1 / 1 | 1 / 0 | `DOUBLE` |
| `"1.5"` | 15 / 1 | 2 / 1 | `DOUBLE` |
| `"0.123456789012345"` (15 fractional) | 15-digit / 15 | 15 / 0 | `DOUBLE` (precision boundary: == 15) |
| `"0.1234567890123456"` (16 fractional) | 16-digit / 16 | 16 / 0 | `BIG_DECIMAL` (precision > 15, scale ≤ 18) |
| `"3.141592653589793238"` (18 fractional) | 19-digit / 18 | 19 / 1 | `BIG_DECIMAL` (definite fit: precision ≤ 38, exp ≤ 20, scale ≤ 18) |
| `"3.14159265358979323846"` (20 fractional) | 21-digit / 20 | 21 / 1 | `UNBOUND_DECIMAL` (scale=20 > 18 — both definite and loose BIG_DECIMAL fits require scale ≤ 18) |
| `"1e-400"` | 1 / 400 | 1 / -399 | `UNBOUND_DECIMAL` (scale=400 exceeds DOUBLE 292-max *and* BIG_DECIMAL 18-max) |
| `"1e400"` | 1 / -400 | 1 / 401 | `UNBOUND_DECIMAL` (exp=401 way past DOUBLE and BIG_DECIMAL loose exp=21) |

Boundary tests for the definite/loose `BIG_DECIMAL` fit (concrete values so the "is this combination even possible?" question is moot):

| Specific input | Expected | Why |
|---|---|---|
| unscaled = `10^37` (38 digits), scale=18 | `BIG_DECIMAL` | precision=38, exp=20, scale=18 — definite fit |
| unscaled = `2^127 - 1` (39 digits), scale=18, `IsInt128()` → true | `BIG_DECIMAL` | precision=39, exp=21, scale=18 — loose fit: Int128 check passes |
| unscaled = `2^127` (39 digits), scale=18, `IsInt128()` → false | `UNBOUND_DECIMAL` | loose fails Int128 |
| unscaled = `10^39` (40 digits), scale=18 | `UNBOUND_DECIMAL` | precision exceeds loose cap 39 |
| unscaled = `1`, scale=-22 (exp=23) | `UNBOUND_DECIMAL` | both definite (exp > 20) and loose (exp > 21) fail on exp |
| unscaled = `10^37` (38 digits), scale=19 | `UNBOUND_DECIMAL` | precision=38, scale=19 > 18 — scale bound fails |

### 6.4 `CollapseNumeric` tests

| Input set | Expected (single DataType) |
|---|---|
| `{INTEGER}` | `INTEGER` |
| `{INTEGER, LONG}` | `LONG` |
| `{LONG, BIG_INTEGER}` | `BIG_INTEGER` |
| `{BIG_INTEGER, UNBOUND_INTEGER}` | `UNBOUND_INTEGER` |
| `{DOUBLE, BIG_DECIMAL}` | `BIG_DECIMAL` (the brainstorming named example collapses to BIG_DECIMAL after FLOAT-drop) |
| `{DOUBLE, UNBOUND_DECIMAL}` | `UNBOUND_DECIMAL` |
| `{INTEGER, DOUBLE}` | `BIG_DECIMAL` (cross-family, integer fits BIG_DECIMAL losslessly) |
| `{LONG, DOUBLE}` | `BIG_DECIMAL` |
| `{BIG_INTEGER, DOUBLE}` | `BIG_DECIMAL` (BIG_INTEGER up to 2^127-1 fits Int128) |
| `{BIG_INTEGER, UNBOUND_DECIMAL}` | `UNBOUND_DECIMAL` |
| `{UNBOUND_INTEGER, DOUBLE}` | `UNBOUND_DECIMAL` |
| `{LONG, UNBOUND_DECIMAL}` | `UNBOUND_DECIMAL` |
| `{INTEGER, LONG, DOUBLE, BIG_DECIMAL}` | `BIG_DECIMAL` (3-way+) |

### 6.5 `TypeSet.Add` integration tests

- Adding `INTEGER` then `LONG` → `{LONG}`.
- Adding `INTEGER` then `DOUBLE` → `{BIG_DECIMAL}`.
- Adding `NULL` then `INTEGER` → `{INTEGER}`.
- Adding `INTEGER` then `NULL` → `{INTEGER}`.
- Adding `INTEGER` then `STRING` → `{INTEGER, STRING}`.
- Adding `INTEGER`, `DOUBLE`, `STRING`, `NULL` in any order → `{BIG_DECIMAL, STRING}`.
- Migration regression: `TypeSet` that contains only non-numeric members behaves identically before and after the change. `{STRING, BOOLEAN}` remains `{STRING, BOOLEAN}` under any `Add` order.

### 6.6 `IsAssignableTo` tests (widening lattice after type drops)

Lattice subset for the remaining DataTypes, from `DataType.kt:239-272` minus dropped types:

- `INTEGER → LONG, DOUBLE, BIG_INTEGER, BIG_DECIMAL, UNBOUND_INTEGER, UNBOUND_DECIMAL` — all true. INTEGER's 2^31 range fits Double's 53-bit mantissa, so `INTEGER → DOUBLE` is lossless.
- `LONG → BIG_INTEGER, BIG_DECIMAL, UNBOUND_INTEGER, UNBOUND_DECIMAL` — all true.
- `LONG → DOUBLE` — **false** — Cyoda Cloud's documented lattice restriction (`DataType.kt:253-268`); 2^63 exceeds Double's 53-bit mantissa.
- `DOUBLE → UNBOUND_DECIMAL` — true. `DOUBLE → BIG_DECIMAL` — **false** (precision/scale envelopes differ).
- `BIG_INTEGER → UNBOUND_INTEGER, UNBOUND_DECIMAL` — true.
- `UNBOUND_INTEGER → UNBOUND_DECIMAL` — true.
- `BIG_DECIMAL → UNBOUND_DECIMAL` — true.
- `UNBOUND_DECIMAL → (nothing)` — self only.
- Self-assignment: every type assigns to itself.
- `NULL → anything` — true.

### 6.7 Walker integration tests (`walker_test.go` additions)

- `"3.141592653589793238"` (18 fractional digits, JSON number) → `BIG_DECIMAL`. *(Regression: current code returns `DOUBLE`.)*
- `"3.14159265358979323846"` (20 fractional digits, JSON number) → `UNBOUND_DECIMAL`. *(Regression: current code returns `DOUBLE` via `strconv.ParseFloat` silent rounding.)*
- `"42"` (unquoted, JSON number) → `INTEGER`.
- `"9223372036854775808"` (2^63) → `BIG_INTEGER`.
- `"1e400"` → `UNBOUND_DECIMAL`.
- `"null"` → `NULL`.
- `"42"` (quoted, JSON string) → `STRING`. (No numeric autodiscovery from strings.)
- `"1.0"` (JSON number literal) → `INTEGER`. *(Value-based classification — diverges from Cyoda Cloud.)*
- `"1e0"` → `INTEGER`.
- `"10e-1"` → `INTEGER`.
- `"0.1"` → `DOUBLE`.
- `"1.5"` → `DOUBLE`.
- `"1.00"` → `INTEGER`. *(Whole number after `StripTrailingZeros`.)*
- Successive walks merging observations: `{"x": 1}` then `{"x": "hello"}` → `{INTEGER, STRING}`.
- Successive walks merging numerics: `{"x": 1}` then `{"x": 1.5}` → `{BIG_DECIMAL}`.

### 6.8 gRPC precision preservation tests (`internal/grpc/entity_test.go`)

- CloudEvent payload with `{"x": 9007199254740993}` (2^53 + 1):
  - Before fix: x is decoded as `float64` 9007199254740992.0; precision lost.
  - After fix: x is preserved as `json.Number("9007199254740993")`; walker classifies as `LONG`.
- CloudEvent payload with `{"x": 12345678901234567890}` (exceeds int64):
  - Before fix: decoded as `float64`, loss of precision.
  - After fix: preserved; walker classifies as `BIG_INTEGER` or `UNBOUND_INTEGER` depending on Int128 fit.
- CloudEvent payload with `{"x": 3.141592653589793238}` (unquoted JSON number, 18 fractional digits):
  - Before fix: decoded as `float64`, truncated to ~15 digits.
  - After fix: preserved; walker classifies as `BIG_DECIMAL`.
- CloudEvent payload with `{"x": 3.14159265358979323846}` (unquoted JSON number, 20 fractional digits):
  - Before fix: decoded as `float64`, truncated to ~15 digits.
  - After fix: preserved; walker classifies as `UNBOUND_DECIMAL` (scale=20 exceeds BIG_DECIMAL's scale-18 bound).

### 6.9 Validation tests (`validate_test.go` additions)

- Schema: `INTEGER`. Data: `13` → accepts. Data: `13.111` → rejects with path + type info.
- Schema: `DOUBLE`. Data: `13` → accepts (integer fits decimal). Data: `13.111` → accepts. Data: `"hello"` → rejects.
- Schema: `BIG_DECIMAL`. Data: `13` → accepts. Data: `13.111` → accepts. Data: `3.14159265358979323846` → accepts.
- Schema: `UNBOUND_DECIMAL`. Data: any numeric → accepts.
- Schema: `{INTEGER, STRING}` (polymorphic). Data: `13` → accepts. Data: `"hello"` → accepts. Data: `13.111` → rejects (neither INTEGER nor STRING).
- Schema: `NULL`. Data: `null` → accepts. Data: `13` → rejects.

### 6.10 Test cases cross-referencing Cyoda Cloud sources

For each of the following Cyoda Cloud test files, at least one equivalent Go test asserts the same observable classification (or the documented cyoda-go divergence):

- `ParserFunctionsKtTest.kt:182-355` — raw node classification, Trino bounds. **Divergences:** cyoda-go classifies `"1.0"`-style whole-number decimals as `INTEGER` (not `DOUBLE` after FLOAT-promotion). Cases that depend on FLOAT or BYTE/SHORT outputs map to the cyoda-go replacement type per §2.3.
- `DataTypeParsingTest.kt:27-416` — string parsing round-trip. In cyoda-go this is `ParseDecimal` round-trip + classifier.
- `JacksonParserTest.kt:738-790` — end-to-end. In cyoda-go this is `walker_test.go`. Cases using Java `Long` inputs test the same integer-family boundaries but don't have 1:1 cyoda-go equivalents (cyoda-go has no `IntNode`/`LongNode` distinction); reframe as value-magnitude tests.
- `DataTypeTest.kt:275-304` — collapse-to-common-type. In cyoda-go, `CollapseNumeric` tests replace this; Cyoda Cloud's STRING-fallback result is replaced by cross-family promotion to decimal (see §5.4 — this is a fix, not a neutral divergence).
- `EntityInteractorIT.kt:843-875` — asymmetric compatibility (integer into decimal OK; decimal into integer NOT OK). In cyoda-go, `IsAssignableTo` tests realize this same asymmetry.

## 7. Implementation sequence

Each step is a RED/GREEN TDD cycle. Each step ends with a commit. The PR assembles all seven commits.

1. **`decimal.go` + `decimal_test.go`.** Stand-alone — no schema dependency. All §6.1 tests pass.
2. **`numeric.go` + `numeric_test.go`.** Depends on `decimal.go`. All §6.2–§6.4 and §6.6 tests pass.
3. **`types.go`.** Drop FLOAT/BYTE/SHORT enum values, renumber iota, update `dataTypeNames`, `ParseDataType`, etc. Modify `TypeSet.Add`. Migration regression test: "`TypeSet` containing only non-numerics behaves identically before and after." §6.5 tests pass. Other packages depending on the dropped enum values get compile errors and are updated at this step.
4. **`walker.go`.** Replace numeric-inference path with value-based `ParseDecimal` → `StripTrailingZeros` → `ClassifyInteger`/`ClassifyDecimal`. Delete `float64` branch. Delete `WalkConfig.IntScope` / `DecimalScope` fields. §6.7 tests pass.
5. **`internal/grpc/entity.go:35`.** Replace `json.Unmarshal(payload, &req)` with UseNumber-based decode. Audit and fix every other gRPC dispatch site that crosses into ingestion. §6.8 tests pass.
6. **`validate.go`.** Swap `isCompatible` for `IsAssignableTo`. §6.9 tests pass.
7. **Cross-reference + audit pass.** Run every equivalent from §6.10; confirm match or document the intended divergence. Audit `internal/e2e/`, `plugins/*/conformance_test.go`, `e2e/parity/`, and `test/recon/` for tests that asserted the old numeric behavior; update them to the new expected results (with PR-description explanation for each change).

## 8. Risks

- **Walker behavior change visible to API clients.** A value previously classified `DOUBLE` may now classify `BIG_DECIMAL` or `INTEGER` (the latter for `"1.0"`-style inputs under value-based classification). This is intentional and documented; existing integration tests may need updates. Audit `internal/e2e/`, `plugins/*/conformance_test.go`, `e2e/parity/`, and `test/recon/` during implementation.
- **Asymmetric validation breaks clients that relied on lenient numeric matching.** `13.111` against an `INTEGER` schema previously validated; now rejected. Intentional, aligns with Cyoda Cloud. The old lenient behavior was a latent bug (silent precision loss); the rejection is correct. Release notes must call this out.
- **A.1 ships independently of A.2** (decided). A.2 adds the extension-via-widen behavior at `ChangeLevel != ""` but does not soften A.1's strict-validate rejection at `ChangeLevel = ""` — a decimal value against an `INTEGER` schema stays rejected at strict validate regardless of A.2. Bundling therefore buys nothing for the strict-validate case, and blocking A.1 on A.2's larger scope (generator infrastructure, property tests, Axis-2 matrix) would delay the correctness fix without mitigating the client-visible behavior change. Release notes accompany A.1 to flag the intended behavior shift.
- **DataType enum value removal is a cyoda-go-internal change only.** No external consumers of the module (confirmed). Enum-iota renumbering affects any test that pattern-matches on integer DataType values rather than constant names; audit at step 3.
- **`math/big.Int` performance.** Not a concern for classification (one parse + one envelope check per value). Would be a concern if we did arithmetic — we don't.
- **No library ≠ no bugs.** Hand-rolled `Decimal` must have exhaustive test coverage on parse edge cases (leading zeros, scientific notation, negative zero, precision boundaries, signed-Int128 boundary). §6.1 lists the critical ones; implementation must add any additional boundary cases the hand-roll exposes.
- **Hand-roll rationale.** Explicitly chosen over `shopspring/decimal` or `cockroachdb/apd` because cyoda-go forgoes arithmetic (delegated to Trino). The full arithmetic surface of those libraries is temptation for future maintainers to add features that belong downstream. `math/big.Int` does the heavy lifting; ~200 lines of `Decimal` wrapper is preferable to a dependency whose API actively invites scope creep.
- **Monotone-up-only schema evolution.** The collapse rule widens; it never narrows. A long-running instance that once observed an outlier value (say, `UNBOUND_DECIMAL`) at a field carries that tag even after all outliers are purged from storage. An operator purging outliers does not reclaim the narrower schema. This is accepted as part of A.1's contract; schema-narrowing is a future concern (not A.2).
- **gRPC audit scope.** §4.5 describes the entity.go:35 fix. There may be other gRPC dispatch sites that unmarshal ingestion payloads without UseNumber; step 5 audits and fixes all of them.

## 9. Success criteria

- All tests in §6 pass.
- `go vet ./...`, `go test -race -short ./...` clean.
- `docs/numeric-classification.md` committed. Documents: the policy, references to `docs/numeric-type-classification-analysis.md` as the raw-classifier source, the two cyoda-go divergences from §2.3 + §5.
- At least one end-to-end ingestion test (existing `internal/e2e/model_extension_test.go` style) confirms an 18-fractional-digit decimal value round-trips as `BIG_DECIMAL` through the full HTTP stack, and a 20-fractional-digit decimal value round-trips as `UNBOUND_DECIMAL` (both diverging from the pre-A.1 `DOUBLE` classification).
- At least one end-to-end gRPC test confirms a `9007199254740993` integer value round-trips as `LONG` through the CloudEvent dispatch (proves the UseNumber fix is live).
- No regressions in existing tests that aren't justified by an intentional divergence documented in §8.

## 10. Follow-on work (not in this sub-project)

- **Sub-project A.2:** schema-transformation round-trip coverage. Builds on A.1.
- **Trino integration layer:** serialization of `BIG_DECIMAL` / `UNBOUND_DECIMAL` to Trino-native types. Likely lives in a new package `internal/trino/` or inside the Cassandra plugin. Out of scope here.
- **Arithmetic for decimals in cyoda-go:** explicitly rejected. All arithmetic delegated to Trino.
- **Numeric schema tightening** (e.g., narrowing `BIG_DECIMAL` back to `DOUBLE` when all observed values fit): out of scope; the collapse rule is monotone-up-only.
- **Search predicate semantics for polymorphic fields:** a field typed `{BIG_DECIMAL, STRING}` needs defined search behavior (e.g., `x = 13.0` matches decimal-tagged values only, not string-tagged values). Lives in whichever sub-project owns search — not A.1 or A.2.
