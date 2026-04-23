# Sub-project A.1 — Numeric Classifier Parity: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port Cyoda Cloud's numeric classification algorithm to cyoda-go with collapse-to-single-type semantics, eliminate silent precision loss in the gRPC ingestion path, and drop `Byte`/`Short`/`Float` from the `DataType` enum.

**Architecture:** New `schema.Decimal` (unscaled `big.Int` + int32 `scale`) + new `schema/numeric.go` with envelope-based classifier and `CollapseNumeric`. `TypeSet.Add` partitions numeric and non-numeric members; numerics always collapse to one type. Walker routes by value (whole-number vs fractional) not by source syntax.

**Tech Stack:** Go 1.26.2, `math/big`, `encoding/json` with `UseNumber`. No new external dependencies.

**Spec:** `docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md` (revision 3).
**Reviews:** `docs/superpowers/reviews/2026-04-21-data-ingestion-qa-subproject-a1-review-01.md`, `-02.md`.

---

## File Map

**Created:**
- `internal/domain/model/schema/decimal.go` — minimal decimal type (parse, classify, compare, serialize).
- `internal/domain/model/schema/decimal_test.go`
- `internal/domain/model/schema/numeric.go` — envelopes, family/rank helpers, `ClassifyInteger`, `ClassifyDecimal`, `IsAssignableTo`, `CollapseNumeric`.
- `internal/domain/model/schema/numeric_test.go`
- `internal/grpc/entity_usenumber_test.go` — precision-preservation tests for gRPC path.
- `internal/e2e/numeric_classification_test.go` — end-to-end HTTP + gRPC precision tests.
- `docs/numeric-classification.md` — cyoda-go policy, citing the analysis doc and the two intentional divergences.

**Modified:**
- `internal/domain/model/schema/types.go` — drop `Byte`, `Short`, `Float`; update `dataTypeNames`, `NumericFamily`, `NumericRank`; rewrite `TypeSet.Add` to use `CollapseNumeric`.
- `internal/domain/model/schema/types_test.go` — remove references to dropped types; add collapse-integration tests.
- `internal/domain/model/schema/validate.go` — replace `isCompatible` with `IsAssignableTo`; simplify `inferDataType`; remove `float64` branch.
- `internal/domain/model/schema/validate_test.go` — add asymmetric-compatibility tests.
- `internal/domain/model/importer/walker.go` — delete `WalkConfig.IntScope`/`DecimalScope` fields, `clampNumeric`, `inferNumericType`, `inferNumericTypeFromString`, the `float64` branch. Rewrite to use `ParseDecimal` + `StripTrailingZeros` + `ClassifyInteger`/`ClassifyDecimal`.
- `internal/domain/model/importer/walker_test.go` — remove `IntScope=Byte`/`DecimalScope=Float` test, update expected classifications.
- `internal/domain/model/exporter/json_schema.go` — remove `Byte`, `Short`, `Float` from numeric-family switch.
- `internal/grpc/entity.go` — replace `json.Unmarshal(payload, &req)` with UseNumber-backed decoder at every ingestion dispatch site (create, update, collection-create, collection-update).

---

## Task 1: `Decimal` type — struct and parse

**Files:**
- Create: `internal/domain/model/schema/decimal.go`
- Create: `internal/domain/model/schema/decimal_test.go`

- [ ] **Step 1: Write the failing parse tests**

Create `internal/domain/model/schema/decimal_test.go`:

```go
package schema

import (
	"math/big"
	"testing"
)

func bigInt(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad test int: " + s)
	}
	return v
}

func TestParseDecimal_ValidInputs(t *testing.T) {
	cases := []struct {
		in       string
		unscaled *big.Int
		scale    int32
	}{
		{"0", bigInt("0"), 0},
		{"0.0", bigInt("0"), 1},
		{"-0", bigInt("0"), 0},
		{"-0.0", bigInt("0"), 1},
		{"0.1", bigInt("1"), 1},
		{"123.456", bigInt("123456"), 3},
		{"1.5e2", bigInt("15"), -1},
		{"1.5E-2", bigInt("15"), 3},
		{"-.5", bigInt("-5"), 1},
		{".5", bigInt("5"), 1},
		{"1e0", bigInt("1"), 0},
		{"1.0", bigInt("10"), 1},
		{"1.00", bigInt("100"), 2},
		{"+42", bigInt("42"), 0},
		{"-42", bigInt("-42"), 0},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			d, err := ParseDecimal(c.in)
			if err != nil {
				t.Fatalf("ParseDecimal(%q): unexpected error: %v", c.in, err)
			}
			if d.unscaled.Cmp(c.unscaled) != 0 {
				t.Errorf("unscaled: got %s, want %s", d.unscaled.String(), c.unscaled.String())
			}
			if d.scale != c.scale {
				t.Errorf("scale: got %d, want %d", d.scale, c.scale)
			}
		})
	}
}

func TestParseDecimal_Invalid(t *testing.T) {
	invalid := []string{
		"", " ", "abc", "1.2.3", "1e", "1..2",
		"NaN", "Infinity", "+Infinity", "-Infinity",
		"1.5.5", "1e1e1", "--1", "++1",
	}
	for _, s := range invalid {
		t.Run(s, func(t *testing.T) {
			if _, err := ParseDecimal(s); err == nil {
				t.Errorf("ParseDecimal(%q): expected error, got nil", s)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify RED**

```bash
go test ./internal/domain/model/schema/... -run 'TestParseDecimal_' -v
```
Expected: compile error — `undefined: ParseDecimal`, `Decimal` has no field `unscaled`/`scale`.

- [ ] **Step 3: Create `decimal.go` with struct and `ParseDecimal`**

Create `internal/domain/model/schema/decimal.go`:

```go
// Package schema ... (existing doc preserved).
package schema

import (
	"fmt"
	"math/big"
	"strings"
)

// Decimal is a fixed-scale arbitrary-precision decimal.
// Value = unscaled × 10^(-scale).
// Scale may be negative (e.g. 1e2 has unscaled=1, scale=-2).
// No arithmetic — cyoda-go delegates arithmetic to Trino.
type Decimal struct {
	unscaled *big.Int
	scale    int32
}

// ParseDecimal parses a decimal string. Accepts integer literals,
// fractional literals, and scientific notation with optional sign.
// Rejects NaN, Infinity, empty strings, and malformed forms.
func ParseDecimal(s string) (Decimal, error) {
	if s == "" {
		return Decimal{}, fmt.Errorf("parse decimal: empty string")
	}
	// Reject well-known non-numeric tokens explicitly; big.Int.SetString
	// would reject them anyway, but the error message is clearer here.
	switch strings.ToLower(s) {
	case "nan", "inf", "infinity", "+inf", "+infinity", "-inf", "-infinity":
		return Decimal{}, fmt.Errorf("parse decimal: non-numeric token %q", s)
	}

	// Split mantissa and exponent.
	var mantissa, expPart string
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, expPart = s[:i], s[i+1:]
	} else {
		mantissa = s
	}
	if mantissa == "" || mantissa == "+" || mantissa == "-" {
		return Decimal{}, fmt.Errorf("parse decimal: empty mantissa in %q", s)
	}

	// Strip mantissa sign for easier processing; remember it.
	sign := ""
	switch mantissa[0] {
	case '+':
		mantissa = mantissa[1:]
	case '-':
		sign = "-"
		mantissa = mantissa[1:]
	}
	if mantissa == "" {
		return Decimal{}, fmt.Errorf("parse decimal: missing digits in %q", s)
	}

	// Split integer and fractional parts.
	var intPart, fracPart string
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		intPart, fracPart = mantissa[:i], mantissa[i+1:]
		// Reject multiple decimal points.
		if strings.ContainsRune(fracPart, '.') {
			return Decimal{}, fmt.Errorf("parse decimal: multiple decimal points in %q", s)
		}
	} else {
		intPart = mantissa
	}

	// If intPart is empty (e.g. ".5"), use "0".
	if intPart == "" {
		intPart = "0"
	}
	// Mantissa must have at least one digit.
	if intPart == "" && fracPart == "" {
		return Decimal{}, fmt.Errorf("parse decimal: no digits in %q", s)
	}

	// Validate digits.
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return Decimal{}, fmt.Errorf("parse decimal: invalid digit %q in %q", r, s)
		}
	}
	for _, r := range fracPart {
		if r < '0' || r > '9' {
			return Decimal{}, fmt.Errorf("parse decimal: invalid digit %q in %q", r, s)
		}
	}

	// Parse exponent.
	var exp int64 = 0
	if expPart != "" {
		var err error
		exp, err = parseInt64(expPart)
		if err != nil {
			return Decimal{}, fmt.Errorf("parse decimal: invalid exponent %q: %w", expPart, err)
		}
	}

	// Build unscaled: sign + intPart + fracPart, skipping leading zeros.
	unscaledStr := sign + intPart + fracPart
	unscaled, ok := new(big.Int).SetString(unscaledStr, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("parse decimal: failed to build unscaled from %q", s)
	}

	// Scale: fractional-digit count minus exponent.
	scale := int64(len(fracPart)) - exp
	if scale > int64(int32Max) || scale < int64(int32Min) {
		return Decimal{}, fmt.Errorf("parse decimal: scale %d out of int32 range", scale)
	}
	return Decimal{unscaled: unscaled, scale: int32(scale)}, nil
}

const (
	int32Max = int64(1<<31 - 1)
	int32Min = int64(-1 << 31)
)

// parseInt64 is a minimal strict-decimal integer parser. Rejects empty
// strings, leading/trailing spaces, and leading "+" on positive values
// is allowed; "--" or "++" rejected by digit-validity check.
func parseInt64(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty integer")
	}
	neg := false
	if s[0] == '+' {
		s = s[1:]
	} else if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, fmt.Errorf("no digits after sign")
	}
	var v int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid digit %q", r)
		}
		v = v*10 + int64(r-'0')
	}
	if neg {
		v = -v
	}
	return v, nil
}
```

- [ ] **Step 4: Run test to verify GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestParseDecimal_' -v
```
Expected: all subtests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/decimal.go internal/domain/model/schema/decimal_test.go
git commit -m "$(cat <<'EOF'
feat(schema): Decimal — struct + ParseDecimal (sub-project A.1 task 1)

Hand-rolled arbitrary-precision decimal using math/big.Int + int32
scale. No arithmetic; parse only. Rejects NaN/Infinity/empty/
malformed. Per spec §4.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `Decimal` — accessors, `IsZero`, `Sign`, `Unscaled`

**Files:**
- Modify: `internal/domain/model/schema/decimal.go`
- Modify: `internal/domain/model/schema/decimal_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/domain/model/schema/decimal_test.go`:

```go
func TestDecimal_IsZero_Sign(t *testing.T) {
	cases := []struct {
		in     string
		isZero bool
		sign   int
	}{
		{"0", true, 0},
		{"0.0", true, 0},
		{"-0", true, 0},
		{"1", false, 1},
		{"-1", false, -1},
		{"0.5", false, 1},
		{"-0.5", false, -1},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			d, err := ParseDecimal(c.in)
			if err != nil {
				t.Fatalf("ParseDecimal: %v", err)
			}
			if d.IsZero() != c.isZero {
				t.Errorf("IsZero: got %v, want %v", d.IsZero(), c.isZero)
			}
			if d.Sign() != c.sign {
				t.Errorf("Sign: got %d, want %d", d.Sign(), c.sign)
			}
		})
	}
}

func TestDecimal_Unscaled_DefensiveCopy(t *testing.T) {
	d, err := ParseDecimal("42")
	if err != nil {
		t.Fatalf("ParseDecimal: %v", err)
	}
	u := d.Unscaled()
	u.SetInt64(999)
	if d.unscaled.Int64() != 42 {
		t.Errorf("Unscaled() did not return a defensive copy; internal state mutated to %d", d.unscaled.Int64())
	}
}

func TestDecimal_Scale(t *testing.T) {
	d, _ := ParseDecimal("1.23")
	if d.Scale() != 2 {
		t.Errorf("Scale: got %d, want 2", d.Scale())
	}
	d, _ = ParseDecimal("1e2")
	if d.Scale() != -2 {
		t.Errorf("Scale: got %d, want -2", d.Scale())
	}
}
```

- [ ] **Step 2: Run — RED**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_IsZero_Sign|TestDecimal_Unscaled_DefensiveCopy|TestDecimal_Scale' -v
```
Expected: compile errors — `IsZero`, `Sign`, `Unscaled`, `Scale` undefined.

- [ ] **Step 3: Add accessors to `decimal.go`**

Append to `internal/domain/model/schema/decimal.go`:

```go
// IsZero reports whether d is numerically zero.
func (d Decimal) IsZero() bool {
	return d.unscaled != nil && d.unscaled.Sign() == 0
}

