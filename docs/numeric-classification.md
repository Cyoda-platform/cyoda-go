# Numeric Classification in cyoda-go

This document describes cyoda-go's numeric classification policy for
data ingestion. The raw algorithm is ported from Cyoda Cloud's
classifier; cyoda-go deliberately diverges in two ways documented
below.

**Authoritative references:**
- Algorithm source: [`numeric-type-classification-analysis.md`](numeric-type-classification-analysis.md)
- Design spec: [`superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md`](superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md)
- Admission predicate design: [`superpowers/specs/2026-09-04-555-type-admission-design.md`](superpowers/specs/2026-09-04-555-type-admission-design.md)

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

**Cross-family collapse** uses the widening lattice (DataType.kt:240-287). The result is the narrowest type in the lattice that every input widens to. Because several cross-family pairs have no direct widening edge, the result is often `UnboundDecimal`:

| Input set | Result | Reason |
|---|---|---|
| `{Integer, Double}` | `Double` | `Integer → Double` is a direct lattice edge |
| `{Integer, BigDecimal}` | `BigDecimal` | `Integer → BigDecimal` direct |
| `{Integer, UnboundDecimal}` | `UnboundDecimal` | direct |
| `{Long, Double}` | `UnboundDecimal` | `Long → Double` blocked (precision loss); intersect = `{UnboundDecimal}` |
| `{Long, BigDecimal}` | `BigDecimal` | `Long → BigDecimal` direct |
| `{Long, UnboundDecimal}` | `UnboundDecimal` | direct |
| `{BigInteger, Double}` | `UnboundDecimal` | no direct edge; intersect = `{UnboundDecimal}` |
| `{BigInteger, BigDecimal}` | `UnboundDecimal` | `BigInteger → BigDecimal` blocked; intersect = `{UnboundDecimal}` |
| `{BigInteger, UnboundDecimal}` | `UnboundDecimal` | direct |
| `{UnboundInteger, any decimal}` | `UnboundDecimal` | `UnboundInteger → UnboundDecimal` is its only outgoing edge |
| `{Double, BigDecimal}` | `UnboundDecimal` | `Double → BigDecimal` blocked (scale mismatch); intersect = `{UnboundDecimal}` |

This matches Cyoda Cloud's `findCommonDataType` (`DataType.kt:293-309`) restricted to numeric inputs. Cyoda additionally falls back to `STRING` for incompatible non-numeric pairs; cyoda-go does not — STRING fallback is a Cyoda-internal plumbing choice (every leaf is also stored as a string ValueMap for search indexing), not semantic behavior cyoda-go replicates. For cross-kind polymorphic cases, cyoda-go keeps the TypeSet as a union (tracked under A.3).

## What a field holds: the admission predicate

Classification (above) answers "what label would this value need if the
model had to widen to hold it?" It does not answer the question that comes
first: does the field, **as already declared**, hold this value at all? That
second question decides whether a write costs a `ChangeLevel` permission, and
getting it wrong either rejects writes that should be free or — the sharper
failure — silently admits a value that the field cannot actually be searched
by.

