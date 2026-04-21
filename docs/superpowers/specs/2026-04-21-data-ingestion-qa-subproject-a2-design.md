# Sub-project A.2 — Schema-Transformation Round-Trip Coverage

**Date:** 2026-04-21
**Revision:** 1
**Parent initiative:** Data-ingestion QA (Option 1 decomposition: sub-projects A, B, C, D)
**Prior work:**
- `docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md` — the ExtendSchema pipeline.
- `docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md` — numeric classifier parity (prereq, complete).
- Context from brainstorming session (2026-04-20): decomposition into A.1/A.2/B/C/D, three-axis property framework from the context-free agent spec, the "shipping A.1 independently" decision.

## 1. Purpose

Establish a single master invariant that drives comprehensive coverage of the schema-transformation pipeline:

> **Round-trip:** `Apply(old, Diff(old, Extend(old, importer.Walk(data), level))) ≡ Extend(old, importer.Walk(data), level)` byte-for-byte (after `schema.Marshal`).

That one assertion, exercised across the full shape-space that `importer.Walk` can produce and the full `ChangeLevel` permission matrix, catches:

- Any `Extend` output that `Diff` can't encode.
- Any `Apply` replay that diverges from `Extend`'s direct result.
- Any commutativity violation across concurrently-produced deltas.
- Any validation-monotonicity violation (a delta that tightens the accepted-value set).

The live bug we hit mid-stream (array-of-OBJECT element not handled by Diff) is exactly the class this coverage catches.

## 2. Scope

### 2.1 In scope

Four orthogonal axes, plus supporting infrastructure:

**Axis 1 — Input shapes.** Every shape `importer.Walk` can produce, driven through the pipeline:
- Scalars at every `DataType` primitive (post-A.1 enum: Integer, Long, BigInteger, UnboundInteger, Double, BigDecimal, UnboundDecimal, String, Character, LocalDate…ZonedDateTime, Year, YearMonth, UUIDType, TimeUUIDType, ByteArray, Boolean, Null).
- Flat object, nested object (depth 2, 5, 10), deeply nested (depth 50) as a recursion-sanity test.
- Wide object (100+ siblings) as a quadratic-cost sanity test.
- Array of same-kind scalars, array of mixed-kind scalars (polymorphic leaf element), array of objects (homogeneous element), array of objects with sparse keys (incomplete across items), nested arrays (2D, 3D), array of array of objects, empty array `[]`, single-element array.
- Null at a leaf slot; null at a position an object/array otherwise occupies.
- Numeric boundary values: 2^53+1, 2^63 (LONG→BIG_INTEGER boundary), 2^127 (BIG_INTEGER→UNBOUND_INTEGER boundary), 18-fractional-digit decimal (BIG_DECIMAL definite-fit), 20-fractional-digit decimal (UNBOUND_DECIMAL scale boundary).
- Same-key-different-depth (`{"a":{"b":1,"c":{"b":"x"}}}`).
- Unicode edge cases in keys (4-byte codepoints, combining marks).

**Axis 2 — Transformations (kind matrix).** Every `(existingKind × incomingKind)` cell at the slot level, for every `ChangeLevel`:
- L-L, L-O, L-A, O-L, O-O, O-A, A-L, A-O, A-A, ⊥-anything, anything-⊥.
- Kind-conflict cells (L-O, L-A, O-L, O-A, A-L, A-O) — where `schema.Merge` promotes to a polymorphic-slot shape — are written as **`t.Skip("polymorphic-slot semantics: see issue #X")`** tests. They document the intended semantics as failing tests; implementation lands in a separate A-prime sub-project.
- Silent-drop cells in Extend (LEAF→ARRAY, OBJECT→LEAF, etc.) get **`Extend`-contract tests** (not round-trip): verify that Extend does what it does today (returns unchanged) and that the behavior is intentional, not a bug the A.2 test suite accidentally validates.

**Axis 3 — `ChangeLevel` enforcement.** For each level ∈ {`""`, `ArrayLength`, `ArrayElements`, `Type`, `Structural`}:
- Every change class ≤ level is accepted.
- Every change class > level is rejected with the correct error (`change level X requires Y`).
- Rejection is deterministic, atomic (no partial schema mutation), and surfaces the offending path.
- Mixed-class inputs under a restricted level fail atomically.

