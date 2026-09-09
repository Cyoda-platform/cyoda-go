# Type admission on write — Cloud twin-alignment spec

cyoda-go **defines** the contract; Cyoda Cloud aligns to it.

This document covers what changed about whether a field **holds** a value it
was not previously declared to hold, and — because the two answers must
agree — which rows a comparison returns against a value that was admitted
this way. `docs/numeric-classification.md` is the detailed numeric reference
this document summarises for Cloud; read that one for the full worked
examples.

## 1. The rule

A field holds a value when the value's JSON kind matches a declared type's
kind, and the value is one that declared type **admits**:

| JSON kind | The field holds it when a declared type `T` is |
|---|---|
| number | numeric, and the value satisfies `T`'s admission predicate (§2) |
| string | `STRING`; or temporal and the string's classification is exactly `T` |
| boolean | `BOOLEAN` |
| null | any declared type |

When a field holds the value, the model does not change and the write is
accepted at any schema-change permission level, including the strictest one.
When it does not, the write proposes a schema change, gated by that
permission the same as any other structural change.

**The `null` row is about admission, not about the model being inert.** A
leaf that already declares a scalar admits `null` and records nothing — an
`[INTEGER]` field that is not marked nullable still holds an explicit `null`
with no model change. A node that declares *no* scalar at all is different:
it still charges the nullable-marker promotion, gated at the scalar-kind
permission level, the same as learning any other first kind. The row means
"no declared type refuses null", not "null never changes the model".

## 2. Numeric admission is a predicate, not a range check

The test is per numeric family, evaluated directly against the value — never
against the value's own classified label, and never against range alone:

| Declared type | The field holds value *v* when |
|---|---|
| an integer family type (32-, 64-, 128-bit) | *v* is whole (after stripping trailing zeros) **and** within the type's own bounds |
| the unbounded integer type | *v* is whole (after stripping trailing zeros) — no bound |
| the IEEE-754-range decimal type ("`DOUBLE`") | \|*v*\| is within its magnitude range **and** representable in at most 15 significant digits **and** a scale of at most 292 |
| the fixed-precision decimal type ("`BIG_DECIMAL`") | \|*v*\| is within its magnitude range — magnitude only, no precision test |
| the unbounded decimal type | always |

**Range alone is not sufficient for the IEEE-754-range type.** A value can be
well inside that type's magnitude range and still be one an equality
comparison can never find. For a non-comparing operator (`EQUALS`/
`NOT_EQUAL`), the query side's own operand bucket refuses to build a
comparison branch for an operand needing more than 15 significant digits or
a scale past 292 — independent of magnitude. (A comparison operator — `<`,
`>`, `<=`, `>=` — rounds such an operand instead of refusing it, so this is
specifically an equality-family hazard.) Admitting such a value on range
alone stores it somewhere `EQUALS` can never find it and `NOT_EQUAL` wrongly
matches it. The precision-and-scale conjunct is what keeps write-time
admission and read-time findability in agreement; a backend that admits by
range alone will diverge from cyoda-go on this class of value.

**One predicate, two consumers.** cyoda-go exports this test as a single
function and uses it in exactly two places: deciding whether a write changes
the model, and filtering a stored value at query time before a comparison is
evaluated against it. A backend that maintains its own numeric bucketing
(rather than deferring to the shared kernel) must keep both decisions on the
same test, or it reproduces the defect this rule exists to close.

## 3. What changed on ingestion

- **A value past the IEEE-754-range type's old classification boundary is
  now held without widening the model**, as long as the value's own
  magnitude, precision and scale fit — a ten-digit whole number, for
  instance. Only a value needing more than 15 significant digits, or a
  scale past 292, or a magnitude past that type's own ceiling, still forces
  a schema change.
- **A text-declared field now holds a date- or timestamp-shaped string
  without changing the model, and an entity write no longer promotes it to
  a temporal type.** Registration (importing sample data) remains the only
  way a field acquires a temporal type alongside its text declaration. A
  field that is *also* declared temporal still holds a value under that
  type only when the string's classification is exactly that type — this
  part is unchanged.
- **Numeric- and temporal-leaf model folding is order-dependent under
  concurrent extension, and Cloud should expect this rather than treat it
  as a bug.** Byte-identical convergence remains the contract for
  structural extension (new fields, new kinds, the nullable
  marker). For numeric- and temporal-leaf widening specifically, the
  property that holds instead is: **every reachable fold is monotone and
  admits every value that was written.** Two nodes applying the same two
  writes in different orders, or under a gossip window where one node's
  model snapshot lags, can converge on different declared sets — never on a
  model that lost a written value, and never on one that narrows what it
  already held.

## 4. Which rows a comparison returns

The rows a comparison returns must reflect the same admission test, applied
to the *stored* value, not the value's narrowest classified label:

