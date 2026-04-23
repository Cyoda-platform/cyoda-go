# Design Review — Sub-project A.1: Numeric Classifier Parity with Cyoda Cloud

**Date:** 2026-04-21
**Reviewing:** `2026-04-21-data-ingestion-qa-subproject-a1-design.md`
**Reference:** `docs/numeric-type-classification-analysis.md` (Cyoda Cloud algorithm spec)

---

## 1. Overall assessment

The design is in good shape. Explicit invariants, clearly-scoped non-requirements, cited line numbers in the Kotlin reference, and an executable test contract. The `CollapseNumeric` design choice is well-defended in §5.4 and strictly improves on Cyoda Cloud's `findCommonDataType` STRING-fallback. The TDD sequencing in §7 is clean. The `IsInt128` note about `-2^127` falling outside `BitLen() <= 127` is exactly the kind of corner-case attentiveness this port needs.

What follows is organized into: decisions taken during review, blockers that must be resolved before coding, specification gaps, questions for team decision, minor items, and a concrete per-section edit checklist at the end.

---

## 2. Decisions taken during review

### 2.1 Drop FLOAT from cyoda-go's `DataType` enum (confirmed)

Cyoda Cloud's default `decimalScope=DOUBLE` means FLOAT is never observed under typical ingestion. Rather than port a DataType that the default config explicitly promotes away, cyoda-go will not have a FLOAT type at all. This is a behavioral no-op for default-config ingestion and diverges only for Cyoda Cloud callers that set `decimalScope=FLOAT`, which cyoda-go does not support.

Consequences throughout the doc are listed in §8 of this review.

### 2.2 Integer/decimal classifier gate is value-based, not syntactic (recommended)

Cyoda Cloud routes on Jackson node kind, which is a leaky abstraction over JSON grammar: `"1.0"` and `"1e0"` both denote the integer 1 but take different classifier branches because Jackson built different node types. This is an accidental consequence of Jackson's internal representation, not a designed invariant.

cyoda-go should classify by value. Any whole-number literal — `"1"`, `"1.0"`, `"1.00"`, `"1e0"`, `"10e-1"` — classifies via the integer branch regardless of how it was written. This is strictly more principled and makes the §4.4 branching gate trivially correct.

This is a cyoda-go divergence from Cyoda Cloud that should be documented in §5 alongside the `CollapseNumeric` divergence.

### 2.3 BYTE and SHORT: open question (recommend dropping; see §5.1)

By the same reasoning that justified dropping FLOAT, BYTE and SHORT are unreachable under Cyoda Cloud's default `intScope=INTEGER`. If cyoda-go has no caller that sets `intScope=BYTE` or `SHORT`, those DataTypes are dead code. Dropping them removes `PromoteToScope` entirely, simplifies the widening lattice, and matches the symmetry of the decimal side. See §5.1 below for the argument and §8 for the edits.

---

## 3. Blockers

These must be resolved before §7 step 1 begins. Each is a real bug in the spec, not a preference.

### 3.1 §6.1 `StripTrailingZeros` row for `"1.200"` is wrong

The table says `"1.200"` → unscaled=12, scale=-1, value=120. That is not stripping — it is multiplying by 100. `"1.200"` is the number 1.2; Java's `BigDecimal("1.200").stripTrailingZeros()` yields unscaled=12, scale=1, value=1.2. The adjacent `"100"` row (unscaled=1, scale=-2) is correct, which suggests a copy-paste slip. It matters because §6.1 is the RED test spec — writing the test to a wrong spec and coding to pass it embeds the error permanently.

**Fix:** `"1.200"` → unscaled=12, scale=1, value=1.2.

### 3.2 §4.4 branching logic contradicts §6.3 test expectations

§4.4 says "split on scale (`scale <= 0` and no fractional part → integer branch; else decimal branch)." §6.3 then expects `"1.00"` (which after `StripTrailingZeros` has scale=0) to take the decimal branch and classify FLOAT → DOUBLE. Under §4.4's gate, `"1.00"` goes to the integer branch.

With the decision in §2.2 of this review (value-based classification), this contradiction dissolves: `"1.00"` is the integer 1, classifies via `ClassifyInteger`, and the §6.3 row is simply removed.

