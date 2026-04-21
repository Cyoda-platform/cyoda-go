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