// Sign returns -1 for negative, 0 for zero, 1 for positive.
func (d Decimal) Sign() int {
	if d.unscaled == nil {
		return 0
	}
	return d.unscaled.Sign()
}

// Scale returns the scale: number of digits after the decimal point.
// Negative scale corresponds to scientific notation like 1e2.
func (d Decimal) Scale() int32 {
	return d.scale
}

// Unscaled returns a defensive copy of the unscaled big.Int.
func (d Decimal) Unscaled() *big.Int {
	if d.unscaled == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(d.unscaled)
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_IsZero_Sign|TestDecimal_Unscaled_DefensiveCopy|TestDecimal_Scale' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/decimal.go internal/domain/model/schema/decimal_test.go
git commit -m "feat(schema): Decimal accessors — IsZero, Sign, Scale, Unscaled

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `Decimal.StripTrailingZeros`

**Files:**
- Modify: `internal/domain/model/schema/decimal.go`
- Modify: `internal/domain/model/schema/decimal_test.go`

- [ ] **Step 1: Write failing tests**

Append:

```go
func TestDecimal_StripTrailingZeros(t *testing.T) {
	cases := []struct {
		in       string
		unscaled *big.Int
		scale    int32
	}{
		// Java BigDecimal("1.200").stripTrailingZeros() → unscaled=12, scale=1.
		{"1.200", bigInt("12"), 1},
		// "100" → unscaled=1, scale=-2.
		{"100", bigInt("1"), -2},
		// "0" and "0.0" → unscaled=0, scale=0 (Java treats zero's stripped scale as 0).
		{"0", bigInt("0"), 0},
		{"0.0", bigInt("0"), 0},
		// Unchanged when no trailing zeros.
		{"1.5", bigInt("15"), 1},
		{"1", bigInt("1"), 0},
		// Negative values.
		{"-1.200", bigInt("-12"), 1},
		// Multiple trailing zeros on integer.
		{"12000", bigInt("12"), -3},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			d, _ := ParseDecimal(c.in)
			stripped := d.StripTrailingZeros()
			if stripped.unscaled.Cmp(c.unscaled) != 0 {
				t.Errorf("unscaled: got %s, want %s", stripped.unscaled.String(), c.unscaled.String())
			}
			if stripped.scale != c.scale {
				t.Errorf("scale: got %d, want %d", stripped.scale, c.scale)
			}
		})
	}
}
```

- [ ] **Step 2: Run — RED**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_StripTrailingZeros' -v
```
Expected: `StripTrailingZeros undefined`.

- [ ] **Step 3: Implement**

Append to `decimal.go`:

```go
// StripTrailingZeros returns a Decimal with trailing zeros removed
// from the unscaled value. Matches Java BigDecimal.stripTrailingZeros
// semantics: a non-zero unscaled value with trailing zero digits has
// those digits removed and the scale decremented accordingly. A zero
// value collapses to unscaled=0, scale=0.
func (d Decimal) StripTrailingZeros() Decimal {
	if d.unscaled == nil || d.unscaled.Sign() == 0 {
		return Decimal{unscaled: new(big.Int), scale: 0}
	}
	u := new(big.Int).Set(d.unscaled)
	scale := d.scale
	ten := big.NewInt(10)
	zero := big.NewInt(0)
	q := new(big.Int)
	r := new(big.Int)
	for {
		q.QuoRem(u, ten, r)
		if r.Cmp(zero) != 0 {
			break
		}
		u.Set(q)
		scale--
	}
	return Decimal{unscaled: u, scale: scale}
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_StripTrailingZeros' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/decimal.go internal/domain/model/schema/decimal_test.go
git commit -m "feat(schema): Decimal.StripTrailingZeros — Java BigDecimal semantics

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `Decimal.Precision`

**Files:**
- Modify: `internal/domain/model/schema/decimal.go`
- Modify: `internal/domain/model/schema/decimal_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestDecimal_Precision(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		// Java BigDecimal.precision() returns 1 for zero.
		{"0", 1},
		{"0.0", 1},
		{"1", 1},
		{"10", 2},
		{"12345", 5},
		{"-12345", 5},
		{"0.1", 1},
		{"1.5", 2},
		{"123.456", 6},
		{"1e10", 1},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			d, _ := ParseDecimal(c.in)
			got := d.Precision()
			if got != c.want {
				t.Errorf("Precision(%q): got %d, want %d", c.in, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run — RED**

Expected: `Precision undefined`.

- [ ] **Step 3: Implement**

Append to `decimal.go`:

```go
// Precision returns the number of significant digits in the unscaled
// value. Matches Java BigDecimal.precision() — returns 1 for zero.
func (d Decimal) Precision() int {
	if d.unscaled == nil || d.unscaled.Sign() == 0 {
		return 1
	}
	// len of absolute-value decimal representation.
	abs := new(big.Int).Abs(d.unscaled)
	return len(abs.String())
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_Precision' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/decimal.go internal/domain/model/schema/decimal_test.go
git commit -m "feat(schema): Decimal.Precision — Java semantics (1 for zero)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: `Decimal.SetScale`

**Files:**
- Modify: `internal/domain/model/schema/decimal.go`
- Modify: `internal/domain/model/schema/decimal_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestDecimal_SetScale(t *testing.T) {
	t.Run("no_op_equal", func(t *testing.T) {
		d, _ := ParseDecimal("1.5")
		got, err := d.SetScale(1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.unscaled.Cmp(bigInt("15")) != 0 || got.scale != 1 {
			t.Errorf("got unscaled=%s scale=%d", got.unscaled, got.scale)
		}
	})
	t.Run("upward_scale_adds_zeros", func(t *testing.T) {
		d, _ := ParseDecimal("1.5")  // unscaled=15, scale=1
		got, err := d.SetScale(3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.unscaled.Cmp(bigInt("1500")) != 0 || got.scale != 3 {
			t.Errorf("got unscaled=%s scale=%d, want unscaled=1500 scale=3", got.unscaled, got.scale)
		}
	})
	t.Run("downward_scale_divisible", func(t *testing.T) {
		d, _ := ParseDecimal("1500")  // unscaled=1500, scale=0 (pre-strip)
		got, err := d.SetScale(-2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.unscaled.Cmp(bigInt("15")) != 0 || got.scale != -2 {
			t.Errorf("got unscaled=%s scale=%d", got.unscaled, got.scale)
		}
	})
	t.Run("downward_scale_lossy_errors", func(t *testing.T) {
		d, _ := ParseDecimal("1.5")
		_, err := d.SetScale(0)
		if err == nil {
			t.Fatal("expected error for lossy downward scale")
		}
	})
	t.Run("negative_scale_allowed", func(t *testing.T) {
		d, _ := ParseDecimal("100")
		got, err := d.SetScale(-3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 100 × 10^-(-3-0) = scale from 0 to -3 → multiply by 10^(-3 - 0) ... hmm, n=-3, scale=0: n<scale so need division by 10^(0-(-3))=10^3=1000; 100/1000 not exact → error.
		_ = got
	})
}
```

Note: the last subtest above is documentational — depending on interpretation, `(100, scale 0)` set to scale `-3` requires division by `10^3`. `100 / 1000` isn't exact; so the call fails. Confirm this and adjust the test expectation:

Replace the `negative_scale_allowed` subtest body with:

```go
	t.Run("negative_scale_lossy_errors", func(t *testing.T) {
		d, _ := ParseDecimal("100")
		_, err := d.SetScale(-3)
		if err == nil {
			t.Fatal("expected error for lossy negative scale")
		}
	})
	t.Run("negative_scale_exact", func(t *testing.T) {
		d, _ := ParseDecimal("1000")  // unscaled=1000, scale=0
		got, err := d.SetScale(-3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.unscaled.Cmp(bigInt("1")) != 0 || got.scale != -3 {
			t.Errorf("got unscaled=%s scale=%d, want unscaled=1 scale=-3", got.unscaled, got.scale)
		}
	})
```

- [ ] **Step 2: Run — RED**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_SetScale' -v
```
Expected: `SetScale undefined`.

- [ ] **Step 3: Implement**

Append to `decimal.go`:

```go
// SetScale returns a Decimal at the requested scale. Upward scale
// (adding fractional digits) multiplies the unscaled value by
// 10^(n-scale) and always succeeds. Downward scale (removing
// fractional digits) succeeds only if the unscaled value is divisible
// by 10^(scale-n); otherwise returns a precision-loss error.
func (d Decimal) SetScale(newScale int32) (Decimal, error) {
	if d.scale == newScale {
		return Decimal{unscaled: new(big.Int).Set(d.unscaled), scale: newScale}, nil
	}
	diff := int64(newScale) - int64(d.scale)
	if diff > 0 {
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(diff), nil)
		u := new(big.Int).Mul(d.unscaled, factor)
		return Decimal{unscaled: u, scale: newScale}, nil
	}
	// diff < 0: divide by 10^(-diff); require exactness.
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(-diff), nil)
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(d.unscaled, factor, r)
	if r.Sign() != 0 {
		return Decimal{}, fmt.Errorf("SetScale: cannot reduce scale from %d to %d without precision loss", d.scale, newScale)
	}
	return Decimal{unscaled: q, scale: newScale}, nil
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_SetScale' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/decimal.go internal/domain/model/schema/decimal_test.go
git commit -m "feat(schema): Decimal.SetScale — upward exact, downward fails on precision loss

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: `Decimal.IsInt128`

**Files:**
- Modify: `internal/domain/model/schema/decimal.go`
- Modify: `internal/domain/model/schema/decimal_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestDecimal_IsInt128(t *testing.T) {
	// Build pre-computed Int128 boundary strings.
	// 2^127 = 170141183460469231731687303715884105728
	// 2^127-1 = 170141183460469231731687303715884105727
	cases := []struct {
		label    string
		unscaled *big.Int
		want     bool
	}{
		{"2^127-1", new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1)), true},
		{"2^127", new(big.Int).Lsh(big.NewInt(1), 127), false},
		{"-2^127", new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127)), true},
		{"-2^127-1", new(big.Int).Sub(new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127)), big.NewInt(1)), false},
		{"0", big.NewInt(0), true},
		{"1", big.NewInt(1), true},
		{"-1", big.NewInt(-1), true},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			d := Decimal{unscaled: c.unscaled, scale: 0}
			got := d.IsInt128()
			if got != c.want {
				t.Errorf("IsInt128(%s): got %v, want %v", c.label, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run — RED**

Expected: `IsInt128 undefined`.

- [ ] **Step 3: Implement**

Append to `decimal.go` (above the rest of the method block is fine; group with other predicates):

```go
// int128Min = -2^127, int128Max = 2^127 - 1.
// Pre-computed once at package init.
var int128Min = new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))
var int128Max = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))

