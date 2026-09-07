# Type Admission Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make one definition of "can this field hold this value" serve ingestion, the model merge and search, so a value that is written is a value that can be found.

**Architecture:** The write path stops converting the incoming document into a throwaway model. Instead a single traversal walks the document against the *stored* model, carrying each value to the decision, and produces a **sparse overlay** — a `*ModelNode` describing only the leaves the model does not already admit. `Validate` renders that overlay's changes as `ValidationError`s; `Extend` checks each change's level and merges the overlay; registration runs the same traversal against an empty model. On the search side, the kernel stops deriving a stored value's *label* and instead asks the same admission predicate, which is exported once from the SPI so the two sides cannot disagree.

**Tech Stack:** Go 1.26+, `github.com/cyoda-platform/cyoda-go-spi` (search kernel, `ModelNode`, `Decimal`), PostgreSQL/SQLite/memory storage plugins, testcontainers-go for E2E.

**Spec:** `docs/superpowers/specs/2026-09-04-555-type-admission-design.md` — read it before Task 1. This plan argues from it; where the two disagree the spec wins and the plan is wrong.

**Research:** `docs/superpowers/research/2026-09-04-555-type-admission-research.md`

## Global Constraints

- **Go 1.26+.** Use `log/slog` exclusively — never `log.Printf` or `fmt.Printf`.
- **TDD is mandatory.** Every implementation step is preceded by a failing test and a run that proves it fails. No exceptions, including for the mechanical rewrites.
- **Verification tiers.** `make test` while iterating; `make test-full` before claiming done; `make race` once before the PR. **Never add `-count=1`** and never add `-v`. A raw `go test ./...` is not sufficient — it reports `ok` for suites that never ran.
- **Docker is a hard requirement** for both test tiers. Run `make preflight` if anything looks wrong; there is no Docker-free fallback.
- **No issue numbers in shipped artefacts** — not in errors, logs, responses, code comments, OpenAPI or help topics. Commit messages, PR bodies and spec/plan documents only.
- **Plugin submodules have their own `go.mod`.** `./...` does not cross module boundaries; only `make test-full` covers `plugins/memory`, `plugins/sqlite`, `plugins/postgres`.
- **4xx errors carry full domain detail with an error code; 5xx carry a generic message plus a ticket UUID.** No stack traces or internals in responses.
- **Wrap errors with context:** `fmt.Errorf("failed to X: %w", err)`.
- **No new error codes are introduced by this change.** Every case either keeps its code or moves from an error to a success. If a task finds itself wanting a new code, stop — that is a spec deviation.
- **Numeric admission constants, verbatim:** `DOUBLE` ceiling `9.99999999999999e292`; `doubleMaxPrecision = 15`; `doubleMaxAbsScale = 292`; `INTEGER` bounds ±2³¹; `LONG` bounds ±2⁶³; `BIG_INTEGER` bounds ±INT128; `BIG_DECIMAL` bounds ±INT128/10¹⁸.
- **Branch:** `fix/555-type-admission`, worktree `.claude/worktrees/fix-555-type-admission`, based on `release/v0.8.4`. The PR targets `release/v0.8.4`, not `main`.
- **SPI repo:** `../cyoda-go-spi`. Cassandra plugin (commercial, out-of-tree): `../cyoda-go-cassandra`.

---

## File Structure

**SPI (`../cyoda-go-spi`)**

| File | Responsibility |
|---|---|
| `numeric_admit.go` *(new)* | `AdmitsNumeric` — the single admission predicate. The only place that answers "does declared type T hold value v". |
| `numeric_admit_test.go` *(new)* | Its table test, including every boundary the spec's case table names. |
| `numeric_bucket.go` | `foldToInt` negative-scale guard; `StripTrailingZeros` underflow guard lives in `decimal.go`. |
| `eval_leaf.go` | Stored-value filter in `evalCompare`/`evalBetween` calls `AdmitsNumeric`; `classifyStoredNumeric` deleted; operand stripped once at `expandCompare`'s `ParseDecimal`. |
| `decimal.go` | `StripTrailingZeros` int32 scale underflow guard. |

**cyoda-go — write path**

| File | Responsibility |
|---|---|
| `internal/domain/model/schema/admit.go` *(new)* | The single traversal. `Admit(model, data)` → sparse overlay + `[]Change`. The one place that decides held/not-held. |
| `internal/domain/model/schema/admit_test.go` *(new)* | Leaf and container admission, per the spec's case table. |
| `internal/domain/model/schema/extend.go` | `Extend(existing, data, level)` — rebuilt on `Admit`. `checkAndExtend`, `checkBranch`, `widensDeclared` deleted. |
| `internal/domain/model/schema/validate.go` | `Validate(model, data)` — rebuilt on `Admit`. `matchesScalarBranch`/`assignableToAny` deleted. |
| `internal/domain/model/importer/walker.go` | Keeps `Walk` for registration's per-document derivation; gains the negative-scale guard. |
| `internal/domain/model/ingest/validate.go` | `ValidateOrExtend`/`ValidateStrict` pass parsed data to `Extend` instead of pre-walking. |

**cyoda-go — consequences**

| File | Responsibility |
|---|---|
| `e2e/parity/oracle.go` | Byte-identity oracle, rewritten onto the new `Extend` signature. |
| `e2e/parity/schema_concurrent_convergence.go` | Convergence contract restated; carve-out documented. |
| `e2e/parity/schema_numeric_fold_carveout.go` *(new)* | The carve-out's property: monotone, admits every written value. |
| `e2e/parity/type_admission.go` *(new)* | The backend-agnostic admission scenarios. |
| `e2e/parity/registry.go`, `registry_count_test.go` | Registration and the count pin. |
| `internal/e2e/type_admission_test.go` *(new)* | HTTP coverage for every §9 row on a real Postgres. |
| `internal/grpc/type_admission_test.go` *(new)* | gRPC envelope coverage for create and update. |
| `plugins/sqlite/query_planner.go` | `isLeafPushable` doc comment corrected. |
| `plugins/postgres/query_planner.go` | `COLLATE "C"` on text comparison branches. |

---

## Task 1: The admission predicate

**Files:**
- Create: `../cyoda-go-spi/numeric_admit.go`
- Test: `../cyoda-go-spi/numeric_admit_test.go`

