package schema

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
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
	switch strings.ToLower(s) {
	case "nan", "inf", "infinity", "+inf", "+infinity", "-inf", "-infinity":
		return Decimal{}, fmt.Errorf("parse decimal: non-numeric token %q", s)
	}

	// Split mantissa and exponent.
	var mantissa, expPart string
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, expPart = s[:i], s[i+1:]
		if expPart == "" {
			return Decimal{}, fmt.Errorf("parse decimal: empty exponent in %q", s)
		}
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

	// Reject bare "." / "-." / "+." — after the sign-strip and
	// intPart="" → "0" substitution above, these would otherwise parse
	// as zero. Malformed input should error, not silently become 0.
	if intPart == "0" && fracPart == "" && strings.ContainsRune(mantissa, '.') {
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
		exp, err = strconv.ParseInt(expPart, 10, 64)
		if err != nil {
			return Decimal{}, fmt.Errorf("parse decimal: invalid exponent %q: %w", expPart, err)
		}
	}

	// Build unscaled: sign + intPart + fracPart.
	unscaledStr := sign + intPart + fracPart
	unscaled, ok := new(big.Int).SetString(unscaledStr, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("parse decimal: failed to build unscaled from %q", s)
	}

	// Scale: fractional-digit count minus exponent.
	scale := int64(len(fracPart)) - exp
	if scale > math.MaxInt32 || scale < math.MinInt32 {
		return Decimal{}, fmt.Errorf("parse decimal: scale %d out of int32 range", scale)
	}
	return Decimal{unscaled: unscaled, scale: int32(scale)}, nil
}

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

// Precision returns the number of significant digits in the unscaled
// value. Matches Java BigDecimal.precision() — returns 1 for zero.
func (d Decimal) Precision() int {
	if d.unscaled == nil || d.unscaled.Sign() == 0 {
		return 1
	}
	abs := new(big.Int).Abs(d.unscaled)
	return len(abs.String())
}

// SetScale returns a Decimal at the requested scale. Upward scale
// (adding fractional digits) multiplies the unscaled value by
// 10^(n-scale) and always succeeds. Downward scale (removing
// fractional digits) succeeds only if the unscaled value is divisible
// by 10^(scale-n); otherwise returns a precision-loss error.
func (d Decimal) SetScale(newScale int32) (Decimal, error) {
	if d.scale == newScale {
		u := new(big.Int)
		if d.unscaled != nil {
			u.Set(d.unscaled)
		}
		return Decimal{unscaled: u, scale: newScale}, nil
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

// int128Min = -2^127, int128Max = 2^127 - 1.
// Pre-computed once at package init to avoid recomputing per call.
var int128Min = new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))
var int128Max = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))

// IsInt128 reports whether the unscaled value fits the signed Int128
// range [-2^127, 2^127-1]. Scale is not considered.
//
// Implementation note: relies on pre-computed boundaries rather than
// big.Int.BitLen() comparisons, because BitLen ignores sign and
// BitLen(-2^127) == 128 — incorrectly excluding the valid minimum.
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

func (d Decimal) IsInt128() bool {
	if d.unscaled == nil {
		return true
	}
	return d.unscaled.Cmp(int128Min) >= 0 && d.unscaled.Cmp(int128Max) <= 0
}

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