// IsInt128 reports whether the unscaled value fits the signed Int128
// range [-2^127, 2^127-1]. Scale is not considered.
//
// Implementation note: relies on pre-computed boundaries rather than
// big.Int.BitLen() comparisons, because BitLen ignores sign and
// BitLen(-2^127) == 128 — incorrectly excluding the valid minimum.
func (d Decimal) IsInt128() bool {
	if d.unscaled == nil {
		return true
	}
	return d.unscaled.Cmp(int128Min) >= 0 && d.unscaled.Cmp(int128Max) <= 0
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_IsInt128' -v
```
Expected: PASS. Critically the `-2^127` case passes — a naive BitLen-based impl would fail this.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/decimal.go internal/domain/model/schema/decimal_test.go
git commit -m "feat(schema): Decimal.IsInt128 — signed Int128 range check

Uses pre-computed big.Int boundaries so -2^127 is correctly included
(BitLen-based check would incorrectly exclude it).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: `Decimal.Cmp`

**Files:**
- Modify: `internal/domain/model/schema/decimal.go`
- Modify: `internal/domain/model/schema/decimal_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestDecimal_Cmp(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.5", "1.50", 0},
		{"1.5", "1.6", -1},
		{"1.6", "1.5", 1},
		{"0", "-0", 0},
		{"0.0", "-0.0", 0},
		{"1e10", "9e9", 1},
		{"1.5000000001", "1.5", 1},
		{"1.5", "1.5000000001", -1},
		{"-1", "1", -1},
		{"1000", "1e3", 0},
	}
	for _, c := range cases {
		t.Run(c.a+"_vs_"+c.b, func(t *testing.T) {
			a, _ := ParseDecimal(c.a)
			b, _ := ParseDecimal(c.b)
			got := a.Cmp(b)
			if got != c.want {
				t.Errorf("Cmp(%q, %q): got %d, want %d", c.a, c.b, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run — RED**

Expected: `Cmp undefined`.

- [ ] **Step 3: Implement**

Append to `decimal.go`:

```go
// Cmp returns -1 if d < other, 0 if equal, 1 if d > other. Exact
// comparison via scale alignment — no rounding modes.
func (d Decimal) Cmp(other Decimal) int {
	// Align to the larger scale by upward SetScale (always exact).
	target := d.scale
	if other.scale > target {
		target = other.scale
	}
	dAligned, err := d.SetScale(target)
	if err != nil {
		// Should never happen — upward SetScale always succeeds.
		panic(fmt.Sprintf("Decimal.Cmp: upward SetScale failed: %v", err))
	}
	oAligned, err := other.SetScale(target)
	if err != nil {
		panic(fmt.Sprintf("Decimal.Cmp: upward SetScale failed: %v", err))
	}
	return dAligned.unscaled.Cmp(oAligned.unscaled)
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_Cmp' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/decimal.go internal/domain/model/schema/decimal_test.go
git commit -m "feat(schema): Decimal.Cmp — exact comparison via scale alignment

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: `Decimal.Canonical` + JSON round-trip

**Files:**
- Modify: `internal/domain/model/schema/decimal.go`
- Modify: `internal/domain/model/schema/decimal_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestDecimal_Canonical_RoundTrip(t *testing.T) {
	inputs := []string{
		"0", "1", "-1", "123.456", "-123.456", "0.1", "1.5", "1000",
		"0.00000001", "123456789012345",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			d1, err := ParseDecimal(in)
			if err != nil {
				t.Fatalf("ParseDecimal: %v", err)
			}
			s := d1.Canonical()
			// No scientific notation.
			if strings.ContainsAny(s, "eE") {
				t.Errorf("Canonical contains scientific notation: %q", s)
			}
			d2, err := ParseDecimal(s)
			if err != nil {
				t.Fatalf("ParseDecimal(Canonical): %v", err)
			}
			if d1.Cmp(d2) != 0 {
				t.Errorf("round-trip value mismatch: %q → %q", in, s)
			}
		})
	}
}

func TestDecimal_JSON_RoundTrip(t *testing.T) {
	d, err := ParseDecimal("3.14159265358979323846")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, err := d.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var d2 Decimal
	if err := d2.UnmarshalJSON(raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Cmp(d2) != 0 {
		t.Errorf("JSON round-trip value mismatch: %s -> %s", d.Canonical(), d2.Canonical())
	}
}
```

Also add `import "strings"` to the test file if missing.

- [ ] **Step 2: Run — RED**

Expected: `Canonical`, `MarshalJSON`, `UnmarshalJSON` undefined.

- [ ] **Step 3: Implement**

Append to `decimal.go`:

```go
// Canonical returns a plain-decimal string representation (no
// scientific notation). Round-trippable through ParseDecimal.
func (d Decimal) Canonical() string {
	if d.unscaled == nil || d.unscaled.Sign() == 0 {
		return "0"
	}
	unscaledStr := d.unscaled.String() // includes leading "-" if negative
	neg := false
	digits := unscaledStr
	if unscaledStr[0] == '-' {
		neg = true
		digits = unscaledStr[1:]
	}
	var result string
	switch {
	case d.scale == 0:
		result = digits
	case d.scale > 0:
		// Insert decimal point (len(digits) - scale) from the left;
		// pad with leading zeros if needed.
		pad := int(d.scale) - len(digits)
		if pad >= 0 {
			result = "0." + strings.Repeat("0", pad) + digits
		} else {
			split := len(digits) - int(d.scale)
			result = digits[:split] + "." + digits[split:]
		}
	case d.scale < 0:
		// Append (|scale|) trailing zeros.
		result = digits + strings.Repeat("0", int(-d.scale))
	}
	if neg {
		result = "-" + result
	}
	return result
}

// MarshalJSON encodes the Decimal as a JSON number (not string) using
// Canonical form.
func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(d.Canonical()), nil
}

// UnmarshalJSON decodes a JSON number or string into the Decimal.
func (d *Decimal) UnmarshalJSON(data []byte) error {
	s := string(data)
	// Strip surrounding quotes if present (accept both number and string forms).
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	parsed, err := ParseDecimal(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal_Canonical|TestDecimal_JSON' -v
```
Expected: PASS.

- [ ] **Step 5: Full decimal test suite green**

```bash
go test ./internal/domain/model/schema/... -run 'TestDecimal|TestParseDecimal' -v
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/model/schema/decimal.go internal/domain/model/schema/decimal_test.go
git commit -m "feat(schema): Decimal.Canonical + JSON round-trip

Plain-decimal string form with no scientific notation. JSON marshal
emits a JSON number; unmarshal accepts number or string form.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Numeric constants + family/rank helpers

**Files:**
- Create: `internal/domain/model/schema/numeric.go`
- Create: `internal/domain/model/schema/numeric_test.go`

Note: this task creates `numeric.go`. The existing `NumericFamily` and `NumericRank` remain in `types.go` for now; they get updated in Task 13 when BYTE/SHORT/FLOAT are dropped. This task defines cyoda-go's numeric envelope constants and declares `IsNumeric`.

- [ ] **Step 1: Write failing tests**

Create `internal/domain/model/schema/numeric_test.go`:

```go
package schema

import "testing"

func TestIsNumeric(t *testing.T) {
	numeric := []DataType{Integer, Long, BigInteger, UnboundInteger, Double, BigDecimal, UnboundDecimal}
	// Note: BYTE, SHORT, FLOAT are dropped in a later task; tests here
	// cover the post-drop enum. For now we just test the permanently-
	// non-numeric ones are non-numeric and the permanently-numeric ones
	// are numeric.
	for _, dt := range numeric {
		if !IsNumeric(dt) {
			t.Errorf("IsNumeric(%s) = false, want true", dt)
		}
	}
	nonNumeric := []DataType{String, Boolean, Null, ByteArray, UUIDType}
	for _, dt := range nonNumeric {
		if IsNumeric(dt) {
			t.Errorf("IsNumeric(%s) = true, want false", dt)
		}
	}
}
```

- [ ] **Step 2: Run — RED**

```bash
go test ./internal/domain/model/schema/... -run 'TestIsNumeric' -v
```
Expected: `IsNumeric undefined`.

- [ ] **Step 3: Create `numeric.go`**

Create `internal/domain/model/schema/numeric.go`:

```go
package schema

// Numeric envelopes from Cyoda Cloud's ParserFunctions.kt:33-59.
// The "exp" constants refer to precision - scale — the decimal
// "characteristic," the magnitude of the unscaled-int-at-scale-18
// representation. This matches Trino's fixed-scale-18 BIG_DECIMAL
// storage format where the unscaled value must fit Int128.
const (
	doubleMaxPrecision     = 15
	doubleMaxAbsScale      = 292
	bigDecimalMaxScale     = 18
	bigDecimalDefinitePrec = 38 // precision ≤ 38 AND exp ≤ 20 → definite fit
	bigDecimalDefiniteExp  = 20 // exp = precision - scale
	bigDecimalLoosePrec    = 39 // precision ≤ 39 AND exp ≤ 21 → verify via SetScale(18).Unscaled().IsInt128()
	bigDecimalLooseExp     = 21
)

// IsNumeric reports whether dt is in either numeric family.
func IsNumeric(dt DataType) bool {
	return NumericFamily(dt) != 0
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestIsNumeric' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/numeric.go internal/domain/model/schema/numeric_test.go
git commit -m "feat(schema): numeric.go — envelope constants + IsNumeric

Per spec §4.2 — constants ported from Cyoda Cloud's
ParserFunctions.kt:33-59. BYTE/SHORT/FLOAT drop happens in a later
task; this file is additive and only uses the permanently-present
enum values.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: `ClassifyInteger`

**Files:**
- Modify: `internal/domain/model/schema/numeric.go`
- Modify: `internal/domain/model/schema/numeric_test.go`

- [ ] **Step 1: Write failing tests**

Append to `numeric_test.go`:

```go
func TestClassifyInteger(t *testing.T) {
	// Boundaries referenced by the spec §6.2.
	mkInt := func(s string) *big.Int {
		v, _ := new(big.Int).SetString(s, 10)
		return v
	}
	cases := []struct {
		label string
		v     *big.Int
		want  DataType
	}{
		{"0", big.NewInt(0), Integer},
		{"-1", big.NewInt(-1), Integer},
		{"127", big.NewInt(127), Integer},
		{"128", big.NewInt(128), Integer},
		{"-128", big.NewInt(-128), Integer},
		{"-129", big.NewInt(-129), Integer},
		{"32767", big.NewInt(32767), Integer},
		{"32768", big.NewInt(32768), Integer},
		{"2^31-1", big.NewInt(1<<31 - 1), Integer},
		{"2^31", big.NewInt(1 << 31), Long},
		{"2^63-1", mkInt("9223372036854775807"), Long},
		{"2^63", mkInt("9223372036854775808"), BigInteger},
		{"2^127-1", new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1)), BigInteger},
		{"2^127", new(big.Int).Lsh(big.NewInt(1), 127), UnboundInteger},
		{"10^30", mkInt("1000000000000000000000000000000"), UnboundInteger},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			got := ClassifyInteger(c.v)
			if got != c.want {
				t.Errorf("ClassifyInteger(%s): got %s, want %s", c.label, got, c.want)
			}
		})
	}
}
```

Add `import "math/big"` if not already present.

- [ ] **Step 2: Run — RED**

Expected: `ClassifyInteger undefined`.

- [ ] **Step 3: Implement**

Append to `numeric.go`:

```go
import "math/big"

// Int32 and Int64 boundary big.Ints for ClassifyInteger.
var (
	int32Min64 = big.NewInt(-1 << 31)
	int32Max64 = big.NewInt(1<<31 - 1)
	int64Min64 = new(big.Int).SetInt64(-1 << 63)
	int64Max64 = new(big.Int).SetUint64(1<<63 - 1)
)

// ClassifyInteger classifies a whole-number value into INTEGER, LONG,
// BIG_INTEGER, or UNBOUND_INTEGER by magnitude. Matches the Cyoda Cloud
// integer-family logic at ParserFunctions.kt:133-155, minus BYTE/SHORT
// (dropped in cyoda-go — spec §2.3).
//
//   - [-2^31, 2^31 - 1]            → INTEGER
//   - [-2^63, 2^63 - 1] outside    → LONG
//   - [-2^127, 2^127 - 1] outside  → BIG_INTEGER (fits signed Int128)
//   - beyond                       → UNBOUND_INTEGER
func ClassifyInteger(v *big.Int) DataType {
	if v.Cmp(int32Min64) >= 0 && v.Cmp(int32Max64) <= 0 {
		return Integer
	}
	if v.Cmp(int64Min64) >= 0 && v.Cmp(int64Max64) <= 0 {
		return Long
	}
	if v.Cmp(int128Min) >= 0 && v.Cmp(int128Max) <= 0 {
		return BigInteger
	}
	return UnboundInteger
}
```

Note: the existing `NumericFamily`/`NumericRank` in `types.go` currently return values for BYTE/SHORT/FLOAT which are still in the enum. `ClassifyInteger`'s return values (`Integer`, `Long`, `BigInteger`, `UnboundInteger`) are unaffected by the enum drop in Task 13.

The import of `math/big` at the top of `numeric.go` is required. If the existing file doesn't import it, add the import block:

```go
package schema

import "math/big"

// ... (existing constants and IsNumeric)
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestClassifyInteger' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/numeric.go internal/domain/model/schema/numeric_test.go
git commit -m "feat(schema): ClassifyInteger — magnitude-based integer family classification

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: `ClassifyDecimal` — envelopes + two-tier BIG_DECIMAL fit

**Files:**
- Modify: `internal/domain/model/schema/numeric.go`
- Modify: `internal/domain/model/schema/numeric_test.go`

- [ ] **Step 1: Write failing tests**

Append to `numeric_test.go`:

```go
func TestClassifyDecimal(t *testing.T) {
	mkD := func(s string) Decimal {
		d, err := ParseDecimal(s)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", s, err)
		}
		return d.StripTrailingZeros()
	}

	// Helper: build a Decimal with a specific (unscaled, scale).
	mkRaw := func(unscaledDec string, scale int32) Decimal {
		u, _ := new(big.Int).SetString(unscaledDec, 10)
		return Decimal{unscaled: u, scale: scale}
	}

	t.Run("DOUBLE_samples", func(t *testing.T) {
		cases := []struct {
			in   string
			want DataType
		}{
			{"0.1", Double},
			{"1.5", Double},
			{"0.123456789012345", Double},   // precision 15, boundary
		}
		for _, c := range cases {
			t.Run(c.in, func(t *testing.T) {
				got := ClassifyDecimal(mkD(c.in))
				if got != c.want {
					t.Errorf("ClassifyDecimal(%q): got %s, want %s", c.in, got, c.want)
				}
			})
		}
	})

	t.Run("BIG_DECIMAL_precision_boundary", func(t *testing.T) {
		// precision=16, scale=16 — exceeds DOUBLE precision limit.
		got := ClassifyDecimal(mkD("0.1234567890123456"))
		if got != BigDecimal {
			t.Errorf("16-digit fractional: got %s, want BIG_DECIMAL", got)
		}
	})

	t.Run("BIG_DECIMAL_pi_18_fractional", func(t *testing.T) {
		got := ClassifyDecimal(mkD("3.141592653589793238"))
		if got != BigDecimal {
			t.Errorf("pi-18: got %s, want BIG_DECIMAL", got)
		}
	})

	t.Run("UNBOUND_DECIMAL_pi_20_fractional", func(t *testing.T) {
		got := ClassifyDecimal(mkD("3.14159265358979323846"))
		if got != UnboundDecimal {
			t.Errorf("pi-20: got %s, want UNBOUND_DECIMAL (scale=20 > 18)", got)
		}
	})

	t.Run("UNBOUND_DECIMAL_underflow", func(t *testing.T) {
		got := ClassifyDecimal(mkD("1e-400"))
		if got != UnboundDecimal {
			t.Errorf("1e-400: got %s, want UNBOUND_DECIMAL", got)
		}
	})

	t.Run("UNBOUND_DECIMAL_overflow", func(t *testing.T) {
		got := ClassifyDecimal(mkD("1e400"))
		if got != UnboundDecimal {
			t.Errorf("1e400: got %s, want UNBOUND_DECIMAL", got)
		}
	})

	t.Run("BIG_DECIMAL_definite_boundary", func(t *testing.T) {
		// unscaled = 10^37 (38 digits), scale=18 → precision=38, exp=20, scale=18.
		tenToThe37 := new(big.Int).Exp(big.NewInt(10), big.NewInt(37), nil)
		d := Decimal{unscaled: tenToThe37, scale: 18}
		got := ClassifyDecimal(d)
		if got != BigDecimal {
			t.Errorf("definite boundary: got %s, want BIG_DECIMAL", got)
		}
	})

	t.Run("BIG_DECIMAL_loose_passes_int128", func(t *testing.T) {
		// unscaled = 2^127 - 1 (39 digits), scale=18 → precision=39, exp=21, IsInt128 true.
		u := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
		d := Decimal{unscaled: u, scale: 18}
		got := ClassifyDecimal(d)
		if got != BigDecimal {
			t.Errorf("loose passes: got %s, want BIG_DECIMAL", got)
		}
	})

	t.Run("UNBOUND_DECIMAL_loose_fails_int128", func(t *testing.T) {
		// unscaled = 2^127 (39 digits), scale=18 → IsInt128 false.
		u := new(big.Int).Lsh(big.NewInt(1), 127)
		d := Decimal{unscaled: u, scale: 18}
		got := ClassifyDecimal(d)
		if got != UnboundDecimal {
			t.Errorf("loose fails: got %s, want UNBOUND_DECIMAL", got)
		}
	})

	t.Run("UNBOUND_DECIMAL_40_digits", func(t *testing.T) {
		// unscaled = 10^39 (40 digits), scale=18 → precision > 39.
		u := new(big.Int).Exp(big.NewInt(10), big.NewInt(39), nil)
		d := Decimal{unscaled: u, scale: 18}
		got := ClassifyDecimal(d)
		if got != UnboundDecimal {
			t.Errorf("40 digits: got %s, want UNBOUND_DECIMAL", got)
		}
	})

	t.Run("UNBOUND_DECIMAL_exp_too_big", func(t *testing.T) {
		// unscaled=1, scale=-22 → exp=23 (>21). Both definite and loose fail on exp.
		got := ClassifyDecimal(mkRaw("1", -22))
		if got != UnboundDecimal {
			t.Errorf("exp=23: got %s, want UNBOUND_DECIMAL", got)
		}
	})

	t.Run("UNBOUND_DECIMAL_scale_too_big", func(t *testing.T) {
		// unscaled = 10^37 (38 digits), scale=19 → precision=38, scale > 18.
		u := new(big.Int).Exp(big.NewInt(10), big.NewInt(37), nil)
		got := ClassifyDecimal(Decimal{unscaled: u, scale: 19})
		if got != UnboundDecimal {
			t.Errorf("scale=19: got %s, want UNBOUND_DECIMAL", got)
		}
	})
}
```

- [ ] **Step 2: Run — RED**

Expected: `ClassifyDecimal undefined`.

- [ ] **Step 3: Implement**

Append to `numeric.go`:

```go
// ClassifyDecimal classifies a non-whole-number decimal value into
// DOUBLE, BIG_DECIMAL, or UNBOUND_DECIMAL. Input MUST be the result of
// StripTrailingZeros. Per spec §4.2:
//
//   - DOUBLE if precision ≤ 15 AND |scale| ≤ 292.
//   - BIG_DECIMAL definite if precision ≤ 38 AND (precision - scale) ≤ 20
//     AND scale ≤ 18.
//   - BIG_DECIMAL loose if precision ≤ 39 AND (precision - scale) ≤ 21
//     AND scale ≤ 18 AND SetScale(18).Unscaled().IsInt128().
//   - Otherwise UNBOUND_DECIMAL.
func ClassifyDecimal(d Decimal) DataType {
	precision := d.Precision()
	scale := int(d.scale)
	absScale := scale
	if absScale < 0 {
		absScale = -absScale
	}
	// DOUBLE envelope.
	if precision <= doubleMaxPrecision && absScale <= doubleMaxAbsScale {
		return Double
	}
	// BIG_DECIMAL definite fit.
	if scale <= bigDecimalMaxScale &&
		precision <= bigDecimalDefinitePrec &&
		(precision-scale) <= bigDecimalDefiniteExp {
		return BigDecimal
	}
	// BIG_DECIMAL loose fit.
	if scale <= bigDecimalMaxScale &&
		precision <= bigDecimalLoosePrec &&
		(precision-scale) <= bigDecimalLooseExp {
		// Verify the unscaled-at-scale-18 representation fits Int128.
		aligned, err := d.SetScale(18)
		if err == nil && aligned.IsInt128() {
			return BigDecimal
		}
	}
	return UnboundDecimal
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestClassifyDecimal' -v
```
Expected: PASS. Each boundary case verifies a distinct envelope transition.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/numeric.go internal/domain/model/schema/numeric_test.go
git commit -m "feat(schema): ClassifyDecimal — precision/scale envelopes + two-tier BIG_DECIMAL

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: `IsAssignableTo` — widening lattice

**Files:**
- Modify: `internal/domain/model/schema/numeric.go`
- Modify: `internal/domain/model/schema/numeric_test.go`

- [ ] **Step 1: Write failing tests**

Append to `numeric_test.go`:

```go
func TestIsAssignableTo(t *testing.T) {
	// Self-assignment: every non-null type assigns to itself.
	for _, dt := range []DataType{Integer, Long, BigInteger, UnboundInteger, Double, BigDecimal, UnboundDecimal, String, Boolean} {
		if !IsAssignableTo(dt, dt) {
			t.Errorf("IsAssignableTo(%s, %s) = false, want true", dt, dt)
		}
	}
	// NULL assigns to anything.
	for _, dt := range []DataType{Integer, Long, Double, BigDecimal, String, Boolean, Null} {
		if !IsAssignableTo(Null, dt) {
			t.Errorf("NULL → %s: got false, want true", dt)
		}
	}
	// Integer family widening.
	allow := map[[2]DataType]bool{
		{Integer, Long}:             true,
		{Integer, BigInteger}:       true,
		{Integer, UnboundInteger}:   true,
		{Integer, Double}:           true,  // 2^31 fits Double mantissa
		{Integer, BigDecimal}:       true,
		{Integer, UnboundDecimal}:   true,
		{Long, BigInteger}:          true,
		{Long, UnboundInteger}:      true,
		{Long, BigDecimal}:          true,
		{Long, UnboundDecimal}:      true,
		{Long, Double}:              false, // precision — 2^63 exceeds Double mantissa
		{BigInteger, UnboundInteger}: true,
		{BigInteger, UnboundDecimal}: true,
		{UnboundInteger, UnboundDecimal}: true,
		// Decimal family.
		{Double, UnboundDecimal}:   true,
		{Double, BigDecimal}:       false,  // envelopes differ; Cyoda Cloud lattice disallows
		{BigDecimal, UnboundDecimal}: true,
		// Cross-direction: decimal does not assign to integer.
		{Double, Integer}:          false,
		{BigDecimal, Long}:         false,
		// Non-numeric.
		{String, Integer}:          false,
		{Integer, String}:          false,
	}
	for pair, want := range allow {
		t.Run(pair[0].String()+"→"+pair[1].String(), func(t *testing.T) {
			got := IsAssignableTo(pair[0], pair[1])
			if got != want {
				t.Errorf("IsAssignableTo(%s, %s): got %v, want %v", pair[0], pair[1], got, want)
			}
		})
	}
}
```

- [ ] **Step 2: Run — RED**

Expected: `IsAssignableTo undefined`.

- [ ] **Step 3: Implement**

Append to `numeric.go`:

```go
// wideningLattice is the reachability map ported from
// DataType.wideningConversionMap (Cyoda Cloud DataType.kt:239-272),
// minus the dropped types (BYTE, SHORT, FLOAT). Key: source DataType.
// Value: set of DataTypes the source can widen to (not including
// itself).
var wideningLattice = map[DataType]map[DataType]bool{
	Integer: {
		Long: true, Double: true, BigInteger: true, BigDecimal: true,
		UnboundInteger: true, UnboundDecimal: true,
	},
	Long: {
		// Note: LONG → DOUBLE is NOT allowed per DataType.kt:253-268
		// (2^63 exceeds Double's 53-bit mantissa).
		BigInteger: true, BigDecimal: true,
		UnboundInteger: true, UnboundDecimal: true,
	},
	BigInteger: {
		UnboundInteger: true, UnboundDecimal: true,
	},
	UnboundInteger: {
		UnboundDecimal: true,
	},
	Double: {
		UnboundDecimal: true,
	},
	BigDecimal: {
		UnboundDecimal: true,
	},
	// UnboundDecimal: no outgoing edges.
}

// IsAssignableTo reports whether a value classified as dataT can
// losslessly assign into schemaT per the widening lattice. NULL assigns
// to any type (absence is universally acceptable).
func IsAssignableTo(dataT, schemaT DataType) bool {
	if dataT == schemaT {
		return true
	}
	if dataT == Null {
		return true
	}
	reachable, ok := wideningLattice[dataT]
	if !ok {
		return false
	}
	return reachable[schemaT]
}
```

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestIsAssignableTo' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/numeric.go internal/domain/model/schema/numeric_test.go
git commit -m "feat(schema): IsAssignableTo — widening lattice with LONG→DOUBLE correctly blocked

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 13: Drop `Byte`/`Short`/`Float` from `DataType` enum + update callers

This is the single biggest coordination task. It touches the enum, the `dataTypeNames` map, `NumericFamily`, `NumericRank`, and every non-test caller in other packages. Tests that reference `schema.Byte`/`schema.Short`/`schema.Float` are updated inline here.

**Files:**
- Modify: `internal/domain/model/schema/types.go`
- Modify: `internal/domain/model/schema/types_test.go`
- Modify: `internal/domain/model/importer/walker.go`
- Modify: `internal/domain/model/importer/walker_test.go`
- Modify: `internal/domain/model/exporter/json_schema.go`

- [ ] **Step 1: Remove `Byte`, `Short`, `Float` from the `DataType` enum in `types.go`**

Edit `internal/domain/model/schema/types.go`:

```go
const (
	// Numeric types

	// Integer is a 32-bit signed integer.
	Integer DataType = iota
	// Long is a 64-bit signed integer.
	Long
	// BigInteger is an arbitrary-precision integer fitting Int128.
	BigInteger
	// UnboundInteger is an integer of arbitrary magnitude.
	UnboundInteger
	// Double is a decimal within the precision-15, scale-292 envelope.
	Double
	// BigDecimal is a decimal fitting Trino's fixed-scale Int128 encoding.
	BigDecimal
	// UnboundDecimal is a decimal of arbitrary precision and scale.
	UnboundDecimal

	// Text types

	// String is a variable-length character sequence.
	String
	// Character is a single Unicode character.
	Character

	// Temporal types

	// LocalDate is a date without time-zone (yyyy-MM-dd).
	LocalDate
	// LocalDateTime is a date-time without time-zone.
	LocalDateTime
	// LocalTime is a time without time-zone.
	LocalTime
	// ZonedDateTime is a date-time with a time-zone.
	ZonedDateTime
	// Year is a year value (e.g. 2026).
	Year
	// YearMonth is a year-month value (e.g. 2026-03).
	YearMonth

	// Identifier types

	// UUIDType is a universally unique identifier.
	UUIDType
	// TimeUUIDType is a time-based UUID.
	TimeUUIDType

	// Binary

	// ByteArray is a variable-length byte sequence.
	ByteArray

	// Other

	// Boolean is a true/false value.
	Boolean
	// Null represents the absence of a value.
	Null
)
```

Update `dataTypeNames`:

```go
var dataTypeNames = map[DataType]string{
	Integer: "INTEGER", Long: "LONG",
	BigInteger: "BIG_INTEGER", UnboundInteger: "UNBOUND_INTEGER",
	Double: "DOUBLE", BigDecimal: "BIG_DECIMAL", UnboundDecimal: "UNBOUND_DECIMAL",
	String: "STRING", Character: "CHARACTER",
	LocalDate: "LOCAL_DATE", LocalDateTime: "LOCAL_DATE_TIME",
	LocalTime: "LOCAL_TIME", ZonedDateTime: "ZONED_DATE_TIME",
	Year: "YEAR", YearMonth: "YEAR_MONTH",
	UUIDType: "UUID_TYPE", TimeUUIDType: "TIME_UUID_TYPE",
	ByteArray: "BYTE_ARRAY", Boolean: "BOOLEAN", Null: "NULL",
}
```

Update `NumericFamily`:

```go
func NumericFamily(dt DataType) int {
	switch dt {
	case Integer, Long, BigInteger, UnboundInteger:
		return 1
	case Double, BigDecimal, UnboundDecimal:
		return 2
	default:
		return 0
	}
}
```

Update `NumericRank`:

```go
func NumericRank(dt DataType) int {
	switch dt {
	case Integer:
		return 0
	case Long:
		return 1
	case BigInteger:
		return 2
	case UnboundInteger:
		return 3
	case Double:
		return 0
	case BigDecimal:
		return 1
	case UnboundDecimal:
		return 2
	default:
		return -1
	}
}
```

- [ ] **Step 2: Update `internal/domain/model/exporter/json_schema.go`**

Find and edit the switch at `json_schema.go:95-99`. Replace:

```go
case schema.Byte, schema.Short, schema.Integer, schema.Long,
```

with:

```go
case schema.Integer, schema.Long,
```

Replace:

```go
case schema.Float, schema.Double, schema.BigDecimal, schema.UnboundDecimal:
```

with:

```go
case schema.Double, schema.BigDecimal, schema.UnboundDecimal:
```

- [ ] **Step 3: Update `internal/domain/model/importer/walker.go`**

This file's internals get a full rewrite in Task 15. For Step 3 right now, just remove references to `schema.Byte`, `schema.Short`, `schema.Float`, and the `IntScope`/`DecimalScope` config fields so the package builds against the trimmed enum. The intermediate state is broken behaviorally but compiles; Task 15 fixes the behavior.

Replace the contents of `internal/domain/model/importer/walker.go` with a transitional stub that still uses `json.Number` but always classifies to `Integer` or `Double`:

```go
package importer

import (
	"encoding/json"
	"fmt"
	"strings"
	"strconv"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// WalkConfig is retained for backward compatibility but no longer
// carries scope fields. Will be removed entirely in a future refactor.
type WalkConfig struct{}

// DefaultWalkConfig returns an empty WalkConfig.
func DefaultWalkConfig() WalkConfig { return WalkConfig{} }

// Walk converts a generic parsed data tree into a ModelNode schema tree.
func Walk(data any) (*schema.ModelNode, error) {
	return WalkWithConfig(data, DefaultWalkConfig())
}

// WalkWithConfig applies the default walk.
func WalkWithConfig(data any, cfg WalkConfig) (*schema.ModelNode, error) {
	w := &walker{cfg: cfg}
	return w.walkValue(data)
}

type walker struct {
	cfg WalkConfig
}

func (w *walker) walkValue(v any) (*schema.ModelNode, error) {
	switch val := v.(type) {
	case map[string]any:
		return w.walkObject(val)
	case []any:
		return w.walkArray(val)
	case string:
		return schema.NewLeafNode(schema.String), nil
	case json.Number:
		// Transitional: integer-looking literals → Integer, fractional → Double.
		// Task 15 replaces this with ClassifyInteger / ClassifyDecimal.
		s := val.String()
		if strings.ContainsAny(s, ".eE") {
			return schema.NewLeafNode(schema.Double), nil
		}
		// Any integer magnitude — Task 15 refines this to Long/BigInteger/UnboundInteger as appropriate.
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			// Out of int64 range — transitional coarse bucket.
			return schema.NewLeafNode(schema.BigInteger), nil
		}
		return schema.NewLeafNode(schema.Integer), nil
	case float64:
		return schema.NewLeafNode(schema.Double), nil
	case bool:
		return schema.NewLeafNode(schema.Boolean), nil
	case nil:
		return schema.NewLeafNode(schema.Null), nil
	default:
		return nil, fmt.Errorf("unsupported type: %T", v)
	}
}

func (w *walker) walkObject(m map[string]any) (*schema.ModelNode, error) {
	node := schema.NewObjectNode()
	for k, v := range m {
		child, err := w.walkValue(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		node.SetChild(k, child)
	}
	return node, nil
}

func (w *walker) walkArray(arr []any) (*schema.ModelNode, error) {
	if len(arr) == 0 {
		return schema.NewArrayNode(schema.NewLeafNode(schema.Null)), nil
	}
	var element *schema.ModelNode
	for _, item := range arr {
		child, err := w.walkValue(item)
		if err != nil {
			return nil, err
		}
		if element == nil {
			element = child
			continue
		}
		element = schema.Merge(element, child)
	}
	node := schema.NewArrayNode(element)
	node.Info().Observe(len(arr))
	return node, nil
}
```

The goal of this step is ONLY to remove dependencies on the dropped enum values so the package compiles. Behavior is intentionally degraded until Task 15.

- [ ] **Step 4: Update `internal/domain/model/importer/walker_test.go`**

Delete or modify any test case that references `schema.Byte`, `schema.Short`, `schema.Float`, or `WalkConfig.IntScope`/`DecimalScope`. Inline expected types.

Find tests that expect `schema.Byte` for small integers — update to `schema.Integer`. Find tests expecting `schema.Short` — same. Find tests expecting `schema.Float` — update to `schema.Double`.

Specifically at `walker_test.go:154`:

```go
cfg := importer.WalkConfig{IntScope: schema.Byte, DecimalScope: schema.Float}
```

→ Delete this test, OR rewrite it (if it has independent value beyond scope config) to use `importer.DefaultWalkConfig()`.

Audit lines 160-165 similarly:

```go
{"127 → Byte", 127, schema.Byte},
{"128 → Short", 128, schema.Short},
```

→ Replace all with `schema.Integer`. Task 15 adds more specific tests for the new value-based classifier.

- [ ] **Step 5: Update `internal/domain/model/schema/types_test.go`**

Find lines referencing `schema.Byte`, `schema.Short`, `schema.Float`:

```go
// types_test.go:131
ts.Add(schema.Byte)
// types_test.go:144
ts.Add(schema.Float)
// types_test.go:167
a.Add(schema.Short)
```

Replace each with a currently-valid numeric type that preserves the test's intent (integer-family latching, decimal-family latching). Use `schema.Integer` for the first and third; `schema.Double` for the second. Adjust any expected assertions.

- [ ] **Step 6: Build check**

```bash
go build ./... 2>&1 | head -30
```
Expected: clean build. Any remaining reference to the dropped types surfaces here.

- [ ] **Step 7: Run existing schema + importer tests**

```bash
go test ./internal/domain/model/schema/... ./internal/domain/model/importer/... ./internal/domain/model/exporter/... -short -v 2>&1 | tail -30
```
Expected: all pass. Walker's new behavior is coarse but consistent (integers classify as Integer/BigInteger, fractionals as Double); tests need to reflect this coarseness.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/model/schema/types.go internal/domain/model/schema/types_test.go \
        internal/domain/model/importer/walker.go internal/domain/model/importer/walker_test.go \
        internal/domain/model/exporter/json_schema.go
git commit -m "$(cat <<'EOF'
refactor(schema): drop Byte/Short/Float from DataType enum

Per spec §2.3: unreachable under Cyoda Cloud's default scope config,
no cyoda-go caller configures narrower scopes. Removes dead enum
values, simplifies NumericFamily/NumericRank, updates json_schema
exporter and walker.

Walker behavior is transitional after this commit — integer literals
classify as Integer or BigInteger (no magnitude discrimination),
fractional literals always classify as Double. Task 15 replaces this
with the full ClassifyInteger / ClassifyDecimal path.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: `CollapseNumeric` same-family

**Files:**
- Modify: `internal/domain/model/schema/numeric.go`
- Modify: `internal/domain/model/schema/numeric_test.go`

- [ ] **Step 1: Write failing tests**

Append to `numeric_test.go`:

```go
func TestCollapseNumeric_SameFamily(t *testing.T) {
	cases := []struct {
		label string
		in    []DataType
		want  DataType
	}{
		{"integer_alone", []DataType{Integer}, Integer},
		{"integer_long", []DataType{Integer, Long}, Long},
		{"long_biginteger", []DataType{Long, BigInteger}, BigInteger},
		{"biginteger_unboundinteger", []DataType{BigInteger, UnboundInteger}, UnboundInteger},
		{"double_bigdecimal", []DataType{Double, BigDecimal}, BigDecimal},
		{"double_unbounddecimal", []DataType{Double, UnboundDecimal}, UnboundDecimal},
		{"bigdecimal_unbounddecimal", []DataType{BigDecimal, UnboundDecimal}, UnboundDecimal},
		{"all_integer_family", []DataType{Integer, Long, BigInteger, UnboundInteger}, UnboundInteger},
		{"all_decimal_family", []DataType{Double, BigDecimal, UnboundDecimal}, UnboundDecimal},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			got := CollapseNumeric(c.in)
			if got != c.want {
				t.Errorf("CollapseNumeric(%v): got %s, want %s", c.in, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run — RED**

Expected: `CollapseNumeric undefined`.

- [ ] **Step 3: Implement same-family branch**

Append to `numeric.go`:

```go
// CollapseNumeric reduces a numeric-only set to a single DataType per
// spec §5. Preconditions: input is non-empty; every element satisfies
// IsNumeric.
func CollapseNumeric(types []DataType) DataType {
	if len(types) == 0 {
		panic("CollapseNumeric: empty input")
	}
	// Same-family case: collapse by highest rank within the family.
	fam := NumericFamily(types[0])
	sameFamily := true
	for _, dt := range types[1:] {
		if NumericFamily(dt) != fam {
			sameFamily = false
			break
		}
	}
	if sameFamily {
		winner := types[0]
		for _, dt := range types[1:] {
			if NumericRank(dt) > NumericRank(winner) {
				winner = dt
			}
		}
		return winner
	}
	// Cross-family case: Task 15 implements this.
	panic("CollapseNumeric: cross-family case not yet implemented")
}
```

- [ ] **Step 4: Run — GREEN for same-family**

```bash
go test ./internal/domain/model/schema/... -run 'TestCollapseNumeric_SameFamily' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/numeric.go internal/domain/model/schema/numeric_test.go
git commit -m "feat(schema): CollapseNumeric — same-family case (rank-based)

Cross-family case panics intentionally; Task 15 adds it.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 15: `CollapseNumeric` cross-family + walker real classifier

**Files:**
- Modify: `internal/domain/model/schema/numeric.go`
- Modify: `internal/domain/model/schema/numeric_test.go`
- Modify: `internal/domain/model/importer/walker.go`
- Modify: `internal/domain/model/importer/walker_test.go`

- [ ] **Step 1: Write failing cross-family tests**

Append to `numeric_test.go`:

```go
func TestCollapseNumeric_CrossFamily(t *testing.T) {
	cases := []struct {
		label string
		in    []DataType
		want  DataType
	}{
		{"integer_double", []DataType{Integer, Double}, BigDecimal},
		{"long_double", []DataType{Long, Double}, BigDecimal},
		{"integer_bigdecimal", []DataType{Integer, BigDecimal}, BigDecimal},
		{"biginteger_double", []DataType{BigInteger, Double}, BigDecimal},
		{"biginteger_bigdecimal", []DataType{BigInteger, BigDecimal}, BigDecimal},
		{"biginteger_unbounddecimal", []DataType{BigInteger, UnboundDecimal}, UnboundDecimal},
		{"unboundinteger_double", []DataType{UnboundInteger, Double}, UnboundDecimal},
		{"unboundinteger_bigdecimal", []DataType{UnboundInteger, BigDecimal}, UnboundDecimal},
		{"unboundinteger_unbounddecimal", []DataType{UnboundInteger, UnboundDecimal}, UnboundDecimal},
		{"long_unbounddecimal", []DataType{Long, UnboundDecimal}, UnboundDecimal},
		{"three_way", []DataType{Integer, Long, Double}, BigDecimal},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			got := CollapseNumeric(c.in)
			if got != c.want {
				t.Errorf("CollapseNumeric(%v): got %s, want %s", c.in, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run — RED**

Expected: panic message "cross-family case not yet implemented".

- [ ] **Step 3: Implement cross-family branch**

Replace the panic in `CollapseNumeric` with the cross-family logic. Final `CollapseNumeric`:

```go
func CollapseNumeric(types []DataType) DataType {
	if len(types) == 0 {
		panic("CollapseNumeric: empty input")
	}
	// Bucket by family, track widest in each.
	var widestInt DataType = -1
	var widestDec DataType = -1
	for _, dt := range types {
		switch NumericFamily(dt) {
		case 1:
			if widestInt == DataType(-1) || NumericRank(dt) > NumericRank(widestInt) {
				widestInt = dt
			}
		case 2:
			if widestDec == DataType(-1) || NumericRank(dt) > NumericRank(widestDec) {
				widestDec = dt
			}
		default:
			panic(fmt.Sprintf("CollapseNumeric: non-numeric input %s", dt))
		}
	}
	// Same-family fast paths.
	if widestInt == DataType(-1) {
		return widestDec
	}
	if widestDec == DataType(-1) {
		return widestInt
	}
	// Cross-family: promote to the narrowest decimal that losslessly
	// contains every integer member, per spec §5.2.
	if widestInt == UnboundInteger {
		return UnboundDecimal
	}
	if widestDec == UnboundDecimal {
		return UnboundDecimal
	}
	// BigInteger, Long, Integer → BigDecimal (Trino Int128 fits these).
	return BigDecimal
}
```

Add `import "fmt"` to `numeric.go` if not already present.

- [ ] **Step 4: Run — GREEN for cross-family**

```bash
go test ./internal/domain/model/schema/... -run 'TestCollapseNumeric' -v
```
Expected: all cases PASS.

- [ ] **Step 5: Write failing walker tests (real classifier)**

Append to `internal/domain/model/importer/walker_test.go`:

```go
func TestWalker_ValueBasedClassification(t *testing.T) {
	cases := []struct {
		in   string
		want schema.DataType
	}{
		// Integer-family literals (including decimal-shaped whole numbers).
		{`42`, schema.Integer},
		{`"1.0"`, schema.String},  // quoted is STRING
		{`9007199254740993`, schema.Long},                   // 2^53 + 1, beyond int32
		{`9223372036854775808`, schema.BigInteger},          // 2^63, beyond int64
		{`170141183460469231731687303715884105728`, schema.UnboundInteger}, // 2^127
		// Whole-number decimals route to integer branch.
		{`1.0`, schema.Integer},
		{`1e0`, schema.Integer},
		// Fractional decimals route to decimal branch.
		{`0.1`, schema.Double},
		{`1.5`, schema.Double},
		// Pi-18 is BIG_DECIMAL; pi-20 is UNBOUND_DECIMAL.
		{`3.141592653589793238`, schema.BigDecimal},
		{`3.14159265358979323846`, schema.UnboundDecimal},
		// Overflow.
		{`1e400`, schema.UnboundDecimal},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			dec := json.NewDecoder(strings.NewReader(c.in))
			dec.UseNumber()
			var v any
			if err := dec.Decode(&v); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			node, err := importer.Walk(v)
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}
			if node.Kind() != schema.KindLeaf {
				t.Fatalf("expected leaf, got %s", node.Kind())
			}
			types := node.Types().Types()
			if len(types) != 1 {
				t.Fatalf("expected single type, got %v", types)
			}
			if types[0] != c.want {
				t.Errorf("walker for %q: got %s, want %s", c.in, types[0], c.want)
			}
		})
	}
}
```

Ensure `walker_test.go` imports `"encoding/json"` and `"strings"`.

- [ ] **Step 6: Run — RED (walker still transitional)**

```bash
go test ./internal/domain/model/importer/... -run 'TestWalker_ValueBasedClassification' -v
```
Expected: failures on the BigDecimal/UnboundDecimal cases and on the boundary-magnitude integer cases (transitional walker classifies all integers as `Integer` or `BigInteger`).

- [ ] **Step 7: Replace walker numeric dispatch with real classifier**

Edit `internal/domain/model/importer/walker.go`. Replace the `json.Number` case in `walkValue` with:

```go
	case json.Number:
		return classifyNumber(val)
