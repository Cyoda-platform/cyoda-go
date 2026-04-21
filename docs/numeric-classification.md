# Numeric Classification in cyoda-go

This document describes cyoda-go's numeric classification policy for
data ingestion. The raw algorithm is ported from Cyoda Cloud's
classifier; cyoda-go deliberately diverges in two ways documented
below.

**Authoritative references:**
- Algorithm source: [`numeric-type-classification-analysis.md`](numeric-type-classification-analysis.md)
- Design spec: [`superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md`](superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md)

## DataType enum

cyoda-go's `DataType` enum carries the following numeric types:

**Integer family** (widening order): `Integer ⊂ Long ⊂ BigInteger ⊂ UnboundInteger`.

**Decimal family** (widening order): `Double ⊂ BigDecimal ⊂ UnboundDecimal`.

Cyoda Cloud additionally has `Byte`, `Short`, and `Float` — **dropped in cyoda-go**. Values Cyoda Cloud classifies as BYTE or SHORT classify as INTEGER in cyoda-go; values Cyoda Cloud classifies as FLOAT classify as DOUBLE.

## Classification algorithm

Every ingested numeric value flows through:

1. `json.NewDecoder(r).UseNumber().Decode(...)` preserves the source literal as a `json.Number` string.
2. `ParseDecimal(s)` parses into `(unscaled *big.Int, scale int32)`.
3. `StripTrailingZeros()` normalizes.
4. **Value-based branch:** `scale <= 0` (whole number, possibly after stripping) → `ClassifyInteger(unscaled × 10^(-scale))`. Otherwise → `ClassifyDecimal(d)`.

### Integer classification

| Magnitude | DataType |
|---|---|
| `[-2^31, 2^31-1]` | `Integer` |
| `[-2^63, 2^63-1] \ Integer range` | `Long` |
| `[-2^127, 2^127-1] \ Long range` | `BigInteger` |
| beyond | `UnboundInteger` |

### Decimal classification

After `StripTrailingZeros`, evaluated in order:

- `precision <= 15 AND |scale| <= 292` → `Double`.
- `precision <= 38 AND (precision - scale) <= 20 AND scale <= 18` → `BigDecimal` (definite fit).
- `precision <= 39 AND (precision - scale) <= 21 AND scale <= 18 AND SetScale(18).Unscaled().IsInt128()` → `BigDecimal` (loose fit).
- Otherwise → `UnboundDecimal`.

The `BigDecimal` boundary is Trino-compatible by design: BigDecimal values fit Trino's fixed-scale Int128 encoding, so downstream Trino-backed storage can index them directly.

## Collapse rule (`CollapseNumeric`)

A field's `TypeSet` always collapses its numeric members to exactly one `DataType`. Non-numeric members (String, Boolean, etc.) are preserved unchanged; `NULL` is dropped when any concrete type is present.

**Same-family collapse:** keep the widest rank observed.

**Cross-family collapse:** the narrowest decimal type that losslessly contains every integer-family member observed:

- `{Integer|Long, Double|BigDecimal}` → `BigDecimal`
- `{Integer|Long, UnboundDecimal}` → `UnboundDecimal`
- `{BigInteger, Double|BigDecimal}` → `BigDecimal` (BigInteger fits Int128 at scale 0)
- `{BigInteger, UnboundDecimal}` → `UnboundDecimal`
- `{UnboundInteger, any decimal}` → `UnboundDecimal`

Cyoda Cloud's polymorphic numeric sets are replaced by this single-type collapse. A field Cyoda Cloud represents as `{FLOAT, DOUBLE, BIG_DECIMAL}` becomes `BigDecimal` in cyoda-go. No information is lost — every observed value remains representable.

## Validation compatibility

`IsAssignableTo(dataT, schemaT)` realizes the widening lattice (per Cyoda Cloud `DataType.kt:239-272`, minus dropped types). Notable asymmetries:

- `Integer → Double` — **allowed**. Integer's 2^31 range fits Double's 53-bit mantissa.
- `Long → Double` — **blocked**. Long's 2^63 exceeds Double's 53-bit mantissa; precision loss.
- `Integer → BigDecimal` — allowed. `Long → BigDecimal` — allowed.
- `Double → BigDecimal` — **blocked**. Envelopes differ.
- Any integer → any decimal family reachable via the above — allowed.
- Any decimal → integer family — **blocked**.
- `NULL → anything` — allowed.

Under strict validation (`ChangeLevel = ""`), a decimal value against an `Integer` schema is rejected. Under extension (`ChangeLevel = Type` or higher), the same input triggers schema widening via the collapse rule — the field becomes `BigDecimal`.

## Intentional divergences from Cyoda Cloud

1. **No polymorphism within numerics.** Cyoda Cloud retains polymorphic numeric sets (e.g., `{FLOAT, DOUBLE, BIG_DECIMAL}`) and collapses only at read time via `findCommonDataType`, which falls back to `STRING` when no common type exists — silently corrupting numeric data. cyoda-go always stores the collapsed numeric type; cross-family mixing promotes to BigDecimal/UnboundDecimal without any STRING fallback. Bug fix, not a neutral divergence.

2. **Value-based integer/decimal split.** Cyoda Cloud routes on Jackson node kind (`IntNode` vs `DecimalNode`), which is a leaky abstraction over JSON grammar: `"1.0"` and `"1e0"` both denote the integer 1 but take different classifier branches. cyoda-go classifies by value — any whole-number literal classifies via `ClassifyInteger` regardless of source syntax.
