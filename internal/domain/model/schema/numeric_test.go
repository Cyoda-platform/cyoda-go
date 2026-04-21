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

func TestClassifyDecimal(t *testing.T) {
	mkD := func(s string) Decimal {
		d, err := ParseDecimal(s)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", s, err)
		}
		return d.StripTrailingZeros()
	}

	t.Run("DOUBLE_samples", func(t *testing.T) {
		cases := []struct {
			in   string
			want DataType
		}{
			{"0.1", Double},
			{"1.5", Double},
			{"0.123456789012345", Double}, // precision 15, boundary
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
		// unscaled = 10^16 (17 digits), scale=-6 → precision=17, exp=23 (>21).
		// precision > 15 bumps out of DOUBLE; exp > 21 blocks both BIG_DECIMAL tiers.
		// NB: an earlier draft used unscaled=1, scale=-22 here, but that has
		// precision=1 and |scale|=22, which correctly classifies as DOUBLE
		// (1e22 is representable). We need precision>15 to exit DOUBLE and
		// then exercise the exp cap.
		u := new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil)
		d := Decimal{unscaled: u, scale: -6}
		got := ClassifyDecimal(d)
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
		{Integer, Long}:                  true,
		{Integer, BigInteger}:            true,
		{Integer, UnboundInteger}:        true,
		{Integer, Double}:                true, // 2^31 fits Double mantissa
		{Integer, BigDecimal}:            true,
		{Integer, UnboundDecimal}:        true,
		{Long, BigInteger}:               true,
		{Long, UnboundInteger}:           true,
		{Long, BigDecimal}:               true,
		{Long, UnboundDecimal}:           true,
		{Long, Double}:                   false, // precision — 2^63 exceeds Double mantissa
		{BigInteger, UnboundInteger}:     true,
		{BigInteger, UnboundDecimal}:     true,
		{UnboundInteger, UnboundDecimal}: true,
		// Decimal family.
		{Double, UnboundDecimal}:     true,
		{Double, BigDecimal}:         false, // envelopes differ
		{BigDecimal, UnboundDecimal}: true,
		// Cross-direction: decimal does not assign to integer.
		{Double, Integer}:  false,
		{BigDecimal, Long}: false,
		// Non-numeric.
		{String, Integer}: false,
		{Integer, String}: false,
	}
	for pair, want := range allow {
		t.Run(pair[0].String()+"_to_"+pair[1].String(), func(t *testing.T) {
			got := IsAssignableTo(pair[0], pair[1])
			if got != want {
				t.Errorf("IsAssignableTo(%s, %s): got %v, want %v", pair[0], pair[1], got, want)
			}
		})
	}
}
