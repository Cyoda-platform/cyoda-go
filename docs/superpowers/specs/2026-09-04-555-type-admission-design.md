# Type admission: one traversal, one definition of what a field can hold

Status: proposed. Tracking issue #555.

## 1. Terms

Three moments. This document never uses one to mean another.

- **Registration** — sample data is imported while the model is `UNLOCKED`.
  cyoda-go reads the values and records each field's declared types.
- **Ingestion** — an entity is written against a `LOCKED` model. The model's
  configured `changeLevel` is a ceiling on how much the model may change to
  accept the write. A model with no `changeLevel` may not change at all.
- **Search** — a query.

And two about types:

- **Declared types** — the DataType set a model field records.
- **Range** — every numeric DataType is a span of values, bounded by a floor and
  a ceiling. `DOUBLE`'s ceiling is `9.99999999999999e292`; `INTEGER`'s is
  2³¹−1; and so on.
- **Admits** — a numeric type admits a value when the search bucket for that
  type would accept it. For most types that is the range and nothing else; for
  `DOUBLE` it is the range *and* a precision and scale bound, and for the
  integer family it is the range *and* wholeness. §5 gives the predicate. The
  distinction is the difference between a value being stored and a value being
  findable, so this document is careful never to say "range" where it means
  "admits".

`changeLevel` is a ceiling, not a budget. Nothing is "consumed" or "spent".

## 2. Why the types exist

The entity model exists so that storage backends can index data by type. A
backend that maintains its own persistence keeps a separate index table per
numeric type; without types, every number would have to be a big decimal and
every index the widest possible. The type dissection is a storage optimisation,
and its correctness requirement follows directly: **a field can hold a value
only if some type the field declares admits it — where "admits" means the index
for that type can be searched for it.**

That single rule has to hold in three places — deciding a value's type,
deciding whether a field accepts it, and searching for it. If any two disagree,
a value can be stored somewhere it cannot be found.

## 3. The problem

Ingestion does not ask whether the field can hold the value. It converts the
incoming document into a throwaway model — each value replaced by a type label
— and compares that model against the stored one. The values are gone before
any decision is made. Three consequences, all measured.

**(a) A whole number past 2³¹ is refused by a `DOUBLE` field.** Its label is
`LONG`, `LONG` is not among the declared types, so the write is treated as a
type change:

```
400 VALIDATION_FAILED: change level violation:
    type change at .amount requires TYPE level, but level is "ARRAY_LENGTH"
```

At `TYPE` the write lands but the field widens to `UNBOUND_DECIMAL`.

*(The same failure for `1000` — an `INTEGER`-range whole number — was #544 and
is fixed in `b3cd9d5`.)*

**(b) A date-shaped string is refused by a `STRING` field.** The field declares
`STRING` because its sample value was `"hello"`. Writing `"2026-03-01"` fails —
including under strict validation, where the model cannot change at all, so no
configuration makes it go away:

```
400 INCOMPATIBLE_TYPE: validation failed:
    note: value of type LOCAL_DATE is not compatible with [STRING]
```

**(c) A query cannot find a value it wrote.** A field declares `INTEGER` and
holds `5`:

```
EQUALS 5      -> 1 hit
EQUALS "5"    -> 1 hit
EQUALS 5.0    -> 0 hits
```

Ingestion strips trailing zeros before classifying, so it read `5.0` as the
whole number 5. The query side does not strip — `foldToInt` tests integrality
with `Scale() <= 0` on the unstripped decimal — so it reads the same literal as
fractional and drops the integer comparison entirely.


## 4. The rule

> **A field can hold a value when the value's JSON kind matches a declared
> type's kind, and the value is one the declared type admits. When it does, the
> model does not change and the write is permitted at any `changeLevel`. When it
> does not, the model must change to hold the value, and that change is
> permitted only at the model's configured level.**

"A value the declared type admits", per JSON kind:

| JSON kind | The field holds it when a declared type T is |
|---|---|
| number | numeric, and the value satisfies T's admission predicate (§5) |
| string | `STRING`; or temporal and the string's classification is exactly T |
| boolean | `BOOLEAN` |
| null | any declared type — see the note below |

A JSON number is not a string and a JSON string is not a number, so `STRING`
does not become a universal sink.

The numeric row is deliberately not "inside T's range". §5 gives the predicate
and says why the range alone is the wrong test.

**The null row is about admission, not about the model being inert.** A leaf
that already declares a scalar admits `null` and records nothing. A node that
declares *no* scalar still charges the nullable-marker promotion at
`scalarLevel` (`extend.go:74-83`), and §6 keeps that. The row means "no declared
type refuses null", not "null never changes the model".

Two further consequences worth stating rather than leaving a reader to infer.

`CHARACTER`, `UUID_TYPE`, `TIME_UUID_TYPE` and `BYTE_ARRAY` never appear in a
declared set: classification never produces them from JSON, and sample-data
import is the only way a model is built. So the string row's silence about them
is not a gap — a leaf declaring one cannot exist. (The search kernel does handle
`CHARACTER` and `UUID_TYPE` on the stored side, an asymmetry that is harmless
only because it is unreachable.)

A leaf that declares `STRING` will no longer learn a temporal subtype at
ingestion. Today a registration-produced `{STRING, LOCAL_DATE}` leaf that
receives `"2026"` grows to include `YEAR`; under §4 it stays as it is, and that
value is then matchable lexically through the `STRING` branch but not by a
temporal predicate. That is the intended trade — the field declares text, and a
write does not silently give it new search semantics — but it is a behaviour
change on the ingestion side, and §8's registration path remains the way a field
acquires temporal types. It has consequences beyond cyoda-go: see §13.

The invariant this establishes, which nothing in the system claims today:

> **The field holds it ⟹ the model is unchanged ⟹ the value is findable
> under a declared type.**

The third arrow is the one §5 exists to secure. It does *not* follow from the
range; it follows from admission using the same predicate the search bucket
uses.

### Case table