```

Add the helper at the end of `walker.go`:

```go
func classifyNumber(n json.Number) (*schema.ModelNode, error) {
	d, err := schema.ParseDecimal(n.String())
	if err != nil {
		return nil, fmt.Errorf("classify number %q: %w", n.String(), err)
	}
	stripped := d.StripTrailingZeros()
	// Whole-number check: scale ≤ 0 means no fractional component remains.
	if stripped.Scale() <= 0 {
		// Reconstruct the integer magnitude: unscaled × 10^(-scale).
		var bigVal *big.Int
		if stripped.Scale() == 0 {
			bigVal = stripped.Unscaled()
		} else {
			factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-stripped.Scale())), nil)
			bigVal = new(big.Int).Mul(stripped.Unscaled(), factor)
		}
		return schema.NewLeafNode(schema.ClassifyInteger(bigVal)), nil
	}
	return schema.NewLeafNode(schema.ClassifyDecimal(stripped)), nil
}
```

Add `import "math/big"` to `walker.go` if not present. Remove the legacy `float64` case from the switch entirely — if a caller somehow delivers `float64`, return an error:

```go
	case float64:
		return nil, fmt.Errorf("walker received float64 value; callers must use json.UseNumber() decoding")
```

Remove the no-longer-used `strconv` import unless the stub's integer-parse is still there (clean it up — the integer-parse in the stub went away).

- [ ] **Step 8: Run — GREEN**

```bash
go test ./internal/domain/model/importer/... -run 'TestWalker_ValueBasedClassification' -v
```
Expected: all cases PASS.

- [ ] **Step 9: Full schema + importer test run**

```bash
go test ./internal/domain/model/schema/... ./internal/domain/model/importer/... -v 2>&1 | tail -15
```
Expected: full green.

- [ ] **Step 10: Commit**

```bash
git add internal/domain/model/schema/numeric.go internal/domain/model/schema/numeric_test.go \
        internal/domain/model/importer/walker.go internal/domain/model/importer/walker_test.go
