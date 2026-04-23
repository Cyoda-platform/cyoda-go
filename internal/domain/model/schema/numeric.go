package schema

import (
	"fmt"
	"math/big"
)

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

// CollapseNumeric reduces a numeric-only set to a single DataType that
// every input widens to, using the Cyoda-compatible widening lattice
// (see wideningLattice above, matching DataType.kt:240-287).
//
// Preconditions: input is non-empty; every element satisfies IsNumeric.
// Panics on either violation.
//
// Invariant: for every input t, IsAssignableTo(t, result) || t == result.
// This guarantees that validation of a pre-collapse value against the
// post-collapse schema succeeds — see A.2 §I3 monotonicity.
//
// Algorithm (equivalent to Cyoda's findCommonDataType at DataType.kt:293,
// restricted to numeric inputs):
//   - Integer-only inputs → widest integer in input (same-family widen).
//   - Otherwise candidate = widest decimal in input. If some input does
//     not widen to the candidate, escalate to UnboundDecimal (which every
//     numeric type reaches).
//
// Divergence from Cyoda: Cyoda's findCommonDataType returns STRING as a
// universal fallback for non-widening pairs. That is Cyoda-internal
// (every leaf is also stored as a string for search). CollapseNumeric is
// scoped to numerics where UnboundDecimal is the universal sink — STRING
// fallback is never needed.
func CollapseNumeric(types []DataType) DataType {
	if len(types) == 0 {
		panic("CollapseNumeric: empty input")
	}
	// Bucket by family, track widest in each.
	const sentinel = DataType(-1)
	widestInt := sentinel
	widestDec := sentinel
	for _, dt := range types {
		switch NumericFamily(dt) {
		case 1:
			if widestInt == sentinel || NumericRank(dt) > NumericRank(widestInt) {
				widestInt = dt
			}
		case 2:
			if widestDec == sentinel || NumericRank(dt) > NumericRank(widestDec) {
				widestDec = dt
			}
		default:
			panic(fmt.Sprintf("CollapseNumeric: non-numeric input %s", dt))
		}
	}
	// Integer-only: widen within family.
	if widestDec == sentinel {
		return widestInt
	}
	// Decimal-only or cross-family: the candidate is the widest decimal seen.
	// Verify every input type can be assigned to it; escalate to UnboundDecimal
	// when the candidate doesn't cover all members (e.g. DOUBLE is not
	// assignable to BIG_DECIMAL; BIG_INTEGER is not assignable to DOUBLE or
	// BIG_DECIMAL).
	candidate := widestDec
	for _, dt := range types {
		if dt != candidate && !IsAssignableTo(dt, candidate) {
			return UnboundDecimal
		}
	}
	return candidate
}
