# Sub-project A.2 — Schema-Transformation Round-Trip Coverage

**Date:** 2026-04-21
**Revision:** 2 (post-review-01, 2026-04-21)
**Parent initiative:** Data-ingestion QA (Option 1 decomposition: sub-projects A, B, C, D)
**Reviews:**
- `docs/superpowers/reviews/2026-04-21-data-ingestion-qa-subproject-a2-review-01.md` (incorporated in rev 2)

**Prior work:**
- `docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md` — the ExtendSchema pipeline.
- `docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md` (rev 3) — numeric classifier parity (prereq, complete).

## 1. Purpose

Establish a single master invariant that drives comprehensive coverage of the schema-transformation pipeline:

> **Round-trip:** `Apply(old, Diff(old, Extend(old, importer.Walk(data), level))) ≡ Extend(old, importer.Walk(data), level)` byte-for-byte (after `schema.Marshal`).

That one assertion, exercised across the full shape-space that `importer.Walk` can produce and the full `ChangeLevel` permission matrix, catches:

- Any `Extend` output that `Diff` can't encode (including the silent-nil class — see I1-bis below).
- Any `Apply` replay that diverges from `Extend`'s direct result.
- Any commutativity violation across concurrently-produced deltas.
- Any validation-monotonicity violation (a delta that tightens the accepted-value set).

The live bug we hit mid-stream (array-of-OBJECT element not handled by Diff, which surfaced as `Diff` silently returning nil) is exactly the class this coverage catches — provided the nil-return case is constrained by I1-bis.

## 2. Scope

### 2.1 In scope

Four orthogonal axes, plus supporting infrastructure:

**Axis 1 — Input shapes.** Every shape `importer.Walk` can produce, driven through the pipeline. The primitive list below is **authoritative — matches A.1 rev 3 §2.3**:
- Scalars at every `DataType` primitive (post-A.1 enum: Integer, Long, BigInteger, UnboundInteger, Double, BigDecimal, UnboundDecimal, String, Character, LocalDate…ZonedDateTime, Year, YearMonth, UUIDType, TimeUUIDType, ByteArray, Boolean, Null).
- Flat object, nested object (depth 2, 5, 10), deeply nested (depth 50) as a recursion-sanity test.
- Wide object (100+ siblings) as a quadratic-cost sanity test.
- Array of same-kind scalars, array of mixed-kind scalars (polymorphic leaf element), array of objects (homogeneous element), array of objects with sparse keys (incomplete across items), nested arrays (2D, 3D), array of array of objects, empty array `[]`, single-element array.
- Null at a leaf slot. (The `{"a": null}`-vs-`{"a": {...}}` null↔object kind-conflict case is polymorphic-slot territory and routes to Axis 2 kind-conflict / A.3 — see §5.4.)
- Numeric boundary values: 2^53+1, 2^63 (LONG→BIG_INTEGER boundary), 2^127 (BIG_INTEGER→UNBOUND_INTEGER boundary), 18-fractional-digit decimal (BIG_DECIMAL definite-fit), 20-fractional-digit decimal (UNBOUND_DECIMAL scale boundary).
- Same-key-different-depth (`{"a":{"b":1,"c":{"b":"x"}}}`).
- Unicode edge cases in keys (4-byte codepoints, combining marks).

**Axis 2 — Transformations (kind matrix).** Every `(existingKind × incomingKind)` cell at the slot level, for every `ChangeLevel`:
- L-L, L-O, L-A, O-L, O-O, O-A, A-L, A-O, A-A, ⊥-anything, anything-⊥.
- Kind-conflict cells (L-O, L-A, O-L, O-A, A-L, A-O) — where `schema.Merge` promotes to a polymorphic-slot shape — are written as **`t.Skip("polymorphic-slot semantics: see issue #X")`** tests. They document the intended semantics as failing tests; implementation lands in Sub-project A.3.
- Silent-drop cells in Extend (LEAF→ARRAY, OBJECT→LEAF, etc.) get **`Extend`-contract tests** (not round-trip): verify that Extend does what it does today (returns unchanged) and that the behavior is intentional, not a bug the A.2 test suite accidentally validates.

**Axis 3 — `ChangeLevel` enforcement.** ChangeLevels form a **linear order**:

