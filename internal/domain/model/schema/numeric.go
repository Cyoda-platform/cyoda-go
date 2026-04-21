package schema

import "math/big"

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

// Int32 and Int64 boundary big.Ints for ClassifyInteger.
var (
	classifyInt32Min = big.NewInt(-1 << 31)
	classifyInt32Max = big.NewInt(1<<31 - 1)
	classifyInt64Min = new(big.Int).SetInt64(-1 << 63)
	classifyInt64Max = new(big.Int).SetUint64(1<<63 - 1)
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
	if v.Cmp(classifyInt32Min) >= 0 && v.Cmp(classifyInt32Max) <= 0 {
		return Integer
	}
	if v.Cmp(classifyInt64Min) >= 0 && v.Cmp(classifyInt64Max) <= 0 {
		return Long
	}
	if v.Cmp(int128Min) >= 0 && v.Cmp(int128Max) <= 0 {
		return BigInteger
	}
	return UnboundInteger
}

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