| Scenario | Result |
|---|---|
| `EQUALS 5.0` against a stored `5` | matches |
| `NOT_EQUAL 5.0` against a stored `5` | does not match |
| `EQUALS 5.000000000000000000` (trailing zeros) against a stored `5` | matches |
| `NOT_EQUAL -0.0` against a stored `0` | does not match |
| a comparison against a value admitted under §1/§2 that would previously have been refused as a type change | matches, where it did not before |
| a condition naming a path no declared type accepts | rejected before evaluation, unchanged |

The operand side needs the matching fix: an operand's trailing zeros were
never stripped before precision was computed, which is exactly why
`EQUALS 5.0` returned no rows — `"5.0"` has two significant digits until
the trailing zero is stripped, and the unstripped decimal is what precision
was measured against. The operand is now stripped once, at the point it is
parsed, so both the integer and decimal numeric families see the same
canonical form: an operand carrying nineteen trailing zeros is no longer
judged "imprecise" and silently dropped from the comparison.

## Cloud obligations

- **Implement the admission test as one function, used at both write time
  and read time.** Two independently-maintained copies — one deciding
  whether a write changes the model, another filtering a stored value at
  query time — will drift, which is the defect this document exists to
  close.
- **Admission is per-value, not per-label.** Do not derive "does the field
  hold this value" by first classifying the value into a type label and
  then checking that label against the declared set; classify only to
  decide what a *rejected* value's schema change would need to widen to.
- **Accept the order-dependence in §3**, and do not add a convergence check
  that assumes byte-identical results for numeric- or temporal-leaf
  widening under concurrent writes — assert monotonicity and that no
  written value is ever lost, not a specific final shape.
- **A backend that classifies a value into an index table by the *stored
  value's own shape* — rather than by the field's declared type — needs to
  re-check that indexing against this change.** The evaluation kernel always
  compares by the field's declared type: a text-declared field compares
  lexically, full stop, whatever a given stored string looks like. Before
  this change, a text-declared field that kept receiving date-shaped strings
  would eventually widen to also declare the matching temporal type, at
  which point a chronological index over it agreed with the kernel's own
  (now temporal) comparison. Under this change that widening no longer
  happens on an ordinary write, so a field can stay declared `STRING` — and
  compared lexically by the kernel — indefinitely, even while every stored
  value in it looks like a timestamp. A value-shape-driven index does not
  know this: it still routes such a value into a chronologically-ordered
  index entry. For a timestamp carrying a non-UTC offset, lexical and
  chronological order do not coincide, so a range query the index answers
  from that entry can omit rows the kernel's own lexical comparison — the
  authoritative one — would have matched, and that omission is not
  recoverable by a post-filter, because the row was never a candidate.
  **The fix is to key indexing eligibility to the field's *declared* type,
  the same input the evaluation kernel itself compares by, never to a
  stored value's own apparent shape.**

## Test surface

- `internal/domain/model/schema/admit_test.go` and
  `internal/domain/model/schema/extend_assignable_test.go` — the admission
  predicate at write time, per numeric family and per JSON kind.
- `internal/domain/model/schema/fold_order_test.go` — application-order
  independence for a delta already produced (a narrower property; it
  explicitly disclaims the production-side order-dependence itself, which
  is a property of `Extend`/`Diff`, not of applying a delta already
  computed).
- `cyoda-go-spi`'s `eval_leaf_test.go` — the stored-value filter in
  `evalCompare`/`evalBetween` judging by admission, not by classified label.
- `e2e/parity/schema_concurrent_convergence.go` — byte-identical convergence
  for structural extension, across every backend wired into the parity
  suite; its doc comment defers the numeric-leaf carve-out to the next item.
- `e2e/parity/schema_numeric_fold_carveout.go`
  (`RunSchemaNumericFoldCarveout`) — the carve-out property itself, exercised
  SEQUENTIALLY: two orders, each against its own tenant, writing the same two
  numeric values in reverse order and asserting each order's leaf declares
  the type that order's widening reaches, with every value still findable.
  Numeric-only — no temporal value appears in either order. §3's broader
  claim (the property holds under genuinely concurrent writes, numeric or
  temporal) is argued from the widening lattice being admission-monotone,
  not demonstrated by a test: nothing in the parity suite exercises
  concurrent numeric-leaf extension.
- `e2e/parity/type_admission.go` — the design's backend-agnostic scenarios:
  held values leave the model byte-identical, a held value is findable
  afterward, the `DOUBLE`-ceiling gated boundary, mixed-kind arrays judged
  element by element, the search-side `EQUALS`/precision fix, registration
  still yielding `{STRING, LOCAL_DATE}`, and strict validation never being
  more permissive than `ARRAY_LENGTH`.
- `internal/e2e/` — the full write-then-search round trip for a value
  admitted under this rule, over HTTP.
- `internal/grpc/type_admission_test.go` — the same coverage over gRPC:
  held values succeeding at every level, an unheld value still rejected,
  and the number-into-`STRING` kind mismatch.