> `"" < ArrayLength < ArrayElements < Type < Structural`, where each level permits all strictly-lower-level changes.

For each level ∈ that order:
- Every change class ≤ level is accepted.
- Every change class > level is rejected with the correct error (`change level X requires Y`).
- Rejection is deterministic, in-memory-atomic (no mutation of the input `*ModelNode`; see I7), and surfaces the offending path.
- Mixed-class inputs under a restricted level fail atomically.

**Axis 4 — Round-trip invariants.** Property-based, generator-driven. See §3 for the full invariant set (I1, I1-bis, I2, I3 with dual+corollary, I4, I5, I6, I7).

### 2.2 Infrastructure

**Generator package** `internal/domain/model/schema/gentree/`:
- `GenValue(rng *rand.Rand, depth, maxWidth int, cfg GenConfig) any` — produces random JSON-ish Go values suitable for `importer.Walk`.
- `GenModelNode(rng, depth, maxWidth, cfg GenConfig) *schema.ModelNode` — produces random `ModelNode` trees directly (tree-driven mode).
- `GenExtensionPair(rng, old *schema.ModelNode, level spi.ChangeLevel, cfg GenConfig) (incoming any)` — given an existing schema and a target ChangeLevel, generates JSON data whose importer output triggers exactly that class of extension.
- `Catalog` — enumerated slice of ~40 named `(old, new, level, expectedDeltaKinds)` fixtures covering every Axis-1 shape + every Axis-2 cell that's not skipped.

**Generator configuration (`GenConfig`):**
- `Seed int64` — reproducible randomness.
- `MaxDepth int` — bounds tree depth for recursion safety.
- `MaxWidth int` — bounds object fan-out and array length.
- `KindWeights struct{Leaf, Object, Array float64}` — skew toward realistic shape distributions.
- `PrimitiveWeights map[DataType]float64` — skew toward common primitives vs. edge-case types.
- `AllowNulls bool` — include `nil` leaves in generated trees.

### 2.3 Principles (non-requirements)

- **No weakening under divergence.** A round-trip failure in a fixture that matches Cyoda Cloud exactly is a real bug in `Diff`/`Apply` — halt and surface, do not weaken the assertion. This is a policy about test evolution, not a risk to mitigate.
- **Canary over logic.** I4 and I5 are corollaries of I1/I2 under the documented semantics of `Apply`; they are still tested independently because a regression in the corollary often surfaces earlier than it would through the master invariant on a specific generated sample (see I4/I5 note in §3).

### 2.4 Out of scope

- **Polymorphic-slot kind-conflict implementation.** Cells that require Extend/Diff/Apply to handle kind changes (LEAF↔OBJECT etc.) are documented as `t.Skip` tests with an issue link. Sub-project A.3 addresses this with its own design cycle.
- **Concurrency under load.** Sub-project C covers multi-writer stress, gossip-loss simulation, cache-staleness under contention.
- **Input boundary hardening.** Sub-project D covers malformed JSON, size limits, Unicode at the HTTP layer.
- **Plugin-internal fold correctness at savepoint boundaries.** Sub-project B covers Postgres savepoint semantics and byte-identical folds across backends.
- **Delta-lattice `merge` function.** No `merge(d1, ..., dN)` operator is designed, exposed, or tested here. If a future sub-project introduces a true associative delta-lattice join, associativity (distinct from I5 permutation-invariance) becomes testable then.
- **Fuzz testing (`testing.F`).** Property-based tests with deterministic seeds give us breadth + reproducibility without the corpus-management overhead. Fuzz is reserved for Sub-project D where bytes-level input is the adversary.

## 3. Invariants

**(I1) Round-trip master invariant.** For every generated (old, incoming, level) tuple:
```
let extended = Extend(old, Walk(incoming), level)
let delta    = Diff(old, extended)
let applied  = Apply(old, delta)
Marshal(applied) == Marshal(extended)
```

