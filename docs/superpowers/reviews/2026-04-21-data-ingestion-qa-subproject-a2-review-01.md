# Design Review — Sub-project A.2

**Date:** 2026-04-21
**Reviewing:** `2026-04-21-data-ingestion-qa-subproject-a2-design.md` (Revision 1)
**Related reviews:** `2026-04-21-data-ingestion-qa-subproject-a1-review-01.md`, `review-02.md`

---

## 1. Overall assessment

Strong spec. The master invariant framing in §1 — one round-trip assertion that catches four failure modes — is the right organizing principle, and the Axis-1/2/3/4 decomposition follows naturally from the context-free agent spec. The `t.Skip`-with-tracking-issue approach to polymorphic-slot kind-conflict cells is exactly the right way to document intent without implementing, and scoping decisions in §2.3 are clean (concurrency → C, boundary hardening → D, plugin fold → B, fuzz → D).

The numeric-boundary list in §2.1 aligns with A.1's envelope boundaries — the 18-vs-20 fractional-digit decimal distinction is exactly the cliff A.1 Review 02 highlighted, and continuity across sub-projects is visible in the spec.

Findings below are organized by severity. Three must-fix items (I5, I7, I1/I6 interaction), plus specification gaps, questions, and minor items.

## 2. Must-fix items

### 2.1 I5 associativity vs. permutation-invariance — two different properties conflated

§2.1 defines associativity as:

> `Apply(Apply(Apply(b,d1),d2),d3) == Apply(b, merge(d1,d2,d3))` under the delta-lattice join.

§3 I5 defines it as:

> `Apply(Apply(Apply(b, d1), d2), d3) == Apply(Apply(Apply(b, d3), d2), d1)`. Extended from I2.

These are not the same property. The first is *associativity* and requires a `merge(d1, d2, ...)` function that composes deltas into a single delta via a lattice join. The second is *N-permutation-invariance* — commutativity extended from 2 to 3+ deltas. The §3 formulation is what the test at §7 step 9 actually describes ("3-delta permutation check").

If `merge(d1, d2)` exists in the codebase as a delta-lattice join, associativity is testable as written in §2.1. If it doesn't, §2.1's form is aspirational and the test described in §7 step 9 tests something else.

**Recommended fix.** Drop the associativity claim in both §2.1 and §3. Rename I5 to "N-permutation-invariance" and frame it as the natural extension of I2 commutativity from 2 deltas to N deltas. Delete the `merge(d1, ..., dN)` reference entirely from §2.1. Associativity — with a proper delta-lattice join — can come later in A-prime or a follow-on sub-project where a `merge` function is actually designed.

If `merge` does exist, reconcile §2.1 and §3 to describe the same property and make the test in §7 step 9 match.

### 2.2 I7 atomicity over-promises on storage paths

§3 I7 as written:

> If `Extend(old, Walk(data), level)` rejects with a change-level violation, the schema state has not been partially mutated on any storage path.

§2.3 scopes plugin-internal behavior out to sub-project B. §4.4 describes the atomicity test at the unit level: "Atomicity is verified by asserting the schema returned is `Marshal`-identical to `old` when Extend errors." That test is a good in-memory check; it does not verify anything about storage paths.

The invariant over-promises and the test under-delivers. Either test must be extended (out of scope per §2.3) or the invariant narrowed.

**Recommended fix.** Narrow I7:

> **(I7) In-memory atomicity on rejection.** If `Extend(old, Walk(data), level)` rejects with a change-level violation, the input `*ModelNode old` is not mutated — `Marshal(old)` returns the same bytes before and after the rejected Extend call. Cross-storage atomicity is sub-project B's concern.

That is testable, honest, and scoped correctly.

### 2.3 I1 / I6 silent-nil case needs an iff rule

§3 I1 says "If `Diff` returns `nil` (no semantic change), `Apply(old, nil) == old` byte-identical to `extended`." The precondition is stated as a parenthetical — "no semantic change" — but the relationship between `Diff` returning `nil` and `old ≡ extended` isn't nailed down.

This matters because §1 cites the array-of-OBJECT Diff bug as exactly the class of problem A.2 is supposed to catch. That bug class is *"Diff silently returns nil for a case it can't encode, making round-trip trivially succeed."* If the I1 assertion only checks that nil-returns round-trip, and Diff returns nil for every case it can't encode, I1 passes on bugs.

I6 ("Extend-completeness") is meant to backstop this, but its current wording — "`Diff(old, extended)` produces a delta whose op kinds are all in the documented catalog. No `extend` output is Diff-unencodable" — doesn't directly forbid silent nil either.

**Recommended fix.** Add an explicit iff rule to I1 or as a new invariant:

> **(I1-bis) Diff-nil correspondence.** `Diff(old, extended) == nil` if and only if `Marshal(old) == Marshal(extended)`. Any case where `old ≠ extended` but `Diff` returns nil is a bug — specifically the class of silent-nil bugs I6 must catch.