git commit -m "$(cat <<'EOF'
feat(schema,importer): CollapseNumeric cross-family + walker value-based classifier

CollapseNumeric cross-family promotes to BigDecimal (or
UnboundDecimal when an integer side has UnboundInteger or a decimal
side has UnboundDecimal). No polymorphism within numerics.

Walker routes by value, not by source syntax: "1.0", "1e0",
"10e-1" all classify via ClassifyInteger. Whole-number magnitudes
bucket across Integer/Long/BigInteger/UnboundInteger correctly.
float64 legacy path removed — callers must use json.UseNumber().

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: `TypeSet.Add` — use `CollapseNumeric`

**Files:**
- Modify: `internal/domain/model/schema/types.go`
- Modify: `internal/domain/model/schema/types_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/domain/model/schema/types_test.go`:

```go
func TestTypeSetAdd_NumericCollapse_SameFamily(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Integer)
	ts.Add(schema.Long)
	got := ts.Types()
	if len(got) != 1 || got[0] != schema.Long {
		t.Errorf("Integer+Long: got %v, want [Long]", got)
	}
}

func TestTypeSetAdd_NumericCollapse_CrossFamily(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Integer)
	ts.Add(schema.Double)
	got := ts.Types()
	if len(got) != 1 || got[0] != schema.BigDecimal {
		t.Errorf("Integer+Double: got %v, want [BigDecimal]", got)
	}
}

func TestTypeSetAdd_NullDropsOnConcrete(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Null)
	ts.Add(schema.Integer)
	got := ts.Types()
	if len(got) != 1 || got[0] != schema.Integer {
		t.Errorf("Null+Integer: got %v, want [Integer]", got)
	}
}

func TestTypeSetAdd_ConcreteDropsNull(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Integer)
	ts.Add(schema.Null)
	got := ts.Types()
	if len(got) != 1 || got[0] != schema.Integer {
		t.Errorf("Integer+Null: got %v, want [Integer]", got)
	}
}

func TestTypeSetAdd_NullAlone(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Null)
	got := ts.Types()
	if len(got) != 1 || got[0] != schema.Null {
		t.Errorf("Null alone: got %v, want [Null]", got)
	}
}

func TestTypeSetAdd_CrossKindPreserved(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Integer)
	ts.Add(schema.String)
	got := ts.Types()
	if len(got) != 2 {
		t.Errorf("Integer+String: got %v, want 2 elements", got)
	}
	hasInt := false
	hasStr := false
	for _, dt := range got {
		if dt == schema.Integer {
			hasInt = true
		}
		if dt == schema.String {
			hasStr = true
		}
	}
	if !hasInt || !hasStr {
		t.Errorf("Integer+String: expected both; got %v", got)
	}
}

func TestTypeSetAdd_CrossKindWithNumericCollapse(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Integer)
	ts.Add(schema.Double)
	ts.Add(schema.String)
	ts.Add(schema.Null)
	got := ts.Types()
	// Expected: [BigDecimal, String] — Null drops, numerics collapse.
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 elements", got)
	}
	hasBD := false
	hasStr := false
	for _, dt := range got {
		if dt == schema.BigDecimal {
			hasBD = true
		}
		if dt == schema.String {
			hasStr = true
		}
	}
	if !hasBD || !hasStr {
		t.Errorf("got %v, want [BigDecimal, String]", got)
	}
}

func TestTypeSetAdd_NonNumericOnlyUnchangedBehavior(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.String)
	ts.Add(schema.Boolean)
	got := ts.Types()
	if len(got) != 2 {
		t.Errorf("String+Boolean: got %v, want 2 elements", got)
	}
}
```