**(I1-bis) Diff-nil correspondence.**
```
Diff(old, extended) == nil   iff   Marshal(old) == Marshal(extended)
```
Any case where `Marshal(old) != Marshal(extended)` but `Diff` returns `nil` is a silent-encoding-gap bug — the class of bug A.2 exists to catch (as the array-of-OBJECT `Diff`-returns-nil incident demonstrated). I1-bis is asserted directly in `roundtrip_property_test.go` on every generated sample: whenever `delta == nil`, the test verifies `Marshal(old) == Marshal(extended)`.

**(I2) Commutativity.** For every pair of deltas `(d1, d2)` produced by Diff on a shared base, in any path-relationship (disjoint, equal, prefix/ancestor), and for every path-distribution sample: `Apply(Apply(b, d1), d2)` and `Apply(Apply(b, d2), d1)` produce `Marshal`-equal trees.

**(I3) Validation-monotonicity.** For every catalog op kind `k`, every document `D` that validates against schema `B` also validates against `Apply(B, {op-of-kind-k})`.

Cyoda's schema model has no required fields — the accepted-set of a schema grows monotonically with each extension because extensions only widen types, add siblings, and expand kind unions. There are no narrowing operations that could shrink the accepted set.

**Dual.** A document `D` rejected by `Apply(B, d)` for a given path and reason is also rejected by `B` at that same path for the same reason. Schema extensions never introduce new rejection causes; they only widen the set of accepted shapes. This is the more informative half of the invariant — it guarantees extensions don't mask non-schema validation failures (value-range violations, type mismatches on present fields, workflow-validation rejections).

**Corollary.** Any document `D` valid against `B` is valid against `Apply(B, d1, d2, ..., dn)` for any sequence of extensions. Once accepted, always accepted — through all future schema evolution.

**(I4) Idempotence.** `Apply(Apply(b, d), d) == Apply(b, d)`. Ingesting the same JSON twice yields the same schema and the same entity body.

**(I5) N-permutation-invariance.** For any set of N deltas `{d1, ..., dN}` produced by Diff on a shared base `b`, any permutation σ over `{1..N}` yields the same result:
```
Apply(Apply(...Apply(b, d_σ(1))..., d_σ(N-1)), d_σ(N))  ==  Apply(Apply(...Apply(b, d1)..., d_{N-1}), dN)
```
N-permutation-invariance is the natural extension of I2 from 2 to N deltas. **Note: this is not associativity.** Associativity would require a delta-lattice `merge` function (`Apply(b, merge(d1, ..., dN))`), which does not exist in cyoda-go today; see §2.4.

> **Note (I4/I5 as canaries).** Both I4 and I5 are corollaries of I1 (round-trip) and I2 (commutativity) under the documented semantics of `Apply`. They are tested independently as canaries: a regression in a corollary often surfaces faster on its targeted test than it would through I1/I2 on a specific generated sample.

**(I6) Extend-completeness.** For every (old, extended) pair where `Marshal(old) != Marshal(extended)`, `Diff(old, extended)` produces a non-nil delta whose op kinds are all in the documented catalog. No `extend` output is Diff-unencodable, and no silent nil-return slips past I1-bis.

**(I7) In-memory atomicity on rejection.** If `Extend(old, Walk(data), level)` rejects with a change-level violation, the input `*ModelNode old` is not mutated — `Marshal(old)` returns the same bytes before and after the rejected `Extend` call. Cross-storage atomicity on plugin persistence paths is Sub-project B's concern.

All invariants are asserted by the property-based test harness and a curated fixture catalog.

## 4. Architecture

### 4.1 Generator package

Lives at `internal/domain/model/schema/gentree/`. Three files:

- `gentree.go` — public surface (`GenValue`, `GenModelNode`, `GenExtensionPair`, `GenConfig`, `DefaultConfig`).
- `catalog.go` — `Catalog` slice of enumerated named fixtures. Each entry carries a human-readable label, an `old` ModelNode or JSON literal, a `new` ModelNode or JSON literal, an expected `ChangeLevel`, and expected `DataType`s in the resulting delta ops.
- `gentree_test.go` — unit tests on the generator itself (determinism by seed, depth/width bounds respected, produced values parseable by `Walk`).

Placement under `internal/domain/model/schema/` keeps the generator colocated with the types it produces. It imports `importer` for its `Walk` convenience in property tests — the generator emits `any`-typed values and callers choose whether to feed through `Walk` or construct `ModelNode`s directly.

