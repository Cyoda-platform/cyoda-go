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
  2³¹−1; and so on. A field of type T holds exactly the values in T's range.
  This is the definition of the type. Nothing else is.

`changeLevel` is a ceiling, not a budget. Nothing is "consumed" or "spent".

## 2. Why the types exist

The entity model exists so that storage backends can index data by type. A
backend that maintains its own persistence keeps a separate index table per
numeric type; without types, every number would have to be a big decimal and
every index the widest possible. The type dissection is a storage optimisation,
and its correctness requirement follows directly: **a field can hold a value
only if the value falls in the range of a type the field declares.**

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
> type's kind, and the value falls within that type. When it does, the model
> does not change and the write is permitted at any `changeLevel`. When it does
> not, the model must change to hold the value, and that change is permitted
> only at the model's configured level.**

"Falls within that type", per JSON kind:

| JSON kind | The field holds it when a declared type T is |
|---|---|
| number | numeric, and the value is inside T's range |
| string | `STRING`; or temporal and the string's classification is exactly T |
| boolean | `BOOLEAN` |
| null | any — the nullable marker, unchanged |

A JSON number is not a string and a JSON string is not a number, so `STRING`
does not become a universal sink.

Two consequences worth stating rather than leaving a reader to infer.

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
change, and §8's registration path remains the way a field acquires temporal
types.

The invariant this establishes, which nothing in the system claims today:

> **The field holds it ⟹ the model is unchanged ⟹ the value is findable
> under a declared type.**

It follows from §2 rather than needing separate machinery: admission uses the
type's range, and search buckets by the same range, so a value admitted into a
field is by construction inside the range that search will look in.

### Case table

| Declared | Value | Held? | Result |
|---|---|---|---|
| `DOUBLE` | `1000`, `1000.0`, `1e3` | yes | model unchanged |
| `DOUBLE` | `2147483648` | yes | model unchanged — (a) |
| `DOUBLE` | `9.99999999999999e292` | yes | at the ceiling, model unchanged |
| `DOUBLE` | `9.99999999999999e300` | no | above the ceiling; model change, gated |
| `DOUBLE` | `9007199254740993` | no | 16 significant digits; model change, gated |
| `INTEGER` | `2147483647` | yes | model unchanged |
| `INTEGER` | `2147483648` | no | above the ceiling; model change, gated |
| `INTEGER` | `13.111` | no | not a whole number; model change, gated |
| `STRING` | `"2026-03-01"` | yes | model unchanged — (b) |
| `STRING` | `"hello"` | yes | model unchanged |
| `STRING` | `5` | no | a number is not a string; model change, gated |
| `STRING` | `true` | no | model change, gated |
| `BOOLEAN` | `"true"` | no | a string is not a boolean; model change, gated |
| `INTEGER` | `"2024"` | no | a string is not a number; model change, gated |
| `ZONED_DATE_TIME` | `"2026-03-01T10:00:00Z"` | yes | model unchanged |
| `ZONED_DATE_TIME` | `"2026"` | no | a year is not a timestamp; model change, gated |
| `LOCAL_DATE_TIME` | `"2026-03-01T10:00:00+05:00"` | no | carries an offset; model change, gated |

## 5. A numeric type is its range, and only its range

The SPI currently carries three different tests for "is this a `DOUBLE`":

- a range ceiling, `9.99999999999999e292`, used when bucketing a search operand;
- a precision-and-scale test — at most 15 significant digits, scale within ±292
  — used when parsing a literal as a `DOUBLE`;
- a third variant used when deciding how to round a comparison.

They disagree. The precision-and-scale test reaches about 10³⁰⁷, fourteen orders
of magnitude past the ceiling.

The range is the definition; the others are approximations of it. Cyoda Cloud
states it the same way — `DOUBLE_MAX_VALUE` is computed from the type's
precision and scale and used as an explicit ceiling, with every numeric type
classified against a floor and ceiling pair.

**§4's numeric row means the range.** Using the precision-and-scale test instead
would admit values above the ceiling, which search cannot then find — the exact
failure §4 exists to prevent.

Reconciling the three tests inside the SPI is not in this change's scope, but
using the correct one here is.

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
- Registration folds in: importing sample data is the same traversal against an
  empty model, not a second code path.

### What the traversal must still do

The current `Extend` handles more than leaf types, and the replacement keeps all
of it: array width growth (`ARRAY_LENGTH`), an array learning its element type
(`ARRAY_ELEMENTS`), a path gaining a kind it does not declare (`STRUCTURAL`),
the nullable-marker promotion, and new object fields (`STRUCTURAL`). The
resulting model must still be a pure widening of the stored one, or `Diff` will
refuse it as non-additive.

## 7. Search

### What is correct and stays