- [ ] **Step 2: Run — RED**

```bash
go test ./internal/domain/model/schema/... -run 'TestTypeSetAdd_Numeric|TestTypeSetAdd_Null|TestTypeSetAdd_CrossKind|TestTypeSetAdd_NonNumericOnly' -v
```
Expected: some failures. In particular the Integer+Double case currently keeps both (polymorphic); the new expected behavior collapses to `BigDecimal`.

- [ ] **Step 3: Rewrite `TypeSet.Add`**

Replace the `Add` function in `internal/domain/model/schema/types.go`:

```go
// Add inserts a DataType into the set and applies the cyoda-go
// collapse rule:
//   - NULL is dropped when any concrete type is present.
//   - Numeric members collapse to a single DataType per CollapseNumeric.
//   - Non-numeric members (other than NULL) are preserved as-is.
func (ts *TypeSet) Add(dt DataType) {
	// Special case: NULL. If the set already has concrete types, skip.
	if dt == Null {
		for _, existing := range ts.types {
			if existing != Null {
				return // drop incoming NULL
			}
		}
		// All existing entries are NULL (or none); allow adding (dedup below).
	}

	// Insert dedup'd.
	for _, existing := range ts.types {
		if existing == dt {
			return
		}
	}
	ts.types = append(ts.types, dt)

	// If the incoming is concrete, strip any pre-existing NULL.
	if dt != Null {
		filtered := ts.types[:0]
		for _, existing := range ts.types {
			if existing != Null {
				filtered = append(filtered, existing)
			}
		}
		ts.types = filtered
	}

	// Collapse numerics if more than one numeric member is present.
	var numerics []DataType
	var nonNumerics []DataType
	for _, existing := range ts.types {
		if IsNumeric(existing) {
			numerics = append(numerics, existing)
		} else {
			nonNumerics = append(nonNumerics, existing)
		}
	}
	if len(numerics) >= 2 {
		collapsed := CollapseNumeric(numerics)
		ts.types = append(nonNumerics, collapsed)
	}
	// Keep the set sorted for stable output.
	sort.Slice(ts.types, func(i, j int) bool { return ts.types[i] < ts.types[j] })
}
```

- [ ] **Step 4: Run — GREEN for new tests**

```bash
go test ./internal/domain/model/schema/... -run 'TestTypeSetAdd_' -v
```
Expected: all new tests PASS.

- [ ] **Step 5: Check existing `TypeSet` tests still pass**

```bash
go test ./internal/domain/model/schema/... -v 2>&1 | tail -30
```
Expected: all PASS. If `TestTypeSetNumericCrossFamily` or similar expected polymorphic retention, update it to reflect the new collapse behavior.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/model/schema/types.go internal/domain/model/schema/types_test.go
git commit -m "$(cat <<'EOF'
feat(schema): TypeSet.Add — NULL drop + numeric collapse via CollapseNumeric

Numerics in a TypeSet always collapse to a single DataType.
Cross-kind polymorphism (numeric + non-numeric) is preserved.
NULL disappears when any concrete type is present.