**Determinism discipline.** Generator code **must not `range` over Go maps** when emitting tree structure. Go's `map` iteration order is explicitly randomized, which would silently break seed-reproducibility. When child ordering depends on map data (e.g., deriving keys from `PrimitiveWeights`), extract a sorted key slice first and iterate that. A meta-test in `gentree_test.go` (`TestGeneratorIsMapFree`) runs the same seed twice and asserts byte-identical `ModelNode` output; a generator that accidentally ranges over a map fails this test with high probability.

### 4.2 Property-test harness

One test file per invariant, under `internal/domain/model/schema/`:

- `roundtrip_property_test.go` — Axis 4 I1 + I1-bis + I6.
- `commutativity_property_test.go` — Axis 4 I2 with path-relationship axis cross-product.
- `monotonicity_property_test.go` — Axis 4 I3 (direct + dual).
- `idempotence_property_test.go` — Axis 4 I4.
- `permutation_property_test.go` — Axis 4 I5.

Each test file:
1. Seeds a deterministic `rand.Rand` from a table of seeds (each seed becomes a subtest).
2. Generates an input sample via `gentree`.
3. Runs the invariant check.
4. On failure: emits `old`, `new`, `delta`, `applied`, and the diffing bytes as subtest failure output for debugging.

Each file runs 100–1000 seeded samples per test depending on the cost per sample. The master round-trip is cheapest; commutativity is moderate; monotonicity involves validation calls and is the most expensive per sample.

### 4.3 Axis-2 kind-matrix harness

Hand-written at `internal/domain/model/schema/axis2_kind_matrix_test.go`. Table-driven, one row per `(existingKind, incomingKind, level)` cell. Kind-conflict rows that require polymorphic-slot implementation are marked `t.Skip("polymorphic-slot semantics: see issue #<N>")` — they name the desired outcome but do not fail until A.3 lands.

### 4.4 Axis-3 ChangeLevel harness

Hand-written at `internal/domain/model/schema/axis3_changelevel_test.go`. Table-driven `(changeLevel, input-class) × (accept|reject|error-path)` cases. In-memory atomicity (I7) is verified by asserting `Marshal(old)` is byte-identical before and after a rejected `Extend` call.

### 4.5 Fixture catalog integration

`gentree.Catalog` entries are exercised twice:
1. Each entry runs through the master round-trip as a named subtest (regression bedrock).
2. Each entry that declares `expectedDeltaKinds` is verified to produce exactly those op kinds — a bidirectional assertion against Diff.

This gives us ~40 named regression tests that `go test -run` can target individually, plus the generator's breadth via random seeds.

### 4.6 Existing test-file migration

Existing tests in `apply_test.go`, `diff_test.go`, `properties_test.go`, and `completeness_test.go` are kept as-is — they cover unit-level Apply/Diff semantics for the three op kinds. A.2 adds the integration-level property/fixture harness on top; it does not replace unit coverage.

## 5. Test specification

### 5.1 Catalog contents (Axis 1 × Axis 2 × sample)

At minimum, the catalog includes one entry for each of:

| Category | Example fixture label |
|---|---|
| Flat object, add property | `FlatObjectAddSibling` |
| Nested object, add property | `NestedObjectAddLeaf` |
| Deeply nested (depth 10+) | `DeeplyNestedIntegerExtend` |
| Wide object (100 fields) | `WideObjectAddOne` |
| Array of scalars, element widen | `ArrayIntegerWidenToLong` |
| Array of objects, add field in element | `ArrayOfObjectAddFieldInElement` |
| Array of objects, widen leaf in element | `ArrayOfObjectWidenLeafInElement` |
| Nested arrays 2D, widen inner leaf | `ArrayOfArrayWidenInnerLeaf` |
| Nested arrays 3D | `ArrayOfArrayOfArrayElement` |
| Polymorphic leaf (integer + string) | `PolymorphicLeafAddInteger` |
| Numeric cross-family collapse | `IntegerFieldSeesDouble` |
| Empty array to populated | `EmptyArrayObservesElement` |
| Nullable field addition | `NullableFieldAppears` |
| Unicode key | `UnicodeKey4ByteCodepoint` |
| Numeric boundary — 2^53+1 | `IntegerBoundaryExceedsDouble` |
| Numeric boundary — 2^63 | `LongBoundaryPromotesBigInteger` |
| Numeric boundary — 18-digit decimal | `DecimalBoundaryFitsBigDecimal` |
| Numeric boundary — 20-digit decimal | `DecimalBoundaryExceedsBigDecimal` |
| Same key at different depth | `SameKeyNestedDifferentType` |
| Array length grows permitted at ArrayLength | `ArrayLengthGrowsPermitted` |
| Structural change rejected at Type | `TypeLevelRejectsStructural` |
| New field rejected at "" | `StrictValidateRejectsNewField` |

