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