**Fix:** Apply §2.2 of this review. Update §4.4's branching prose to state "any input with a non-zero fractional part takes the decimal branch; all whole-number inputs — including `"1.0"`, `"1e0"`, `"10e-1"` — take the integer branch." Remove the `"1.00"` row from §6.3.

### 3.3 `CollapseNumeric` interface is described three different ways

- §2.1: "reduces any numeric sub-set of a `TypeSet` to exactly one `DataType`" (numeric-only input)
- §4.2 signature + comment: takes intermixed input, passes non-numerics through, drops NULL
- §4.3: `CollapseNumeric(numericMembers)` — caller pre-filters, `TypeSet.Add` owns NULL drop
- §5 formal spec: "Given a slice of DataType values, all in a numeric family"

**Fix:** Pick one. Recommended contract: `CollapseNumeric` takes numeric-only input, returns a single `DataType`. `TypeSet.Add` owns NULL-dropping and non-numeric pass-through. Update §2.1, §4.2, §4.3, and §5 to state this consistently. The function signature becomes `func CollapseNumeric(types []DataType) DataType` (single return, not a slice).

### 3.4 `float64` legacy path silently punctures I1 and I2

§4.4 preserves a `float64` fallback for callers that bypass `json.UseNumber()`, labeled "lossy but behaves identically to Cyoda Cloud's 'pre-UseNumber' shape."

Two problems:

1. §3's invariants are stated unconditionally — "for every ingested scalar value." A `float64` fallback in the classifier means classification is non-deterministic across callers and reconstruction is not lossless on the fallback path. I1 and I2 are silently false.
2. The "Cyoda Cloud pre-UseNumber shape" this claims to reproduce does not exist. Per §3.1 of the analysis, Cyoda Cloud enables `USE_BIG_DECIMAL_FOR_FLOATS` unconditionally. The fallback is a cyoda-go implementation wart, not a parity behavior.

**Fix:** Pick one of:
- Migrate all ingestion callers to `json.UseNumber()` as part of this sub-project and delete the fallback. Preferred.
- Keep the fallback and scope I1/I2 explicitly to `UseNumber`-backed callers. Enumerate which entry points qualify. Rename the fallback path to reflect that it's a cyoda-go compatibility shim, not Cyoda Cloud parity.

---

## 4. Specification gaps

### 4.1 `bigDecimalDefiniteExp` and `bigDecimalLooseExp` refer to `precision - scale`, not `scale`

From §3.4 of the analysis: "definite fit if `precision <= 38` and exponent (`precision - scale`) `<= 20`; possible fit if `precision <= 39` and exponent `<= 21`, then `setScale(18).unscaledValue().isInt128()` must succeed."

The design lists `bigDecimalDefiniteExp = 20` and `bigDecimalLooseExp = 21` with no gloss. A Go implementer without Kotlin access can reasonably misread "Exp" as the raw scale. This is the subtlest part of the classifier and needs explicit prose in §4.2 or §6.3.

**Fix:** Add a comment block above the constants defining "exp" as `precision - scale` (the "characteristic"). Add a short paragraph to §4.2 describing the two-tier definite/loose logic.

### 4.2 Missing boundary tests for BIG_DECIMAL ↔ UNBOUND_DECIMAL

§6.3 covers small decimals and huge exponents but not the two-tier definite/loose fit boundary. Required new test rows:

- 38-digit integer, exponent ≤ 20 → BIG_DECIMAL (definite fit)
- 39-digit integer, exponent 21, passes `setScale(18).unscaledValue().isInt128()` → BIG_DECIMAL (loose fit)
- 39-digit integer, exponent 21, fails that check → UNBOUND_DECIMAL
- Precision 38, exponent 21 → UNBOUND_DECIMAL (exceeds definite, exceeds loose precision window)
- Precision 39, exponent 22 → UNBOUND_DECIMAL

Without these, the classifier can ship with a broken definite/loose boundary and all §6.3 tests still pass.

### 4.3 Invariant I2 should say "value-equal, not representation-equal"

I2 as written reads as "storage reconstructs the byte-equal canonical form," which a user could reasonably interpret as preserving the input bytes. It does not. A user who sends `"1.200"` gets back `"1.2"`. That is fine and matches Java stripTrailingZeros conventions, but the invariant should say so:

> **(I2) Reconstruction is lossless in numerical value.** For any value `v` classified as `DataType T`, the storage layer can reconstruct a value numerically equal to `v`. Original representation (trailing zeros, exponent form) is not preserved.

### 4.4 NaN/Inf handling in any remaining `float64` path

§6.1 confirms `ParseDecimal` rejects `"NaN"` and `"Infinity"`. If the `float64` fallback survives the decision in §3.4 above, its NaN/±Inf behavior must be specified. Cyoda Cloud rejects them; cyoda-go should too.

### 4.5 `parseStrings` and `alsoSaveInStrings` scope

These are Cyoda Cloud `ParsingSpec` knobs (§5 of the analysis). The design doesn't mention either in scope or non-scope. Probably out of scope for A.1, but §2.2 should say so explicitly. A reader currently cannot tell.

### 4.6 `SetScale(n == scale)` and negative `n`

§4.1 defines behavior for `n > scale` and `n < scale` but not `n == scale` (no-op assumed) and doesn't say whether negative `n` is valid. Java's `BigDecimal.setScale(-2)` is valid; matching that is reasonable but should be stated.

### 4.7 §8 audit scope

§8 mentions auditing `internal/e2e/` and `plugins/*/conformance_test.go`. The repo also has `e2e/parity/` and `test/recon/`. Confirm these are in scope (or explicitly out).

---

## 5. Questions for team decision

### 5.1 Drop BYTE and SHORT? (Recommended: yes)

The FLOAT-drop argument applies symmetrically to the integer side. Under Cyoda Cloud's default `intScope=INTEGER`, BYTE and SHORT are never observed. If cyoda-go has no caller that sets `intScope=BYTE` or `intScope=SHORT`, they are dead code.

Dropping them:

- Simplifies `ClassifyInteger` to {INTEGER, LONG, BIG_INTEGER, UNBOUND_INTEGER}.
- Removes the entire `PromoteToScope` function (decimalScope is already going away with FLOAT).
- Drops four range-boundary constants (Byte.MIN/MAX, Short.MIN/MAX) from `numeric.go`.
- Simplifies the widening lattice — several rows in §6.6 disappear.
- Drops the `intScope` knob from any cyoda-go `ParsingSpec` equivalent.
- Matches the symmetry of the decimal side after FLOAT goes.

The only counter-argument is storage compactness for small integers, but plugins can inspect the `big.Int` value at byte-encoding time; the schema tag does not need to drive it.

**Team decision needed.** If yes, edits parallel the FLOAT edits and should ship in the same PR rather than as a follow-up. Doing it later costs the same rework at higher total cost.

### 5.2 A.1 and A.2 shipping coordination

§4.5 defers schema-widening-on-ingestion to sub-project A.2. Between A.1 shipping and A.2 shipping, a client sending `13.111` against an `INTEGER` schema gets a rejection that A.2 would have turned into a schema widen. §8 implies no production caller depends on that behavior today.

**Team decision needed.** Confirm no production dependency, or plan A.1 + A.2 as a single release.

### 5.3 Hand-rolled `Decimal` rationale

§8 acknowledges the risk of a hand-roll but doesn't defend the choice against depending on `cockroachdb/apd` or `shopspring/decimal`. Since cyoda-go is forgoing arithmetic, the case is probably "math/big.Int does the heavy lifting, ~200 lines of Decimal wrapper isn't worth a dep." Worth saying so explicitly so a future reader doesn't relitigate.

---

## 6. Cyoda Cloud behaviors: keep-as-is decisions

The user explicitly invited skepticism here. These are Cyoda Cloud behaviors cyoda-go is on track to inherit; each should be an explicit decision rather than defaulted.

### 6.1 Envelope-based decimal classification (keep)

`0.1` classifies as decimal-family despite lacking exact IEEE representation. `Float.MAX_VALUE.toString()` exceeds the FLOAT envelope and classifies higher. Both are consequences of using decimal-digit precision envelopes rather than round-trip analysis. The alternative (round-tripping each value through float32/float64 and comparing) is much more expensive. Keep the envelope approach.