**A field holds a numeric value when the value satisfies the declared type's
admission predicate. That predicate is not "the value is inside the declared
type's range."** An earlier draft of the design said exactly that — "a
numeric type is its range, and only its range" — and it is wrong. `DOUBLE`'s
range extends past 10²⁹², so `9007199254740993` is well inside it, yet a
field declared `DOUBLE` cannot actually hold that value: for a
non-comparing op (`EQUALS`/`NOT_EQUAL`), the search operand side
(`produceDecimalInRange`, in `cyoda-go-spi`'s `numeric_bucket.go`) drops the
`DOUBLE` comparison branch entirely for any operand needing more than 15
significant digits, regardless of magnitude — a comparison op (`<`, `>`,
`<=`, `>=`) instead rounds the operand and keeps the branch, since a rounded
bound admits exactly the same values as the original. Admit
`9007199254740993` into a `[DOUBLE]` field on the strength of "it's in
range" and `EQUALS 9007199254740993` finds nothing while `NOT_EQUAL
9007199254740993` wrongly matches it — the exact defect admission exists to
prevent, reintroduced by the rule meant to fix it. `1.234567890123456`
(16 significant digits) and `1e-400` (scale past 292) fail the same way:
both are "in range", neither is `EQUALS`-findable.

So admission is a **conjunction**, tested directly against the value — never
against a classified label, and never range alone:

| Declared type | The field holds value *v* when |
|---|---|
| `INTEGER`, `LONG`, `BIG_INTEGER` | *v* is whole (after stripping trailing zeros) **and** within the type's bounds |
| `UNBOUND_INTEGER` | *v* is whole (after stripping trailing zeros) — no bound |
| `DOUBLE` | \|*v*\| ≤ 9.99999999999999 × 10²⁹² **and** precision ≤ 15 **and** scale ≤ 292 |
| `BIG_DECIMAL` | \|*v*\| is within ±INT128/10¹⁸ — magnitude only |
| `UNBOUND_DECIMAL` | always |

**Why `DOUBLE` needs the precision conjunct, and why the other families
mostly don't.** `DOUBLE` is the one family where the range test and the
precision test are *both* load-bearing; a reader who keeps only "range" will
silently reintroduce the bug two paragraphs up. The reason is the search
operand bucket, `cyoda-go-spi`'s `numeric_bucket.go`: `isDoubleBucketPrecise`
answers whether an operand fits in 15 significant digits and a scale of at
most 292, and it is `produceDecimalInRange` that acts on that answer,
refusing to produce a `DOUBLE` comparison branch for `EQUALS`/`NOT_EQUAL`
when it does not — independent of whether the value's magnitude fits
`DOUBLE`'s range at all. (A comparison op rounds instead of refusing, so
this is specifically an equality-family hazard, per the note above.) A value
can be comfortably inside the range and still fail that test, as
`9007199254740993` does. Admission has to fail the same value
`EQUALS`/`NOT_EQUAL` would refuse to compare against, or the two disagree
and the "held ⟹ findable" invariant below breaks.

`BIG_DECIMAL` really is magnitude-only: the search operand side documents
its own scale ≤ 18 restriction as a Trino storage constraint irrelevant to a
search condition, and emits a high-scale in-magnitude value verbatim — the
range rule is correct there. `DOUBLE` is the sole outlier, and it needs both
conjuncts in both directions: an in-range, low-precision value is held; an
in-range, high-precision value is not.

**This is also the mantissa argument, aimed at the right target.** A prior
design pinned "a whole number past 2³¹ is a genuine type change, because a
value classified `LONG` has a 2⁶³ range that exceeds `DOUBLE`'s 53-bit
mantissa" — worried that admitting such a value into `DOUBLE` would silently
reshape stored data. The worry was correct; judging it by the classified
*label* was not. `2147483648` (10 digits, exactly representable in a
`DOUBLE`) and `9007199254740993` (16 digits, not exactly representable) both
classify no more precisely than `LONG` — the label cannot tell them apart.
Precision can: **a decimal of at most 15 significant digits round-trips
uniquely through a binary64 `double`** — the guarantee both `DOUBLE`'s own
findability and a lossless `float8` pushdown on PostgreSQL need —
and `2147483648` is inside that bound while `9007199254740993` is not. The
boundary lands exactly where the mantissa puts it; what changed is the
instrument judging it — the value's own precision, not its classified
label.

**One predicate, two consumers.** The same per-family test is what the
search kernel uses to filter a stored value at query time (`evalCompare`,
`evalBetween` judge a stored number by what the declared type admits, not by
its narrowest classified label). A value that would not be admitted on write
is also not matched by a comparison at read time, even if it somehow ended
up stored by another route. This is what makes the invariant hold: **the
field holds it ⟹ the model does not change ⟹ the value is findable under a
declared type.** The third arrow does not follow from the range; it follows
from write-time admission and the read-time filter deferring to the same
predicate.

**How this fits the change-level gate.** A value the field already admits,
by this predicate, consumes no `ChangeLevel` permission and the model does
not change. A value it does not admit is a genuine type change: the model
widens, via the collapse rule above, to the narrowest type that holds both
the field's current declaration and the value's classified label — gated by
`ChangeLevel` like any other schema change. Classification (the section
above) is what decides *which* label a rejected value would need; admission
is what decides whether classification is consulted at all.

## Intentional divergences from Cyoda Cloud

1. **No polymorphism within numerics.** Cyoda Cloud retains polymorphic numeric sets (e.g., `{FLOAT, DOUBLE, BIG_DECIMAL}`) and collapses only at read time via `findCommonDataType`, which falls back to `STRING` when no common type exists — silently corrupting numeric data. cyoda-go always stores the collapsed numeric type; cross-family mixing promotes to BigDecimal/UnboundDecimal without any STRING fallback. Bug fix, not a neutral divergence.

2. **Value-based integer/decimal split.** Cyoda Cloud routes on Jackson node kind (`IntNode` vs `DecimalNode`), which is a leaky abstraction over JSON grammar: `"1.0"` and `"1e0"` both denote the integer 1 but take different classifier branches. cyoda-go classifies by value — any whole-number literal classifies via `ClassifyInteger` regardless of source syntax.