**Axis 4 — Round-trip invariants.** Property-based, generator-driven:
- **Round-trip:** master invariant from §1.
- **Commutativity:** `Apply(Apply(b, d1), d2) ≡ Apply(Apply(b, d2), d1)` for generated (d1, d2) pairs covering disjoint paths, same path with different payloads (set-union merge), ancestor/descendant paths.
- **Validation-monotonicity:** for every catalog op kind, any entity valid against base validates against `Apply(base, delta)`.
- **Idempotence:** `Apply(Apply(b, d), d) == Apply(b, d)`; ingesting the same entity twice yields the same schema (no `maxWidth` double-counting, no spurious unions).
- **Associativity (for deltas):** `Apply(Apply(Apply(b,d1),d2),d3) == Apply(b, merge(d1,d2,d3))` under the delta-lattice join.
- **Extend-completeness:** every `DataType` kind + `ChangeLevel` combination that `schema.Extend` can produce is `Diff`-encodable.

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

### 2.3 Out of scope

- **Polymorphic-slot kind-conflict implementation.** Cells that require Extend/Diff/Apply to handle kind changes (LEAF↔OBJECT etc.) are documented as `t.Skip` tests with an issue link. A-prime sub-project addresses this with its own design cycle.
- **Concurrency under load.** Sub-project C covers multi-writer stress, gossip-loss simulation, cache-staleness under contention.
- **Input boundary hardening.** Sub-project D covers malformed JSON, size limits, Unicode at the HTTP layer.
- **Plugin-internal fold correctness at savepoint boundaries.** Sub-project B covers Postgres savepoint semantics and byte-identical folds across backends.
- **Fuzz testing (`testing.F`).** Property-based tests with deterministic seeds give us breadth + reproducibility without the corpus-management overhead. Fuzz is reserved for Sub-project D where bytes-level input is the adversary.

## 3. Invariants

**(I1) Round-trip master invariant.** For every generated (old, incoming, level) tuple:
```
let extended = Extend(old, Walk(incoming), level)
let delta    = Diff(old, extended)
let applied  = Apply(old, delta)
Marshal(applied) == Marshal(extended)
```

If `Diff` returns `nil` (no semantic change), `Apply(old, nil) == old` byte-identical to `extended`.

**(I2) Commutativity.** For every pair of deltas `(d1, d2)` produced by Diff on a shared base, in any path-relationship (disjoint, equal, prefix/ancestor), and for every path-distribution sample: `Apply(Apply(b, d1), d2)` and `Apply(Apply(b, d2), d1)` produce `Marshal`-equal trees.

**(I3) Validation-monotonicity.** For every catalog op kind `k`, every document `D` that validates against schema `B` also validates against `Apply(B, {op-of-kind-k})`. Dual: a document rejected by `Apply(B, d)` was not acceptable by `B` *purely* because of schema insufficiency — the rejection reason is the same or broader.

**(I4) Idempotence.** `Apply(Apply(b, d), d) == Apply(b, d)`. Ingesting the same JSON twice yields the same schema and the same entity body.

**(I5) Associativity.** `Apply(Apply(Apply(b, d1), d2), d3) == Apply(Apply(Apply(b, d3), d2), d1)`. Extended from I2.

**(I6) Extend-completeness.** For every concrete (kind × changeLevel × shape) combination in Axis 1 × Axis 2 × Axis 3 that `schema.Extend` produces a non-nil result for, `Diff(old, extended)` produces a delta whose op kinds are all in the documented catalog. No `extend` output is Diff-unencodable.

**(I7) Change-level atomicity.** If `Extend(old, Walk(data), level)` rejects with a change-level violation, the schema state has not been partially mutated on any storage path. (Verified at unit level; storage semantics come from Sub-project B.)

All invariants are asserted by the property-based test harness and a curated fixture catalog.

## 4. Architecture

### 4.1 Generator package

Lives at `internal/domain/model/schema/gentree/`. Three files:

- `gentree.go` — public surface (`GenValue`, `GenModelNode`, `GenExtensionPair`, `GenConfig`, `DefaultConfig`).
- `catalog.go` — `Catalog` slice of enumerated named fixtures. Each entry carries a human-readable label, an `old` ModelNode or JSON literal, a `new` ModelNode or JSON literal, an expected `ChangeLevel`, and expected `DataType`s in the resulting delta ops.
- `gentree_test.go` — unit tests on the generator itself (determinism by seed, depth/width bounds respected, produced values parseable by `Walk`).

