# Array shape and the change-level ladder — Cloud twin-alignment spec

cyoda-go **defines** the contract; Cyoda Cloud aligns to it.

This document states what a model says about an array, what each
`changeLevel` therefore permits, and what the exports and the search surface
show. It exists because the ladder's two lowest levels were named after a
positional array model that cyoda-go does not have, and the words on the
levels had drifted from what the system did.

## 1. The rule

**A model's array is a homogeneous list.** An array branch declares exactly
one thing: its element, a node like any other. The element may be
polymorphic — it holds the set of scalar types the positions have been
observed as, and it may carry an object or array branch of its own — but
every position is described by that one node.

Two things are deliberately **not** part of the model:

- **Position.** There is no type per index. `[1, "a", true]` is a list whose
  element declares `{INTEGER, STRING, BOOLEAN}`, not a three-slot tuple. A
  write of `["a", 1]`, `[true, true, true, true]` or `[]` against that field is
  held without any schema change.
- **Length.** The model records no width, minimum or maximum. A list of any
  length is held by the array that declared its element. A limit on how many
  entries a list may carry is a validation rule a user declares with a number
  they chose; it is never derived from what discovery happened to see.

The consequence for the write path is uniform: the only change an array can
propose is a change to its element.

## 2. The ladder

Levels are hierarchical; each permits everything below it.

| Level | Permits |
|---|---|
| `ARRAY_LENGTH` | **No schema change at all.** This is the floor of the ladder: a locked model at this level accepts exactly the documents the model already holds, a longer array included, and refuses every write that would move the model. |
| `ARRAY_ELEMENTS` | An array's element learning its first scalar type (an array branch that declares no element yet), or widening the scalar type it declares; the level keeps applying through nested array levels. Nothing outside an array may change. An object inside an array is a fresh scope: its fields cost `TYPE`. |
| `TYPE` | A leaf anywhere widening its declared scalar type. |
| `STRUCTURAL` | A new field, or a path gaining a kind it does not declare. |

`ARRAY_LENGTH` keeps its name for continuity with the enum every client
already spells. Its meaning is "nothing may change". A write that proposes
no change — which a longer array never does — is accepted at every level and
under strict validation (no `changeLevel` set), and the stored model is
byte-identical afterwards.

## 3. Exports

`SIMPLE_VIEW` renders an array's element by its declared scalar types under
the `[*]`-hopped path — `".tags[*]": "STRING"`, `".m[*][*]": "INTEGER"`,
`".poly[*]": "[INTEGER, STRING]"` — and never a width or a position. An
export describes the model, not the route the model took into memory: the
tree derived from a sample and the same tree read back from storage render
identically. `JSON_SCHEMA` emits `items`, never `prefixItems`, `minItems` or
`maxItems`.

## 4. Search

A positional path such as `$.a[0]` resolves against the model's single
`$.a[*]` entry and is evaluated against the stored value at that position.
The model has no per-index entry to consult. `docs/cloud-parity/path-grammar.md`
already records this; nothing here changes it.

## 5. What Cloud does today, and what aligning means

Cloud's structure model distinguishes a `UniTypeArray` (one element type plus
an explicit width, serialised `(T x N)`) from a `MultiTypeArray` (an ordered
array of per-index type sets, serialised `[T0|T1|T2]`); a heterogeneous
array is discovered as the positional form and collapses to the uniform one
only when every position agrees. Both forms persist width and positions, and
`SIMPLE_VIEW` shows them. Its ladder is defined against that model:
`ARRAY_LENGTH` permits a `UniTypeArray` to grow, `ARRAY_ELEMENTS` permits a
`MultiTypeArray` to change without introducing a type. Enforcement is
asymmetric: a shorter array is always accepted and recorded as nothing, an
index beyond the modelled width is coerced against the union of the
positions' types, and a reordered heterogeneous array is refused only when a
value fails coercion at its index.

Aligning means the array model becomes the list in §1: one element node,
no width, no positions, and the ladder reading in §2. The `(T x N)` and
`[T0|T1|T2]` renderings leave `SIMPLE_VIEW`, and the parse-time per-index
coercion is replaced by coercion against the element. Cloud's JSON Schema
export already presents arrays this way, so the external schema surface does
not move.

**The Trino schema generator is the one Cloud consumer of positions.** It
flattens a `MultiTypeArray` into one scalar column per index, and does the
same for arrays of `ZONED_DATE_TIME`. The second case is a workaround for a
tracked Cloud-side defect in how a zoned timestamp reaches the connector
(the zone is lost on the way), not a connector limitation. The Trino
connector itself needs neither: it builds `ARRAY(T)` from one element type
per array field, temporal element types included, and derives an array's
length at read time from the `path[i]` keys the data carries. Under the list model every array is emitted as a single `ARRAY(T)`
column, `T` being the element's collapsed type; a heterogeneous element is
emitted as `ARRAY(JSON)`. An integration between cyoda-go and the connector
starts from this contract.

## 6. Tests

- `internal/domain/model/schema/admit_test.go` —
  `TestAdmit_ArrayLengthIsNotASchemaChange`: a longer array against the very
  tree derived from a shorter one records nothing at `Admit`, `Validate` and
  `Extend` at `ARRAY_LENGTH`, and the model is byte-identical.
- `internal/domain/model/schema/extend_test.go` —
  `TestExtendArrayLengthHoldsALongerArray`.
- `internal/domain/model/exporter/faithfulness_test.go` —
  `TestSimpleView_ExportIsIndependentOfPersistence`: a derived tree and its
  storage round trip export byte-identically.
- `internal/e2e/model_extension_test.go` — `TestModelExtension_ArrayLength`:
  a longer array is accepted over HTTP at `ARRAY_LENGTH` and under strict
  validation, and the exported model does not move.
- `e2e/parity/type_admission.go` —
  `RunTypeAdmissionLongerArrayHeldAtEveryLevel`: on every backend, a longer
  array is accepted at `ARRAY_LENGTH` and under strict validation and the
  exported model is byte-identical afterwards;
  `RunTypeAdmissionStrictNeverMorePermissiveThanArrayLength`: the floor grants
  no permission a `TYPE`-level change needs.