Add a sentence to §5 or §8 clarifying: "DOUBLE, BIG_DECIMAL, and UNBOUND_DECIMAL here are precision-envelope schema tags, not storage formats. All numeric values are held as `Decimal` regardless of tag." This prevents readers from assuming DOUBLE means IEEE 754 storage with attendant precision loss.

### 6.2 `findCommonDataType` STRING fallback (drop — already decided)

Already a documented divergence in §5.4. Frame it in the prose as a clear bug fix rather than a neutral "divergence" — silently promoting numeric-plus-string sets to STRING is user-hostile.

### 6.3 Syntactic integer/decimal split (drop — see §2.2 of this review)

Decided above. Update §5 of the design to list this as a second intentional divergence.

### 6.4 Wire-format-dependent integer classification (cannot reproduce)

Cyoda Cloud's `IntNode` vs `LongNode` distinction is Jackson-internal and JSON text does not carry it. cyoda-go's value-only `ClassifyInteger(*big.Int)` is correct and more principled. Worth saying so explicitly in §5, because it means `JacksonParserTest.kt` cases that use Java `Long` inputs won't all map 1:1.

---

## 7. Minor items and polish

- **`SetScale` signature.** `(Decimal, bool)` drops the reason for failure. Prefer `(Decimal, error)` — more idiomatic Go and extensible if new failure modes emerge.
- **`Precision` test for zero.** §6.1 should include `ParseDecimal("0").Precision() == 1`. Java returns 1 for `BigDecimal.ZERO.precision()` — a naive implementation will return 0.
- **`TypeSet.Add` migration test.** §7 step 3 mentions a migration test but doesn't specify it. Minimum assertion: "calling `Add` on a `TypeSet` that contains no numerics behaves identically before and after the change."
- **Monotone-up-only collapse, long-running instances.** §10 correctly defers schema-narrowing. §8 should acknowledge that long-running instances accumulate over-widened schemas if they ever see outlier values; an operator purging outliers does not get the schema back.
- **Polymorphic field search predicates.** Out of scope for A.1, but a sentence on intended behavior would help. `x = 13.0` on a `{BIG_DECIMAL, STRING}` field presumably matches only decimal-tagged values. Worth a pointer to whichever sub-project owns search.

---

## 8. Concrete edit checklist, by section

Organized by design-doc section for easy incorporation. `[FLOAT]` tags items that fall out of the §2.1 decision. `[BYTE/SHORT?]` tags items contingent on the §5.1 decision.

### §2.1 (In scope)
- `[FLOAT]` Remove mention of FLOAT from the classifier port description.
- `[BYTE/SHORT?]` If dropping: remove BYTE/SHORT from classifier scope; remove `PromoteToScope` mention.
- Change `CollapseNumeric` description to match the unified contract from §3.3 of this review.

### §2.3 (Non-requirements)
- Add: "cyoda-go does not have a FLOAT DataType. Values Cyoda Cloud classifies as FLOAT classify as DOUBLE in cyoda-go."
- `[BYTE/SHORT?]` Add parallel statement for BYTE/SHORT if dropped.
- Add: "cyoda-go classifies integer-vs-decimal by value, not by JSON syntax. `"1.0"`, `"1e0"`, and `"10e-1"` all classify via the integer branch as the value 1."
- Add: "`parseStrings` and `alsoSaveInStrings` are not ported in this sub-project."

### §3 (Invariants)
- Rewrite I2 per §4.3 of this review — "lossless in numerical value."

### §4.1 (`decimal.go`)
- Specify `SetScale(n == scale)` behavior (no-op) and negative `n` (allowed).
- Change `SetScale` return type to `(Decimal, error)`.

### §4.2 (`numeric.go`)
- `[FLOAT]` Delete `floatMaxPrecision` and `floatMaxAbsScale`.
- Add comment defining "exp" in `bigDecimalDefiniteExp` / `bigDecimalLooseExp` as `precision - scale`.
- Add short prose describing the definite/loose two-tier logic.
- `[BYTE/SHORT?]` If dropping: remove `PromoteToScope` function entirely; simplify `ClassifyInteger` to return INTEGER/LONG/BIG_INTEGER/UNBOUND_INTEGER.
- Change `CollapseNumeric` signature to `func CollapseNumeric(types []DataType) DataType`.