(Full catalog drafted in the implementation plan, ~40 entries.)

### 5.2 Generator coverage requirements

- `GenValue` must produce each of the Axis-1 shape classes at a minimum frequency of 1 per 50 samples when `MaxDepth >= 3`.
- `GenExtensionPair(old, level)` must produce a `new` that exceeds `level` with probability ~5% — to exercise Axis-3 rejection paths within the same generator.
- Determinism: same seed → same value across Go versions. Use `math/rand/v2`'s `ChaCha8`/`PCG` for stable RNG output. The generator must additionally respect the determinism discipline in §4.1 (no `range` over maps in generator paths).

### 5.3 Property-test iteration counts

- Round-trip: 1000 seeds per run (cheap per sample).
- Commutativity: 500 seeds per run (two Apply invocations per sample).
- Monotonicity: 200 seeds per run (Validate runs are the expensive step).
- Idempotence: 500 seeds per run.
- Permutation-invariance: 200 seeds per run (three Apply invocations, 6 permutations each).

The full property suite's runtime budget is defined in §6 and enforced in §8.

### 5.4 Polymorphic-slot skip-tests

File: `internal/domain/model/schema/axis2_kind_matrix_test.go`. For each kind-conflict cell (6 cells — LEAF↔OBJECT, LEAF↔ARRAY, OBJECT↔ARRAY in both directions), a subtest:

```go
t.Run("KindConflict_LEAF_OBJECT_at_Structural", func(t *testing.T) {
    t.Skip("polymorphic-slot semantics pending — see issue #<N>")
    // Document the intended semantics as executable expectations, even
    // though the test skips.
    old := /* LEAF */
    extended := /* expected polymorphic-slot OBJECT|LEAF */
    roundTrip(t, old, extended)
})
```

## 6. Success criteria

- All invariants I1, I1-bis, I2, I3 (incl. dual + corollary), I4, I5, I6, I7 verified by passing tests.
- Generator produces every Axis-1 shape at frequency ≥ 1 per 50 samples.
- Generator meta-test `TestGeneratorIsMapFree` passes (seed reproducibility across runs).
- Catalog contains ≥ 40 named fixtures covering every cell in §5.1.
- Polymorphic-slot skip-tests are registered with a single tracking issue linked in their Skip messages.
- **Runtime budget:** full property suite completes in ≤ **45 s** under `go test -short` on a developer machine, and ≤ **60 s** on CI. §8 meta-test enforces the 60 s CI ceiling as a hard fail; the 45 s local figure is advisory and tracked in the PR description so drift is visible.
- `go test -short ./...` green.
- `go vet ./...` clean.
- `go test -race -short ./internal/domain/model/schema/...` clean (race-detector sanity pass over the property harness).
- A polymorphic-slot tracking issue (Sub-project A.3) is filed with links to:
  1. The axis coverage table in §9 below (the authoritative reference).
  2. The design doc §2.1 note and §5.4 skip-test location.
  3. Sub-project A.1 rev 3 §2.3 where the dropped-types divergence lives (for context on the overall classifier work).

## 7. Implementation sequence

