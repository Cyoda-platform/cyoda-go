package schema_test

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

func TestDataTypeString(t *testing.T) {
	if schema.String.String() != "STRING" {
		t.Errorf("expected STRING, got %s", schema.String)
	}
	if schema.Integer.String() != "INTEGER" {
		t.Errorf("expected INTEGER, got %s", schema.Integer)
	}
}

func TestTypeSetAdd(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.String)
	ts.Add(schema.Integer)
	ts.Add(schema.String) // duplicate

	types := ts.Types()
	if len(types) != 2 {
		t.Fatalf("expected 2 types, got %d", len(types))
	}
}

func TestTypeSetIsSorted(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.String)
	ts.Add(schema.Boolean)
	ts.Add(schema.Integer)

	types := ts.Types()
	for i := 1; i < len(types); i++ {
		if types[i] < types[i-1] {
			t.Errorf("types not sorted: %v", types)
		}
	}
}

func TestTypeSetUnion(t *testing.T) {
	a := schema.NewTypeSet()
	a.Add(schema.String)

	b := schema.NewTypeSet()
	b.Add(schema.Integer)

	c := schema.Union(a, b)
	types := c.Types()
	if len(types) != 2 {
		t.Fatalf("expected 2 types in union, got %d", len(types))
	}
}

func TestParseDataType(t *testing.T) {
	dt, ok := schema.ParseDataType("INTEGER")
	if !ok {
		t.Fatal("expected ParseDataType to find INTEGER")
	}
	if dt != schema.Integer {
		t.Errorf("expected Integer, got %v", dt)
	}

	_, ok = schema.ParseDataType("BOGUS")
	if ok {
		t.Error("expected ParseDataType to return false for unknown name")
	}
}

func TestTypeSetIsEmpty(t *testing.T) {
	ts := schema.NewTypeSet()
	if !ts.IsEmpty() {
		t.Error("new TypeSet should be empty")
	}
	ts.Add(schema.String)
	if ts.IsEmpty() {
		t.Error("TypeSet should not be empty after Add")
	}
}

func TestDataTypeStringUnknown(t *testing.T) {
	unknown := schema.DataType(9999)
	if unknown.String() != "UNKNOWN" {
		t.Errorf("expected UNKNOWN, got %s", unknown.String())
	}
}

func TestTypeSetUnionOverlapping(t *testing.T) {
	a := schema.NewTypeSet()
	a.Add(schema.String)
	a.Add(schema.Integer)

	b := schema.NewTypeSet()
	b.Add(schema.String)
	b.Add(schema.Boolean)

	c := schema.Union(a, b)
	types := c.Types()
	if len(types) != 3 {
		t.Fatalf("expected 3 types in overlapping union, got %d", len(types))
	}
}

func TestTypeSetEqual(t *testing.T) {
	a := schema.NewTypeSet()
	b := schema.NewTypeSet()
	if !a.Equal(b) {
		t.Error("two empty TypeSets should be equal")
	}

	a.Add(schema.String)
	a.Add(schema.Integer)
	b.Add(schema.String)
	b.Add(schema.Integer)
	if !a.Equal(b) {
		t.Error("identical TypeSets should be equal")
	}

	c := schema.NewTypeSet()
	c.Add(schema.Boolean)
	if a.Equal(c) {
		t.Error("different TypeSets should not be equal")
	}
}

func TestTypeSetNumericLatching(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Integer)
	ts.Add(schema.Long)
	types := ts.Types()
	if len(types) != 1 {
		t.Fatalf("expected 1 type after latching, got %d: %v", len(types), types)
	}
	if types[0] != schema.Long {
		t.Errorf("expected Long, got %v", types[0])
	}
}

func TestTypeSetNumericLatchingDecimal(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Double)
	ts.Add(schema.BigDecimal)
	types := ts.Types()
	if len(types) != 1 {
		t.Fatalf("expected 1 type after latching, got %d: %v", len(types), types)
	}
	if types[0] != schema.BigDecimal {
		t.Errorf("expected BigDecimal, got %v", types[0])
	}
}

func TestTypeSetNumericCrossFamily(t *testing.T) {
	ts := schema.NewTypeSet()
	ts.Add(schema.Integer)
	ts.Add(schema.Double)
	types := ts.Types()
	if len(types) != 2 {
		t.Fatalf("expected 2 types (cross-family), got %d: %v", len(types), types)
	}
}

func TestTypeSetNumericLatchingViaUnion(t *testing.T) {
	a := schema.NewTypeSet()
	a.Add(schema.Integer)
	b := schema.NewTypeSet()
	b.Add(schema.Long)
	c := schema.Union(a, b)
	types := c.Types()
	if len(types) != 1 {
		t.Fatalf("expected 1 type after union latching, got %d: %v", len(types), types)
	}
	if types[0] != schema.Long {
		t.Errorf("expected Long, got %v", types[0])
	}
}

func TestTypeSetIsPolymorphic(t *testing.T) {
	mono := schema.NewTypeSet()
	mono.Add(schema.String)
	if mono.IsPolymorphic() {
		t.Error("single type should not be polymorphic")
	}

	poly := schema.NewTypeSet()
	poly.Add(schema.String)
	poly.Add(schema.Integer)
	if !poly.IsPolymorphic() {
		t.Error("two types should be polymorphic")
	}
}
