package schema_test

import (
	"encoding/json"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// num builds a json.Number the way the HTTP decoder does.
func num(s string) any { return json.Number(s) }

// TestAdmit_Leaf is the spec's case table. "Held" means Admit returns no
// change: the field holds the value and the model does not move.
func TestAdmit_Leaf(t *testing.T) {
	cases := []struct {
		name     string
		declared []schema.DataType
		value    any
		held     bool
	}{
		{"double holds 1000", []schema.DataType{schema.Double}, num("1000"), true},
		{"double holds 1000.0", []schema.DataType{schema.Double}, num("1000.0"), true},
		{"double holds 1e3", []schema.DataType{schema.Double}, num("1e3"), true},
		{"double holds a whole past 2^31", []schema.DataType{schema.Double}, num("2147483648"), true},
		{"double holds the ceiling", []schema.DataType{schema.Double}, num("9.99999999999999e292"), true},
		{"double refuses above the ceiling", []schema.DataType{schema.Double}, num("9.99999999999999e300"), false},
		{"double refuses 16 significant digits", []schema.DataType{schema.Double}, num("9007199254740993"), false},
		{"integer holds its ceiling", []schema.DataType{schema.Integer}, num("2147483647"), true},
		{"integer refuses past its ceiling", []schema.DataType{schema.Integer}, num("2147483648"), false},
		{"integer refuses a fraction", []schema.DataType{schema.Integer}, num("13.111"), false},
		{"unbound integer refuses a fraction", []schema.DataType{schema.UnboundInteger}, num("1.5"), false},
		{"big decimal holds a high scale", []schema.DataType{schema.BigDecimal},
			num("1.23456789012345678901234567890"), true},

		{"string holds a date-shaped string", []schema.DataType{schema.String}, "2026-03-01", true},
		{"string holds hello", []schema.DataType{schema.String}, "hello", true},
		{"string refuses a number", []schema.DataType{schema.String}, num("5"), false},
		{"string refuses a boolean", []schema.DataType{schema.String}, true, false},
		{"boolean refuses a string", []schema.DataType{schema.Boolean}, "true", false},
		{"integer refuses a string", []schema.DataType{schema.Integer}, "2024", false},

		{"zoned holds a timestamp", []schema.DataType{schema.ZonedDateTime}, "2026-03-01T10:00:00Z", true},
		{"zoned refuses a year", []schema.DataType{schema.ZonedDateTime}, "2026", false},
		{"local date time refuses an offset", []schema.DataType{schema.LocalDateTime},
			"2026-03-01T10:00:00+05:00", false},

		{"a scalar leaf admits null", []schema.DataType{schema.String}, nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leaf := schema.NewLeafNode(tc.declared[0])
			for _, dt := range tc.declared[1:] {
				leaf.AddScalarTypes(dt)
			}
			overlay, changes, err := schema.Admit(leaf, tc.value)
			if err != nil {
				t.Fatalf("Admit: %v", err)
			}
			held := len(changes) == 0
			if held != tc.held {
				t.Errorf("held = %v, want %v (changes: %+v)", held, tc.held, changes)
			}
			if held && overlay != nil {
				t.Errorf("a held value must produce no overlay, got %v", overlay.DeclaredTypes())
			}
			if !held && overlay == nil {
				t.Error("a change must produce an overlay describing it")
			}
		})
	}
}

// A held value must leave the declared set byte-identical.
func TestAdmit_HeldValueDoesNotMoveTheModel(t *testing.T) {
	leaf := schema.NewLeafNode(schema.Double)
	before := append([]schema.DataType(nil), leaf.DeclaredTypes()...)

	if _, changes, err := schema.Admit(leaf, num("2147483648")); err != nil || len(changes) != 0 {
		t.Fatalf("expected the value to be held, got changes=%+v err=%v", changes, err)
	}

	after := leaf.DeclaredTypes()
	if len(before) != len(after) {
		t.Fatalf("declared types changed: %v -> %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("declared types changed: %v -> %v", before, after)
		}
	}
}

// A change records the level it costs and the context the error needs.
func TestAdmit_LeafChangeCarriesLevelAndProps(t *testing.T) {
	leaf := schema.NewLeafNode(schema.Integer)
	_, changes, err := schema.Admit(leaf, num("2147483648"))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("want 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Reason != schema.ReasonLeafType {
		t.Errorf("Reason = %v, want ReasonLeafType", c.Reason)
	}
	if c.Required != spi.ChangeLevelType {
		t.Errorf("Required = %v, want TYPE", c.Required)
	}
	if c.Observed != schema.Long {
		t.Errorf("Observed = %v, want LONG", c.Observed)
	}
	if len(c.Declared) != 1 || c.Declared[0] != schema.Integer {
		t.Errorf("Declared = %v, want [INTEGER]", c.Declared)
	}
}

// A leaf never admits a container, whatever its declared types.
func TestAdmit_LeafRefusesAContainer(t *testing.T) {
	leaf := schema.NewLeafNode(schema.String)
	for _, v := range []any{map[string]any{"k": "v"}, []any{"a"}} {
		_, changes, err := schema.Admit(leaf, v)
		if err != nil {
			t.Fatalf("Admit: %v", err)
		}
		if len(changes) == 0 {
			t.Errorf("a STRING leaf must not admit %T", v)
		}
		if changes[0].Reason != schema.ReasonNewKind {
			t.Errorf("Reason = %v, want ReasonNewKind", changes[0].Reason)
		}
	}
}