| Declared | Value | Held? | Result |
|---|---|---|---|
| `DOUBLE` | `1000`, `1000.0`, `1e3` | yes | model unchanged |
| `DOUBLE` | `2147483648` | yes | model unchanged — (a) |
| `DOUBLE` | `9.99999999999999e292` | yes | at the ceiling, model unchanged |
| `DOUBLE` | `9.99999999999999e300` | no | above the ceiling; model change, gated |
| `DOUBLE` | `9007199254740993` | no | 16 significant digits; model change, gated |
| `DOUBLE` | `1.234567890123456` | no | 16 significant digits; model change, gated |
| `DOUBLE` | `1e-400` | no | scale past 292; model change, gated |
| `INTEGER` | `2147483647` | yes | model unchanged |
| `INTEGER` | `2147483648` | no | above the ceiling; model change, gated |
| `INTEGER` | `13.111` | no | not a whole number; model change, gated |
| `UNBOUND_INTEGER` | `1.5` | no | not a whole number; model change, gated |
| `BIG_DECIMAL` | `1.23456789012345678901234567890` | yes | magnitude-only bucket; model unchanged |
| `STRING` | `"2026-03-01"` | yes | model unchanged — (b) |
| `STRING` | `"hello"` | yes | model unchanged |
| `STRING` | `5` | no | a number is not a string; model change, gated |
| `STRING` | `true` | no | model change, gated |
| `BOOLEAN` | `"true"` | no | a string is not a boolean; model change, gated |
| `INTEGER` | `"2024"` | no | a string is not a number; model change, gated |
| `ZONED_DATE_TIME` | `"2026-03-01T10:00:00Z"` | yes | model unchanged |
| `ZONED_DATE_TIME` | `"2026"` | no | a year is not a timestamp; model change, gated |
| `LOCAL_DATE_TIME` | `"2026-03-01T10:00:00+05:00"` | no | carries an offset; model change, gated |

## 5. What a numeric type admits

An earlier draft of this design said "a numeric type is its range, and only its
range". That is wrong, and the case table above is the evidence: it gates
`9007199254740993`, which is *inside* `DOUBLE`'s range. Both statements cannot
stand. The range-only rule is the one that breaks, and it breaks the invariant
§4 exists to establish.

### Why the range alone is not the test

Admission must agree with what search can find. On the operand side,
`produceDecimalInRange` (`cyoda-go-spi/numeric_bucket.go:186-195`) *drops the
`DOUBLE` branch entirely* for a value that fails `isDoubleBucketPrecise`
(precision ≤ 15 and scale ≤ 292, `numeric_bucket.go:208-210`) under a
non-comparing op. Measured against `declared=[DOUBLE]`:

```
EQUALS 9007199254740993   stored 9007199254740993   -> false   (0 numeric branches)
EQUALS 1.234567890123456  stored 1.234567890123456  -> false   (0 numeric branches)
EQUALS 1e-400             stored 1e-400             -> false   (0 numeric branches)
```

All three are inside `DOUBLE`'s range. Admit them on range alone and they are
stored in a field where `EQUALS` can never find them and `NOT_EQUAL` wrongly
matches them — the exact failure §4 exists to prevent, reintroduced by the rule
meant to fix it. §7(i) cannot rescue these: it changes the *stored*-value
filter, and here the expansion is already empty on the operand side.

The integer family has the mirror problem, and `UNBOUND_INTEGER` shows it
starkly: it has no range bound at all, so a range-only rule admits every
fractional value into it, after which `foldToInt` drops the whole int family and
`EQUALS 1.5` finds nothing.

### The predicate

The test is per-family, and it is exactly the operand-side bucket admission in
`numeric_bucket.go` — not a choice among the SPI's three `DOUBLE` tests, but the
conjunction the bucket already applies:

| Declared T | The field holds value *v* when |
|---|---|
| `INTEGER`, `LONG`, `BIG_INTEGER` | *v* is whole after `StripTrailingZeros` **and** within T's bounds |
| `UNBOUND_INTEGER` | *v* is whole after `StripTrailingZeros` |
| `DOUBLE` | \|*v*\| ≤ `9.99999999999999e292` **and** precision ≤ 15 **and** scale ≤ 292 |
| `BIG_DECIMAL` | \|*v*\| within ±INT128/10¹⁸ — magnitude only |
| `UNBOUND_DECIMAL` | always |

`BIG_DECIMAL` really is magnitude-only: `produceDecimalInRange` documents the
scale ≤ 18 restriction as a Trino storage constraint irrelevant to a search
condition (`numeric_bucket.go:172-178`), and emits a high-scale in-magnitude
value verbatim. So the range rule *is* correct there; `DOUBLE` is the sole
outlier, and it needs both conjuncts in both directions.

**This is one predicate, exported once from the SPI** — `AdmitsNumeric(DataType,
Decimal) bool` — and shared by the kernel's stored-value filter (§7(i)) and
cyoda-go's `assignableToAny`. Two copies would be two things that can disagree,
which is the defect this whole document is about.

### It is also the mantissa argument, and it settles two other things

`extend_assignable_test.go:105-112` pins today's boundary and states its reason:
*"a whole number past 2^31 classifies LONG, whose 2^63 range exceeds DOUBLE's
53-bit mantissa — the lattice refuses that conversion deliberately … Pinning
this stops a later 'any whole number is fine' simplification from silently
reshaping stored data."*

That objection is correct, and the predicate answers it rather than overriding
it. **A decimal of at most 15 significant digits round-trips uniquely through a
binary64 `double`** — the guarantee both the `DOUBLE` bucket's findability and
a lossless Postgres pushdown (below) need — and `precision ≤ 15` excludes
every value that guarantee does not cover. (This is not the same claim as "any
integer above 2^53 needs 16 significant digits": a value like `1e16` needs
only one significant digit after stripping trailing zeros despite exceeding
2^53, and the predicate correctly admits it — the guarantee is about
significant digits, not raw magnitude.) What the lattice got wrong was not the
concern but the instrument: it judged the *label* `LONG`, which condemns
`2147483648` — 10 digits, exactly representable — along with
`9007199254740993`. The predicate judges the value's own precision, and keeps
the boundary exactly where the mantissa puts it.