**Interfaces:**
- Consumes: `Decimal` (`Precision() int`, `Scale() int32`, `StripTrailingZeros() Decimal`, `Cmp(Decimal) int`), `DataType`, `IsNumeric(DataType) bool`, and the unexported bucket bounds `doubleBucketMax`, `bd128Min`, `bd128Max`, `intBoundInteger32Min/Max`, `intBoundLong64Min/Max`, `intBound128Min/Max`, `doubleMaxPrecision`, `doubleMaxAbsScale`, `toRange`, `inRangePos` — all already in `numeric_bucket.go` and `numeric.go` in this same package.
- Produces: `func AdmitsNumeric(t DataType, v Decimal) bool` — used by Task 2 (the kernel's stored-value filter) and Task 7 (cyoda-go's leaf admission, reached through the `schema` package's alias).

Read the spec's §5 before writing anything. The predicate is per-family and is *not* the range alone; that is the whole point of the task.

- [ ] **Step 1: Write the failing test**

Create `../cyoda-go-spi/numeric_admit_test.go`:

```go
package spi

import "testing"

func TestAdmitsNumeric(t *testing.T) {
	cases := []struct {
		name  string
		typ   DataType
		value string
		want  bool
	}{
		// INTEGER: whole and in bounds.
		{"integer at ceiling", Integer, "2147483647", true},
		{"integer past ceiling", Integer, "2147483648", false},
		{"integer at floor", Integer, "-2147483648", true},
		{"integer fractional", Integer, "13.111", false},
		{"integer whole with trailing zeros", Integer, "5.0", true},
		{"integer whole via exponent", Integer, "1e3", true},

		// LONG / BIG_INTEGER: same rule, wider bounds.
		{"long holds past 2^31", Long, "2147483648", true},
		{"long past ceiling", Long, "9223372036854775808", false},
		{"big integer holds past 2^63", BigInteger, "9223372036854775808", true},

		// UNBOUND_INTEGER: whole, no bound.
		{"unbound integer holds a huge whole", UnboundInteger, "1e40", true},
		{"unbound integer refuses a fraction", UnboundInteger, "1.5", false},

		// DOUBLE: range AND precision AND scale. This is the heart of §5.
		{"double holds a whole past 2^31", Double, "2147483648", true},
		{"double holds 1000", Double, "1000", true},
		{"double at the ceiling", Double, "9.99999999999999e292", true},
		{"double above the ceiling", Double, "9.99999999999999e300", false},
		{"double refuses 16 significant digits", Double, "9007199254740993", false},
		{"double refuses 16 significant digits, fractional", Double, "1.234567890123456", false},
		{"double holds 15 significant digits", Double, "1.23456789012345", true},
		{"double refuses scale past 292", Double, "1e-400", false},
		{"double holds scale at 292", Double, "1e-292", true},

		// BIG_DECIMAL: magnitude only — high scale is admitted.
		{"big decimal holds a high-scale value", BigDecimal, "1.23456789012345678901234567890", true},
		{"big decimal above magnitude", BigDecimal, "1e40", false},

		// UNBOUND_DECIMAL: the sink.
		{"unbound decimal holds anything", UnboundDecimal, "9.99999999999999e300", true},
		{"unbound decimal holds a fraction", UnboundDecimal, "1.5", true},

		// Non-numeric declared types are never asked, and answer false.
		{"string is not numeric", String, "5", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ParseDecimal(tc.value)
			if err != nil {
				t.Fatalf("ParseDecimal(%q): %v", tc.value, err)
			}
			if got := AdmitsNumeric(tc.typ, v); got != tc.want {
				t.Errorf("AdmitsNumeric(%s, %s) = %v, want %v", tc.typ, tc.value, got, tc.want)
			}
		})
	}
}

// The invariant the predicate exists to secure: a value a type admits is a
// value an EQUALS on that type can find. Ranges alone do not give this.
func TestAdmitsNumeric_AdmittedValueIsFindable(t *testing.T) {
	values := []string{
		"1000", "2147483648", "9.99999999999999e292", "9007199254740993",
		"1.234567890123456", "1e-400", "1.23456789012345", "13.111", "1.5",
		"1.23456789012345678901234567890", "5.0", "-0.0",
	}
	types := []DataType{Integer, Long, BigInteger, UnboundInteger, Double, BigDecimal, UnboundDecimal}

	for _, raw := range values {
		v, err := ParseDecimal(raw)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", raw, err)
		}
		for _, dt := range types {
			if !AdmitsNumeric(dt, v) {
				continue
			}
			exp, err := ExpandLeaf(FilterEq, raw, []DataType{dt})
			if err != nil {
				t.Errorf("%s admits %s but ExpandLeaf errored: %v", dt, raw, err)
				continue
			}
			if !EvalLeaf(exp, rawJSONNumber(raw)) {
				t.Errorf("%s admits %s but EQUALS cannot find it", dt, raw)
			}
		}
	}
}
```

`rawJSONNumber` is a helper the existing `eval_leaf_test.go` already has for building a `gjson.Result` from a numeric literal — reuse it; do not write a second one. If its name differs in the file, use the existing one and adjust this test.

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd ../cyoda-go-spi && go test ./... -run 'TestAdmitsNumeric'
```

Expected: FAIL — `undefined: AdmitsNumeric`.

- [ ] **Step 3: Write the implementation**

Create `../cyoda-go-spi/numeric_admit.go`:

```go
package spi

// AdmitsNumeric reports whether a field declaring t can hold the value v.
//
// This is the single definition of numeric admission. Ingestion asks it to
// decide whether a write changes the model; the search kernel asks it to
// decide whether a stored value belongs to the declared type. Two copies
// would be two things that can disagree, which is the defect this predicate
// exists to remove.
//
// It is deliberately NOT "is v inside t's range". Admission must agree with
// what search can find, and for DOUBLE the operand bucket drops the EQUALS
// branch entirely for a value that exceeds 15 significant digits or a scale
// of 292 (see produceDecimalInRange / isDoubleBucketPrecise) — so a value
// admitted on range alone would be stored where EQUALS could never find it
// and NOT_EQUAL would wrongly match it. The integer family needs wholeness on
// top of its bounds for the same reason: foldToInt drops the whole family for
// a fractional operand, and UNBOUND_INTEGER has no bound to hide behind.
//
// The precision bound is also the mantissa argument stated as a value test
// rather than a label test: every integer above 2^53 needs at least 16
// significant digits, so precision <= 15 excludes exactly the values a
// 53-bit mantissa cannot hold — without condemning a 10-digit value like
// 2147483648 by association with its LONG label.
//
// A non-numeric t is never admitted; callers route by JSON kind first.
func AdmitsNumeric(t DataType, v Decimal) bool {
	if !IsNumeric(t) {
		return false
	}
	stripped := v.StripTrailingZeros()

	switch t {
	case UnboundDecimal:
		// The lattice sink: no bound, no precision limit, emitted verbatim by
		// expandDecimalFamily.
		return true

	case UnboundInteger:
		// No bound, but foldToInt still drops a fractional value.
		return stripped.Scale() <= 0

	case Integer:
		return admitsWholeInRange(stripped, intBoundInteger32Min, intBoundInteger32Max)
	case Long:
		return admitsWholeInRange(stripped, intBoundLong64Min, intBoundLong64Max)
	case BigInteger:
		return admitsWholeInRange(stripped, intBound128Min, intBound128Max)

	case Double:
		// Range AND the bucket's precision test. Both conjuncts are
		// load-bearing; see the doc comment above.
		if toRange(stripped, doubleBucketMax.neg(), doubleBucketMax) != inRangePos {
			return false
		}
		return isDoubleBucketPrecise(stripped)

	case BigDecimal:
		// Magnitude only. produceDecimalInRange documents the scale <= 18
		// restriction as a Trino storage constraint irrelevant to a search
		// condition, and emits a high-scale in-magnitude value verbatim.
		return toRange(stripped, bd128Min, bd128Max) == inRangePos

	default:
		return false
	}
}

// admitsWholeInRange is the integer-family rule: whole after stripping, and
// inside the type's bounds.
func admitsWholeInRange(stripped, floor, ceiling Decimal) bool {
	if stripped.Scale() > 0 {
		return false
	}
	return toRange(stripped, floor, ceiling) == inRangePos
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd ../cyoda-go-spi && go test ./... -run 'TestAdmitsNumeric'
```

Expected: PASS, both tests.

If `TestAdmitsNumeric_AdmittedValueIsFindable` fails for a value, **the predicate is wrong, not the test** — the whole point is that admitted implies findable. Fix `AdmitsNumeric`; do not weaken the property test.

- [ ] **Step 5: Add `NewEmptyNode`, which Task 7 needs**

`ModelNode`'s fields are unexported, so `&ModelNode{}` is not constructible from `cyoda-go`. Task 7's `describe` needs a node that declares *nothing*, and `NewLeafNode(Null)` is not it — it sets `nullable = true` (`model_schema.go:283-285`), so a field whose only observed value is `null` would be admitted against it, produce no overlay, and vanish from the model. Today `walkValue(nil)` records a nullable node. The distinction is load-bearing.

This must be in the tag Task 5 cuts. Write the test first:

```go
func TestNewEmptyNode_DeclaresNothing(t *testing.T) {
	n := NewEmptyNode()
	if len(n.Kinds()) != 0 {
		t.Errorf("Kinds() = %v, want none", n.Kinds())
	}
	if n.Nullable() {
		t.Error("an empty node has not been observed as null")
	}
	if n.Scalar() != nil || n.Object() != nil || n.Array() != nil {
		t.Error("an empty node carries no branch")
	}
}

// The distinction from NewLeafNode(Null), which is nullable-empty.
func TestNewEmptyNode_IsNotNullLeaf(t *testing.T) {
	if NewLeafNode(Null).Nullable() == NewEmptyNode().Nullable() {
		t.Error("NewLeafNode(Null) is nullable; NewEmptyNode is not")
	}
}
```

Then in `model_schema.go`, beside the other constructors:

```go
// NewEmptyNode returns a node that declares nothing: no branch, and not
// nullable. Every value is a change against it, which is what makes it the
// model a fresh derivation walks against — deriving a field's description is
// the same traversal as admitting a value, run against a model that admits
// nothing.
//
// This is NOT NewLeafNode(Null), which records that a path HAS been observed
// holding null and therefore already admits it.
func NewEmptyNode() *ModelNode {
	return &ModelNode{branches: make(map[NodeKind]Branch, 1)}
}
```

- [ ] **Step 6: Run the full SPI suite**

```bash
cd ../cyoda-go-spi && go test ./...
```

Expected: PASS. Nothing calls `AdmitsNumeric` or `NewEmptyNode` yet, so this only proves nothing broke.

- [ ] **Step 7: Commit**

```bash
cd ../cyoda-go-spi
git add numeric_admit.go numeric_admit_test.go model_schema.go model_schema_test.go
git commit -m "feat(numeric): one predicate for what a numeric type admits

Ingestion and search each answered 'does this type hold this value' by a
different route. This is the single answer, and it is not the range: for
DOUBLE the operand bucket drops the EQUALS branch above 15 significant
digits or a scale of 292, so a value admitted on range alone is stored
where EQUALS cannot find it and NOT_EQUAL wrongly matches it.

The precision bound is the 53-bit mantissa argument as a value test rather
than a label test, so a 10-digit value past 2^31 is admitted while a
16-digit one is not.

Nothing calls it yet."
```

---

## Task 2: The kernel judges a stored number by admission, not by label

**Files:**
- Modify: `../cyoda-go-spi/eval_leaf.go` — `evalCompare` (the numeric branch, currently around `:415-435`), `evalBetween` (around `:512-530`), and delete `classifyStoredNumeric` (around `:663-673`)
- Test: `../cyoda-go-spi/eval_leaf_test.go`

**Interfaces:**
- Consumes: `AdmitsNumeric(DataType, Decimal) bool` from Task 1.
- Produces: no new exported surface. After this task `classifyStoredNumeric` no longer exists — Task 3 must not reference it.

The two sites have **different shapes** and both need writing. `evalCompare` tests per sub-condition (`sc.Type`); `evalBetween` scans the whole declared numeric set (`e.numTypes`). Under the collapse invariant they coincide, but the code does not — do not try to share one loop between them.

- [ ] **Step 1: Write the failing test**

Append to `../cyoda-go-spi/eval_leaf_test.go`:

```go
// A stored value the declared type admits must be matched. Before this, the
// kernel derived the stored value's label and asked IsAssignableTo, so a
// whole number past 2^31 in a [DOUBLE] leaf was skipped even though the
// operand had produced a DOUBLE sub-condition.
func TestEvalCompare_StoredValueJudgedByAdmission(t *testing.T) {
	cases := []struct {
		name     string
		declared []DataType
		op       FilterOp
		operand  string
		stored   string
		want     bool
	}{
		{"double leaf holds a whole past 2^31", []DataType{Double}, FilterEq, "2147483648", "2147483648", true},
		{"double leaf, ordering op", []DataType{Double}, FilterGt, "2147483647", "2147483648", true},
		{"big decimal leaf, high scale", []DataType{BigDecimal}, FilterEq,
			"1.23456789012345678901234567890", "1.23456789012345678901234567890", true},
		{"out-of-range operand NotNull residual", []DataType{Double}, FilterLt, "1e300", "2147483648", true},
		{"a value the type does not admit is still not matched", []DataType{Integer}, FilterEq, "5", "2147483648", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp, err := ExpandLeaf(tc.op, tc.operand, tc.declared)
			if err != nil {
				t.Fatalf("ExpandLeaf: %v", err)
			}
			if got := EvalLeaf(exp, rawJSONNumber(tc.stored)); got != tc.want {
				t.Errorf("EvalLeaf = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvalBetween_StoredValueJudgedByAdmission(t *testing.T) {
	exp, err := ExpandLeaf(FilterBetweenInclusive, "2147483647,2147483649", []DataType{Double})
	if err != nil {
		t.Fatalf("ExpandLeaf: %v", err)
	}
	if !EvalLeaf(exp, rawJSONNumber("2147483648")) {
		t.Error("a stored value the DOUBLE leaf admits must fall inside an inclusive between")
	}
}
```

Match the existing file's helper names for building operands — `ExpandLeaf`'s between operand spelling is whatever `eval_leaf_test.go` already uses for `FilterBetweenInclusive`. Copy that spelling; do not invent one.

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd ../cyoda-go-spi && go test ./... -run 'TestEvalCompare_StoredValueJudgedByAdmission|TestEvalBetween_StoredValueJudgedByAdmission'
```

Expected: FAIL — the `[DOUBLE]`/`2147483648` cases return `false`.

- [ ] **Step 3: Replace the filter in `evalCompare`**

In `../cyoda-go-spi/eval_leaf.go`, the numeric branch currently reads:

```go
		storedT := classifyStoredNumeric(dec)
		for _, sc := range e.numeric {
			if !IsAssignableTo(storedT, sc.Type) {
				continue
			}
```

Replace with:

```go
		for _, sc := range e.numeric {
			// Judge the stored value against what the declared type admits,
			// not against the label the value happens to classify as. A
			// [DOUBLE] leaf holds 2147483648 — the label LONG does not widen
			// into DOUBLE, but the value is inside DOUBLE's range and well
			// within its mantissa, so the leaf holds it and search must find
			// it. AdmitsNumeric is the same predicate ingestion used to let
			// the value in.
			if !AdmitsNumeric(sc.Type, dec) {
				continue
			}
```

- [ ] **Step 4: Replace the filter in `evalBetween`**

Currently:

```go
		storedT := classifyStoredNumeric(dec)
		assignable := false
		for _, u := range e.numTypes {
			if IsAssignableTo(storedT, u) {
				assignable = true
				break
			}
		}
		if !assignable {
			return false
		}
```

Replace with:

```go
		// Same admission discipline as evalCompare, but over the whole
		// declared numeric set rather than per sub-condition: expandBetween
		// carries e.numTypes, not a sub-condition list. Under the collapse
		// invariant the set has at most one member and the two coincide.
		admitted := false
		for _, u := range e.numTypes {
			if AdmitsNumeric(u, dec) {
				admitted = true
				break
			}
		}
		if !admitted {
			return false
		}
```

- [ ] **Step 5: Delete `classifyStoredNumeric`**

It now has no callers. Delete the function and its doc comment outright — Gate 6 says resolve, not defer. Confirm it is gone:

```bash
cd ../cyoda-go-spi && grep -rn "classifyStoredNumeric" . ; echo "exit=$?"
```

Expected: no matches (`exit=1`).

- [ ] **Step 6: Run the tests**

```bash
cd ../cyoda-go-spi && go test ./...
```

Expected: PASS. If an existing kernel test fails, read it before changing it — a test asserting that a `[DOUBLE]` leaf does *not* match `2147483648` is one this change deliberately inverts, and its comment must be rewritten to the new reasoning, not deleted.

- [ ] **Step 7: Commit**

```bash
cd ../cyoda-go-spi
git add eval_leaf.go eval_leaf_test.go
git commit -m "fix(search): a stored value is judged by what the type admits, not by its label

The kernel derived a stored number's narrowest label and asked
IsAssignableTo, so a [DOUBLE] leaf never matched 2147483648 even when the
operand had produced a DOUBLE sub-condition: LONG does not widen into
DOUBLE. Ask AdmitsNumeric instead, which is the predicate that let the
value into the field.

evalCompare tests per sub-condition and evalBetween scans the declared
set; the shapes differ and both are written. classifyStoredNumeric had no
other caller and is deleted."
```

---

## Task 3: Strip the operand once, where both families see it

**Files:**
- Modify: `../cyoda-go-spi/eval_leaf.go` — `expandCompare`, the `ParseDecimal(operand)` at roughly `:232`
- Test: `../cyoda-go-spi/eval_leaf_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: no new exported surface.

The spec's §7(ii) is explicit that the fix goes at `expandCompare`'s `ParseDecimal`, **not** inside `foldToInt`. `foldToInt` covers only the integer family; precision is computed on the unstripped operand at `numeric_bucket.go:208`, so the decimal family has the identical defect one function away. Fixing half of it is the heterogeneous landscape Gate 6 asks us not to leave.

- [ ] **Step 1: Write the failing test**

Append to `../cyoda-go-spi/eval_leaf_test.go`:

```go
// One literal must have one meaning. Ingestion strips trailing zeros before
// classifying; the operand side must too, or EQUALS and NOT_EQUAL disagree
// with what was stored.
func TestExpandCompare_OperandTrailingZerosStripped(t *testing.T) {
	cases := []struct {
		name     string
		declared []DataType
		op       FilterOp
		operand  string
		stored   string
		want     bool
	}{
		// The integer family — the originally reported defect.
		{"eq 5.0 finds a stored 5", []DataType{Integer}, FilterEq, "5.0", "5", true},
		{"ne 5.0 does not match a stored 5", []DataType{Integer}, FilterNe, "5.0", "5", false},
		{"eq 5 still finds a stored 5", []DataType{Integer}, FilterEq, "5", "5", true},
		{"eq 12.5 still finds nothing on an integer leaf", []DataType{Integer}, FilterEq, "12.5", "12", false},

		// The decimal family — the same defect, one function away.
		{"eq 5.000000000000000000 finds a stored 5", []DataType{Double}, FilterEq, "5.000000000000000000", "5", true},
		{"eq 10.500000000000000000 finds a stored 10.5", []DataType{Double}, FilterEq, "10.500000000000000000", "10.5", true},
		{"ne 10.500000000000000000 does not match a stored 10.5", []DataType{Double}, FilterNe, "10.500000000000000000", "10.5", false},

		// Negative zero, which stripping also settles.
		{"ne -0.0 does not match a stored 0", []DataType{Integer}, FilterNe, "-0.0", "0", false},
		{"eq 0.000 finds a stored 0", []DataType{Integer}, FilterEq, "0.000", "0", true},

		// A genuinely imprecise operand is still dropped.
		{"eq on a 16-digit operand still finds nothing", []DataType{Double}, FilterEq, "1.234567890123456", "10.5", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp, err := ExpandLeaf(tc.op, tc.operand, tc.declared)
			if err != nil {
				t.Fatalf("ExpandLeaf: %v", err)
			}
			if got := EvalLeaf(exp, rawJSONNumber(tc.stored)); got != tc.want {
				t.Errorf("EvalLeaf(%s %s vs stored %s) = %v, want %v",
					tc.op, tc.operand, tc.stored, got, tc.want)
			}
		})
	}
}

// Ordering operands must be unaffected: rounding a whole value is identity.
func TestExpandCompare_StripDoesNotDisturbOrderingOps(t *testing.T) {
	cases := []struct {
		op      FilterOp
		operand string
		stored  string
		want    bool
	}{
		{FilterGt, "12.5", "13", true},
		{FilterGt, "12.5", "12", false},
		{FilterGt, "5.0", "6", true},
		{FilterLte, "5.0", "5", true},
		{FilterLt, "3e100", "5", true},
	}
	for _, tc := range cases {
		exp, err := ExpandLeaf(tc.op, tc.operand, []DataType{Integer})
		if err != nil {
			t.Fatalf("ExpandLeaf: %v", err)
		}
		if got := EvalLeaf(exp, rawJSONNumber(tc.stored)); got != tc.want {
			t.Errorf("%s %s vs stored %s = %v, want %v", tc.op, tc.operand, tc.stored, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd ../cyoda-go-spi && go test ./... -run 'TestExpandCompare_OperandTrailingZerosStripped'
```

Expected: FAIL on the `5.0`, `5.000000000000000000`, `10.500000000000000000`, `-0.0` and `0.000` cases.

- [ ] **Step 3: Strip at the parse**

In `expandCompare`, change:

```go
	if len(numericDeclared) > 0 {
		if dec, err := ParseDecimal(operand); err == nil {
			engaged = true // the operand IS a number → the numeric family is applicable
			e.numeric = ExpandNumericOperand(dec, numericDeclared, op)
		}
	}
```

to:

```go
	if len(numericDeclared) > 0 {
		if dec, err := ParseDecimal(operand); err == nil {
			engaged = true // the operand IS a number → the numeric family is applicable
			// One literal, one meaning. Ingestion classifies by value, after
			// stripping trailing zeros; the operand must be read the same way
			// or "5.0" and "5" denote different things on the two sides.
			// Stripping here rather than inside foldToInt covers both
			// families: the decimal bucket computes precision on the operand
			// too, so "5.000000000000000000" would otherwise be judged
			// imprecise and have its EQUALS branch dropped.
			e.numeric = ExpandNumericOperand(dec.StripTrailingZeros(), numericDeclared, op)
		}
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd ../cyoda-go-spi && go test ./... -run 'TestExpandCompare_'
```

Expected: PASS.

- [ ] **Step 5: Run the full SPI suite**

```bash
cd ../cyoda-go-spi && go test ./...
```

Expected: PASS. A failing `NOT_EQUAL`-under-`NOT` assertion is one this change deliberately corrects — `NOT(EQUALS 5.0)` against a stored `5` goes from `true` (vacuous, no candidate) to `false`. Rewrite such a test's expectation and its comment; do not revert the fix.

- [ ] **Step 6: Commit**

```bash
cd ../cyoda-go-spi
git add eval_leaf.go eval_leaf_test.go
git commit -m "fix(search): one literal, one meaning — strip the operand once

Ingestion strips trailing zeros before classifying and the operand side did
not, so EQUALS 5.0 could not find a stored 5 and NOT_EQUAL 5.0 wrongly
matched it. The strip belongs at expandCompare's ParseDecimal, not inside
foldToInt: the decimal bucket computes precision on the operand too, so
EQUALS 5.000000000000000000 against a stored 5 had the identical defect one
function away. Negative zero falls out with it."
```

---

## Task 4: The negative-scale expansion guard

**Files:**
- Modify: `../cyoda-go-spi/numeric_bucket.go` — `foldToInt`
- Modify: `../cyoda-go-spi/decimal.go` — `StripTrailingZeros` scale underflow
- Test: `../cyoda-go-spi/numeric_bucket_test.go` (or the file where `foldToInt` is already exercised)

**Interfaces:**
- Consumes: nothing new.
- Produces: no new exported surface. `foldToInt` keeps its `(Decimal, FilterOp, bool)` signature; an unrepresentable scale returns `ok=false`, which the caller already handles by dropping the int family.

`foldToInt`'s `SetScale(0)` on a negative scale materialises `10^|scale|` via `big.Int.Exp`. `ParseDecimal` bounds scale only to int32, so a 13-byte operand allocates hundreds of megabytes before a single row is read — reachable from an ordinary search request through `ValidateConditionValueTypes`. The project already guards exactly this in `internal/domain/model/schema/validate.go:296-312`; copy that guard's shape and its 39-digit reasoning.

- [ ] **Step 1: Write the failing test**

```go
// A huge negative scale must be refused, not expanded. ParseDecimal bounds
// scale only to int32, so 1e10000000 is a 13-byte operand that would
// otherwise materialise a ten-million-digit big.Int before any row is read.
func TestFoldToInt_HugeNegativeScaleIsRefusedNotExpanded(t *testing.T) {
	for _, operand := range []string{"1e1000000", "1e10000000", "1e2000000000"} {
		v, err := ParseDecimal(operand)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", operand, err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if _, _, ok := foldToInt(v, FilterEq); ok {
				t.Errorf("foldToInt(%s) must refuse an unrepresentable scale", operand)
			}
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("foldToInt(%s) did not return within 2s — the scale was expanded", operand)
		}
	}
}

// An UNBOUND_INTEGER leaf must still not match such an operand, and the
// request must not hang.
func TestExpandLeaf_HugeNegativeScaleOperandDoesNotExpand(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := ExpandLeaf(FilterEq, "1e10000000", []DataType{UnboundInteger}); err == nil {
			t.Log("expansion returned without error; the int family must simply be empty")
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ExpandLeaf did not return within 2s")
	}
}
```

Add `"time"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd ../cyoda-go-spi && go test ./... -run 'HugeNegativeScale' -timeout 120s
```

Expected: FAIL — the 2s deadline is exceeded (measured: `1e10000000` takes ~928 ms and `1e2000000000` far longer).

- [ ] **Step 3: Guard `foldToInt`**

```go
// maxFoldDigits bounds the decimal digit count foldToInt will materialise.
// A whole value is folded by multiplying the coefficient by 10^-scale, and
// ParseDecimal bounds scale only to int32, so an operand like "1e10000000"
// would allocate a ten-million-digit big.Int before a single row is read —
// from a 13-byte search condition. INT128 max has 39 decimal digits, so any
// integer needing more than a few hundred is far outside every declared
// type's bounds and can only ever produce a NotNull residual or nothing;
// refusing it costs no reachable match. The same guard, with the same
// reasoning, is in the schema validator's classifier.
const maxFoldDigits = 1024

func foldToInt(value Decimal, op FilterOp) (Decimal, FilterOp, bool) {
	if value.Scale() <= 0 {
		if value.Precision()+int(-int64(value.Scale())) > maxFoldDigits {
			// Unrepresentable at any reasonable cost. Dropping the int family
			// is the caller's existing handling for "this operand produces no
			// int condition", and is correct here: no declared integer type
			// has a bound anywhere near this magnitude.
			return Decimal{}, "", false
		}
		normalized, err := value.SetScale(0)
		if err != nil {
			// Unreachable: scale ≤ 0 is always an exact upward rescale.
			panic("foldToInt: SetScale(0) failed on whole value: " + err.Error())
		}
		return normalized, op, true
	}
	if isComparingOp(op) {
		return value.roundToScale(0, roundingModeFor(op)), op, true
	}
	return Decimal{}, "", false
}
```

- [ ] **Step 4: Guard the `StripTrailingZeros` scale underflow**

In `decimal.go`, `StripTrailingZeros` decrements an `int32` scale in a loop with no underflow check. Add the bound:

```go
		if d.scale == math.MinInt32 {
			// Refusing to wrap is the only safe answer; a value at the int32
			// scale floor cannot be stripped further and returning it
			// unstripped is exact.
			break
		}
```

immediately before the decrement, and add `"math"` to the imports if it is not already there. Read the surrounding loop before editing — the exact variable and loop shape must match what is there.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd ../cyoda-go-spi && go test ./... -run 'HugeNegativeScale' -timeout 120s && go test ./...
```

Expected: PASS for both.

- [ ] **Step 6: Commit**

```bash
cd ../cyoda-go-spi
git add numeric_bucket.go decimal.go numeric_bucket_test.go
git commit -m "fix(search): a 13-byte operand must not allocate a ten-million-digit integer

foldToInt folds a whole value by multiplying the coefficient by 10^-scale,
and ParseDecimal bounds scale only to int32, so \"1e10000000\" allocated for
roughly a second before a single row was read — reachable from an ordinary
search request through condition validation. The schema validator already
carries this guard with this reasoning; the search side did not.

StripTrailingZeros decremented an int32 scale with no underflow check on
the same values."
```

---

## Task 5: Tag the SPI and pin it

**Files:**
- Modify: `../cyoda-go-spi/MAINTAINING.md` (release notes for the tag)
- Modify: `go.mod`, `go.sum` (root), and each `plugins/*/go.mod` via `make repin-plugins`
- Modify: `COMPATIBILITY.md`

**Interfaces:**
- Consumes: Tasks 1–4, all committed in `../cyoda-go-spi`.
- Produces: a pinned SPI version the rest of the plan builds against. Every later task assumes `spi.AdmitsNumeric` is reachable from `cyoda-go`.

Follow `../cyoda-go-spi/MAINTAINING.md` — read it first, it is the authority and this summary is not. Tags are immutable: if the tag is wrong, cut a new one. The `go.work` `use` line pointing at the local SPI stays **uncommitted**.

- [ ] **Step 1: Run the SPI suite one more time before tagging**

```bash
cd ../cyoda-go-spi && go test ./... && go vet ./...
```

Expected: PASS both. Do not tag on a red suite.

- [ ] **Step 2: Update `MAINTAINING.md` with the release note and commit**

Summarise Tasks 1–4 in the file's established format: the new `AdmitsNumeric` predicate, the stored-value filter change, the operand strip, the DoS guard. Commit.

- [ ] **Step 3: Tag and push**

```bash
cd ../cyoda-go-spi
git tag v0.8.4-<per MAINTAINING.md's scheme>
git push origin main --tags
```

- [ ] **Step 4: Pin in cyoda-go, in one commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.claude/worktrees/fix-555-type-admission
go get github.com/cyoda-platform/cyoda-go-spi@<the new tag>
go mod tidy
make repin-plugins
```

- [ ] **Step 5: Update `COMPATIBILITY.md`**

Gate 4 requires the matrix to move with the pin. Add the row for this SPI tag.

- [ ] **Step 6: Verify the pin builds**

```bash
make test
```

Expected: PASS, except for tests this change deliberately inverts — at this point the kernel is new but the write path is not, so `e2e/parity/numeric_classification.go` and the search-side assertions may move. Record which fail and why; Tasks 12 and 13 own them. **Do not "fix" a test here to get green** — the write path has not been written yet.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum plugins/*/go.mod plugins/*/go.sum COMPATIBILITY.md
git commit -m "build(spi): pin the admission predicate and the kernel fixes

One pin commit for the SPI tag carrying AdmitsNumeric, the stored-value
filter, the operand strip and the negative-scale guard."
```

---

## Task 6: `Admit` — the traversal, leaves only

**Files:**
- Create: `internal/domain/model/schema/admit.go`
- Test: `internal/domain/model/schema/admit_test.go`

**Interfaces:**
- Consumes: `spi.AdmitsNumeric` (Task 1, reachable as `AdmitsNumeric` once aliased in `coretypes.go`); `ModelNode` (`Scalar()`, `DeclaredTypes()`, `Object()`, `Array()`, `Kinds()`, `Nullable()`, `NewLeafNode`, `NewObjectNode`, `NewArrayNode`, `SetChild`, `SetElement`, `ObserveArrayWidth`, `SetNullable`, `AddScalarTypes`); `ParseDecimal`; `ClassifyTemporalString`; `spi.ChangeLevel`.
- Produces, and every later task depends on these exact names:

```go
type ChangeReason int

const (
    ReasonLeafType ChangeReason = iota // a leaf does not admit the value
    ReasonNewField                     // an object gains a field
    ReasonNewKind                      // a path gains a kind it does not declare
    ReasonArrayWidth                   // an array grows wider than observed
    ReasonArrayElement                 // an array learns its element type
    ReasonNullable                     // the nullable-marker promotion
)

type Change struct {
    Path     string          // display path, e.g. ".order.amount", ".tags[]"
    Reason   ChangeReason
    Required spi.ChangeLevel // the level this change costs
    Observed DataType        // ReasonLeafType only; zero otherwise
    Declared []DataType      // ReasonLeafType only; what the leaf declares
    Value    any             // the value that forced the change, for error rendering
}

func Admit(model *ModelNode, data any) (overlay *ModelNode, changes []Change, err error)
```

`overlay` is `nil` when the model already admits the whole document. `err` is non-nil only for walk-level failures the model cannot express — an invalid field name (`importer.ErrInvalidFieldName`), a `float64` that escaped `json.UseNumber`, an unsupported Go type. Everything else is a `Change`.

This task implements **leaves only**: a scalar value against a leaf node. Task 7 adds containers. Splitting here is deliberate — a reviewer can accept the admission rule without yet accepting the container traversal.

- [ ] **Step 1: Alias `AdmitsNumeric` into the schema package**

In `internal/domain/model/schema/coretypes.go`, beside the existing aliases:

```go
// AdmitsNumeric is the SPI's single numeric admission predicate. The write
// path and the search kernel both ask it, so a value admitted into a field is
// by construction a value search can find in that field.
var AdmitsNumeric = spi.AdmitsNumeric
```

- [ ] **Step 2: Write the failing test**

Create `internal/domain/model/schema/admit_test.go`. This is the spec's §4 case table, executable:

```go
package schema_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// num builds a json.Number the way the HTTP decoder does.
func num(s string) any { return json.Number(s) }

// TestAdmit_Leaf is the spec's case table. "Held" means Admit returns no
// change: the field holds the value and the model does not move.
func TestAdmit_Leaf(t *testing.T) {
	cases := []struct {
		name     string
		declared []schema.DataType
		value    any
		held     bool
	}{
		{"double holds 1000", []schema.DataType{schema.Double}, num("1000"), true},
		{"double holds 1000.0", []schema.DataType{schema.Double}, num("1000.0"), true},
		{"double holds 1e3", []schema.DataType{schema.Double}, num("1e3"), true},
		{"double holds a whole past 2^31", []schema.DataType{schema.Double}, num("2147483648"), true},
		{"double holds the ceiling", []schema.DataType{schema.Double}, num("9.99999999999999e292"), true},
		{"double refuses above the ceiling", []schema.DataType{schema.Double}, num("9.99999999999999e300"), false},
		{"double refuses 16 significant digits", []schema.DataType{schema.Double}, num("9007199254740993"), false},
		{"integer holds its ceiling", []schema.DataType{schema.Integer}, num("2147483647"), true},
		{"integer refuses past its ceiling", []schema.DataType{schema.Integer}, num("2147483648"), false},
		{"integer refuses a fraction", []schema.DataType{schema.Integer}, num("13.111"), false},
		{"unbound integer refuses a fraction", []schema.DataType{schema.UnboundInteger}, num("1.5"), false},
		{"big decimal holds a high scale", []schema.DataType{schema.BigDecimal},
			num("1.23456789012345678901234567890"), true},

		{"string holds a date-shaped string", []schema.DataType{schema.String}, "2026-03-01", true},
		{"string holds hello", []schema.DataType{schema.String}, "hello", true},
		{"string refuses a number", []schema.DataType{schema.String}, num("5"), false},
		{"string refuses a boolean", []schema.DataType{schema.String}, true, false},
		{"boolean refuses a string", []schema.DataType{schema.Boolean}, "true", false},
		{"integer refuses a string", []schema.DataType{schema.Integer}, "2024", false},

		{"zoned holds a timestamp", []schema.DataType{schema.ZonedDateTime}, "2026-03-01T10:00:00Z", true},
		{"zoned refuses a year", []schema.DataType{schema.ZonedDateTime}, "2026", false},
		{"local date time refuses an offset", []schema.DataType{schema.LocalDateTime},
			"2026-03-01T10:00:00+05:00", false},

		{"a scalar leaf admits null", []schema.DataType{schema.String}, nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leaf := schema.NewLeafNode(tc.declared[0])
			for _, dt := range tc.declared[1:] {
				leaf.AddScalarTypes(dt)
			}
			overlay, changes, err := schema.Admit(leaf, tc.value)
			if err != nil {
				t.Fatalf("Admit: %v", err)
			}
			held := len(changes) == 0
			if held != tc.held {
				t.Errorf("held = %v, want %v (changes: %+v)", held, tc.held, changes)
			}
			if held && overlay != nil {
				t.Errorf("a held value must produce no overlay, got %v", overlay.DeclaredTypes())
			}
			if !held && overlay == nil {
				t.Error("a change must produce an overlay describing it")
			}
		})
	}
}

// A held value must leave the declared set byte-identical.
func TestAdmit_HeldValueDoesNotMoveTheModel(t *testing.T) {
	leaf := schema.NewLeafNode(schema.Double)
	before := append([]schema.DataType(nil), leaf.DeclaredTypes()...)

	if _, changes, err := schema.Admit(leaf, num("2147483648")); err != nil || len(changes) != 0 {
		t.Fatalf("expected the value to be held, got changes=%+v err=%v", changes, err)
	}

	after := leaf.DeclaredTypes()
	if len(before) != len(after) {
		t.Fatalf("declared types changed: %v -> %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("declared types changed: %v -> %v", before, after)
		}
	}
}

// A change records the level it costs and the context the error needs.
func TestAdmit_LeafChangeCarriesLevelAndProps(t *testing.T) {
	leaf := schema.NewLeafNode(schema.Integer)
	_, changes, err := schema.Admit(leaf, num("2147483648"))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("want 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Reason != schema.ReasonLeafType {
		t.Errorf("Reason = %v, want ReasonLeafType", c.Reason)
	}
	if c.Required != spi.ChangeLevelType {
		t.Errorf("Required = %v, want TYPE", c.Required)
	}
	if c.Observed != schema.Long {
		t.Errorf("Observed = %v, want LONG", c.Observed)
	}
	if len(c.Declared) != 1 || c.Declared[0] != schema.Integer {
		t.Errorf("Declared = %v, want [INTEGER]", c.Declared)
	}
}

// A leaf never admits a container, whatever its declared types.
func TestAdmit_LeafRefusesAContainer(t *testing.T) {
	leaf := schema.NewLeafNode(schema.String)
	for _, v := range []any{map[string]any{"k": "v"}, []any{"a"}} {
		_, changes, err := schema.Admit(leaf, v)
		if err != nil {
			t.Fatalf("Admit: %v", err)
		}
		if len(changes) == 0 {
			t.Errorf("a STRING leaf must not admit %T", v)
		}
		if changes[0].Reason != schema.ReasonNewKind {
			t.Errorf("Reason = %v, want ReasonNewKind", changes[0].Reason)
		}
	}
	_ = strings.TrimSpace
}
```

Import `spi "github.com/cyoda-platform/cyoda-go-spi"` in the test file.

- [ ] **Step 3: Run the test to verify it fails**

```bash
go test ./internal/domain/model/schema/ -run 'TestAdmit_'
```

Expected: FAIL — `undefined: schema.Admit`.

- [ ] **Step 4: Write the leaf half of the traversal**

Create `internal/domain/model/schema/admit.go`:

```go
package schema

import (
	"encoding/json"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// ChangeReason names why the stored model did not already admit a value.
type ChangeReason int

const (
	// ReasonLeafType — a leaf declares scalar types, none of which admits
	// this value.
	ReasonLeafType ChangeReason = iota
	// ReasonNewField — an object does not declare this field.
	ReasonNewField
	// ReasonNewKind — a path does not declare the value's JSON kind.
	ReasonNewKind
	// ReasonArrayWidth — an array is wider than any observed so far.
	ReasonArrayWidth
	// ReasonArrayElement — an array observed without content learns its
	// element type.
	ReasonArrayElement
	// ReasonNullable — a node declaring no scalar is observed as null.
	ReasonNullable
)

// Change is one observation the stored model does not admit, together with
// what admitting it would cost. Extend refuses the write when any change
// exceeds the configured level; Validate renders every change as a
// ValidationError.
type Change struct {
	Path     string
	Reason   ChangeReason
	Required spi.ChangeLevel
	Observed DataType
	Declared []DataType
	Value    any
}

// Admit walks data against model, one value at a time, and reports what the
// model would have to become to hold it.
//
// This is the single traversal the write path runs. The previous design
// converted the document into a throwaway model first, which discarded every
// value before any decision was made and fused array elements into one
// description — so a value-aware rule could not be evaluated at all, and
// [2147483648, "hello"] could not be judged element by element.
//
// The overlay is SPARSE: it describes only the leaves and branches that must
// change, so Merge(model, overlay) moves exactly the paths the traversal
// objected to. Merging a full walk of the document instead widened unrelated
// fields — a leaf's declared types depended on what else the document
// happened to carry.
//
// A nil overlay with no changes means the model already admits the document.
func Admit(model *ModelNode, data any) (*ModelNode, []Change, error) {
	a := &admitter{}
	overlay, err := a.node(model, data, "", 0, spi.ChangeLevelType)
	if err != nil {
		return nil, nil, err
	}
	return overlay, a.changes, nil
}

type admitter struct {
	changes []Change
}

func (a *admitter) record(c Change) { a.changes = append(a.changes, c) }

// node admits one value against one model node.
//
// scalarLevel is the level a scalar-shaped change costs at this position:
// ChangeLevelType normally, ChangeLevelArrayElements directly on an array's
// element. It is preserved through nested array levels and reset when
// descending into an object's children, which is exactly where the element
// rules stop applying.
func (a *admitter) node(model *ModelNode, data any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	if depth >= MaxValidationDepth {
		return nil, fmt.Errorf("%s: validation depth exceeded (max %d)", displayPath(path), MaxValidationDepth)
	}

	switch v := data.(type) {
	case nil:
		return a.null(model, path, scalarLevel), nil
	case map[string]any:
		return a.object(model, v, path, depth, scalarLevel)
	case []any:
		return a.array(model, v, path, depth, scalarLevel)
	case float64:
		return nil, fmt.Errorf("%s: received float64 value; callers must use json.UseNumber() decoding", displayPath(path))
	case json.Number, string, bool:
		return a.scalar(model, data, path, scalarLevel)
	default:
		return nil, fmt.Errorf("%s: unsupported type: %T", displayPath(path), data)
	}
}

// scalar is §4's rule: the value's JSON kind must match a declared type's
// kind, and that type must admit the value.
func (a *admitter) scalar(model *ModelNode, data any, path string, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	observed := inferDataType(data)

	if s := model.Scalar(); s != nil {
		if holdsScalar(s.Types(), data) {
			return nil, nil
		}
		// The scalar kind IS declared here — the value's kind is not the
		// complaint, its type is.
		a.record(Change{
			Path: path, Reason: ReasonLeafType, Required: scalarLevel,
			Observed: observed, Declared: s.Types(), Value: data,
		})
		return NewLeafNode(observed), nil
	}

	// The node declares no scalar at all. Establishing the first kinds on a
	// node that declares nothing is the nullable-marker promotion, which keeps
	// the level it has always had; adding a scalar beside a declared container
	// is a new branch, which is more fundamental than a new field.
	required := spi.ChangeLevelStructural
	if len(model.Kinds()) == 0 {
		required = scalarLevel
	}
	a.record(Change{
		Path: path, Reason: ReasonNewKind, Required: required,
		Observed: observed, Declared: model.DeclaredTypes(), Value: data,
	})
	return NewLeafNode(observed), nil
}

// holdsScalar answers §4's per-kind table for one value against one declared
// set. A JSON number is not a string and a JSON string is not a number, so
// STRING does not become a universal sink.
func holdsScalar(declared []DataType, data any) bool {
	switch v := data.(type) {
	case json.Number:
		dec, err := ParseDecimal(string(v))
		if err != nil {
			return false
		}
		for _, dt := range declared {
			if AdmitsNumeric(dt, dec) {
				return true
			}
		}
		return false

	case string:
		// Every JSON string is a string. A temporal type additionally admits
		// a string whose classification is EXACTLY that type: a
		// LOCAL_DATE_TIME parser accepts an offset-bearing timestamp by
		// discarding the offset, and those denote different instants, so the
		// value would be stored unfindable.
		classified, isTemporal := ClassifyTemporalString(v)
		for _, dt := range declared {
			if dt == String {
				return true
			}
			if isTemporal && dt == classified {
				return true
			}
		}
		return false

	case bool:
		for _, dt := range declared {
			if dt == Boolean {
				return true
			}
		}
		return false
	}
	return false
}

// null is the nullable marker. A node that already declares a scalar admits
// null and records nothing; a node that declares none charges the promotion.
func (a *admitter) null(model *ModelNode, path string, scalarLevel spi.ChangeLevel) *ModelNode {
	if model.Nullable() || model.Scalar() != nil {
		return nil
	}
	a.record(Change{
		Path: path, Reason: ReasonNullable, Required: scalarLevel,
		Observed: Null, Declared: model.DeclaredTypes(),
	})
	overlay := NewLeafNode(Null)
	overlay.SetNullable()
	return overlay
}
```

Add temporary stubs so the package compiles while Task 7 is outstanding:

```go
func (a *admitter) object(model *ModelNode, m map[string]any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	return nil, fmt.Errorf("admit: object traversal not yet implemented")
}

func (a *admitter) array(model *ModelNode, arr []any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	return nil, fmt.Errorf("admit: array traversal not yet implemented")
}
```

`TestAdmit_LeafRefusesAContainer` expects a `ReasonNewKind` change, not an error — so give the leaf-vs-container case its answer inside `node` before the stubs are reached:

```go
	case map[string]any:
		if model.Object() == nil {
			return a.wrongKind(model, data, path, scalarLevel), nil
		}
		return a.object(model, v, path, depth, scalarLevel)
	case []any:
		if model.Array() == nil {
			return a.wrongKind(model, data, path, scalarLevel), nil
		}
		return a.array(model, v, path, depth, scalarLevel)
```

with:

```go
// wrongKind records a path gaining a kind it does not declare. A node that
// declares nothing at all is the nullable-marker promotion instead.
func (a *admitter) wrongKind(model *ModelNode, data any, path string, scalarLevel spi.ChangeLevel) *ModelNode {
	required := spi.ChangeLevelStructural
	if len(model.Kinds()) == 0 {
		required = scalarLevel
	}
	a.record(Change{
		Path: path, Reason: ReasonNewKind, Required: required,
		Declared: model.DeclaredTypes(), Value: data,
	})
	switch data.(type) {
	case map[string]any:
		return NewObjectNode()
	default:
		return NewArrayNode(NewLeafNode(Null))
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test ./internal/domain/model/schema/ -run 'TestAdmit_'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/model/schema/admit.go internal/domain/model/schema/admit_test.go internal/domain/model/schema/coretypes.go
git commit -m "feat(model): the traversal that carries the value to the decision, leaves first

Admit walks the document against the stored model and asks, of each value,
whether the field holds it. The answer is a sparse overlay describing only
what must change, so a merge moves exactly the paths the traversal objected
to rather than every path the document happened to mention.

Leaves only; containers are stubbed and land next."
```

---

## Task 7: `Admit` — containers

**Files:**
- Modify: `internal/domain/model/schema/admit.go` — replace the `object` and `array` stubs
- Test: `internal/domain/model/schema/admit_test.go`

**Interfaces:**
- Consumes: everything from Task 6.
- Produces: no new exported names. `Admit` now handles whole documents.

The traversal must keep **every** non-leaf rule the old `checkAndExtend`/`checkBranch` enforced: array width growth at `ARRAY_LENGTH`, an array learning its element type at `ARRAY_ELEMENTS`, a new object field at `STRUCTURAL`, a new kind at `STRUCTURAL`, the nullable-marker promotion, and the `scalarLevel` discipline — `ARRAY_ELEMENTS` persists through nested array levels and resets to `TYPE` when descending into an object's children. Read `extend.go`'s `checkBranch` before writing this; it is being replaced, not reinterpreted.

- [ ] **Step 1: Write the failing test**

Append to `admit_test.go`:

```go
// The defect Extend had: Merge ran over the whole walked document whenever
// any node changed, so an unrelated field widened on a verdict about another.
func TestAdmit_OverlayTouchesOnlyTheChangedPath(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("x", schema.NewLeafNode(schema.Double))
	model.SetChild("y", schema.NewLeafNode(schema.Integer))

	doc := map[string]any{"x": num("2147483648"), "y": num("1.5")}

	overlay, changes, err := schema.Admit(model, doc)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("want exactly 1 change (y), got %d: %+v", len(changes), changes)
	}
	if changes[0].Path != ".y" {
		t.Errorf("changed path = %q, want \".y\"", changes[0].Path)
	}
	if overlay.Object().Child("x") != nil {
		t.Error("x is held; the overlay must not mention it")
	}

	merged := schema.Merge(model, overlay)
	got := merged.Object().Child("x").DeclaredTypes()
	if len(got) != 1 || got[0] != schema.Double {
		t.Errorf("x must stay [DOUBLE], got %v", got)
	}
}

// Each array element is judged on its own; nothing is fused beforehand.
func TestAdmit_MixedKindArrayJudgedElementByElement(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("tags", schema.NewArrayNode(schema.NewLeafNode(schema.Double)))
	model.Object().Child("tags").ObserveArrayWidth(2)

	doc := map[string]any{"tags": []any{num("2147483648"), "hello"}}

	overlay, changes, err := schema.Admit(model, doc)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	// 2147483648 is held by DOUBLE; only "hello" forces a change.
	if len(changes) != 1 {
		t.Fatalf("want exactly 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Required != spi.ChangeLevelArrayElements {
		t.Errorf("Required = %v, want ARRAY_ELEMENTS", changes[0].Required)
	}

	merged := schema.Merge(model, overlay)
	got := merged.Object().Child("tags").Array().Element().DeclaredTypes()
	if len(got) != 2 {
		t.Fatalf("element must declare DOUBLE and STRING, got %v", got)
	}
}

func TestAdmit_ContainerRules(t *testing.T) {
	cases := []struct {
		name         string
		model        func() *schema.ModelNode
		doc          any
		wantChanges  int
		wantRequired spi.ChangeLevel
	}{
		{
			name: "a new object field is STRUCTURAL",
			model: func() *schema.ModelNode {
				m := schema.NewObjectNode()
				m.SetChild("a", schema.NewLeafNode(schema.String))
				return m
			},
			doc:          map[string]any{"a": "x", "b": "y"},
			wantChanges:  1,
			wantRequired: spi.ChangeLevelStructural,
		},
		{
			name: "a wider array is ARRAY_LENGTH",
			model: func() *schema.ModelNode {
				m := schema.NewObjectNode()
				arr := schema.NewArrayNode(schema.NewLeafNode(schema.String))
				arr.ObserveArrayWidth(2)
				m.SetChild("a", arr)
				return m
			},
			doc:          map[string]any{"a": []any{"x", "y", "z"}},
			wantChanges:  1,
			wantRequired: spi.ChangeLevelArrayLength,
		},
		{
			name: "an element type change is ARRAY_ELEMENTS",
			model: func() *schema.ModelNode {
				m := schema.NewObjectNode()
				arr := schema.NewArrayNode(schema.NewLeafNode(schema.String))
				arr.ObserveArrayWidth(2)
				m.SetChild("a", arr)
				return m
			},
			doc:          map[string]any{"a": []any{"x", num("5")}},
			wantChanges:  1,
			wantRequired: spi.ChangeLevelArrayElements,
		},
		{
			name: "an object inside an array resets to TYPE",
			model: func() *schema.ModelNode {
				inner := schema.NewObjectNode()
				inner.SetChild("k", schema.NewLeafNode(schema.String))
				arr := schema.NewArrayNode(inner)
				arr.ObserveArrayWidth(1)
				m := schema.NewObjectNode()
				m.SetChild("a", arr)
				return m
			},
			doc:          map[string]any{"a": []any{map[string]any{"k": num("5")}}},
			wantChanges:  1,
			wantRequired: spi.ChangeLevelType,
		},
		{
			name: "a document the model fully admits produces nothing",
			model: func() *schema.ModelNode {
				m := schema.NewObjectNode()
				m.SetChild("a", schema.NewLeafNode(schema.Double))
				return m
			},
			doc:         map[string]any{"a": num("2147483648")},
			wantChanges: 0,
		},
		{
			name: "a missing field is not a change",
			model: func() *schema.ModelNode {
				m := schema.NewObjectNode()
				m.SetChild("a", schema.NewLeafNode(schema.String))
				m.SetChild("b", schema.NewLeafNode(schema.String))
				return m
			},
			doc:         map[string]any{"a": "x"},
			wantChanges: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, changes, err := schema.Admit(tc.model(), tc.doc)
			if err != nil {
				t.Fatalf("Admit: %v", err)
			}
			if len(changes) != tc.wantChanges {
				t.Fatalf("want %d changes, got %d: %+v", tc.wantChanges, len(changes), changes)
			}
			if tc.wantChanges > 0 && changes[0].Required != tc.wantRequired {
				t.Errorf("Required = %v, want %v", changes[0].Required, tc.wantRequired)
			}
		})
	}
}

// Against an empty model everything is a change and the overlay is the whole
// document's description — which is what registration needs.
func TestAdmit_AgainstEmptyModelDescribesTheWholeDocument(t *testing.T) {
	doc := map[string]any{
		"note":  "hello",
		"when":  "2026-03-01",
		"count": num("5"),
		"tags":  []any{"a", "b"},
	}
	overlay, changes, err := schema.Admit(schema.NewObjectNode(), doc)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("an empty model admits nothing; every field must be a change")
	}
	for _, f := range []string{"note", "when", "count", "tags"} {
		if overlay.Object().Child(f) == nil {
			t.Errorf("overlay is missing %q", f)
		}
	}
	if got := overlay.Object().Child("when").DeclaredTypes(); len(got) != 1 || got[0] != schema.LocalDate {
		t.Errorf("when = %v, want [LOCAL_DATE] — registration discovers temporal types", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/domain/model/schema/ -run 'TestAdmit_'
```

Expected: FAIL — "object traversal not yet implemented".

- [ ] **Step 3: Implement `object`**

Replace the stub:

```go
// object admits a JSON object against a node's object branch. Children of an
// object are ordinary positions again: an array's element rules do not reach
// through a nested object.
func (a *admitter) object(model *ModelNode, m map[string]any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	var overlay *ModelNode
	ensure := func() *ModelNode {
		if overlay == nil {
			overlay = NewObjectNode()
		}
		return overlay
	}

	obj := model.Object()
	for name, val := range m {
		childPath := path + "." + name
		child := obj.Child(name)

		if child == nil {
			// A field the model does not declare. Validate the key before it
			// can become a schema field — this is the one point both
			// field-set-establishing ingresses share.
			if err := ValidateFieldName(path, name); err != nil {
				return nil, err
			}
			a.record(Change{
				Path: childPath, Reason: ReasonNewField,
				Required: spi.ChangeLevelStructural, Value: val,
			})
			derived, err := describe(val, childPath)
			if err != nil {
				return nil, err
			}
			ensure().SetChild(name, derived)
			continue
		}

		childOverlay, err := a.node(child, val, childPath, depth+1, spi.ChangeLevelType)
		if err != nil {
			return nil, err
		}
		if childOverlay != nil {
			ensure().SetChild(name, childOverlay)
		}
	}
	// A field the model declares but the document omits is not a change: the
	// model describes known structure, not required fields.
	return overlay, nil
}
```

- [ ] **Step 4: Implement `array`**

```go
// array admits a JSON array against a node's array branch, judging each
// element individually. The old walk fused every element into one description
// before anything was judged, so [2147483648, "hello"] became a single entry
// meaning "large integer or text" and the number had already been widened.
func (a *admitter) array(model *ModelNode, arr []any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	exArr := model.Array()
	elemPath := path + "[]"

	var elemOverlay *ModelNode
	widened := false

	if exArr.Element() == nil {
		// The array was observed, but never with content, so it declares no
		// element type. Learning one is the same promotion a node declaring
		// no kind undergoes, at the level an array element's changes cost.
		if len(arr) > 0 {
			a.record(Change{
				Path: elemPath, Reason: ReasonArrayElement,
				Required: spi.ChangeLevelArrayElements,
			})
			for _, item := range arr {
				derived, err := describe(item, elemPath)
				if err != nil {
					return nil, err
				}
				if elemOverlay == nil {
					elemOverlay = derived
				} else {
					elemOverlay = Merge(elemOverlay, derived)
				}
			}
		}
	} else {
		// ARRAY_ELEMENTS applies at an array's element and keeps applying
		// through further array levels.
		for _, item := range arr {
			itemOverlay, err := a.node(exArr.Element(), item, elemPath, depth+1, spi.ChangeLevelArrayElements)
			if err != nil {
				return nil, err
			}
			if itemOverlay == nil {
				continue
			}
			if elemOverlay == nil {
				elemOverlay = itemOverlay
			} else {
				elemOverlay = Merge(elemOverlay, itemOverlay)
			}
		}
	}

	if len(arr) > exArr.MaxWidth() {
		a.record(Change{
			Path: path, Reason: ReasonArrayWidth,
			Required: spi.ChangeLevelArrayLength,
		})
		widened = true
	}

	if elemOverlay == nil && !widened {
		return nil, nil
	}
	overlay := NewArrayNode(elemOverlay)
	if widened {
		overlay.ObserveArrayWidth(len(arr))
	}
	return overlay, nil
}
```

If `NewArrayNode(nil)` is not legal, pass `NewLeafNode(Null)` and confirm `Merge` treats it as the no-observation element — read `merge.go` before deciding, and match whatever `importer.walkArray` does for an empty array.

- [ ] **Step 5: Add `describe`, the empty-model derivation**

```go
// describe derives the model fragment a value implies, with no stored model to
// compare against — the shape a brand-new field or element takes. It is
// Admit against an empty node, expressed directly because there is nothing to
// walk against.
func describe(v any, path string) (*ModelNode, error) {
	a := &admitter{}
	overlay, err := a.node(emptyNode(), v, path, 0, spi.ChangeLevelType)
	if err != nil {
		return nil, err
	}
	return overlay, nil
}

// emptyNode is a node declaring nothing: every value is a change against it.
func emptyNode() *ModelNode { return spi.NewEmptyNode() }
```

`spi.NewEmptyNode` comes from Task 1 step 5 and is in the pinned tag. Do **not** substitute `NewLeafNode(Null)`: that node is already nullable, so a field whose only observed value is `null` would be admitted against it, produce no overlay, and disappear from the derived model.

- [ ] **Step 6: Export `ValidateFieldName` from the importer, or move it**

`validateFieldName` currently lives unexported in `internal/domain/model/importer/walker.go`. `admit.go` needs it. Move the function and `ErrInvalidFieldName` into the `schema` package and have `importer` call the moved version, so there is one definition rather than two. Update `internal/domain/model/ingest/validate.go`'s `errors.Is(err, importer.ErrInvalidFieldName)` accordingly.

- [ ] **Step 7: Run the tests to verify they pass**

```bash
go test ./internal/domain/model/schema/ -run 'TestAdmit_'
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/model/schema/admit.go internal/domain/model/schema/admit_test.go internal/domain/model/importer/walker.go internal/domain/model/ingest/validate.go
git commit -m "feat(model): the traversal handles containers, element by element

Every rule the old checkBranch enforced is kept: array width at
ARRAY_LENGTH, an array learning its element at ARRAY_ELEMENTS, a new field
and a new kind at STRUCTURAL, the nullable-marker promotion, and the
scalarLevel discipline that persists through nested arrays and resets
inside an object.

What changes is that an array's elements are judged one at a time instead
of being fused into a single description first, and that the overlay names
only the paths that objected — so a verdict about one field no longer
widens another."
```

---

## Task 8: `Extend` and `Validate` become one mechanism

**Files:**
- Modify: `internal/domain/model/schema/extend.go` — new `Extend`; delete `checkAndExtend`, `checkBranch`, `widensDeclared`
- Modify: `internal/domain/model/schema/validate.go` — new `Validate`; delete `matchesScalarBranch`, `assignableToAny`, `validateNode`, `validateObject`, `validateArray`, `validateLeaf`
- Modify: `internal/domain/model/ingest/validate.go` — stop pre-walking
- Test: `internal/domain/model/schema/extend_test.go`, `validate_test.go`

**Interfaces:**
- Consumes: `Admit`, `Change`, `ChangeReason`, `ReasonLeafType`, `ReasonNewField`, `ReasonNewKind` (Tasks 6–7).
- Produces, and Task 9 onward depends on these signatures:

```go
func Extend(existing *ModelNode, data any, level spi.ChangeLevel) (*ModelNode, error)
func Validate(model *ModelNode, data any) []ValidationError
```

`Extend`'s second parameter changes from `incoming *ModelNode` to `data any`. That is the signature break Task 9 cleans up after.

The error strings must not drift: `classifyValidateOrExtendErr` routes on typed errors, but the e2e suites assert on message shape. Keep the existing wording verbatim — `"type change at %s requires %s level, but level is %q"`, `"new field %q at %s requires STRUCTURAL level, but level is %q"`, `"array width change at %s requires %s level, but level is %q"`, `"array element type at %s requires ARRAY_ELEMENTS level, but level is %q"`, `"new %s branch at %s requires %s level, but level is %q"`, `"nullable marker at %s requires %s level, but level is %q"`.

- [ ] **Step 1: Write the failing test**

Add to `internal/domain/model/schema/extend_test.go`:

```go
// Extend takes the document now, not a pre-walked model. The two write doors
// share one traversal, so they cannot disagree about what a leaf accepts.
func TestExtend_TakesTheDocument(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("amount", schema.NewLeafNode(schema.Double))

	// Held at every level, including the most restrictive.
	for _, level := range []spi.ChangeLevel{
		spi.ChangeLevelArrayLength, spi.ChangeLevelArrayElements,
		spi.ChangeLevelType, spi.ChangeLevelStructural,
	} {
		got, err := schema.Extend(model, map[string]any{"amount": num("2147483648")}, level)
		if err != nil {
			t.Fatalf("level %s: %v", level, err)
		}
		types := got.Object().Child("amount").DeclaredTypes()
		if len(types) != 1 || types[0] != schema.Double {
			t.Errorf("level %s: amount = %v, want [DOUBLE] unchanged", level, types)
		}
	}
}

// A value the field does not hold still costs its level.
func TestExtend_UnheldValueStillGated(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("amount", schema.NewLeafNode(schema.Double))

	_, err := schema.Extend(model, map[string]any{"amount": num("9007199254740993")}, spi.ChangeLevelArrayLength)
	if err == nil {
		t.Fatal("a 16-significant-digit value is not held by DOUBLE; it must be gated")
	}
	if !strings.Contains(err.Error(), "requires TYPE level") {
		t.Errorf("error must name the level it needs, got %q", err)
	}

	got, err := schema.Extend(model, map[string]any{"amount": num("9007199254740993")}, spi.ChangeLevelType)
	if err != nil {
		t.Fatalf("at TYPE the change is permitted: %v", err)
	}
	if types := got.Object().Child("amount").DeclaredTypes(); len(types) != 1 || types[0] != schema.UnboundDecimal {
		t.Errorf("amount = %v, want [UNBOUND_DECIMAL]", types)
	}
}

// The defect: a verdict about one leaf must not widen another.
func TestExtend_AGatedChangeElsewhereDoesNotWidenAHeldLeaf(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("x", schema.NewLeafNode(schema.Double))
	model.SetChild("y", schema.NewLeafNode(schema.Integer))

	got, err := schema.Extend(model,
		map[string]any{"x": num("2147483648"), "y": num("1.5")}, spi.ChangeLevelType)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if types := got.Object().Child("x").DeclaredTypes(); len(types) != 1 || types[0] != schema.Double {
		t.Errorf("x = %v, want [DOUBLE] — only y changed", types)
	}
}
```

Add to `internal/domain/model/schema/validate_test.go`:

```go
// Strict validation and the change-level gate answer the same question, so a
// value a field holds passes strict validation too — including the case no
// changeLevel could work around.
func TestValidate_HeldValuesPassStrict(t *testing.T) {
	cases := []struct {
		name     string
		declared schema.DataType
		value    any
	}{
		{"a date-shaped string in a STRING field", schema.String, "2026-03-01"},
		{"a whole number past 2^31 in a DOUBLE field", schema.Double, num("2147483648")},
		{"an ordinary string", schema.String, "hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := schema.NewObjectNode()
			model.SetChild("f", schema.NewLeafNode(tc.declared))
			if errs := schema.Validate(model, map[string]any{"f": tc.value}); len(errs) != 0 {
				t.Errorf("want no errors, got %v", errs)
			}
		})
	}
}

// A value the field does not hold keeps its code and its Props.
func TestValidate_UnheldValueKeepsIncompatibleType(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("f", schema.NewLeafNode(schema.String))

	errs := schema.Validate(model, map[string]any{"f": num("5")})
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %v", errs)
	}
	if errs[0].Kind != schema.ErrKindIncompatibleType {
		t.Errorf("Kind = %v, want ErrKindIncompatibleType", errs[0].Kind)
	}
	if errs[0].ActualType != schema.Integer {
		t.Errorf("ActualType = %v, want INTEGER", errs[0].ActualType)
	}
	if len(errs[0].ExpectedTypes) != 1 || errs[0].ExpectedTypes[0] != schema.String {
		t.Errorf("ExpectedTypes = %v, want [STRING]", errs[0].ExpectedTypes)
	}
}

// An unknown field is still the stale-schema signal handlers branch on.
func TestValidate_UnknownFieldKeepsItsKind(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("known", schema.NewLeafNode(schema.String))

	errs := schema.Validate(model, map[string]any{"known": "x", "surprise": "y"})
	if len(errs) != 1 || errs[0].Kind != schema.ErrKindUnknownElement {
		t.Fatalf("want one ErrKindUnknownElement, got %v", errs)
	}
	if !schema.HasUnknownSchemaElement(errs) {
		t.Error("HasUnknownSchemaElement must still recognise it")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/domain/model/schema/ -run 'TestExtend_TakesTheDocument|TestExtend_UnheldValue|TestExtend_AGatedChange|TestValidate_Held|TestValidate_Unheld|TestValidate_UnknownField'
```

Expected: FAIL to compile — `Extend` still takes a `*ModelNode`.

- [ ] **Step 3: Rewrite `Extend`**

Replace `Extend`, `checkAndExtend`, `checkBranch` and `widensDeclared` in `extend.go` with:

```go
// Extend admits data against existing, constrained by the given change level.
// When existing already holds every value, existing is returned unchanged.
// When a change is needed that exceeds the permitted level, an error names
// the path, the level the change costs and the level configured.
//
// The verdict and the resulting model come from one traversal, so they cannot
// diverge. They used to agree only by coincidence — the gate and TypeSet.Add
// happened to give the same answers — and that coincidence would not have
// survived a value-aware gate.
func Extend(existing *ModelNode, data any, level spi.ChangeLevel) (*ModelNode, error) {
	overlay, changes, err := Admit(existing, data)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return existing, nil
	}
	for _, c := range changes {
		if !levelPermits(level, c.Required) {
			return nil, changeLevelError(c, level)
		}
	}
	return Merge(existing, overlay), nil
}

// changeLevelError renders a refused change in the wording the API has always
// used. The message shape is asserted by the e2e suites; do not reword it.
func changeLevelError(c Change, level spi.ChangeLevel) error {
	switch c.Reason {
	case ReasonLeafType:
		return fmt.Errorf("type change at %s requires %s level, but level is %q",
			displayPath(c.Path), c.Required, level)
	case ReasonNewField:
		return fmt.Errorf("new field %q at %s requires STRUCTURAL level, but level is %q",
			lastSegment(c.Path), displayPath(c.Path), level)
	case ReasonNewKind:
		return fmt.Errorf("new %s branch at %s requires %s level, but level is %q",
			kindNameFor(c.Value), displayPath(c.Path), c.Required, level)
	case ReasonArrayWidth:
		return fmt.Errorf("array width change at %s requires %s level, but level is %q",
			displayPath(c.Path), c.Required, level)
	case ReasonArrayElement:
		return fmt.Errorf("array element type at %s requires ARRAY_ELEMENTS level, but level is %q",
			displayPath(c.Path), level)
	case ReasonNullable:
		return fmt.Errorf("nullable marker at %s requires %s level, but level is %q",
			displayPath(c.Path), c.Required, level)
	}
	return fmt.Errorf("schema change at %s requires %s level, but level is %q",
		displayPath(c.Path), c.Required, level)
}
```

Write `lastSegment` and `kindNameFor` as small helpers beside it; `kindNameFor` maps a value to `"object"`/`"array"`/`"scalar"` matching the old `NodeKind`'s `String()` output — check what `checkAndExtend` printed for `k` and match it exactly.

`changeLevelRank` and `levelPermits` stay as they are.

- [ ] **Step 4: Rewrite `Validate`**

Replace `Validate` and its helpers in `validate.go` with:

```go
// Validate checks whether data conforms to the model without extending it.
// It returns a slice of validation errors; an empty slice means the data is
// valid.
//
// This asks Admit the same question Extend asks and renders every change as
// a refusal, so strict validation and the change-level gate cannot disagree
// about what a field accepts.
func Validate(model *ModelNode, data any) []ValidationError {
	_, changes, err := Admit(model, data)
	if err != nil {
		return []ValidationError{{Message: err.Error(), Kind: ErrKindGeneric}}
	}
	errs := make([]ValidationError, 0, len(changes))
	for _, c := range changes {
		errs = append(errs, validationErrorFor(c))
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validationErrorFor(c Change) ValidationError {
	switch c.Reason {
	case ReasonLeafType:
		expected := make([]DataType, len(c.Declared))
		copy(expected, c.Declared)
		return ValidationError{
			Path:          c.Path,
			Message:       fmt.Sprintf("value of type %s is not compatible with %v", c.Observed, c.Declared),
			Kind:          ErrKindIncompatibleType,
			ExpectedTypes: expected,
			ActualType:    c.Observed,
		}
	case ReasonNewField:
		return ValidationError{
			Path:    c.Path,
			Message: "unexpected field not present in model",
			Kind:    ErrKindUnknownElement,
		}
	default:
		return ValidationError{
			Path:    c.Path,
			Message: "expected " + declaredKindNames(c) + ", got " + JSONKindName(c.Value),
			Kind:    ErrKindGeneric,
		}
	}
}
```

`declaredKindNames` currently takes a `*ModelNode`; give `Change` enough to render it (add a `DeclaredKinds string` field populated at record time in `admit.go`) rather than reaching back for the node. Keep the output wording identical: `"object"`, `"array"`, `"scalar"`, joined with `" or "`, and `"no value"` when there are none.

`MaxValidationDepth`, `ErrorKind`, `ValidationError`, `HasUnknownSchemaElement`, `FirstIncompatibleType`, `InferDataType`, `inferDataType` and `JSONKindName` all stay.

- [ ] **Step 5: Update `ingest.ValidateOrExtend`**

In `internal/domain/model/ingest/validate.go`, delete the `importer.Walk` call and pass the document straight through:

```go
	extended, err := schema.Extend(modelNode, parsedData, desc.ChangeLevel)
	if err != nil {
		if errors.Is(err, schema.ErrInvalidFieldName) {
			// A field name the wire jsonPath grammar cannot address is a
			// client contract violation with a concrete remedy — rename the
			// key — so it gets the same 400 VALIDATION_FAILED the explicit
			// model import answers.
			return common.Operational(http.StatusBadRequest, common.ErrCodeValidationFailed, err.Error())
		}
		return fmt.Errorf("change level violation: %w", err)
	}
```

Everything after that — the unique-key guard, `Diff`, `ExtendSchema` — is unchanged.

- [ ] **Step 6: Run the package tests**

```bash
go test ./internal/domain/model/... ./internal/domain/entity/...
```

Expected: many failures in the suites Task 9 rewrites — that is the signature break, not a regression. The **new** tests from Step 1 must pass. Check them specifically:

```bash
go test ./internal/domain/model/schema/ -run 'TestExtend_TakesTheDocument|TestExtend_UnheldValue|TestExtend_AGatedChange|TestValidate_Held|TestValidate_Unheld|TestValidate_UnknownField'
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/model/schema/extend.go internal/domain/model/schema/validate.go internal/domain/model/ingest/validate.go internal/domain/model/schema/extend_test.go internal/domain/model/schema/validate_test.go
git commit -m "feat(model)!: one mechanism for what a field accepts

Extend takes the document instead of a pre-walked model, and both write
doors now run the same traversal. A value the field holds is admitted at any
change level — including strict validation, where no configuration could
have worked around it — and a value it does not hold still costs exactly the
level it costs today, with the same message.

The model-to-model Extend algebra is gone; its callers are rewritten next.
Wire error codes, message shapes and Props are unchanged."
```

---

## Task 9: Retire the model-to-model algebra

**Files:**
- Modify: `internal/domain/model/schema/extend_test.go`, `extend_assignable_test.go`, `extend_array_element_test.go`, `extend_branch_level_test.go`, `extend_kindmismatch_test.go`, `extend_nullable_test.go`, `add_kind_branch_test.go`, `axis2_kind_matrix_test.go`, `axis3_changelevel_test.go`
- Modify: `internal/domain/model/schema/commutativity_property_test.go`, `idempotence_property_test.go`, `monotonicity_property_test.go`, `permutation_property_test.go`, `roundtrip_property_test.go`, `fold_order_test.go`
- Modify: `e2e/parity/oracle.go:52,130`, `e2e/parity/schema_extension_property.go`

**Interfaces:**
- Consumes: `Extend(existing *ModelNode, data any, level spi.ChangeLevel)` from Task 8.
- Produces: a green root-module suite. Task 10 assumes `make test` is clean apart from the deliberately inverting assertions.

44 call sites pass a pre-walked `*ModelNode` as the second argument. Each needs the **document** that model was walked from. Most tests build their incoming side with `schema.NewLeafNode(schema.Long)` and similar — those must become the document that produces that observation, e.g. `map[string]any{"amount": json.Number("2147483648")}`.

**This is not a mechanical find-and-replace.** A test that built `NewLeafNode(Long)` was asserting something about the *label*; under the new rule the question is about the *value*, and several such tests change verdict. Read each test's intent before rewriting it. Three of them invert deliberately and are Task 13's, not this task's — leave `extend_assignable_test.go:113`, `e2e/parity/numeric_classification.go:187` and `internal/e2e/model_double_whole_number_test.go:59` failing and note them.

- [ ] **Step 1: Inventory the call sites**

```bash
grep -rn "schema\.Extend(\|Extend(existing\|Extend(current" --include="*.go" internal/ e2e/ | grep -v "^internal/domain/model/schema/extend.go"
```

Record the list. Work through it file by file, committing per file.

- [ ] **Step 2: Rewrite the property suites**

These are the executable statement of the schema algebra and matter most. Each currently walks a random tree into a `*ModelNode` and feeds it to `Extend`. The replacement feeds the **document** and states the same property:

- `commutativity_property_test.go` — Merge/Diff commutativity is about deltas, not about `Extend`. If the suite only used `Extend` to *produce* a delta, produce it with `Extend(model, doc, ChangeLevelStructural)` instead and keep the property intact.
- `monotonicity_property_test.go` — "the extended model is a pure widening of the stored one" holds unchanged and is now more important, since `Diff` refuses a non-additive result. Keep it.
- `idempotence_property_test.go` — extending twice with the same document must be a no-op the second time. Under the new rule this is *stronger* than before: the first extension makes the value held.
- `roundtrip_property_test.go` — codec round-trip; likely only needs the call site updated.
- `permutation_property_test.go` and `fold_order_test.go` — **read Task 12 first.** These are where the accepted order-dependence bites. `fold_order_test.go` asserts that fold order does not decide whether a delta applies; that remains true for the structural shapes it names (it was written for the `add_kind_branch` shapes) and its doc comment must now say so explicitly rather than claiming it generally.

- [ ] **Step 3: Rewrite `e2e/parity/oracle.go`**

Two call sites, `:52` and `:130`. The oracle is the byte-identity authority for the whole parity suite, so this is load-bearing. It currently walks the imported document and calls `schema.Extend(current, walked, ChangeLevelStructural)`; it should now call `schema.Extend(current, doc, ChangeLevelStructural)` with the document it already has. Confirm the oracle still reproduces the server's exported schema byte-for-byte after the change.

- [ ] **Step 4: Run the root suite**

```bash
make test
```

Expected: PASS, except the three deliberately inverting assertions from Task 13 and anything Task 12 owns. List what remains red and confirm each is on that list — **if something red is on neither list, it is a regression and this task is not done.**

- [ ] **Step 5: Vet**

```bash
go vet ./...
```

Expected: clean. In particular, no unused imports left by removed `importer.Walk` calls.

- [ ] **Step 6: Commit**

Commit per file or per coherent group, e.g.:

```bash
git add internal/domain/model/schema/
git commit -m "test(model): state the schema algebra over documents, not over models

Extend takes the document now, so every suite written against the
model-to-model signature is restated. The properties are unchanged in
substance — monotonicity, idempotence, round-trip — but they are now stated
in the terms the write path actually works in, and idempotence is stronger
than it was: once a value is held, extending with it again is a no-op by
construction rather than by coincidence."
```

```bash
git add e2e/parity/oracle.go e2e/parity/schema_extension_property.go
git commit -m "test(parity): the byte-identity oracle follows the new write path"
```

---

## Task 10: Registration runs the same traversal

**Files:**
- Modify: `internal/domain/model/service.go:155`, `internal/domain/model/importer/sample_documents.go:44`
- Test: `internal/domain/model/importer/sample_documents_test.go`, `internal/domain/model/service_test.go`

**Interfaces:**
- Consumes: `Admit` (Tasks 6–7).
- Produces: no signature changes. `importer.Walk` stays — it is how a single document is described.

**The per-document and per-element merge discipline stays.** §8 is explicit: registering `{"note":"hello"}` then `{"note":"2026-03-01"}` must still yield `{STRING, LOCAL_DATE}`. If registration ran incrementally against the model built so far, the second document's value would be *held* by `STRING` and the temporal type would never be discovered — which would remove temporal search from genuinely temporal fields, the exact outcome §8 exists to prevent.

So this task is small and its test is a guard, not a change of behaviour.

- [ ] **Step 1: Write the failing test**

```go
// Registration discovers types; ingestion checks against them. These give
// different declared sets for the same value, and that is the design: a field
// registered from both a word and a date supports temporal predicates, while
// a field locked as text and then written a date stays text.
func TestRegistration_DiscoversTemporalTypesAcrossDocuments(t *testing.T) {
	docs := []map[string]any{
		{"note": "hello"},
		{"note": "2026-03-01"},
	}
	node := importer.DeriveFromSampleDocuments(t.Context(), docs)

	got := node.Object().Child("note").DeclaredTypes()
	if len(got) != 2 {
		t.Fatalf("note = %v, want {STRING, LOCAL_DATE}", got)
	}
	var hasString, hasDate bool
	for _, dt := range got {
		hasString = hasString || dt == schema.String
		hasDate = hasDate || dt == schema.LocalDate
	}
	if !hasString || !hasDate {
		t.Errorf("note = %v, want both STRING and LOCAL_DATE", got)
	}
}

// The same within one document, across array elements.
func TestRegistration_FusesArrayElements(t *testing.T) {
	node := importer.DeriveFromSampleDocuments(t.Context(),
		[]map[string]any{{"tags": []any{"hello", "2026-03-01"}}})

	got := node.Object().Child("tags").Array().Element().DeclaredTypes()
	if len(got) != 2 {
		t.Errorf("element = %v, want {STRING, LOCAL_DATE}", got)
	}
}
```

Use whatever the real entry point is — `DeriveFromSampleDocuments` is a placeholder for the function `sample_documents.go` actually exports. Read the file and use its real name and signature.

- [ ] **Step 2: Run the test**

```bash
go test ./internal/domain/model/importer/ -run 'TestRegistration_'
```

If it already passes, that is the correct outcome — registration is unchanged and the test is a regression guard. Say so in the commit rather than manufacturing a change.

- [ ] **Step 3: Confirm registration shares the traversal without sharing the accumulation**

Read `service.go:155` and `sample_documents.go:44`. They call `Merge` over per-document derivations. `importer.Walk` derives one document. Under Task 7, `describe` does the same job inside `Admit`. **Consolidate them** — Gate 6, and the "don't leave a heterogeneous landscape" rule: two functions deriving a model from a document is exactly the duplication this change exists to remove. Have `importer.Walk` delegate to `schema.Describe` (export `describe` as `Describe`), keeping `Walk`'s field-name validation and error wrapping.

- [ ] **Step 4: Verify no second derivation path remains**

```bash
grep -rn "func .*walkValue\|func classifyNumber\|func .*walkArray\|func .*walkObject" --include="*.go" internal/
```

Expected: no matches, or only inside `schema/admit.go`. If `importer` still has its own recursive walker, this task is not done.

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/domain/model/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/model/
git commit -m "refactor(model): one derivation, used by registration and ingestion alike

The importer had its own recursive walker deriving a model from a document,
and the admission traversal grew a second one. They are now the same
function. Registration keeps its per-document and per-element merge
discipline, which is what makes a field registered from a word and a date
support temporal predicates while a field locked as text does not — the
distinction is the accumulation, not the derivation."
```

---

## Task 11: The convergence contract, restated

**Files:**
- Modify: `e2e/parity/schema_concurrent_convergence.go` — the doc comment at `:19-23`
- Create: `e2e/parity/schema_numeric_fold_carveout.go`
- Modify: `e2e/parity/registry.go`, `e2e/parity/registry_count_test.go:9`
- Modify: `internal/domain/model/schema/fold_order_test.go` — doc comment

**Interfaces:**
- Consumes: `BackendFixture`, `client.Client` from the parity harness; `Admit`/`Extend` from Tasks 6–8.
- Produces: `RunSchemaNumericFoldCarveout(t *testing.T, fixture BackendFixture)`, registered in `registry.go`.

Read the spec's §6 "The fold becomes order-dependent, and that is accepted" before writing this. The decision is settled; this task writes it down where it is enforced.

The carve-out is **not** "anything goes". The property that replaces byte-identity is: every reachable fold is monotone, and every reachable fold admits every value that was written. A fold that lost a written value is still a defect.

- [ ] **Step 1: Write the failing test**

Create `e2e/parity/schema_numeric_fold_carveout.go`:

```go
package parity

import (
	"encoding/json"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// RunSchemaNumericFoldCarveout asserts the property that replaces
// byte-identical convergence for numeric-leaf widening.
//
// A value a leaf already holds does not move the model, so whether a write
// contributes its type depends on what the leaf declared when the write was
// judged — which depends on what arrived before it, and on what arrived
// concurrently. Writing 2147483648 then 12.5 into an [INTEGER] leaf leaves
// [UNBOUND_DECIMAL]; the reverse order leaves [DOUBLE], because by then the
// leaf holds the larger number. Both are correct: each admits every value
// that was written, and each is a widening of what came before.
//
// That is the property asserted here. Byte-identity is asserted for
// structural extension by RunSchemaExtensionConcurrentConvergence, where it
// still holds.
func RunSchemaNumericFoldCarveout(t *testing.T, fixture BackendFixture) {
	orders := [][]string{
		{"2147483648", "12.5"},
		{"12.5", "2147483648"},
	}

	for _, order := range orders {
		tenant := fixture.NewTenant(t)
		c := client.NewClient(fixture.BaseURL(), tenant.Token)

		const modelName = "numeric-fold-carveout"
		const modelVersion = 1

		seed, _ := json.Marshal(map[string]any{"amount": 1})
		if err := c.ImportModel(t, modelName, modelVersion, string(seed)); err != nil {
			t.Fatalf("ImportModel: %v", err)
		}
		if err := c.LockModel(t, modelName, modelVersion); err != nil {
			t.Fatalf("LockModel: %v", err)
		}
		if err := c.SetChangeLevel(t, modelName, modelVersion, "TYPE"); err != nil {
			t.Fatalf("SetChangeLevel: %v", err)
		}

		for _, v := range order {
			body := `{"amount":` + v + `}`
			if _, err := c.CreateEntity(t, modelName, modelVersion, body); err != nil {
				t.Fatalf("write %s: %v", v, err)
			}
		}

		// The property: every value that was written is findable in the
		// model that resulted. Not "the model is a particular set of bytes".
		for _, v := range order {
			hits, err := c.SearchEquals(t, modelName, modelVersion, "amount", v)
			if err != nil {
				t.Fatalf("search %s: %v", v, err)
			}
			if hits == 0 {
				t.Errorf("order %v: %s was written but cannot be found", order, v)
			}
		}
	}
}
```

Use the parity client's real method names — read `e2e/parity/client/` and match them; `CreateEntity` and `SearchEquals` are placeholders for whatever it exposes.

- [ ] **Step 2: Register it and bump the count**

In `e2e/parity/registry.go`, add the entry alongside its neighbours. Then in `e2e/parity/registry_count_test.go:9`, bump `wantParityScenarioCount` from `261` by the number of scenarios added — this task adds one, Task 14 adds more, and the constant moves with each. Update the `registry.go` header comment in the same edit; the count test exists because that comment drifted silently for many PRs.

- [ ] **Step 3: Run it and verify it passes**

```bash
make test
```

Expected: PASS, including the new scenario on memory, sqlite and postgres.

- [ ] **Step 4: Restate the convergence contract**

In `e2e/parity/schema_concurrent_convergence.go`, replace the doc comment at `:19-23`. It currently says the assertion must not be loosened; it must now say precisely where it still holds and where it does not:

```go
// RunSchemaExtensionConcurrentConvergence asserts B-I7 for STRUCTURAL
// extension: N concurrent new-field extensions on the same model all succeed
// and the final fold is byte-identical to a serial replay via the in-memory
// oracle. This asserts permutation invariance of delta application (B-I5)
// through the HTTP layer.
//
// If a backend's output depends on delta application order (e.g. field order
// in the exported schema reflects insertion order), this test will fail. That
// failure is the invariant we want to catch — do not loosen the assertion for
// the shapes tested here.
//
// Byte-identity does NOT extend to numeric- and temporal-leaf widening. A
// value a leaf already holds does not move the model, so whether a write
// contributes its type depends on the snapshot the writing node held — the
// ordinary state during a cross-node gossip window. The weaker property that
// does hold there (every fold is monotone and admits every written value) is
// asserted by RunSchemaNumericFoldCarveout. Adding a numeric leaf to the
// scenario below would make it flaky, not stricter.
```

- [ ] **Step 5: Restate `fold_order_test.go`'s claim**

Its doc comment says *"every pair of legal deltas must apply in either order and reach the same model"*. Delta application is still commutative, so the test stays true — but the comment reads as a claim about the write path, which is now false. Narrow it to what it tests: delta *application*, for the structural shapes it names, and point at the carve-out for what changed.

- [ ] **Step 6: Run the suite**

```bash
make test
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add e2e/parity/ internal/domain/model/schema/fold_order_test.go
git commit -m "test(parity): the fold is order-dependent for numeric leaves, and the suite says so

Byte-identical convergence stays the contract for structural extension,
where it holds. Numeric- and temporal-leaf widening is carved out with the
property that actually holds — every fold is monotone and admits every value
that was written — and gets its own scenario, because a carve-out nothing
exercises is how this was missed in the first place.

fold_order_test's comment claimed permutation invariance generally; it tests
delta application, which is still commutative, and now says so."
```

---

## Task 12: The three inverting assertions

**Files:**
- Modify: `internal/domain/model/schema/extend_assignable_test.go:113`
- Modify: `internal/e2e/model_double_whole_number_test.go:59`
- Modify: `e2e/parity/numeric_classification.go:187`

**Interfaces:**
- Consumes: Tasks 6–9.
- Produces: a green suite for these three.

These are the parity, HTTP-e2e and unit legs of one behaviour. **Rewrite their reasoning, do not delete their comments.** `extend_assignable_test.go:105-112` pins the invariant this change removes in so many words — *"Pinning this stops a later 'any whole number is fine' simplification from silently reshaping stored data."* That objection is correct and the design answers it; the comment must carry the answer.

- [ ] **Step 1: Rewrite the unit leg**

`TestExtend_WholeNumberPastIntegerRange_IsStillATypeChange` becomes the opposite assertion under a name that says what it now pins. Replace the test and its doc comment with:

```go
// The boundary, and why it moved. Classification by LABEL condemned every
// whole number past 2^31 as LONG, and LONG does not widen into DOUBLE
// because 2^63 exceeds Double's 53-bit mantissa. The mantissa argument is
// right; the instrument was wrong. 2147483648 is ten significant digits and
// exactly representable, and it was refused only by association with values
// that are not.
//
// Admission judges the value: a DOUBLE leaf holds a number inside DOUBLE's
// range that needs at most 15 significant digits. Every integer above 2^53
// needs at least 16, so the mantissa boundary is exactly where it was —
// 9007199254740993 is still a type change, and is asserted below so a later
// "any whole number is fine" simplification still cannot pass.
func TestExtend_WholeNumberInDoubleRangeIsHeld(t *testing.T) {
	build := func() *schema.ModelNode {
		m := schema.NewObjectNode()
		m.SetChild("amount", schema.NewLeafNode(schema.Double))
		return m
	}

	// Held at the most restrictive level: no model change is needed.
	got, err := schema.Extend(build(), map[string]any{"amount": num("2147483648")}, spi.ChangeLevelArrayLength)
	if err != nil {
		t.Fatalf("a DOUBLE leaf holds 2147483648: %v", err)
	}
	if types := got.Object().Child("amount").DeclaredTypes(); len(types) != 1 || types[0] != schema.Double {
		t.Errorf("amount = %v, want [DOUBLE] unchanged", types)
	}

	// The mantissa boundary is unmoved.
	if _, err := schema.Extend(build(), map[string]any{"amount": num("9007199254740993")}, spi.ChangeLevelArrayLength); err == nil {
		t.Error("16 significant digits exceed Double's mantissa; this must stay a gated type change")
	}
}
```

- [ ] **Step 2: Rewrite the HTTP-e2e leg**

`internal/e2e/model_double_whole_number_test.go` — **both** halves invert: the `400` becomes `200`, and the assertion that the leaf widened to `UNBOUND_DECIMAL` becomes an assertion that it stayed `[DOUBLE]`. Add the `9007199254740993` case in the same test so the boundary is covered at this layer too.

- [ ] **Step 3: Rewrite the parity leg**

`e2e/parity/numeric_classification.go:187` `RunNumericClassificationDoubleSchemaAcceptsWholeNumber` — the name is now accurate and the assertion changes from `400` to `200`. Add the model-unchanged check: the point is not only that the write succeeds but that the model did not move.

- [ ] **Step 4: Run the tiers**

```bash
make test
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/extend_assignable_test.go internal/e2e/model_double_whole_number_test.go e2e/parity/numeric_classification.go
git commit -m "test: the three legs of the boundary that moved, and the one that did not

A DOUBLE leaf holds 2147483648 now, at every change level, and does not
widen to hold it. The mantissa boundary these tests were defending is
unmoved — 9007199254740993 is still a gated type change — and each test now
asserts both halves, so the reasoning survives rather than the verdict
alone."
```

---

## Task 13: The pushdown consequences

**Files:**
- Modify: `plugins/sqlite/query_planner.go:295-303` — the `isLeafPushable` doc comment
- Modify: `plugins/postgres/query_planner.go:836-844` — `orderingOp`, add `COLLATE "C"`
- Test: `plugins/postgres/` — a text-comparison test under a non-C collation

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: no signature changes.

- [ ] **Step 1: Write the failing test**

The kernel compares strings with `strings.Compare` — byte order. Postgres's `orderByFieldExpr` already uses `COLLATE "C"` for exactly this reason (`searcher.go:349`); the WHERE-clause path does not. Under a non-C database collation a narrowing range can drop rows the kernel would match, and this change fills `[STRING]` leaves with punctuation-heavy ISO timestamps where ICU and byte order diverge.

Write a postgres e2e test that stores strings whose ICU and byte order differ (mixing `-`, `:`, `+`, `Z` and letters), runs a `>=` range over them, and asserts the row set matches what the kernel alone returns. Model it on the existing pushdown-parity tests in `plugins/postgres/`.

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./plugins/postgres/... -run '<the new test name>'
```

Expected: FAIL under a non-C collation. If the test database is already C-collated, the test passes vacuously — set the collation explicitly in the fixture so the test means something.

- [ ] **Step 3: Add the collation**

In `orderingOp`, emit `COLLATE "C"` on the text comparison branches, mirroring `orderByFieldExpr`'s spelling exactly.

- [ ] **Step 4: Correct sqlite's doc comment**

`isLeafPushable`'s comment justifies its `len(f.Declared) > 1` gate as *"its stored values may span different type families / SQLite storage classes"*. A monomorphic `[DOUBLE]` leaf now routinely holds both INTEGER-class and REAL-class scalars, so the stated reason no longer supports the conclusion. The gate is still sound — SQLite compares INTEGER and REAL numerically, and the real worry was TEXT-vs-numeric, which the JSON-kind rule keeps out. Say that.

- [ ] **Step 5: Guard the importer's negative-scale expansion**

`internal/domain/model/importer/walker.go:99-102` materialises `10^-scale` with no bound, the same hazard Task 4 fixed in the SPI and that `schema/validate.go:296-312` already guards. If Task 10 consolidated the walker into `schema.Describe`, the guard belongs there and this step is confirming it, not adding it.

```bash
grep -rn "big.NewInt(10)" --include="*.go" internal/
```

Every site must be guarded. Confirm each.

- [ ] **Step 6: Run the tiers**

```bash
make test-full
```

Expected: PASS across root and all three plugin submodules.

- [ ] **Step 7: Commit**

```bash
git add plugins/sqlite/query_planner.go plugins/postgres/ internal/domain/model/importer/
git commit -m "fix(search): postgres text comparisons compare the way the kernel does

The kernel compares strings byte-wise and postgres's ORDER BY already says
COLLATE \"C\" for that reason; the WHERE path did not, so under a non-C
database collation a narrowing range could drop rows the kernel matched.
Admitting date-shaped strings into text leaves fills those leaves with the
punctuation-heavy values where ICU and byte order diverge, which turns a
latent divergence into a reachable one.

sqlite's pushability gate is unchanged and still sound, but its stated
reason — that a polymorphic leaf may span storage classes — no longer
describes what it is protecting against."
```

---

## Task 14: Full coverage — HTTP, gRPC, parity

**Files:**
- Create: `internal/e2e/type_admission_test.go`
- Create: `internal/grpc/type_admission_test.go`
- Create: `e2e/parity/type_admission.go`
- Modify: `e2e/parity/registry.go`, `e2e/parity/registry_count_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `RunTypeAdmission*` scenarios registered in the parity registry.

The spec's §10 matrix is the checklist. **A missing cell blocks merge unless waived with a one-line reason in the commit message.** Work the matrix row by row; do not sample it.

- [ ] **Step 1: Write the HTTP e2e coverage**

`internal/e2e/type_admission_test.go`, against the real Postgres `TestMain` already starts. Cover every §9 row:

- Held value with no `changeLevel` → `200` (this is the case no configuration could work around).
- Held value at each level below `TYPE` → `200`.
- Unheld value with no `changeLevel` → `400 INCOMPATIBLE_TYPE`, with `fieldPath`, `expectedType`, `actualType` Props.
- Unheld value below `TYPE` → `400 VALIDATION_FAILED`, error naming the level.
- Unheld value at `TYPE` → `200`, model widened.
- Wrong JSON kind for every declared type → `400`.
- Kind mismatch, array into scalar → `400 VALIDATION_FAILED`.
- `PUT /api/entity/JSON/{id}` with a held value → `200`.
- `PATCH /api/entity/JSON/{id}` with a held value → `200`.
- `POST …/collection` with a held value → `200`.
- Unique-key leaf made non-scalar → `422 INVALID_UNIQUE_KEY_DEFINITION`.
- The processor-output ingress: a processor returning a date-shaped string into a `STRING` leaf completes the transition. This is a workflow behaviour change, not just an API one — cover it.
- Search: `EQUALS 5.0` finds a stored `5`; `NOT_EQUAL 5.0` does not; `EQUALS 5.000000000000000000` finds a stored `5` on a `DOUBLE` leaf; `NOT_EQUAL -0.0` does not match a stored `0`; `[DOUBLE] < 1e300` finds a stored `2147483648`; a condition no declared type accepts → `400 CONDITION_TYPE_MISMATCH`.

- [ ] **Step 2: Write the gRPC coverage**

`internal/grpc/type_admission_test.go`. `internal/grpc/entity.go:36,88` handles **both** `EntityCreateRequest` and `EntityUpdateRequest`; the rule says HTTP and gRPC are separate entry points and both are covered. Assert the envelope — `Success`, `Error.Code` — for:

- Held value, strict → `Success: true` (was a `CLIENT_ERROR` envelope).
- Held value, level below `TYPE` → `Success: true`.
- Unheld value, strict → `CLIENT_ERROR` with the `INCOMPATIBLE_TYPE` code.
- JSON number into `STRING` → `CLIENT_ERROR`.
- The same set for `EntityUpdateRequest`.

There is no existing gRPC leg for the `LONG` case, so these are all new tests.

- [ ] **Step 3: Write the parity scenarios**

`e2e/parity/type_admission.go` for the backend-agnostic rows: held-and-unchanged, held-then-found, the gated boundaries, the mixed-kind array, `EQUALS 5.0`, registration still yielding `{STRING, LOCAL_DATE}`, and strict validation never being more permissive than `ARRAY_LENGTH`.

**Concurrency tests do not go here.** Task 11's carve-out scenario is a sequential ordering test, which is fine; a goroutine storm belongs in an isolated single-backend e2e, never in the shared parity suite.

- [ ] **Step 4: Register and bump the count**

Add every new scenario to `e2e/parity/registry.go` and bump `wantParityScenarioCount` in `registry_count_test.go:9` to match, updating the `registry.go` header comment in the same edit.

- [ ] **Step 5: Write the two property tests**

The spec's §10 marks these as the design's invariant:

- **held ⟹ declared types byte-identical**, over the full type × value matrix, **in documents that also carry a gated change in another field** — without that, it passes vacuously against the very defect §6 fixes.
- **held ⟹ findable**, over the same matrix, including the precision-16 and scale-292 `DOUBLE` boundaries where the range-only rule fails.

- [ ] **Step 6: Run everything**

```bash
make test-full
```

Expected: PASS, root and all three plugin submodules.

- [ ] **Step 7: Commit**

```bash
git add internal/e2e/ internal/grpc/ e2e/parity/
git commit -m "test: every cell of the admission matrix, on a running backend

Both write doors at every change level, both entry points, and both
properties the design rests on — held implies the model does not move, and
held implies the value is findable. The second is stated over the precision
and scale boundaries where a range-only rule would have passed the first and
failed the second.

The processor-output ingress is covered too: a processor returning a
date-shaped string into a text leaf now completes its transition instead of
failing it, which is a workflow behaviour change and not only an API one."
```

---

## Task 15: Documentation and cloud parity

**Files:**
- Modify: `cmd/cyoda/help/content/config/models.md` (or wherever the model help topic lives — check `cmd/cyoda/help/content/`)
- Modify: `docs/numeric-classification.md`
- Modify: `CHANGELOG.md`
- Modify: `COMPATIBILITY.md` (if Task 5 did not already cover the final tag)
- Create: `docs/cloud-parity/<feature>.md`

**Interfaces:**
- Consumes: everything above.
- Produces: the Gate 4 and Gate 7 artefacts.

- [ ] **Step 1: Update `docs/numeric-classification.md`**

This is where the range-vs-admission distinction belongs in permanent form. State the per-family predicate, and state why `DOUBLE` needs the precision conjunct — a reader who sees only "range" will reintroduce the defect. Do not reference the issue number.

- [ ] **Step 2: Update the model help topic**

A field holds a value when its declared type admits it; the model changes only when it does not. Note the behaviour change: a text-declared field no longer learns a temporal subtype from a write, and registration is how a field acquires one.

- [ ] **Step 3: Update `CHANGELOG.md`**

v0.8.4 is a PATCH release but backward compatibility is **not** a constraint for it. Declare the breaking changes under a `### Breaking` heading:

- A `DOUBLE` field now accepts a whole number past 2³¹ without widening.
- A `STRING` field now accepts a date-shaped string, and no longer learns a temporal subtype from an entity write.
- `EQUALS 5.0` now finds a stored `5`; `NOT_EQUAL 5.0` no longer matches it.
- Numeric-leaf model folding is order-dependent under concurrent extension; every fold remains monotone and admits every written value.

- [ ] **Step 4: Write the cloud-parity record**

`docs/cloud-parity/` takes one file per feature or behaviour. This change alters the integration contract — what a field accepts on write, and which rows a search returns — so Gate 7 applies. Record what cyoda-go now does and what Cloud must align to.

- [ ] **Step 5: Check `README.md` and `CONTRIBUTING.md`**

No env vars change and no developer workflow changes, so these are likely untouched — confirm rather than assume.

- [ ] **Step 6: Commit**

```bash
git add docs/ cmd/cyoda/help/ CHANGELOG.md COMPATIBILITY.md
git commit -m "docs: what a field accepts, and the three ways it used to disagree with itself

numeric-classification.md gets the per-family admission predicate in
permanent form, including why DOUBLE needs the precision bound: a reader who
takes away 'the range' will reintroduce the defect.

The changelog declares the breaking changes plainly, including the one that
is easy to miss — numeric-leaf folding is order-dependent under concurrent
extension now, and the guarantee that replaces byte-identity is that every
fold is monotone and admits every value written."
```

---

## Task 16: Verification and the PR

**Files:** none — this is the Gate 5 checkpoint.

- [ ] **Step 1: Full suite**

```bash
make test-full
```

Expected: PASS, root plus `plugins/memory`, `plugins/sqlite`, `plugins/postgres`, including `internal/e2e`. **Do not claim done on a partial run.** If Docker misbehaves, `make preflight` and fix it; there is no narrower run that substitutes.

- [ ] **Step 2: Vet, root and each plugin**

```bash
go vet ./...
(cd plugins/memory && go vet ./...)
(cd plugins/sqlite && go vet ./...)
(cd plugins/postgres && go vet ./...)
```

Expected: clean.

- [ ] **Step 3: Race detector, once**

```bash
make race
```

Expected: PASS. This is the CI-parity scope and excludes `internal/e2e` by design.

- [ ] **Step 4: Confirm the exit checks**

```bash
# No second derivation path.
grep -rn "importer.Walk(" --include="*.go" internal/ e2e/
# The label-based stored filter is gone from the kernel.
grep -rn "classifyStoredNumeric" ../cyoda-go-spi/
# No issue numbers in shipped artefacts.
grep -rn "#555" --include="*.go" --include="*.md" internal/ plugins/ cmd/ api/ | grep -v docs/superpowers
```

Expected: the first two empty or explained; the third empty.

- [ ] **Step 5: Cassandra plugin check**

The SPI change is a strict loosening on the stored side and removes no symbol the plugin references, but the spec's §13 records two consequences there. Build the plugin against the new pin and run its suite:

```bash
cd ../cyoda-go-cassandra && go build ./... && go test ./...
```

If it is red, that is in scope — a backend diverging on the same contract is a bug to fix, not an accepted divergence. If the §13 range-path issue surfaces, raise it on `Cyoda/cyoda-go-cassandra#96` with the reproduction rather than working around it here.

- [ ] **Step 6: Fresh-context code review**

**This is a required gate and a standing request from the user — dispatch a subagent, do not run it inline and do not ask first.** Use `superpowers:requesting-code-review`. If anything prevents dispatching one, say so and stop; never drop the gate silently.

- [ ] **Step 7: Security review**

`antigravity-bundle-security-developer:cc-skill-security-review`. The DoS guards from Tasks 4 and 13 are the security-relevant surface; input validation at the write boundary is the other.

- [ ] **Step 8: Open the PR against `release/v0.8.4`**

Check the base before creating it — milestone PRs target the release branch, not `main`.

---

## Self-Review

**Spec coverage.** §1 terms → Task 15 docs. §2 rationale → Tasks 1, 15. §3(a)(b)(c) → Tasks 1–3, 6–8, 14. §4 rule and case table → Tasks 6, 7, 14. §5 predicate → Task 1. §6 traversal → Tasks 6, 7, 8; its "what it replaces" → Task 9; its order-dependence ruling → Task 11; its registration clause → Task 10. §7(i) → Task 2; §7(ii) → Task 3; the DoS guard → Tasks 4, 13; the gate that must not move → Task 2 (untouched, asserted in Task 14); pushdown → Task 13; the sortability improvement → Task 15's changelog. §8 → Task 10. §9 error table → Task 14. §10 matrix → Task 14, with the inverting assertions in Task 12 and the parity count in Tasks 11 and 14. §11 work list → all tasks. §12 not-changing → asserted throughout, chiefly Tasks 10 and 14. §13 cross-repo → Task 16 step 5. §14 → Task 15's cloud-parity record.

**A dependency that would otherwise have been found too late.** Task 7's `describe` needs a node declaring nothing, and `ModelNode`'s fields are unexported, so it cannot be built from `cyoda-go`. `NewLeafNode(Null)` looks like the answer and is not — it sets `nullable = true`, so a field observed only as `null` would be admitted against it and vanish from the derived model. `NewEmptyNode` is therefore in Task 1, before the tag Task 5 cuts, rather than discovered during Task 7 when the tag is already immutable.

**Placeholder scan.** Task 10's `DeriveFromSampleDocuments`, Task 11's `CreateEntity`/`SearchEquals` and Task 13's test name are explicitly marked as placeholders for real names to be read from the code, with the file to read named in each case. Task 5's tag string follows `MAINTAINING.md`. Everything else is concrete.

**Type consistency.** `Admit(model *ModelNode, data any) (*ModelNode, []Change, error)` is used with that signature in Tasks 6, 7, 8 and 10. `Change`'s fields — `Path`, `Reason`, `Required`, `Observed`, `Declared`, `Value` — are consistent across Tasks 6, 7, 8, plus the `DeclaredKinds` field Task 8 step 4 adds and names explicitly. `Extend(existing *ModelNode, data any, level spi.ChangeLevel)` is consistent across Tasks 8, 9, 12. `AdmitsNumeric(DataType, Decimal) bool` is consistent across Tasks 1, 2, 6.