Existing TestTypeSetNumericCrossFamily-style tests that assumed
polymorphic retention across numeric families have been updated to
reflect the new collapse behavior.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 17: gRPC ingestion path — `UseNumber` decoder fix

**Files:**
- Modify: `internal/grpc/entity.go`
- Create: `internal/grpc/entity_usenumber_test.go`

The gRPC path at `entity.go:35, :77, :124, :152, :213, :264` uses `json.Unmarshal` which decodes numerics through `float64`. All sites that unmarshal a CloudEvent body whose payload eventually reaches the entity handler must switch to a `UseNumber`-backed decoder.

- [ ] **Step 1: Write failing tests**

Create `internal/grpc/entity_usenumber_test.go`:

```go
package grpc_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestCloudEventDecode_UseNumber_LargeInteger ensures that
// numerics larger than 2^53 survive the gRPC dispatch decoder.
// The production fix installs json.UseNumber() at every ingestion
// site in internal/grpc/entity.go; this test asserts the library
// behavior we rely on.
func TestCloudEventDecode_UseNumber_LargeInteger(t *testing.T) {
	const payload = `{"payload":{"data":{"x":9007199254740993}}}`
	type Envelope struct {
		Payload struct {
			Data any `json:"data"`
		} `json:"payload"`
	}

	// Without UseNumber — the current production path.
	var without Envelope
	if err := json.Unmarshal([]byte(payload), &without); err != nil {
		t.Fatalf("without UseNumber: %v", err)
	}
	withoutStr := mustMarshal(t, without.Payload.Data)
	if !strings.Contains(withoutStr, "9007199254740993") {
		// Expected: float64 round-trip loses the final digit.
		t.Logf("without UseNumber re-marshals as: %s (precision lost)", withoutStr)
	}

	// With UseNumber — the fix.
	var with Envelope
	dec := json.NewDecoder(bytes.NewReader([]byte(payload)))
	dec.UseNumber()
	if err := dec.Decode(&with); err != nil {
		t.Fatalf("with UseNumber: %v", err)
	}
	withStr := mustMarshal(t, with.Payload.Data)
	if !strings.Contains(withStr, "9007199254740993") {
		t.Errorf("with UseNumber: expected exact magnitude preserved; got %s", withStr)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
```

This test confirms `UseNumber` behavior in the Go stdlib itself, then the actual production fix follows in Step 3.

- [ ] **Step 2: Run — passes as-is (library test); verify**

```bash
go test ./internal/grpc/... -run 'TestCloudEventDecode_UseNumber_LargeInteger' -v
```
Expected: PASS.

- [ ] **Step 3: Replace every `json.Unmarshal(payload, &req)` in `internal/grpc/entity.go` with a UseNumber decoder**

Find every call-site (grep: `json.Unmarshal(payload, &req)` in `internal/grpc/entity.go` — expect 6+ sites on lines 35, 77, 124, 152, 213, 264). Replace each with:

```go
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var req events.<ThatType>
	if err := dec.Decode(&req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payload: %v", err)
	}
```

Ensure `import "bytes"` is present in the file.

Do the same audit across `internal/grpc/*.go` for any other dispatch site that unmarshal CloudEvent payloads to `interface{}`-bearing types. Typical pattern: `json.Unmarshal(payload, &req)` where `req` has a `Data interface{}` field.

- [ ] **Step 4: Add an integration test at the gRPC dispatch layer**

Append to `internal/grpc/entity_usenumber_test.go` — wire a CloudEvent with a large-integer payload through the dispatch handler and assert the classified schema type is `Long`. This requires a minimal handler harness — see `internal/grpc/*_test.go` for existing patterns. If no existing harness is suitable, defer this end-to-end assertion to Task 20's E2E test and leave this file with only the library-level confirmation.

- [ ] **Step 5: Build and run**

```bash
go build ./...
go test ./internal/grpc/... -v 2>&1 | tail -15
```
Expected: clean build, all grpc tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/grpc/entity.go internal/grpc/entity_usenumber_test.go
git commit -m "$(cat <<'EOF'
fix(grpc): use json.UseNumber() for CloudEvent payload decoding

The gRPC ingestion path at internal/grpc/entity.go (create, update,
collection-create, collection-update sites) previously used
json.Unmarshal into events.*Json structs with Data interface{}
fields. Numerics in that path decoded through float64 and lost
precision above 2^53 before reaching the entity service's
UseNumber-backed decoder.

This is a production bug. Switching to json.NewDecoder(...).UseNumber()
at every gRPC dispatch site preserves numeric precision end to end.

Per spec §4.5 (A.1 scope).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 18: `validate.go` — asymmetric validation via `IsAssignableTo`

**Files:**
- Modify: `internal/domain/model/schema/validate.go`
- Modify: `internal/domain/model/schema/validate_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/domain/model/schema/validate_test.go`:

```go
func TestValidate_IntegerSchema_RejectsDecimalValue(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("x", schema.NewLeafNode(schema.Integer))
	data := map[string]any{"x": json.Number("13.111")}
	errs := schema.Validate(model, data)
	if len(errs) == 0 {
		t.Fatal("expected rejection")
	}
}

func TestValidate_DoubleSchema_AcceptsIntegerValue(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("x", schema.NewLeafNode(schema.Double))
	data := map[string]any{"x": json.Number("13")}
	errs := schema.Validate(model, data)
	if len(errs) != 0 {
		t.Errorf("expected acceptance; got errors: %v", errs)
	}
}

func TestValidate_BigDecimalSchema_AcceptsHighPrecision(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("x", schema.NewLeafNode(schema.BigDecimal))
	data := map[string]any{"x": json.Number("3.141592653589793238")}
	errs := schema.Validate(model, data)
	if len(errs) != 0 {
		t.Errorf("expected acceptance; got errors: %v", errs)
	}
}

func TestValidate_IntegerSchema_AcceptsInteger(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("x", schema.NewLeafNode(schema.Integer))
	data := map[string]any{"x": json.Number("13")}
	errs := schema.Validate(model, data)
	if len(errs) != 0 {
		t.Errorf("expected acceptance; got errors: %v", errs)
	}
}

func TestValidate_PolymorphicSchema_AcceptsEither(t *testing.T) {
	model := schema.NewObjectNode()
	leaf := &schema.ModelNode{} // hand-build a polymorphic leaf
	// Use the Leaf-builder path via test helpers. If NewLeafNode only
	// takes one type, build the TypeSet externally.
	types := schema.NewTypeSet()
	types.Add(schema.Integer)
	types.Add(schema.String)
	// See types_test.go for how to construct polymorphic leaves. If
	// unavailable, skip this test and log why.
	_ = leaf
	_ = types
}
```

If `NewLeafNode` doesn't support multi-type polymorphic leaves directly, construct via the codec round-trip (see `codec.go`). If the harness is awkward, drop the polymorphic test and note it as covered by Task 16's `TypeSet.Add` tests.

- [ ] **Step 2: Run — RED**

```bash
go test ./internal/domain/model/schema/... -run 'TestValidate_(IntegerSchema|DoubleSchema|BigDecimalSchema)_' -v
```
Expected: the `IntegerSchema_RejectsDecimalValue` case currently passes validation (the old `isCompatible` = any-numeric); so this fails after fix. Before fix, our test fails the other way.

Actually, examining the test order: `TestValidate_IntegerSchema_RejectsDecimalValue` asserts that the old lenient behavior is now strict. Before the code change, the test FAILS because `isCompatible` accepts it. After the code change, it PASSES.

So this is the expected RED → GREEN for this task.

- [ ] **Step 3: Replace `isCompatible` with `IsAssignableTo` wiring**

Edit `internal/domain/model/schema/validate.go`:

1. Remove the `isCompatible` function (lines ~183-197).
2. Remove the `isNumeric` function (lines ~199-208) — now `IsNumeric` in `numeric.go` does the job.
3. Replace `inferDataType` numeric branches to classify via the same logic the walker uses. Since the validator sees decoded `any` values, the `json.Number` case must route through `ParseDecimal` + `ClassifyInteger`/`ClassifyDecimal`:

```go
func inferDataType(v any) DataType {
	switch n := v.(type) {
	case bool:
		return Boolean
	case json.Number:
		d, err := ParseDecimal(string(n))
		if err != nil {
			// Malformed — conservatively say String (validator will reject).
			return String
		}
		stripped := d.StripTrailingZeros()
		if stripped.Scale() <= 0 {
			var bigVal *big.Int
			if stripped.Scale() == 0 {
				bigVal = stripped.Unscaled()
			} else {
				factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-stripped.Scale())), nil)
				bigVal = new(big.Int).Mul(stripped.Unscaled(), factor)
			}
			return ClassifyInteger(bigVal)
		}
		return ClassifyDecimal(stripped)
	case string:
		return String
	case nil:
		return Null
	default:
		return String
	}
}
```

Remove the `float64`, `int`, `int64` cases — callers must use UseNumber. If a `float64` leaks through, treat it as an error path — map to `String` so validation fails noisily.

4. Replace the call in `validateLeaf`:

```go
func validateLeaf(model *ModelNode, data any, path string) []ValidationError {
	if data == nil {
		return nil
	}
	dataType := inferDataType(data)
	modelTypes := model.Types().Types()
	for _, mt := range modelTypes {
		if IsAssignableTo(dataType, mt) {
			return nil
		}
	}
	return []ValidationError{{
		Path:    path,
		Message: fmt.Sprintf("value of type %s is not compatible with %v", dataType, modelTypes),
		Kind:    ErrKindGeneric,
	}}
}
```

Add `import "math/big"` to `validate.go`.

- [ ] **Step 4: Run — GREEN**

```bash
go test ./internal/domain/model/schema/... -run 'TestValidate' -v 2>&1 | tail -20
```
Expected: all pass. The `IntegerSchema_RejectsDecimalValue` test now asserts the correct strict behavior.

- [ ] **Step 5: Full schema test run**

```bash
go test ./internal/domain/model/schema/... -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/model/schema/validate.go internal/domain/model/schema/validate_test.go
git commit -m "$(cat <<'EOF'
feat(schema): validate.go — asymmetric compatibility via IsAssignableTo

Replaces the lenient isCompatible (any-numeric-vs-any-numeric) with
a widening-lattice check. An INTEGER field now rejects a "13.111"
value at strict validate; a DOUBLE field still accepts "13". Matches
Cyoda Cloud's asymmetric semantics via IsAssignableTo.

inferDataType now routes json.Number through ParseDecimal +
ClassifyInteger/ClassifyDecimal so the inferred type is the same
one the walker would produce.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 19: Cross-reference audit pass

**Files:**
- Audit: `internal/e2e/`
- Audit: `plugins/*/conformance_test.go`
- Audit: `e2e/parity/`
- Audit: `test/recon/`

- [ ] **Step 1: Identify failing tests**

```bash
go test -short ./... 2>&1 | grep -E '^--- FAIL|^FAIL' | head -30
```
Expected: some failures — tests that asserted the old lenient numeric matching or the old walker `DOUBLE` classification for high-precision decimals.

- [ ] **Step 2: For each failing test, apply one of:**

- **Update the test** to assert the new correct behavior. Document the change in the test comment explaining that it was updated for A.1.
- **Report as a regression** if the new behavior is undesirable. Do NOT silently accept a divergence from the expected behavior without a comment.

For each updated test, the commit message for that test file should cite spec §2.3 or §4.6 as the source of truth.

- [ ] **Step 3: Run full non-E2E test suite**

```bash
go test -short ./... 2>&1 | grep -E '^ok|^FAIL|^---' | tail -30
```
Expected: all ok.

- [ ] **Step 4: Commit audit-pass fixes**

```bash
git add <each-modified-test-file>
git commit -m "$(cat <<'EOF'
test: update cross-reference tests for A.1 classifier changes

Tests in internal/e2e/, plugins/*/conformance_test.go, e2e/parity/,
and test/recon/ that asserted the old lenient numeric matching
(any-vs-any) or expected DOUBLE for high-precision decimals are
updated to the new behavior. See spec §2.3 and §4.6.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 20: End-to-end numeric classification tests

**Files:**
- Create: `internal/e2e/numeric_classification_test.go`

- [ ] **Step 1: Write the E2E tests**

Create `internal/e2e/numeric_classification_test.go`:

```go
package e2e_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestNumericClassification_HTTP_18DigitDecimal verifies that an
// 18-fractional-digit decimal ingested via HTTP round-trips as
// BIG_DECIMAL in the exported schema.
func TestNumericClassification_HTTP_18DigitDecimal(t *testing.T) {
	const model = "e2e-num-bigdecimal"
	const version = 1
	const payload = `{"name":"x","value":3.141592653589793238}`

	importAndLockModel(t, model, version, payload)

	schema := exportModelE2E(t, model, version)
	raw := fmt.Sprintf("%v", schema)
	if !strings.Contains(raw, "BIG_DECIMAL") {
		t.Errorf("expected BIG_DECIMAL classification for 18-fractional-digit value; schema: %s", raw)
	}
}

// TestNumericClassification_HTTP_20DigitDecimal verifies that a
// 20-fractional-digit decimal (exceeds Trino scale-18) ingested via
// HTTP round-trips as UNBOUND_DECIMAL.
func TestNumericClassification_HTTP_20DigitDecimal(t *testing.T) {
	const model = "e2e-num-unbounddecimal"
	const version = 1
	const payload = `{"name":"x","value":3.14159265358979323846}`

	importAndLockModel(t, model, version, payload)

	schema := exportModelE2E(t, model, version)
	raw := fmt.Sprintf("%v", schema)
	if !strings.Contains(raw, "UNBOUND_DECIMAL") {
		t.Errorf("expected UNBOUND_DECIMAL classification for 20-fractional-digit value; schema: %s", raw)
	}
}

// TestNumericClassification_HTTP_LargeInteger verifies that a 2^53+1
// integer ingested via HTTP round-trips as LONG.
func TestNumericClassification_HTTP_LargeInteger(t *testing.T) {
	const model = "e2e-num-long"
	const version = 1
	// 2^53 + 1 = 9007199254740993
	const payload = `{"id":9007199254740993,"name":"x"}`

	importAndLockModel(t, model, version, payload)

	schema := exportModelE2E(t, model, version)
	raw := fmt.Sprintf("%v", schema)
	if !strings.Contains(raw, "LONG") {
		t.Errorf("expected LONG classification for 2^53+1 integer; schema: %s", raw)
	}
}

// TestNumericClassification_HTTP_CreateWithIntegerOnIntegerSchema confirms
// strict validation accepts integer data on an integer schema.
func TestNumericClassification_HTTP_CreateWithIntegerOnIntegerSchema(t *testing.T) {
	const model = "e2e-num-strict-int"
	const version = 1
	importAndLockModel(t, model, version, `{"qty":5}`)
	resp := doCreateEntityHTTPIfExists(t, model, version, `{"qty":42}`)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Errorf("expected 200; got %d: %s", resp.StatusCode, body)
	}
}

// TestNumericClassification_HTTP_CreateWithDecimalOnIntegerSchema_Rejected
// confirms strict validation rejects decimal data on an integer schema.
func TestNumericClassification_HTTP_CreateWithDecimalOnIntegerSchema_Rejected(t *testing.T) {
	const model = "e2e-num-strict-int2"
	const version = 1
	importAndLockModel(t, model, version, `{"qty":5}`)
	resp := doCreateEntityHTTPIfExists(t, model, version, `{"qty":13.111}`)
	if resp.StatusCode == http.StatusOK {
		t.Error("expected rejection of decimal value against integer schema; got 200")
	}
}

// doCreateEntityHTTPIfExists is a local helper that tries the HTTP
// create path; pattern follows other E2E tests in the package. If
// internal/e2e/ has an existing createEntityE2E that panics on non-
// 200, use that instead and assert via t.Run.
func doCreateEntityHTTPIfExists(t *testing.T, model string, version int, body string) *http.Response {
	t.Helper()
	url := fmt.Sprintf("%s/api/entity/JSON/%s/%d", serverURL, model, version)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+getAdminToken(t))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}
```

Note: `importAndLockModel`, `exportModelE2E`, `serverURL`, `getAdminToken`, `readBody` are existing helpers in `internal/e2e/` — reuse them. If naming differs (`getAdminToken` vs `getToken`), adjust.

If there is no existing `getAdminToken`, use whichever helper creates an authenticated request (see `doAuth` in `internal/e2e/entity_test.go`).

- [ ] **Step 2: Run the tests**

```bash
go test ./internal/e2e/... -run 'TestNumericClassification_' -v -count=1 -timeout 120s 2>&1 | tail -30
```
Expected: all PASS. If any fail, the fault is likely in an upstream task's implementation — re-run that task's verification.

- [ ] **Step 3: Commit**

```bash
git add internal/e2e/numeric_classification_test.go
git commit -m "$(cat <<'EOF'
test(e2e): numeric classification — full-stack precision & asymmetric-validation

Asserts BIG_DECIMAL / UNBOUND_DECIMAL / LONG classifications for
high-precision decimals and 2^53+1 integers through the HTTP
ingestion path. Asserts INTEGER-schema strict-validate accepts
integer data and rejects decimal data.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 21: Documentation — `docs/numeric-classification.md`

**Files:**
- Create: `docs/numeric-classification.md`

- [ ] **Step 1: Write the doc**

Create `docs/numeric-classification.md`:

```markdown
# Numeric Classification in cyoda-go

This document describes cyoda-go's numeric classification policy for
data ingestion. The raw algorithm is ported from Cyoda Cloud's
classifier; cyoda-go deliberately diverges in two ways documented
below.

**Authoritative references:**
- Algorithm source: [`numeric-type-classification-analysis.md`](numeric-type-classification-analysis.md)
- Design spec: [`superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md`](superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md)

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

**Cross-family collapse:** the narrowest decimal type that losslessly contains every integer-family member observed:

- `{Integer|Long, Double|BigDecimal}` → `BigDecimal`
- `{Integer|Long, UnboundDecimal}` → `UnboundDecimal`
- `{BigInteger, Double|BigDecimal}` → `BigDecimal` (BigInteger fits Int128 at scale 0)
- `{BigInteger, UnboundDecimal}` → `UnboundDecimal`
- `{UnboundInteger, any decimal}` → `UnboundDecimal`

Cyoda Cloud's polymorphic numeric sets are replaced by this single-type collapse. A field Cyoda Cloud represents as `{FLOAT, DOUBLE, BIG_DECIMAL}` becomes `BigDecimal` in cyoda-go. No information is lost — every observed value remains representable.

## Validation compatibility

`IsAssignableTo(dataT, schemaT)` realizes the widening lattice (per Cyoda Cloud `DataType.kt:239-272`, minus dropped types). Notable asymmetries:

- `Integer → Double` — **allowed**. Integer's 2^31 range fits Double's 53-bit mantissa.
- `Long → Double` — **blocked**. Long's 2^63 exceeds Double's 53-bit mantissa; precision loss.
- `Integer → BigDecimal` — allowed. `Long → BigDecimal` — allowed.
- `Double → BigDecimal` — **blocked**. Envelopes differ.
- Any integer → any decimal family reachable via the above — allowed.
- Any decimal → integer family — **blocked**.
- `NULL → anything` — allowed.

Under strict validation (`ChangeLevel = ""`), a decimal value against an `Integer` schema is rejected. Under extension (`ChangeLevel = Type` or higher), the same input triggers schema widening via the collapse rule — the field becomes `BigDecimal`.

## Intentional divergences from Cyoda Cloud

1. **No polymorphism within numerics.** Cyoda Cloud retains polymorphic numeric sets (e.g., `{FLOAT, DOUBLE, BIG_DECIMAL}`) and collapses only at read time via `findCommonDataType`, which falls back to `STRING` when no common type exists — silently corrupting numeric data. cyoda-go always stores the collapsed numeric type; cross-family mixing promotes to BigDecimal/UnboundDecimal without any STRING fallback. Bug fix, not a neutral divergence.

2. **Value-based integer/decimal split.** Cyoda Cloud routes on Jackson node kind (`IntNode` vs `DecimalNode`), which is a leaky abstraction over JSON grammar: `"1.0"` and `"1e0"` both denote the integer 1 but take different classifier branches. cyoda-go classifies by value — any whole-number literal classifies via `ClassifyInteger` regardless of source syntax.
```

- [ ] **Step 2: Commit**

```bash
git add docs/numeric-classification.md
git commit -m "docs(numeric): cyoda-go numeric classification policy

Complements docs/numeric-type-classification-analysis.md. Describes
the cyoda-go policy, the two intentional divergences (collapse-to-
single-type, value-based classification), and the asymmetric
validation behavior.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 22: Final verification

**Files:**
- None (verification only)

- [ ] **Step 1: Build clean across all modules**

```bash
go build ./... 2>&1 | head -10
(cd plugins/memory && go build ./...)
(cd plugins/sqlite && go build ./...)
(cd plugins/postgres && go build ./...)
```
Expected: no errors.

- [ ] **Step 2: `go vet`**

```bash
go vet ./...
(cd plugins/memory && go vet ./...)
(cd plugins/sqlite && go vet ./...)
(cd plugins/postgres && go vet ./...)
```
Expected: clean.

- [ ] **Step 3: Full short test suite**

```bash
go test -short ./... 2>&1 | grep -E '^FAIL|^ok' | tail -25
```
Expected: all `ok`, no `FAIL`.

- [ ] **Step 4: Plugin submodule tests**

```bash
(cd plugins/memory && go test ./... -count=1)
(cd plugins/sqlite && go test ./... -count=1)
(cd plugins/postgres && CYODA_TEST_DB_URL='postgres://minicyoda:minicyoda@127.0.0.1:5432/minicyoda?sslmode=disable' go test ./... -count=1 2>&1 | tail -5)
```
Expected: all pass (postgres requires local container running).

- [ ] **Step 5: E2E suite**

```bash
go test ./internal/e2e/... -v -count=1 -timeout 180s 2>&1 | grep -E '^--- FAIL|^FAIL' | head -10
```
Expected: no output (no failures).

- [ ] **Step 6: Race detector sanity pass**

```bash
go test -race -short ./... 2>&1 | grep -E 'DATA RACE|^FAIL' | head -5
```
Expected: no output.

- [ ] **Step 7: Commit a verification marker**

No code change — just confirm via comment or a release notes entry. Typically the release-notes update at the end of the sub-project goes here, or an empty allow-empty commit if the project uses those.

If the project maintains a release-note section in README or CHANGELOG, append an A.1 summary:

```markdown
## A.1: Numeric Classifier Parity

- Port Cyoda Cloud's precision/scale-based decimal classifier.
- Drop BYTE, SHORT, FLOAT from the DataType enum.
- Numeric TypeSet collapses to one DataType; cross-kind polymorphism preserved.
- Asymmetric validation: decimal values no longer validate against integer schemas at strict validate.
- Fix: gRPC ingestion path now uses json.UseNumber() — numbers above 2^53 preserve precision.
```

- [ ] **Step 8: Final commit if release notes updated**

```bash
git add <release-notes-file>
git commit -m "docs: A.1 release notes"
```

---

## Self-review

Ran through each spec section against the task list.

**Coverage:**
- §2.1 in-scope items: all covered by Tasks 1-21 (decimal, numeric classifier, types.go drop, walker, validate, grpc, docs).
- §2.2 out-of-scope items: correctly absent from task list.
- §2.3 divergences (FLOAT/BYTE/SHORT drop, value-based classification, STRING-fallback fix): encoded in Tasks 13, 15, 16, 18 + documented in Task 21.
- §3 invariants: covered by tests in Tasks 18 (I1, I2 via validate), 20 (I1, I2 via E2E), 15 (I3 via walker).
- §4.1 Decimal type: Tasks 1-8.
- §4.2 numeric.go: Tasks 9-12, 14, 15.
- §4.3 TypeSet.Add: Task 16.
- §4.4 walker: Task 15.
- §4.5 gRPC fix: Task 17.
- §4.6 validate: Task 18.
- §5 CollapseNumeric: Tasks 14-15.
- §6.1-6.10 test tables: Tasks 1-8 (Decimal), 10-11 (classifiers), 12 (lattice), 14-15 (collapse), 16 (TypeSet), 17 (gRPC), 18 (validate), 20 (E2E).
- §7 implementation sequence: Tasks 1-22 follow it (with finer granularity).
- §8 risks: audit pass in Task 19; gRPC audit in Task 17.
- §9 success criteria: verified in Task 22.
- §10 follow-on: no tasks needed (explicitly out of scope).

**Placeholder scan:**
- No "TBD"/"TODO".
- Every step has concrete code or concrete commands.
- File paths are exact.
- The only "do an audit" step is Task 19, which is genuinely an audit pass that can't be specified exhaustively without knowing which tests break.

**Type consistency:**
- `Decimal` shape (`unscaled *big.Int, scale int32`) used consistently across Tasks 1-8.
- `ParseDecimal` return type `(Decimal, error)` consistent.
- `SetScale` signature `(int32) (Decimal, error)` consistent.
- `CollapseNumeric(types []DataType) DataType` consistent (single-DataType return).
- `ClassifyInteger(v *big.Int) DataType` and `ClassifyDecimal(d Decimal) DataType` consistent.
- `IsAssignableTo(dataT, schemaT DataType) bool` consistent.

Plan passes self-review.