Two consequences fall out:

- **Postgres pushdown stays lossless.** `cyoda_try_float8` is lossy above 2^53
  (`plugins/postgres/query_planner.go:609,632`), and a `[DOUBLE]` leaf holding
  such a value would be dropped by the SQL pre-filter while the kernel matched
  it — and *not* dropped by sqlite, which binds an exact `int64`. That would be
  a cross-backend result divergence, which this project treats as a bug. Under
  the predicate it cannot arise: every admitted value has at most 15
  significant digits, and such a decimal round-trips uniquely through
  `float8` regardless of its raw magnitude, so no two admitted values collide.
- **§12's "reconciling the three tests is separate work" needs re-reading.** The
  design cannot pick "the correct one", because for `DOUBLE` two of the three
  are jointly load-bearing. What stays out of scope is *unifying* them; what
  this change does is name the conjunction as the admission predicate and give
  it one home.

The third test, `isDoubleEnvelope` (`cyoda-go-spi/numeric.go:82-90`), reaches
about 10³⁰⁷ and is the one that is simply wrong as an admission test. It is
currently inert — its only live consumer is `ClassifyDecimal`, called only with
scale > 0, and `parseDecimalType`'s `DOUBLE` branch is unreachable for a numeric
declared type because `bucketDeclared` (`eval_leaf.go:209-220`) routes numerics
away from the `others` bucket. Leaving it inert is acceptable; using it here
would not be.

## 6. One traversal over the document

Everything in §3 comes from the same structural decision: the write path
converts the document into a throwaway model before deciding anything.

```
document → throwaway model   (values discarded; array elements fused into one)
         → compare against the stored model
         → if different, merge the two models
```

The values are gone by the first arrow, so §4's rule cannot be evaluated. And
`walkArray` fuses every element into a single description, so `[2147483648,
"hello"]` becomes one entry meaning "large integer or text" — by then there is
no individual value left to judge, and the fusion has already widened the
number.

**The write path walks the document against the stored model instead**, visiting
one value at a time and asking §4's question of each: does the field hold this?
If yes, nothing happens. If no, record the change it needs and the level that
change costs. Apply the recorded changes at the end, if the level permits all of
them.

`schema.Validate` already has this shape — it takes the model and the parsed
data together. This makes `Extend` the same shape, and then the two are one
mechanism rather than two that can disagree.

What that buys:

- The value is available where the decision is made.
- Each array element is judged individually; nothing is fused beforehand.
- The verdict and the resulting model change come from one traversal, so they
  cannot diverge. Today they agree only by coincidence — the gate and
  `TypeSet.Add` happen to give the same answers — and that coincidence breaks
  the moment the gate becomes value-aware.

### What the traversal must still do

The current `Extend` handles more than leaf types, and the replacement keeps all
of it: array width growth (`ARRAY_LENGTH`), an array learning its element type
(`ARRAY_ELEMENTS`), a path gaining a kind it does not declare (`STRUCTURAL`),
the nullable-marker promotion, and new object fields (`STRUCTURAL`). The
resulting model must still be a pure widening of the stored one, or `Diff` will
refuse it as non-additive.

### What it replaces, and what that costs

Changing `Extend`'s signature from `(existing, incoming *ModelNode, level)` to
one that takes parsed data retires the model-to-model extension algebra. That is
not a rename; it is the removal of a contract several suites are written
against, and §11 budgets for it:

- 44 call sites across 15 test files in `internal/domain/model/schema/`, led by
  `extend_test.go`, `extend_assignable_test.go`, `extend_branch_level_test.go`,
  `extend_nullable_test.go` and `add_kind_branch_test.go`.
- The property suites — `commutativity_property_test.go`,
  `idempotence_property_test.go`, `monotonicity_property_test.go`,
  `permutation_property_test.go`, `roundtrip_property_test.go`,
  `fold_order_test.go` — which build their incoming side with `importer.Walk`
  and feed it to `Extend`. These are the executable statement of the schema
  algebra; each needs a replacement stated over documents rather than models.
- `e2e/parity/oracle.go:52,130`, which calls `schema.Extend(current, walked,
  ChangeLevelStructural)`. The oracle is the byte-identity authority for the
  whole parity suite, so this is load-bearing, not incidental.
- `e2e/parity/schema_extension_property.go`, which runs through that oracle.

### The fold becomes order-dependent, and that is accepted

**Settled. The order-dependence is accepted and the convergence invariant is
restated to what actually holds.** What follows is the reasoning, kept because
the invariant it retires was pinned with "do not loosen the assertion" and a
future reader is owed the argument.

Today the gate skips a value only when its label is already absorbed by the
declared set, and `Merge`/`CollapseNumeric` is commutative over the label
multiset — so the folded model does not depend on arrival order. §4 skips values
whose labels the declared set does *not* absorb, and that does not commute:

| Leaf `[INTEGER]`, two writes | Result |
|---|---|
| `2147483648` then `12.5` | `[LONG]` → `[UNBOUND_DECIMAL]` |
| `12.5` then `2147483648` | `[DOUBLE]` → held, stays `[DOUBLE]` |
| both concurrently, each diffing from `[INTEGER]` | both deltas apply → `[UNBOUND_DECIMAL]` |

Today all three land on `[UNBOUND_DECIMAL]`. The same divergence exists for
strings: a `[LOCAL_DATE]` leaf receiving `"hello"` then `"2026"` ends at
`{LOCAL_DATE, STRING}`, and in the other order at `{LOCAL_DATE, STRING, YEAR}`.

This matters because it is not a rare interleaving.
`internal/domain/model/schema/fold_order_test.go:9-17` describes it as *"the
ordinary state during a cross-node gossip window and under concurrent writes
against a cached descriptor"*, and
`e2e/parity/schema_concurrent_convergence.go:19-23` pins byte-identical
convergence with *"do not loosen the assertion"*.

