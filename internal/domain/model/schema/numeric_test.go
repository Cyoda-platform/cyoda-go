package schema

import (
	"math/big"
	"testing"
)

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

func TestClassifyInteger(t *testing.T) {
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
		{"10^40", mkInt("10000000000000000000000000000000000000000"), UnboundInteger},
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