Placement under `internal/domain/model/schema/` keeps the generator colocated with the types it produces. It imports `importer` for its `Walk` convenience in property tests — the generator emits `any`-typed values and callers choose whether to feed through `Walk` or construct `ModelNode`s directly.

### 4.2 Property-test harness

One test file per invariant, under `internal/domain/model/schema/`:

- `roundtrip_property_test.go` — Axis 4 I1 + I6.
- `commutativity_property_test.go` — Axis 4 I2 with path-relationship axis cross-product.
- `monotonicity_property_test.go` — Axis 4 I3.
- `idempotence_property_test.go` — Axis 4 I4.
- `associativity_property_test.go` — Axis 4 I5.

Each test file:
1. Seeds a deterministic `rand.Rand` from a table of seeds (each seed becomes a subtest).
2. Generates an input sample via `gentree`.
3. Runs the invariant check.
4. On failure: emits `old`, `new`, `delta`, `applied`, and the diffing bytes as subtest failure output for debugging.

Each file runs 100–1000 seeded samples per test depending on the cost per sample. The master round-trip is cheapest; commutativity is moderate; monotonicity involves validation calls and is the most expensive per sample.

### 4.3 Axis-2 kind-matrix harness

Hand-written at `internal/domain/model/schema/axis2_kind_matrix_test.go`. Table-driven, one row per `(existingKind, incomingKind, level)` cell. Kind-conflict rows that require polymorphic-slot implementation are marked `t.Skip("polymorphic-slot semantics: see issue #<N>")` — they name the desired outcome but do not fail until A-prime lands.

### 4.4 Axis-3 ChangeLevel harness

Hand-written at `internal/domain/model/schema/axis3_changelevel_test.go`. Table-driven `(changeLevel, input-class) × (accept|reject|error-path)` cases. Atomicity is verified by asserting the schema returned is `Marshal`-identical to `old` when Extend errors.

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
| NumericCrossFamilyCollapse | `IntegerFieldSeesDouble` |
| Empty array to populated | `EmptyArrayObservesElement` |
| Nullable field addition | `NullableFieldAppears` |
| Unicode key | `UnicodeKey4ByteCodepoint` |
| Numeric boundary — 2^53+1 | `IntegerBoundaryExceedsDouble` |
| Numeric boundary — 2^63 | `LongBoundaryPromotesBigInteger` |
| Numeric boundary — 18-digit decimal | `DecimalBoundaryFitsBigDecimal` |
| Numeric boundary — 20-digit decimal | `DecimalBoundaryExceedsBigDecimal` |
| Same key at different depth | `SameKeyNestedDifferentType` |
| ChangeLevel=ArrayLength permits | `ArrayLengthGrowsPermitted` |
| ChangeLevel=Type rejects struct | `TypeLevelRejectsStructural` |
| ChangeLevel="" rejects all extension | `StrictValidateRejectsNewField` |

(Full catalog drafted in the implementation plan, ~40 entries.)

### 5.2 Generator coverage requirements

- `GenValue` must produce each of the Axis-1 shape classes at a minimum frequency of 1 per 50 samples when `MaxDepth >= 3`.
- `GenExtensionPair(old, level)` must produce a `new` that exceeds `level` with probability ~5% — to exercise Axis-3 rejection paths within the same generator.
- Determinism: same seed → same value across Go versions (use `math/rand/v2` `ChaCha8` / `PCG` for reproducibility).

### 5.3 Property-test iteration counts

- Round-trip: 1000 seeds per run (cheap per sample).
- Commutativity: 500 seeds per run (two Apply invocations per sample).
- Monotonicity: 200 seeds per run (Validate runs are the expensive step).
- Idempotence: 500 seeds per run.
- Associativity: 200 seeds per run (three Apply invocations).

All tests run in `go test -short` mode within a 30-second target budget for the full property suite.

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

- All invariants I1–I7 verified by passing tests.
- Generator produces every Axis-1 shape at frequency ≥ 1 per 50 samples.
- Catalog contains ≥ 40 named fixtures covering every cell in §5.1.
- Polymorphic-slot skip-tests are registered with a single tracking issue linked in their Skip messages.
- Property test suite completes in ≤ 30 seconds under `go test -short`.
- `go test -short ./...` green.
- `go vet ./...` clean.
- `go test -race -short ./internal/domain/model/schema/...` clean (race-detector sanity pass over the property harness).
- A polymorphic-slot tracking issue is filed with links to:
  1. The original context-free spec passage defining kind-conflict semantics.
  2. The design doc §2.1 note and §5.4 skip-test location.
  3. Sub-project A.1 rev 3 §2.3 where the dropped-types divergence lives (for context on the overall classifier work).