The operand machinery — folding a comparison value into each declared type's
range, classifying it as below, inside or above, and rounding directionally — is
mathematically correct and is not being removed. Measured:

```
[DOUBLE]  <  3e100   stored 10.5  -> true    every double is below 3e100
[DOUBLE]  >  3e100   stored 10.5  -> false
[DOUBLE]  == 3e100   stored 10.5  -> false
[INTEGER] <  3e100   stored 5     -> true
[INTEGER] >  -3e100  stored 5     -> true
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
is not matched. The kernel must judge a stored number the way §4 does: against
the declared type's range. Applies to `evalCompare` and `evalBetween`.

Because numerics collapse to at most one per leaf, this filter never chooses
between buckets — it only asks whether the stored value belongs to the single
declared numeric type, and under §4 the answer is yes by construction.

**(ii) Operand normalisation — §3(c).** Strip trailing zeros in `foldToInt`
before the integrality test, as the ingestion classifier already does. This also
flips `NOT_EQUAL 5.0` against a stored `5` from wrongly-matching to correctly
not matching.

### The gate that must not move

Two gates sit near each other; only the first changes.

- *Does the stored value belong to the declared numeric type?* — (i) above.
- *Does this leaf declare a numeric type at all?* — **keep**. When a leaf
  declares none, the operand expansion produces an error, which surfaces as
  `400 INVALID_CONDITION` for an unresolvable path. Removing it would make such
  a path start matching rows via negative operators.

### Temporal values — no change

`"2026-03-01T10:00:00+05:00"` classifies as `ZONED_DATE_TIME`. A parser for
`LOCAL_DATE_TIME` also accepts it, by discarding the `+05:00` — and those denote
different instants. If a value like that were admitted into a
`[LOCAL_DATE_TIME]` field it would be stored with its offset, classified
`ZONED_DATE_TIME` at search time, and found by nothing; making search parse
instead of classify would make a query for 10:00 match a value that is really
05:00 UTC.

So for temporal types, "the field holds it" means the string's classification
equals the declared type. That is what the code already does on both sides, so
this design changes nothing here.

### Pushdown

Unaffected. The pushed SQL is built from the operand, not the declared types,
and the kernel re-checks every row it returns. Temporal data comparisons are not
pushed at all.

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
| `PATCH /api/entity/JSON/{id}` | as strict validation above | `400 INCOMPATIBLE_TYPE` | **`200`** when held |
| `POST /api/model/import/…/SAMPLE_DATA/…` | any | unchanged | unchanged |
| gRPC `EntityCreateRequest` | mirrors the HTTP rows | `CLIENT_ERROR` envelope | **`Success: true`** where HTTP becomes `200` |

Unique keys keep their guard: a write making a unique-key leaf non-scalar still
returns `422 INVALID_UNIQUE_KEY_DEFINITION`.

Search endpoints gain no status change, but §7 changes which rows they return:

| Endpoint | Scenario | Today | Under this rule |
|---|---|---|---|
| `POST /api/search/direct/{name}/{ver}` | `EQUALS 5.0` against an `INTEGER` leaf holding `5` | `200`, 0 hits | `200`, **1 hit** |
| `POST /api/search/direct/{name}/{ver}` | `NOT_EQUAL 5.0` against the same | `200`, **1 hit (wrong)** | `200`, 0 hits |
| `POST /api/search/direct/{name}/{ver}` | comparison against a stored value admitted under §4 | `200`, 0 hits | `200`, **the row** |
| `POST /api/search/direct/{name}/{ver}` | condition on a path declaring no matching type | `400 INVALID_CONDITION` | unchanged |

## 10. Coverage matrix

| Scenario | Unit | Running-backend e2e | Cross-backend parity | gRPC |
|---|---|---|---|---|
| whole number into `DOUBLE`, all four levels | ✓ | ✓ | ✓ | ✓ |
| `2147483648` into `DOUBLE`: held, model unchanged | ✓ | ✓ | ✓ | ✓ |
| `2147483648` into `DOUBLE`: then found by search | ✓ | ✓ | ✓ | — |
| value at the `DOUBLE` ceiling: held, then found | ✓ | ✓ | ✓ | — |
| value above the `DOUBLE` ceiling: still gated | ✓ | ✓ | ✓ | — |
| `9007199254740993` into `DOUBLE` still gated | ✓ | ✓ | ✓ | — |
| a held value never widens the leaf | ✓ | ✓ | ✓ | — |
| date-shaped string into `STRING`, strict | ✓ | ✓ | ✓ | ✓ |
| date-shaped string into `STRING`, each level | ✓ | ✓ | ✓ | — |
| date-shaped string into `STRING` then found by search | ✓ | ✓ | ✓ | — |
| JSON number into `STRING` still gated | ✓ | ✓ | ✓ | ✓ |
| JSON boolean into `STRING` still gated | ✓ | ✓ | ✓ | — |
| JSON string `"2024"` into `INTEGER` still gated | ✓ | ✓ | ✓ | — |
| `"2026"` into `ZONED_DATE_TIME` still gated | ✓ | ✓ | ✓ | — |
| `"…+05:00"` into `LOCAL_DATE_TIME` still gated | ✓ | ✓ | ✓ | — |
| `EQUALS 5.0` finds a stored `5` | ✓ | ✓ | ✓ | — |
| `NOT_EQUAL 5.0` does not match a stored `5` | ✓ | ✓ | ✓ | — |
| `EQUALS 12.5` still finds nothing on `INTEGER` | ✓ | ✓ | — | — |
| out-of-range operands: `< 3e100` all rows, `> 3e100` none | ✓ | ✓ | ✓ | — |
| `BETWEEN` / `BETWEEN_INCLUSIVE` over the changed filter | ✓ | ✓ | ✓ | — |
| **mixed-kind array** `[2147483648, "hello"]` into a `[DOUBLE]` element | ✓ | ✓ | ✓ | — |
| homogeneous array element at `ARRAY_ELEMENTS` | ✓ | ✓ | ✓ | — |
| array width growth still gated at `ARRAY_LENGTH` | ✓ | ✓ | ✓ | — |
| new field still `STRUCTURAL`; new kind still `STRUCTURAL` | ✓ | ✓ | ✓ | — |
| nullable-marker promotion unchanged | ✓ | ✓ | — | — |
| unique-key leaf unaffected | ✓ | ✓ | — | — |
| registration still yields `{STRING, LOCAL_DATE}` | ✓ | ✓ | ✓ | — |
| strict validation is never more permissive than `ARRAY_LENGTH` | ✓ | ✓ | ✓ | — |
| **property: held ⟹ declared types byte-identical, in a document that also carries a gated change in another field** | ✓ | ✓ | ✓ | — |
| **property: held ⟹ findable** | ✓ | ✓ | ✓ | — |

The last two are the design's invariant and must be property tests over the full
type × value matrix. The first of them must use documents carrying a gated
change in another field, or it passes vacuously against §6's defect.

**An existing parity assertion inverts.**
`RunNumericClassificationDoubleSchemaAcceptsWholeNumber` currently asserts that
`{"amount":2147483648}` returns `400`. Under §4 it returns `200`. Updating it is
work, not incidental.

## 11. Work

**cyoda-go**
1. Replace the walk-then-compare write path with a single traversal of the
   document against the stored model (§6), carrying the value to each decision
   and visiting array elements individually. `Validate` and `Extend` become one
   mechanism. Preserve every non-leaf rule §6 lists.
2. Registration becomes the same traversal against an empty model.
3. §4's admission test, using the declared type's range for numbers (§5).
4. Update the inverted parity assertion (§10).
5. Docs: `cmd/cyoda/help/content/models.md`, `docs/numeric-classification.md`,
   `CHANGELOG.md`.
6. Gate 7: reconcile with cyoda-cloud, log in `docs/cloud-parity/`.

**SPI (`cyoda-go-spi`)**
7. `eval_leaf.go`: judge a stored number against the declared type's range in
   `evalCompare` and `evalBetween` (§7 (i)). Leave the "declares a numeric type
   at all" gate untouched.
8. `numeric_bucket.go`: strip trailing zeros in `foldToInt` (§7 (ii)).
9. Tag, one pin commit (`MAINTAINING.md`, `make repin-plugins`,
   `COMPATIBILITY.md`).

## 12. Not changing

- The numeric collapse — one collapsed numeric type per field, monotone-up-only,
  widening-only op catalog. A deliberate decision (sub-project A.1 §1, §5;
  A.2 §I3), and correct. The defect is the false premise the label comparison
  hands it.
- Registration semantics (§8).
- The `changeLevel` contract: the model never changes without permission.
- Temporal semantics on either side (§7).
- The operand range-and-round machinery (§7).
- Raw-document storage. Values are never rewritten on the way in.
- Reconciling the SPI's three "is this a `DOUBLE`" tests (§5) — this change uses
  the correct one; unifying them is separate work.

## 13. Cross-repo

Admitting a timestamp-shaped string into a text-declared field at any change
level makes it ordinary for one field path to carry both text and date index
partitions in the out-of-tree Cassandra backend, whose equality guard for that
situation covers only the numeric-and-text pair. Pre-existing; this design
widens the exposure. Tracked as `Cyoda/cyoda-go-cassandra#96`.

That backend classifies values into index tables by the value's own shape, so
its bucketing is not otherwise affected, and it references no SPI symbol this
change removes.

## 14. Adjacent, not in scope

#553 — the SQL pre-filter can drop rows the kernel would match. A different
layer, independent in both directions.