Then adjust I6 to explicitly reference this: "For every (old, extended) pair where `Marshal(old) != Marshal(extended)`, `Diff(old, extended)` produces a non-nil delta whose op kinds are all in the documented catalog."

## 3. Specification gaps

### 3.1 ChangeLevel ordering is implicit

§2.1 Axis 3 enumerates `{"", ArrayLength, ArrayElements, Type, Structural}` without stating whether they form a linear order. I7 atomicity claims and Axis-3 "every change class ≤ level is accepted" assertions both depend on a defined ordering.

**Recommended fix.** Add a one-sentence ordering assertion to §2.1 or §3:

> The ChangeLevels form a linear order `"" < ArrayLength < ArrayElements < Type < Structural`, where each level permits all strictly-lower-level changes.

If the ordering is *not* linear (if `ArrayLength` and `ArrayElements` are incomparable, for instance), state the actual partial order. Readers should not have to grep the Extend code to find out.

### 3.2 Generator determinism has an under-stated trap

§5.2 claims "same seed → same value across Go versions" using `math/rand/v2`'s ChaCha8/PCG. `math/rand/v2` provides stable RNG output — but the generator's *consumption pattern* also has to be deterministic. Go's `map` iteration order is explicitly randomized. If `GenValue` ever ranges over a map to emit keys or children, determinism is lost silently.

**Recommended fix.** Add a note to §4.1:

> `GenValue` and related generator functions must use only ordered data structures internally (slices, not maps) when emitting tree structure. `range` over maps is forbidden in generator paths — use a sorted key slice instead. A lint check or code-review checklist item enforces this.

### 3.3 "Null at a position an object/array otherwise occupies" is ambiguous

§2.1 Axis 1 includes this phrase. Two plausible readings:

- `{"a": null}` where other documents have `{"a": {"b": 1}}`. This is a null↔object kind conflict — a polymorphic-slot case that §2.3 defers to A-prime.
- `{"a": [1, null, 3]}` with null at an array position — already covered by other Axis-1 entries.

If the first reading is intended, this shape routes through the `t.Skip` path and should be named as such. If the second, it's redundant with other list items.

**Recommended fix.** Either clarify with a concrete example or remove. If the former case is intended, move it adjacent to the kind-conflict discussion in Axis 2 so readers see immediately that it's polymorphic-slot territory.

## 4. I3 validation-monotonicity — noting the review trajectory

This item is resolved, but the reasoning is worth preserving in case anyone else reads this invariant and has the same initial confusion I did.

Initial reading flagged I3 as backwards: "extensions tighten schema, so accepted-set should shrink, not grow." That reading assumed a conventional schema-extension model with required fields and narrowing constraints.

**Cyoda's design disposes of that assumption.** There are no required fields — the system does not distinguish between required and optional at the schema level; that is an application concern handled by workflow validation. Under this design, schema extension is purely permissive: it adds fields, widens types, expands kind unions. It never removes permissions, because there are no structural permissions to remove.

I3 as written is therefore correct: `D` valid against `B` ⟹ `D` valid against `Apply(B, d)`.

Two refinements are still worth making in §3:

**Sharpen the dual.** Current text:

> Dual: a document rejected by `Apply(B, d)` was not acceptable by `B` *purely* because of schema insufficiency — the rejection reason is the same or broader.

"Broader" is fuzzy. Proposed rewording:

> **Dual.** A document `D` rejected by `Apply(B, d)` for a given path and reason is also rejected by `B` at that same path for the same reason. Schema extensions do not introduce new rejection causes; they only widen the set of accepted shapes. This is the more informative half of the invariant — it guarantees extensions don't mask non-schema validation failures (value-range violations, type mismatches on present fields, workflow-validation rejections).

**Name the corollary.** I3 combined with induction gives the user-facing property operators actually care about:

> **Corollary.** Any document `D` valid against `B` is valid against `Apply(B, d1, d2, ..., dn)` for any sequence of extensions. Once accepted, always accepted — through all future schema evolution.

Worth stating explicitly rather than leaving readers to derive.

## 5. Items worth questioning

### 5.1 "Five independent invariants" slightly overstates independence

Given I1 (round-trip) and I2 (commutativity), both I4 (idempotence) and I5 (permutation-invariance / associativity) are near-corollaries:

- **I4 idempotence.** `Apply(Apply(b, d), d) == Apply(b, d)` — second application is a no-op if Apply is union-style merge. If it isn't, that's a bug I1 would likely catch (re-running Walk on the same input produces the same schema).
- **I5 permutation-invariance.** Extension of I2 from 2 to N deltas; not logically independent.

Not wrong to test them independently — canary tests are cheap and a regression in I4 or I5 might surface before it surfaces through I1/I2 in a specific generated sample.

**Recommendation.** Add a note to §3:

