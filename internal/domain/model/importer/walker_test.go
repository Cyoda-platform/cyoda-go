package importer_test

import (
	"encoding/json"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

func TestWalkFlatObject(t *testing.T) {
	data := map[string]any{
		"name": "Alice",
		"age":  float64(30),
	}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.Kind() != schema.KindObject {
		t.Fatalf("expected OBJECT, got %v", node.Kind())
	}
	nameChild := node.Child("name")
	if nameChild == nil {
		t.Fatal("expected 'name' child")
	}
	types := nameChild.Types().Types()
	if len(types) != 1 || types[0] != schema.String {
		t.Errorf("expected [STRING], got %v", types)
	}
}

func TestWalkNestedObject(t *testing.T) {
	data := map[string]any{
		"address": map[string]any{
			"city": "Berlin",
			"zip":  "10115",
		},
	}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	addr := node.Child("address")
	if addr == nil {
		t.Fatal("expected 'address' child")
	}
	if addr.Kind() != schema.KindObject {
		t.Errorf("expected OBJECT, got %v", addr.Kind())
	}
	if addr.Child("city") == nil {
		t.Error("expected 'city' under address")
	}
}

func TestWalkArray(t *testing.T) {
	data := map[string]any{
		"tags": []any{"a", "b", "c"},
	}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tags := node.Child("tags")
	if tags == nil {
		t.Fatal("expected 'tags' child")
	}
	if tags.Kind() != schema.KindArray {
		t.Errorf("expected ARRAY, got %v", tags.Kind())
	}
	if tags.Element() == nil {
		t.Fatal("expected element descriptor")
	}
	elemTypes := tags.Element().Types().Types()
	if len(elemTypes) != 1 || elemTypes[0] != schema.String {
		t.Errorf("expected [STRING] elements, got %v", elemTypes)
	}
}

func TestWalkArrayOfObjects(t *testing.T) {
	data := map[string]any{
		"items": []any{
			map[string]any{"name": "x"},
			map[string]any{"name": "y", "price": float64(10)},
		},
	}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items := node.Child("items")
	if items == nil || items.Kind() != schema.KindArray {
		t.Fatal("expected 'items' as ARRAY")
	}
	elem := items.Element()
	if elem == nil || elem.Kind() != schema.KindObject {
		t.Fatal("expected element to be OBJECT")
	}
	if elem.Child("name") == nil {
		t.Error("expected 'name' in array element")
	}
	if elem.Child("price") == nil {
		t.Error("expected 'price' in array element (merged from second item)")
	}
}

func TestWalkBoolean(t *testing.T) {
	data := map[string]any{"active": true}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	active := node.Child("active")
	types := active.Types().Types()
	if len(types) != 1 || types[0] != schema.Boolean {
		t.Errorf("expected [BOOLEAN], got %v", types)
	}
}

func TestWalkNumericInferenceWithDefaultScope(t *testing.T) {
	// Transitional walker (A.1 Task 13): all float64 inputs classify as Double.
	// Task 15 will replace this with the full ClassifyInteger/ClassifyDecimal path.
	tests := []struct {
		name     string
		value    float64
		expected schema.DataType
	}{
		{"127 → Double", 127, schema.Double},
		{"128 → Double", 128, schema.Double},
		{"32767 → Double", 32767, schema.Double},
		{"32768 → Double", 32768, schema.Double},
		{"2147483647 → Double", 2147483647, schema.Double},
		{"2147483648 → Double", 2147483648, schema.Double},
		{"-129 → Double", -129, schema.Double},
		{"1.5 → Double", 1.5, schema.Double},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]any{"v": tc.value}
			node, err := importer.Walk(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			types := node.Child("v").Types().Types()
			if len(types) != 1 || types[0] != tc.expected {
				t.Errorf("expected [%v], got %v", tc.expected, types)
			}
		})
	}
}

func TestWalkEmptyArray(t *testing.T) {
	data := map[string]any{"items": []any{}}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items := node.Child("items")
	if items == nil || items.Kind() != schema.KindArray {
		t.Fatal("expected 'items' as ARRAY")
	}
	elem := items.Element()
	if elem == nil {
		t.Fatal("expected element descriptor")
	}
	elemTypes := elem.Types().Types()
	if len(elemTypes) != 1 || elemTypes[0] != schema.Null {
		t.Errorf("expected [NULL] element type, got %v", elemTypes)
	}
}

func TestWalkJsonNumber(t *testing.T) {
	// With default scope (intScope=INTEGER), 42 is clamped to INTEGER.
	data := map[string]any{"v": json.Number("42")}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	types := node.Child("v").Types().Types()
	if len(types) != 1 || types[0] != schema.Integer {
		t.Errorf("expected [INTEGER], got %v", types)
	}
}

func TestWalkJsonNumberLarge(t *testing.T) {
	// Transitional walker (A.1 Task 13): any int64-fitting integer classifies
	// as Integer (no magnitude discrimination). Task 15 replaces this with the
	// full ClassifyInteger path that will promote 2^53+1 to Long.
	data := map[string]any{"v": json.Number("9007199254740993")}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	types := node.Child("v").Types().Types()
	if len(types) != 1 || types[0] != schema.Integer {
		t.Errorf("expected [INTEGER], got %v", types)
	}
}

func TestWalkJsonNumberBigInteger(t *testing.T) {
	// Value exceeds int64 range → BigInteger.
	data := map[string]any{"v": json.Number("99999999999999999999")}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	types := node.Child("v").Types().Types()
	if len(types) != 1 || types[0] != schema.BigInteger {
		t.Errorf("expected [BIG_INTEGER], got %v", types)
	}
}

func TestWalkJsonNumberDecimal(t *testing.T) {
	data := map[string]any{"v": json.Number("3.14")}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	types := node.Child("v").Types().Types()
	if len(types) != 1 || types[0] != schema.Double {
		t.Errorf("expected [DOUBLE], got %v", types)
	}
}

func TestWalkUnsupportedType(t *testing.T) {
	data := map[string]any{"x": struct{}{}}
	_, err := importer.Walk(data)
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestWalkNull(t *testing.T) {
	data := map[string]any{"missing": nil}
	node, err := importer.Walk(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	missing := node.Child("missing")
	types := missing.Types().Types()
	if len(types) != 1 || types[0] != schema.Null {
		t.Errorf("expected [NULL], got %v", types)
	}
}