### §4.3 (`types.go`)
- Clarify `TypeSet.Add` owns NULL-drop and non-numeric pass-through; it calls `CollapseNumeric` only with numeric-only input.

### §4.4 (walker)
- Rewrite the branching prose per §3.2 of this review: value-based split.
- Resolve the `float64` fallback per §3.4 of this review.

### §5 (CollapseNumeric)
- `[FLOAT]` Remove FLOAT column from §5.2 table.
- Add subsection for syntactic-vs-value divergence alongside §5.4.
- Strengthen §5.4 framing: `findCommonDataType` STRING fallback is a bug fix, not a neutral divergence.

### §6.1 (`Decimal` tests)
- **Fix `"1.200"` row:** unscaled=12, scale=1, value=1.2.
- Add `ParseDecimal("0").Precision() == 1` test.

### §6.2 (`ClassifyInteger` tests)
- `[BYTE/SHORT?]` If dropping: remove BYTE/SHORT rows, keep INTEGER/LONG/BIG_INTEGER/UNBOUND_INTEGER boundary tests.

### §6.3 (`ClassifyDecimal` tests)
- `[FLOAT]` Replace all FLOAT outputs with DOUBLE.
- `[FLOAT]` Delete the `"1.00" → FLOAT → DOUBLE after scope` row entirely (under value-based classification, `"1.00"` is integer 1).
- Add boundary tests for BIG_DECIMAL ↔ UNBOUND_DECIMAL per §4.2 of this review.

### §6.4 (`CollapseNumeric` tests)
- `[FLOAT]` Remove all rows with FLOAT inputs.
- Verify remaining rows against the unified contract.

### §6.6 (`IsAssignableTo` tests)
- `[FLOAT]` Remove FLOAT-related rows.
- `[BYTE/SHORT?]` If dropping: remove BYTE/SHORT source and target rows.

### §6.7 (walker tests)
- `[BYTE/SHORT?]` If dropping: remove `intScope=BYTE` test.
- Add tests for value-based integer/decimal classification: `"1.0"` → INTEGER, `"1e0"` → INTEGER, `"10e-1"` → INTEGER, `"0.1"` → DOUBLE, `"1.5"` → DOUBLE.

### §6.9 (cross-references)
- Note: Cyoda Cloud test cases that use FLOAT classification are cyoda-go divergences that produce DOUBLE.
- `[BYTE/SHORT?]` If dropping: parallel note for BYTE/SHORT cases.

### §7 (Implementation sequence)
- `[BYTE/SHORT?]` If dropping: step 2 simplifies (no `PromoteToScope`).
- Step 3 migration test: specify at minimum "existing non-numeric-merge `TypeSet.Add` behavior is unchanged."

### §8 (Risks)
- Add: hand-roll rationale (§5.3 of this review).
- Add: monotone-up-only collapse caveat for long-running instances (§7 of this review).
- Expand audit scope per §4.7 of this review.

### §10 (Follow-on)
- No changes needed.

---

## 9. What's strong and should not change

For the team's reassurance — these pieces of the design are correct and worth preserving through the edits above:

- The invariant framework (I1–I5) is well-chosen and testable. Only I2's wording needs a small tightening.
- Explicit non-requirements in §2.3 are a significant anti-scope-creep lever. Extending this section is cheap and high-value.
- `CollapseNumeric` as a replacement for Cyoda Cloud's `Polymorphic` / `findCommonDataType` is strictly better; no room for debate.
- Asymmetric validation (integer-into-decimal OK, decimal-into-integer rejected) is the right semantics and matches the Cyoda Cloud integration tests.
- The `IsInt128` boundary note (comparing against precomputed `big.Int` bounds rather than relying on `BitLen()`) is exactly the level of care this hand-roll needs.
- TDD step sequencing in §7 minimizes cross-step dependencies.
- The decision to delegate all arithmetic to Trino/downstream is clean and should be defended against future creep.

---

## 10. Summary of open items for the team

Three decisions are required before §7 step 1 can begin:

1. **§3.4:** `float64` fallback — migrate and delete, or scope the invariants to `UseNumber`-backed callers?
2. **§5.1:** Drop BYTE and SHORT alongside FLOAT?
3. **§5.2:** A.1 ships independently, or bundled with A.2?

All other items above are mechanical edits or clarifications that can be made without further review.