Delta *application* stays commutative, so no existing property test goes red —
what §4 makes order-dependent is whether a delta is **produced at all**, which
now depends on the snapshot the writing node holds. The concurrent-convergence
parity scenario exercises only new-field additions, which stay commutative, so
it will not catch this either. Silence from the suite is not evidence here.

What is *not* lost: every reachable fold is monotone, and every reachable fold
admits every value that was written. What is lost is that the set of *future*
values admitted without permission depends on history and on concurrency — a
later `9007199254740993` is gated against `[DOUBLE]` and admitted against
`[UNBOUND_DECIMAL]`.

The alternatives were considered and do not work. Recording the value's label
even when the value is held restores determinism but widens the model without
the level permitting it, which contradicts the `changeLevel` contract §12 keeps.
Deriving the declared set from an order-independent "observed labels" set has
the same defect, since a held value would still have to contribute its label.
Judging by label, as today, is the status quo and is the defect. Order
dependence appears to be intrinsic to "held ⟹ the model does not change".

**The decision.** Accept the order-dependence and say so precisely:

- **Byte-identical convergence remains the contract for structural extension** —
  new fields, new kinds, array width, the nullable marker. It still holds there,
  and `RunSchemaExtensionConcurrentConvergence` keeps asserting it for the
  shapes it already exercises.
- **Numeric- and temporal-leaf widening is carved out**, with the weaker
  property that actually holds: every reachable fold is monotone, and every
  reachable fold admits every value that was written. That is the property the
  carve-out must assert — it is not "anything goes", and a fold that lost a
  written value would still be a defect.

Both halves have to be written into that scenario's doc comment, which currently
says the opposite, and the carve-out needs its own parity scenario exercising
the numeric case. Leaving the numeric case uncovered would let the suite keep
passing while saying nothing, which is how this got missed in the first place.

### Registration folds in — against an empty model, per document

Registration becomes the same traversal, run against an empty model. **Per
document, and per array element, exactly as today** — `service.go:155`,
`importer/sample_documents.go:44` and `importer/walker.go:124` each merge a
freshly derived description into the accumulating one, and that stays.

The distinction is load-bearing. If registration instead ran incrementally
against the model built so far, importing `{"note":"hello"}` then
`{"note":"2026-03-01"}` would yield `{STRING}` rather than `{STRING,
LOCAL_DATE}` — §8's worked example would be wrong, and temporal search would
disappear from genuinely temporal fields, which is the outcome §8 exists to
prevent. "The same traversal" means the same code, not the same accumulation
discipline.

## 7. Search

### What is correct and stays

The operand machinery — folding a comparison value into each declared type's
range, classifying it as below, inside or above, and rounding directionally — is
mathematically correct and is not being removed. Measured:

```
[DOUBLE]  <  3e100   stored 10.5  -> true    3e100 is inside DOUBLE's range;
[DOUBLE]  >  3e100   stored 10.5  -> false   an ordinary in-range comparison
[DOUBLE]  == 3e100   stored 10.5  -> false
[INTEGER] <  3e100   stored 5     -> true    above INTEGER's ceiling: the
[INTEGER] >  -3e100  stored 5     -> true    out-of-range NotNull residual
[INTEGER] >  12.5    stored 13    -> true    integers above 12.5 are 13, 14, …
[INTEGER] >  12.5    stored 12    -> false
[INTEGER] == 12.5    stored 12    -> false   no integer equals 12.5
```

`>` floors the operand and `<` ceilings it, so the rounded bound admits exactly
the same values as the original. This is also what lets one predicate become one
predicate per index table, which §2's backends need.

### Two changes

**(i) The stored-value filter.** A value admitted under §4 is stored raw. At
search time the kernel derives that stored value's *label* and checks it against
the declared type with `IsAssignableTo`, so a `2147483648` in a `[DOUBLE]` leaf
is not matched. The kernel must judge a stored number the way §4 does — with
§5's predicate. Applies to `evalCompare` (`eval_leaf.go:424-427`) and
`evalBetween` (`eval_leaf.go:519-529`).

The two sites have different shapes and both need writing: `evalCompare` tests
per sub-condition, while `evalBetween` scans the whole declared numeric set
`e.numTypes`. Under the collapse invariant they coincide, but the code does not.

This also fixes the `NotNull` residual branches, which are gated by the same
filter: `[DOUBLE] < 1e300` against a stored `2147483648` is false today and
becomes correctly true.

`classifyStoredNumeric` (`eval_leaf.go:663-673`) has exactly these two callers
and becomes dead code. Delete it in the same change.

Because numerics collapse to at most one per leaf, this filter never chooses
between buckets. That collapse is a cyoda-go `TypeSet` guarantee
(`datatype.go:147-162`), not an SPI-boundary one — `ExpandLeaf` accepts any
declared slice — so the kernel contract should say that a multi-numeric declared
set is outside its guarantees rather than leave it implied.

**(ii) Operand normalisation — §3(c).** The diagnosis is that ingestion strips
trailing zeros before classifying and search does not. The fix must be applied
where the asymmetry is, which is not only `foldToInt`: precision is computed on
the *unstripped* operand at `numeric_bucket.go:208`, so the decimal family has
the identical defect. Measured:

```
[DOUBLE]  EQUALS 5.000000000000000000   stored 5     -> false  (0 branches)
[DOUBLE]  EQUALS 10.500000000000000000  stored 10.5  -> false  (0 branches)
```

Nineteen trailing zeros make a `DOUBLE` operand "imprecise", `produceDecimalInRange`
drops the `EQ` branch, and `NOT_EQUAL` then wrongly matches — the same defect,
next door. **Strip once, at the `ParseDecimal` in `expandCompare`
(`eval_leaf.go:232`)**, not inside `foldToInt`. Fixing only the integer half
would leave exactly the heterogeneous landscape Gate 6 asks us not to leave.

This also fixes negative zero: `StripTrailingZeros` short-circuits on
`Sign() == 0` (`decimal.go:151-154`), so `NOT_EQUAL -0.0` against a stored `0`
stops wrongly matching.