## 7. Implementation sequence

1. **Generator foundation** — `gentree/gentree.go` with `GenValue`, `GenModelNode`, `GenConfig`, `DefaultConfig`; `gentree_test.go` with determinism and bounds tests.
2. **Catalog** — populate `gentree/catalog.go` with ≥ 40 named fixtures. Each fixture's `old`, `new`, `level`, `expectedDeltaKinds` is a literal.
3. **Round-trip property test** (I1, I6) — `roundtrip_property_test.go` running catalog + 1000 random seeds.
4. **Axis-2 kind matrix** (`axis2_kind_matrix_test.go`) — hand-table of every (existingKind, incomingKind) cell; polymorphic cells marked `t.Skip` with issue link.
5. **Axis-3 ChangeLevel matrix** (`axis3_changelevel_test.go`) — hand-table of each ChangeLevel × input-class cell with atomicity assertion.
6. **Commutativity property** (`commutativity_property_test.go`) — path-relationship cross-product over generator samples.
7. **Monotonicity property** (`monotonicity_property_test.go`) — validation-preserving check per op kind.
8. **Idempotence property** (`idempotence_property_test.go`) — double-apply + double-ingest.
9. **Associativity property** (`associativity_property_test.go`) — 3-delta permutation check.
10. **Polymorphic-slot tracking issue** — file on GitHub, link in skip-test messages.
11. **Performance budget verification** — confirm the full property suite runs in ≤ 30 s under `-short`.
12. **Final pass** — `go vet`, race detector, full short test, E2E regression (the model-schema-extensions E2E suite must still be green).

## 8. Risks

- **Generator shape-coverage gaps.** If the generator under-produces a shape, the property tests pass even though a class of shape is undertested. Mitigation: §5.2 coverage thresholds asserted by a meta-test (`gentree_test.go: TestCoverageDistribution`) that runs the generator 10_000 times and asserts each shape is produced at or above threshold.
- **Test runtime creep.** Property tests are easy to make slow by nesting too many assertions per sample. §5.3 budget is enforced by a benchmark that fails if the full suite exceeds 45s locally.
- **Cyoda Cloud divergence discovery.** A round-trip failure in a fixture that matches Cyoda Cloud exactly would be a real bug in Diff/Apply — halt and surface rather than weaken the assertion.
- **Polymorphic-slot blast radius.** The 6 skip-tests document behavior cyoda-go doesn't yet support. Pressure to "just implement it in A.2" would balloon scope. Resist; A-prime is the correct home.
- **Sub-project B interaction.** Plugin-internal fold (Postgres savepoints) can produce byte-different serializations even for semantically-identical schemas. A.2's round-trip uses `schema.Marshal` byte equality which bypasses plugin storage. Sub-project B handles cross-plugin byte identity; A.2 does not depend on it.

## 9. Test-case inventory reference

See the context-free agent's spec (produced during brainstorming, captured in the conversation transcript) for the full Axis-1/2/3/4 enumeration. A.2's catalog and property tests implement a strict subset of that enumeration — the kind-conflict cells are deferred to A-prime.

Agent's spec axis coverage in A.2:
- Axis 1 — 95% covered (Unicode-combining-mark edge cases deferred to D's Unicode fuzz work).
- Axis 2 — 66% covered (6 kind-conflict cells skipped, pending A-prime).
- Axis 3 — 100% covered.
- Axis 4 — 100% covered for the documented op catalog.
- Axes 5-7 (concurrency/durability, error-path determinism, edge cases) — covered by Sub-projects B/C/D.

## 10. Follow-on work

- **Sub-project A-prime** — polymorphic-slot kind-conflict implementation. Takes A.2's `t.Skip` tests as its RED spec.
- **Sub-project B** — plugin persistence (savepoint boundaries, cross-plugin byte-identical fold).
- **Sub-project C** — concurrency + multi-node stress.
- **Sub-project D** — input boundary hardening (fuzz at the HTTP/JSON layer).