> I4 and I5 are corollaries of I1 and I2 under reasonable assumptions about Apply's semantics. They are tested independently as canaries — a regression in the corollary often surfaces faster than it would through I1/I2 on specific generated samples.

That frames the invariant list honestly without removing the canary coverage.

### 5.2 30-second performance budget is aggressive

§6 states the full property suite completes in ≤ 30 seconds under `go test -short`. §8 softens to "fails if the full suite exceeds 45s locally." These are inconsistent — and 30s is tight for 2500+ samples across five property tests, each running Walk + Extend + Diff + Apply + Marshal (with Validate added for monotonicity) on potentially-deep random trees.

CI runners vary; local development machines are faster than shared CI. A budget that's realistic locally can fail on slow CI, causing flakes.

**Recommendation.** Unify §6 and §8 on one number. Honest target: 45s local, 60s CI hard-fail. State both explicitly.

### 5.3 "Cyoda Cloud divergence discovery" is a policy, not a risk

§8 includes:

> **Cyoda Cloud divergence discovery.** A round-trip failure in a fixture that matches Cyoda Cloud exactly would be a real bug in Diff/Apply — halt and surface rather than weaken the assertion.

This is a design principle about when to weaken tests vs. fix bugs. It's not a risk in the probabilistic sense ("something that might go wrong"). Move it to §2.3 (non-requirements / principles) or §3 (invariants) as a stated principle.

## 6. Minor items

### 6.1 Axis 1 primitive list should be flagged as authoritative

§2.1 Axis 1 enumerates post-A.1 DataTypes. Readers coming from A.1 Review 02 won't immediately know if the list is authoritative or illustrative. Add one phrase: "authoritative — matches A.1 rev 3 §2.3."

### 6.2 §5.1 catalog table has a row mis-formatted

The row `NumericCrossFamilyCollapse | IntegerFieldSeesDouble` reads as if the category and fixture label are swapped relative to adjacent rows. Double-check and reorder.

### 6.3 §6 forward-references A.1 "rev 3"

Success criteria reference "A.1 rev 3 §2.3 where the dropped-types divergence lives." A.1 is currently at Revision 2 after Review 02. If this references a rev 3 that will land before A.2 ships, fine; if off-by-one, the reference will dangle.

### 6.4 §9 "context-free agent's spec" reference isn't durable

§9 cites "the context-free agent's spec (produced during brainstorming, captured in the conversation transcript)." Conversation transcripts are not durable references. Either commit the agent's spec to `docs/superpowers/` as a durable artifact, or inline the relevant axis-coverage table into the design itself and drop the reference.

### 6.5 "A-prime" naming is inconsistent

§2.3, §8, §10, §5.4 all reference "A-prime" as a sub-project name. If A.1/A.2 use dot-numbered names, "A.3" (or `A'` if the mathematical flavor is intentional) would be more consistent. "A-prime" reads as placeholder-pending-a-name. Pick one and propagate.

### 6.6 ChangeLevel label formatting in §5.1

Rows like `ChangeLevel=ArrayLength permits` use the level in the label itself. Adjacent rows (like `StrictValidateRejectsNewField`) already follow a cleaner naming pattern where the fixture's `level` field carries the level. Consider dropping the `ChangeLevel=X` prefix and using descriptive fixture names.

## 7. What's strong and should not change

- **Master invariant framing in §1.** One testable assertion, four failure modes it catches. Correct organizing principle; survives any rewording of the other invariants.
- **`t.Skip` + tracking-issue for kind-conflict cells.** Documents intent without implementing, keeps A.2 scoped, gives A-prime its RED spec ready-made.
- **Axis-1 numeric boundary list.** Aligns with A.1's envelope boundaries; continuity across sub-projects is visible.
- **§2.3 scoping.** Clean division of labor across A.2 / B / C / D. Self-imposed-discipline spec that doesn't try to do everything.
- **§4.6 existing-test preservation.** Calling out that A.2 adds integration-level coverage on top of existing unit tests (rather than replacing them) avoids a common scope-creep failure mode.
- **§8 performance meta-test.** Self-enforcing runtime budget is the right way to prevent test-suite rot over time, assuming the 30-vs-45-second inconsistency is resolved.
- **I3 validation-monotonicity, given the no-required-fields design.** The invariant is correctly stated in the permissive-extension direction. The dual is the more informative half and deserves sharpened wording per §4 above.

## 8. Summary

Three must-fix items, three specification gaps, two questions, six minor items. None are architectural; all are clarifications, scope adjustments, or wording refinements. Once the must-fix items (I5 conflation, I7 over-promise, I1/I6 silent-nil) are resolved and the ChangeLevel ordering is made explicit, the implementation sequence in §7 is ready to execute.

The strongest parts of the spec — master invariant, axis decomposition, polymorphic-slot deferral via `t.Skip` — are all worth preserving through the edit pass.
