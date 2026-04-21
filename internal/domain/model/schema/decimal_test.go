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
		{"1e+2", bigInt("1"), -2},
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
		"1e99999999999999999999",  // exponent overflows int64 — must error
		"1e-99999999999999999999", // symmetric
	}
	for _, s := range invalid {
		t.Run(s, func(t *testing.T) {
			if _, err := ParseDecimal(s); err == nil {
				t.Errorf("ParseDecimal(%q): expected error, got nil", s)
			}
		})
	}
}

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
		d, _ := ParseDecimal("1.5") // unscaled=15, scale=1
		got, err := d.SetScale(3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.unscaled.Cmp(bigInt("1500")) != 0 || got.scale != 3 {
			t.Errorf("got unscaled=%s scale=%d, want unscaled=1500 scale=3", got.unscaled, got.scale)
		}
	})
	t.Run("downward_scale_divisible", func(t *testing.T) {
		// ParseDecimal("1500") gives unscaled=1500, scale=0.
		d, _ := ParseDecimal("1500")
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
	t.Run("negative_scale_lossy_errors", func(t *testing.T) {
		d, _ := ParseDecimal("100")
		_, err := d.SetScale(-3)
		if err == nil {
			t.Fatal("expected error for lossy negative scale")
		}
	})
	t.Run("negative_scale_exact", func(t *testing.T) {
		d, _ := ParseDecimal("1000") // unscaled=1000, scale=0
		got, err := d.SetScale(-3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.unscaled.Cmp(bigInt("1")) != 0 || got.scale != -3 {
			t.Errorf("got unscaled=%s scale=%d, want unscaled=1 scale=-3", got.unscaled, got.scale)
		}
	})
}