1. **Generator foundation** — `gentree/gentree.go` with `GenValue`, `GenModelNode`, `GenConfig`, `DefaultConfig`; `gentree_test.go` with determinism, bounds, and `TestGeneratorIsMapFree`.
2. **Catalog** — populate `gentree/catalog.go` with ≥ 40 named fixtures. Each fixture's `old`, `new`, `level`, `expectedDeltaKinds` is a literal.
3. **Round-trip property test** (I1, I1-bis, I6) — `roundtrip_property_test.go` running catalog + 1000 random seeds. I1-bis assertion runs on every sample.
4. **Axis-2 kind matrix** (`axis2_kind_matrix_test.go`) — hand-table of every (existingKind, incomingKind) cell; polymorphic cells marked `t.Skip` with issue link.
5. **Axis-3 ChangeLevel matrix** (`axis3_changelevel_test.go`) — hand-table of each ChangeLevel × input-class cell with in-memory atomicity assertion (I7).
6. **Commutativity property** (`commutativity_property_test.go`) — path-relationship cross-product over generator samples.
7. **Monotonicity property** (`monotonicity_property_test.go`) — validation-preserving check per op kind; asserts both the direct form and the dual.
8. **Idempotence property** (`idempotence_property_test.go`) — double-apply + double-ingest.
9. **Permutation-invariance property** (`permutation_property_test.go`) — 3-delta permutation check (6 permutations per sample).
10. **Polymorphic-slot tracking issue** — file on GitHub for Sub-project A.3, link in skip-test messages.
11. **Performance budget verification** — confirm the full property suite runs in ≤ 45 s local / ≤ 60 s CI under `-short`.
12. **Final pass** — `go vet`, race detector, full short test, E2E regression (the model-schema-extensions E2E suite must still be green).

## 8. Risks

- **Generator shape-coverage gaps.** If the generator under-produces a shape, the property tests pass even though a class of shape is undertested. Mitigation: §5.2 coverage thresholds asserted by a meta-test (`gentree_test.go: TestCoverageDistribution`) that runs the generator 10_000 times and asserts each shape is produced at or above threshold.
- **Test runtime creep.** Property tests are easy to make slow by nesting too many assertions per sample. Mitigation: a runtime meta-test that hard-fails if the full property suite exceeds **60 s on CI**. The **45 s local** target is advisory — surfaced in the PR description but not a hard fail, since developer hardware varies.
- **Polymorphic-slot blast radius.** The 6 skip-tests document behavior cyoda-go doesn't yet support. Pressure to "just implement it in A.2" would balloon scope. Resist; A.3 is the correct home.
- **Sub-project B interaction.** Plugin-internal fold (Postgres savepoints) can produce byte-different serializations even for semantically-identical schemas. A.2's round-trip uses `schema.Marshal` byte equality which bypasses plugin storage. Sub-project B handles cross-plugin byte identity; A.2 does not depend on it.

## 9. Axis coverage reference

A.2 implements a strict subset of the four-axis enumeration; the kind-conflict cells are deferred to A.3. This table is the authoritative coverage reference — there is no external "context-free agent spec" document; the coverage intent is captured here.

| Axis | A.2 coverage | Deferred to |
|---|---|---|
| Axis 1 — Input shapes | 95% — all primitives, nested/wide/deep, arrays, numeric boundaries, Unicode 4-byte keys | Unicode combining-mark edge cases → D (HTTP-layer fuzz) |
| Axis 2 — Kind matrix | 66% — same-kind cells (L-L, O-O, A-A), bottom-⊥ cells, and silent-drop Extend-contract cells | 6 kind-conflict cells (L↔O, L↔A, O↔A) → A.3 (polymorphic slots) |
| Axis 3 — ChangeLevel enforcement | 100% — linear order "" < ArrayLength < ArrayElements < Type < Structural, accept + reject + atomicity per cell | — |
| Axis 4 — Round-trip invariants | 100% of the documented op catalog — I1, I1-bis, I2, I3+dual+corollary, I4, I5, I6, I7 | — |
| (Axes 5–7 from early brainstorm) Concurrency, durability, error-path, byte-level edge cases | — | B (plugin fold / savepoints), C (concurrency + multi-node), D (HTTP/JSON boundary hardening) |

## 10. Follow-on work

- **Sub-project A.3** — polymorphic-slot kind-conflict implementation. Takes A.2's `t.Skip` tests as its RED spec.
- **Sub-project B** — plugin persistence (savepoint boundaries, cross-plugin byte-identical fold).
- **Sub-project C** — concurrency + multi-node stress.
- **Sub-project D** — input boundary hardening (fuzz at the HTTP/JSON layer).