`expandBetween` is already clean — it compares `Decimal`s directly
(`eval_leaf.go:275-287`), so `BETWEEN 4.0 AND 6.0` on `[INTEGER]` works today.
§3(c) is correctly an `EQUALS`/`NOT_EQUAL` defect.

**A scale-driven DoS sits on the function (ii) edits, and is fixed here.**
`foldToInt`'s `SetScale(0)` on a negative scale materialises `10^|scale|` via
`big.Int.Exp` (`decimal.go:195-199`), and `ParseDecimal` bounds scale only to
int32. Measured in-process: `ExpandLeaf(eq, "1e1000000", [INTEGER])` takes 25 ms
and `"1e10000000"` takes 928 ms — from a 13-byte operand, allocated before a
single row is read, reachable from an ordinary search request via
`ValidateConditionValueTypes` (`internal/domain/search/condition_type_validate.go:235`).
The project already guards exactly this hazard in one of three places —
`internal/domain/model/schema/validate.go:296-312` carries the explicit
*"Guard against DoS: a huge negative scale"* comment. The other two, `foldToInt`
and `importer/walker.go:99-102`, are unguarded and both are inside this change's
blast radius. Guard all three. (Secondary: `StripTrailingZeros` decrements an
int32 scale with no underflow check, `decimal.go:167`.)

### The gate that must not move

Two gates sit near each other; only the first changes.

- *Does the stored value belong to the declared numeric type?* — (i) above.
- *Did any declared type accept the operand?* — **keep**. When none did,
  `expandCompare`'s `engaged` flag (`eval_leaf.go:227-253`) produces an error.
  Note this is not "does the leaf declare a numeric type at all": a `[STRING]`
  leaf accepts the operand `5` through `ParseStringOrNull` and never errors.
  Removing the gate would make an unresolvable path start matching rows via
  negative operators.

That error surfaces as **`400 CONDITION_TYPE_MISMATCH`**, not
`INVALID_CONDITION` — `classifyConditionTypeErrCode`'s default arm
(`internal/domain/search/condition_type_validate.go:390-398`);
`INVALID_CONDITION` is reserved for operator-class rejections. An earlier draft
of this spec named the wrong code, and since the error table is the test
checklist, that would have produced tests asserting the wrong one.

### Temporal values — no change on the search side

`"2026-03-01T10:00:00+05:00"` classifies as `ZONED_DATE_TIME`. A parser for
`LOCAL_DATE_TIME` also accepts it, by discarding the `+05:00` — and those denote
different instants. If a value like that were admitted into a
`[LOCAL_DATE_TIME]` field it would be stored with its offset, classified
`ZONED_DATE_TIME` at search time, and found by nothing; making search parse
instead of classify would make a query for 10:00 match a value that is really
05:00 UTC.

So for temporal types, "the field holds it" means the string's classification
equals the declared type. That is what the code already does on both sides, so
this design changes nothing in the search kernel here. It does change ingestion
— see §4 — and §13 records what that costs elsewhere.

### Pushdown

**The conclusion holds; the reason an earlier draft gave was wrong.** It is not
true that the pushed SQL is built from the operand and not the declared types:
sqlite's `comparisonBind` picks its bind form from `f.Declared[0]`
(`plugins/sqlite/query_planner.go:396-401`) and `isLeafPushable` refuses
polymorphic comparison leaves (`:338`). The conclusion survives for a better
reason — `planQuery` installs the full original filter as `postFilter` unless
the plan is provably exact, and `leafExact` admits only `IsNull`/`NotNull`
(`plugins/postgres/query_planner.go:45-52,129-148`), so the one path where the
pre-filter is authoritative (the `postFilter == nil` fast path gating SQL
`LIMIT`/`OFFSET`/native `GROUP BY`) can never contain a comparison leaf.

The superset property was checked in the newly-matching direction on both
backends: `EQUALS 5.0` vs stored `5`, and a `[DOUBLE]` leaf newly holding
`2147483648`, both match under `cyoda_try_float8` and under sqlite's REAL/INTEGER
numeric comparison. `Ne` is not pushable on either backend, so §9's `NOT_EQUAL`
row is kernel-only. §5's predicate is what keeps this sound above 2^53.

Two items this change must nevertheless fix:

- **sqlite's stated premise for `isLeafPushable` becomes false.** Its doc
  comment (`plugins/sqlite/query_planner.go:295-303`) justifies the
  `len(f.Declared) > 1` gate as *"its stored values may span different type
  families / SQLite storage classes"*. Under §4 a **monomorphic** `[DOUBLE]`
  leaf routinely holds both INTEGER-class and REAL-class scalars. The gate is
  still sound — the comment's real worry was TEXT-vs-numeric, which §4's JSON
  kind rule keeps out — but the reasoning no longer supports the conclusion and
  must be corrected.
- **`COLLATE "C"` is missing on postgres text comparisons.** §4(b) keeps
  date-shaped data in `[STRING]` leaves, so `ClassifyType` yields `OrderString`,
  `Coercion` is `CoerceNone`, and the comparison is pushed as text
  (`condition_filter.go:388-404`). `orderingOp` (`query_planner.go:836-844`)
  emits no collation, while the same plugin's `orderByFieldExpr` explicitly uses
  *"COLLATE "C" (byte-order comparison)"* (`searcher.go:349`) precisely to match
  the kernel's `strings.Compare`. Under a non-C database collation a narrowing
  WHERE can under-select, and §4(b) fills `[STRING]` leaves with exactly the
  punctuation-heavy ISO strings where ICU and byte order diverge. Pre-existing,
  cheap, and made materially more reachable by this change.

### An incidental improvement, recorded so it is not mistaken for a regression

A leaf that today widens to `{STRING, ZONED_DATE_TIME}` is **unsortable** —
`ClassifyTypesFold` errors on mixed order classes. Under §4 it stays `[STRING]`
and becomes sortable lexically. That is a behaviour change, and it is in the
right direction.

## 8. Registration is unchanged

