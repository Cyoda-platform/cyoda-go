package match

import (
	"encoding/json"
	"testing"

	"github.com/cyoda-platform/cyoda-go-spi/predicate"
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

// TestOpEquals_JSONNumber proves that the toFloat64 extension propagates
// through opEquals on the scalar EQUALS path — not just the array path.
// Spec Section 4.3 calls for this integration-level coverage explicitly,
// because PR-2's XML import produces json.Number values that flow into
// EQUALS predicates against scalar entity fields, not only into array
// predicates. This test guards against future regressions to opEquals
// or toFloat64 that would silently break the scalar EQUALS path.
func TestOpEquals_JSONNumber(t *testing.T) {
	data := []byte(`{"score":1.5}`)
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.score",
		OperatorType: "EQUALS",
		Value:        json.Number("1.5"),
	}
	got, err := Match(cond, data, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected match: scalar EQUALS with json.Number(\"1.5\") against JSON 1.5")
	}
}
