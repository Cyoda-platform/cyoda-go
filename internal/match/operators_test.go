package match

import (
	"encoding/json"
	"testing"
)

func TestToFloat64_JSONNumber(t *testing.T) {
	cases := []struct {
		in   json.Number
		want float64
	}{
		{"0", 0},
		{"42", 42},
		{"-1.5", -1.5},
		{"1e10", 1e10},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			got, err := toFloat64(tc.in)
			if err != nil {
				t.Fatalf("toFloat64(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("toFloat64(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