Registration discovers types; ingestion checks against them. They give different
declared sets for the same value, and that is the design:

- Registering `{"note":"hello"}` then `{"note":"2026-03-01"}` yields
  `{STRING, LOCAL_DATE}`.
- Locking after `{"note":"hello"}` and then ingesting `"2026-03-01"` leaves
  `{STRING}`.

Both accept the value. The registered model additionally supports temporal
predicates on that field. Absorbing `LOCAL_DATE` into `STRING` at registration
would remove temporal search from genuinely temporal fields.

§6's "registration folds in" is about sharing the traversal code, not about
changing this. The per-document, per-element merge discipline stays.

## 9. Error and status codes

No new error codes. Cases move from error to success; every remaining error
keeps its code, message shape and Props.

| Endpoint | Scenario | Today | Under this rule |
|---|---|---|---|
| `POST /api/entity/JSON/{name}/{ver}` | field holds the value, no `changeLevel` | `400 INCOMPATIBLE_TYPE` | **`200`** |
| `POST /api/entity/JSON/{name}/{ver}` | field holds the value, level below `TYPE` | `400 VALIDATION_FAILED` | **`200`** |
| `POST /api/entity/JSON/{name}/{ver}` | field does not, no `changeLevel` | `400 INCOMPATIBLE_TYPE` | unchanged |
| `POST /api/entity/JSON/{name}/{ver}` | field does not, level below `TYPE` | `400 VALIDATION_FAILED` (level named) | unchanged |
| `POST /api/entity/JSON/{name}/{ver}` | field does not, level ≥ `TYPE` | `200`, model widened | unchanged |
| `POST /api/entity/JSON/{name}/{ver}` | wrong JSON kind for every declared type | `400` | unchanged |
| `POST /api/entity/JSON/{name}/{ver}` | kind mismatch (array into scalar, …) | `400 VALIDATION_FAILED` | unchanged |
| `PUT /api/entity/JSON/{id}` | field holds the value | `400 INCOMPATIBLE_TYPE` | **`200`** |
| `PATCH /api/entity/JSON/{id}` | as strict validation above | `400 INCOMPATIBLE_TYPE` | **`200`** when held |
| `POST /api/entity/JSON/{name}/{ver}/collection` | field holds the value | `400` | **`200`** |
| processor output ingress (no HTTP status) | processor returns a held value | transition fails | **transition succeeds** |
| `POST /api/model/import/…/SAMPLE_DATA/…` | any | unchanged | unchanged |
| gRPC `EntityCreateRequest` | mirrors the HTTP rows | `CLIENT_ERROR` envelope | **`Success: true`** where HTTP becomes `200` |
| gRPC `EntityUpdateRequest` | mirrors the HTTP rows | `CLIENT_ERROR` envelope | **`Success: true`** where HTTP becomes `200` |

`ValidateOrExtend` is reached from `service.go:246` (create), `:1903` (update),
`:2197` (PUT), `:2566` (createCollection) and
`internal/domain/workflow/processor_output.go:68`. The last is the ingress the
`ingest` package's doc comment exists to serve, and a processor returning a
date-shaped string into a `STRING` leaf goes from failing the transition to
succeeding — a user-visible workflow behaviour change, not just an API one.

Unique keys keep their guard: a write making a unique-key leaf non-scalar still
returns `422 INVALID_UNIQUE_KEY_DEFINITION`. Unique-key signatures canonicalise
numerics by value and never read declared types (`cyoda-go-spi/unique_signature.go:164,178`),
so they are unaffected.

Search endpoints gain no status change, but §7 changes which rows they return:

| Endpoint | Scenario | Today | Under this rule |
|---|---|---|---|
| `POST /api/search/direct/{name}/{ver}` | `EQUALS 5.0` against an `INTEGER` leaf holding `5` | `200`, 0 hits | `200`, **1 hit** |
| `POST /api/search/direct/{name}/{ver}` | `NOT_EQUAL 5.0` against the same | `200`, **1 hit (wrong)** | `200`, 0 hits |
| `POST /api/search/direct/{name}/{ver}` | `EQUALS 5.000000000000000000` against a `DOUBLE` leaf holding `5` | `200`, 0 hits | `200`, **1 hit** |
| `POST /api/search/direct/{name}/{ver}` | `NOT_EQUAL -0.0` against a leaf holding `0` | `200`, **1 hit (wrong)** | `200`, 0 hits |
| `POST /api/search/direct/{name}/{ver}` | comparison against a stored value admitted under §4 | `200`, 0 hits | `200`, **the row** |
| `POST /api/search/direct/{name}/{ver}` | `[DOUBLE] < 1e300` against a stored `2147483648` | `200`, 0 hits | `200`, **the row** |
| `POST /api/search/direct/{name}/{ver}` | condition on a path no declared type accepts | `400 CONDITION_TYPE_MISMATCH` | unchanged |

## 10. Coverage matrix

