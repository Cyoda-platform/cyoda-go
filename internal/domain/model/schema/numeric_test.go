package schema

import "testing"

func TestIsNumeric(t *testing.T) {
	// Test only the types permanently present (BYTE/SHORT/FLOAT may be
	// present today but will be dropped in Task 13; they are asserted
	// as numeric via NumericFamily indirectly and do not break this
	// test — but we don't reference them to avoid a later sweep).
	numeric := []DataType{Integer, Long, BigInteger, UnboundInteger, Double, BigDecimal, UnboundDecimal}
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