| Scenario | Unit | Running-backend e2e | Cross-backend parity | gRPC |
|---|---|---|---|---|
| whole number into `DOUBLE`, all four levels | ✓ | ✓ | ✓ | ✓ |
| `2147483648` into `DOUBLE`: held, model unchanged | ✓ | ✓ | ✓ | ✓ |
| `2147483648` into `DOUBLE`: then found by search | ✓ | ✓ | ✓ | — |
| value at the `DOUBLE` ceiling: held, then found | ✓ | ✓ | ✓ | — |
| value above the `DOUBLE` ceiling: still gated | ✓ | ✓ | ✓ | — |
| `9007199254740993` into `DOUBLE` still gated (precision) | ✓ | ✓ | ✓ | — |
| `1.234567890123456` into `DOUBLE` still gated (precision) | ✓ | ✓ | — | — |
| `1e-400` into `DOUBLE` still gated (scale) | ✓ | ✓ | — | — |
| `1.5` into `UNBOUND_INTEGER` still gated (not whole) | ✓ | ✓ | — | — |
| high-scale value into `BIG_DECIMAL`: held, then found | ✓ | ✓ | ✓ | — |
| a held value never widens the leaf | ✓ | ✓ | ✓ | — |
| date-shaped string into `STRING`, strict | ✓ | ✓ | ✓ | ✓ |
| date-shaped string into `STRING`, each level | ✓ | ✓ | ✓ | — |
| date-shaped string into `STRING` then found by search | ✓ | ✓ | ✓ | — |
| `"2026"` into a `{STRING, LOCAL_DATE}` leaf stays `{STRING, LOCAL_DATE}` | ✓ | ✓ | ✓ | — |
| JSON number into `STRING` still gated | ✓ | ✓ | ✓ | ✓ |
| JSON boolean into `STRING` still gated | ✓ | ✓ | ✓ | — |
| JSON string `"2024"` into `INTEGER` still gated | ✓ | ✓ | ✓ | — |
| `"2026"` into `ZONED_DATE_TIME` still gated | ✓ | ✓ | ✓ | — |
| `"…+05:00"` into `LOCAL_DATE_TIME` still gated | ✓ | ✓ | ✓ | — |
| `EQUALS 5.0` finds a stored `5` | ✓ | ✓ | ✓ | — |
| `NOT_EQUAL 5.0` does not match a stored `5` | ✓ | ✓ | ✓ | — |
| `NOT(EQUALS 5.0)` and `NOT(NOT_EQUAL 5.0)` against a stored `5` | ✓ | ✓ | — | — |
| `EQUALS 5.000000000000000000` finds a stored `5` on `DOUBLE` | ✓ | ✓ | ✓ | — |
| `NOT_EQUAL -0.0` does not match a stored `0`; `EQUALS 0.000` does | ✓ | ✓ | — | — |
| `EQUALS 12.5` still finds nothing on `INTEGER` | ✓ | ✓ | — | — |
| out-of-range operands: `< 3e100` all rows, `> 3e100` none | ✓ | ✓ | ✓ | — |
| out-of-range `NotNull` residual: `[DOUBLE] < 1e300`, stored `2147483648` | ✓ | ✓ | ✓ | — |
| a large-scale operand is rejected, not expanded (DoS guard) | ✓ | ✓ | — | — |
| `BETWEEN` / `BETWEEN_INCLUSIVE` over the changed filter | ✓ | ✓ | ✓ | — |
| **mixed-kind array** `[2147483648, "hello"]` into a `[DOUBLE]` element | ✓ | ✓ | ✓ | — |
| homogeneous array element at `ARRAY_ELEMENTS` | ✓ | ✓ | ✓ | — |
| array width growth still gated at `ARRAY_LENGTH` | ✓ | ✓ | ✓ | — |
| new field still `STRUCTURAL`; new kind still `STRUCTURAL` | ✓ | ✓ | ✓ | — |
| nullable-marker promotion unchanged | ✓ | ✓ | — | — |
| unique-key leaf unaffected | ✓ | ✓ | — | — |
| registration still yields `{STRING, LOCAL_DATE}` | ✓ | ✓ | ✓ | — |
| strict validation is never more permissive than `ARRAY_LENGTH` | ✓ | ✓ | ✓ | — |
| `PATCH` with a held value succeeds | ✓ | ✓ | ✓ | — |
| `PUT` and collection-create with a held value succeed | ✓ | ✓ | ✓ | — |
| processor output returning a held value completes the transition | ✓ | ✓ | ✓ | — |
| numeric-leaf fold under concurrent extension (§6's open question) | ✓ | ✓ | ✓ | — |
| **property: held ⟹ declared types byte-identical, in a document that also carries a gated change in another field** | ✓ | ✓ | ✓ | — |
| **property: held ⟹ findable**, including the precision-16 and scale-292 `DOUBLE` boundaries | ✓ | ✓ | ✓ | — |

The last two are the design's invariant and must be property tests over the full
type × value matrix. The first of them must use documents carrying a gated
change in another field, or it passes vacuously against §6's defect.

### Existing assertions that invert

Three, and they are the parity, HTTP-e2e and unit legs of one behaviour:

- `e2e/parity/numeric_classification.go:187`
  `RunNumericClassificationDoubleSchemaAcceptsWholeNumber` — asserts `400` for
  `{"amount":2147483648}`; becomes `200`.
- `internal/e2e/model_double_whole_number_test.go:59` — **both** halves invert,
  the `400` and the `UNBOUND_DECIMAL` widening assertion.
- `internal/domain/model/schema/extend_assignable_test.go:113`
  `TestExtend_WholeNumberPastIntegerRange_IsStillATypeChange` — its verdict
  inverts and its doc comment must be rewritten to §5's reasoning, not deleted.

There is **no gRPC leg** for the `LONG` case —
`internal/grpc/model_double_whole_number_test.go:15` covers only `1000` and
stays green — so §10's gRPC ticks on the `2147483648` rows are all new tests.

Confirmed near-misses that do **not** invert, checked so they are not disturbed:
every `13.111`-into-`INTEGER` case; number and boolean into `STRING`; string
into a numeric leaf; and `e2e/parity/externalapi/polymorphism.go:220`, whose
`[LOCAL_DATE, YEAR_MONTH]` assertion comes from registration, which §8 leaves
alone. No `plugins/*` test touches `schema.Extend` or `ValidateOrExtend`.

One `gentree` catalog fixture did invert, for exactly §5's reason:
`DecimalBoundaryExceedsBigDecimal` fed a `BIG_DECIMAL` leaf a value with 20
fractional digits, expecting a widen — but `BIG_DECIMAL` admission is
magnitude-only, so a value differing only in fractional digits is already
held. Fixed by restating the fixture with a value whose magnitude exceeds
`BIG_DECIMAL`'s bound, which is what actually forces the widen.

## 11. Work

**cyoda-go**
1. Replace the walk-then-compare write path with a single traversal of the
   document against the stored model (§6), carrying the value to each decision
   and visiting array elements individually. `Validate` and `Extend` become one
   mechanism. Preserve every non-leaf rule §6 lists.
2. Retire the model-to-model `Extend` algebra: rewrite the 44 call sites and the
   six property suites §6 enumerates, and `e2e/parity/oracle.go:52,130` plus
   `e2e/parity/schema_extension_property.go`.
3. Registration becomes the same traversal against an empty model, keeping the
   per-document and per-element merge discipline (§6, §8).
4. §4's admission test, calling §5's shared SPI predicate.
5. Restate the convergence contract per §6: keep byte-identity for structural
   extension in `e2e/parity/schema_concurrent_convergence.go`, rewrite its doc
   comment to carve out numeric- and temporal-leaf widening, and add a parity
   scenario asserting the carve-out's property — monotone, and admits every
   written value.
6. Update the three inverting assertions (§10), rewriting the unit test's doc
   comment to §5's reasoning.
7. Register every new parity scenario in `e2e/parity/registry.go` **and** bump
   `wantParityScenarioCount` in `e2e/parity/registry_count_test.go:9` in the
   same commit.
8. Correct sqlite's `isLeafPushable` doc comment (§7) and add `COLLATE "C"` to
   postgres's text comparison branches (`query_planner.go:836-844`).
9. Guard the negative-scale expansion in `importer/walker.go:99-102` (§7).
10. Docs: `cmd/cyoda/help/content/models.md`, `docs/numeric-classification.md`,
    `CHANGELOG.md`.
11. Gate 7: reconcile with cyoda-cloud, log in `docs/cloud-parity/`.

**SPI (`cyoda-go-spi`)**
12. Export §5's `AdmitsNumeric(DataType, Decimal) bool` as the single admission
    predicate, and use it for the stored-value filter in `evalCompare` and
    `evalBetween` (§7 (i)). Delete the now-dead `classifyStoredNumeric`. Leave
    the "no declared type accepted the operand" gate untouched, and document
    that a multi-numeric declared set is outside the kernel's guarantees.
13. Strip trailing zeros once at `expandCompare`'s `ParseDecimal`
    (`eval_leaf.go:232`), covering both the integer and decimal families
    (§7 (ii)).
14. Guard `foldToInt`'s negative-scale expansion, and the `StripTrailingZeros`
    int32 scale underflow (§7).
15. Tag, one pin commit (`MAINTAINING.md`, `make repin-plugins`,
    `COMPATIBILITY.md`).

## 12. Not changing

- The numeric collapse — one collapsed numeric type per field, monotone-up-only,
  widening-only op catalog. A deliberate decision (sub-project A.1 §1, §5;
  A.2 §I3), and correct. The defect is the false premise the label comparison
  hands it.
- Registration semantics (§8).
- The `changeLevel` contract: the model never changes without permission.
- Temporal semantics **in the search kernel** (§7). Ingestion-side temporal
  admission *does* change — §4 — and an earlier draft wrongly generalised the
  kernel's "no change" to both sides.
- The operand range-and-round machinery (§7).
- Raw-document storage. Values are never rewritten on the way in.
- Unifying the SPI's three `DOUBLE` tests. This change names the conjunction two
  of them form as the admission predicate and gives it one home (§5); merging
  the third, inert one into it is separate work.

## 13. Cross-repo

Two consequences in the out-of-tree Cassandra backend, both made reachable by
§4(b) keeping a text-declared leaf at `[STRING]` while every subsequent write is
a timestamp. Tracked as `Cyoda/cyoda-go-cassandra#96`.

**The range path is the sharp one, and it is created by this design, not
widened by it.** `rangeSource` (`search/planner.go:190-198`) index-plans a
lookup whenever the operand classifies `Date` and the path's partition set is
Date-only. Under §4(b) that partition set is exactly `{Date}` — the model says
`[STRING]`, so the kernel compares **lexically** (`eval_leaf.go:460-465`,
`strings.Compare`) while the Date index orders **chronologically**. For UTC-`Z`
strings they agree; for mixed offsets they do not:

```
stored:  "2026-03-01T10:00:00+05:00"   (chronologically 05:00Z)
query:   >= "2026-03-01T06:00:00Z"
kernel  ([STRING], lexical):                    MATCH
index   (Date partition, chronological):        NOT A CANDIDATE  ->  row lost
```

Index under-selection is unrecoverable — `spi.MatchFilter` is the authoritative
post-filter (`search/direct_executor.go:411,464`) but never sees the row. Today
that write either returns `400` or widens the leaf to `{STRING,
ZONED_DATE_TIME}`, after which the kernel's temporal branch compares
chronologically and agrees with the index. §4(b) is what pins the model at
`[STRING]` and breaks that agreement.

**The equality guard gap is the pre-existing one.** The D1 guard
(`search/planner.go:308-316`) sits under `case op == OpEquals` and enumerates
only the numeric-and-text pair, so a path carrying both text and date partitions
can commit to one and miss rows in the other. §4(b) makes that coexistence
ordinary rather than occasional.

**Also worth naming:** `indextype.Classify` casts an out-of-int64-range float64
with a bare `int64(v)` (`internal/indextype/indextype.go:39-41`), while the
query side has `clampFloatToInt64` (`search/planner.go:227-236`) precisely to
avoid *"an implementation-defined int64() overflow cast"*. A stored `1e100`
indexes at int64-min while `> 1e50` plans `>= MaxInt64`, so the row is missed.
Today that needs an `UNBOUND_*` leaf; under §4 a plain `[DOUBLE]` leaf admits up
to ~1e293, so the set of models where it is reachable grows substantially.

That backend classifies values into index tables by the value's own shape, so
its bucketing is not otherwise affected, and it references no SPI symbol this
change removes — `classifyStoredNumeric` is unexported and `IsAssignableTo`
stays.

## 14. Adjacent, not in scope

#553 — the SQL pre-filter can drop rows the kernel would match. A different
layer, and §5's predicate keeps this change from feeding it: no admitted value
exceeds 2^53, so postgres's `cyoda_try_float8` pushdown gains no new lossy
cases, and `UNBOUND_*` leaves already admitted everything before this change.
