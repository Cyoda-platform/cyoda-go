package schema

import (
	"testing"
)

func TestFieldsFlatObject(t *testing.T) {
	root := NewObjectNode()
	root.SetChild("name", NewLeafNode(String))
	root.SetChild("age", NewLeafNode(Integer))

	fields := root.Fields()
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}

	m := root.FieldsMap()
	nameF, ok := m["$.name"]
	if !ok {
		t.Fatal("expected field $.name")
	}
	if len(nameF.Types) != 1 || nameF.Types[0] != String {
		t.Errorf("expected [STRING], got %v", nameF.Types)
	}
	if nameF.IsArray {
		t.Error("$.name should not be an array field")
	}

	ageF, ok := m["$.age"]
	if !ok {
		t.Fatal("expected field $.age")
	}
	if len(ageF.Types) != 1 || ageF.Types[0] != Integer {
		t.Errorf("expected [INTEGER], got %v", ageF.Types)
	}
}

func TestFieldsNestedObject(t *testing.T) {
	root := NewObjectNode()
	address := NewObjectNode()
	address.SetChild("city", NewLeafNode(String))
	root.SetChild("address", address)

	fields := root.Fields()
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if fields[0].Path != "$.address.city" {
		t.Errorf("expected path $.address.city, got %s", fields[0].Path)
	}
}

func TestFieldsArray(t *testing.T) {
	root := NewObjectNode()
	root.SetChild("tags", NewArrayNode(NewLeafNode(String)))

	m := root.FieldsMap()
	f, ok := m["$.tags[*]"]
	if !ok {
		t.Fatal("expected field $.tags[*]")
	}
	if !f.IsArray {
		t.Error("$.tags[*] should be an array field")
	}
	if len(f.Types) != 1 || f.Types[0] != String {
		t.Errorf("expected [STRING], got %v", f.Types)
	}
}

func TestFieldsArrayOfObjects(t *testing.T) {
	root := NewObjectNode()
	item := NewObjectNode()
	item.SetChild("name", NewLeafNode(String))
	item.SetChild("price", NewLeafNode(Double))
	root.SetChild("items", NewArrayNode(item))

	m := root.FieldsMap()
	if _, ok := m["$.items[*].name"]; !ok {
		t.Error("expected field $.items[*].name")
	}
	if _, ok := m["$.items[*].price"]; !ok {
		t.Error("expected field $.items[*].price")
	}
	for _, f := range root.Fields() {
		if f.IsArray {
			t.Errorf("field %s should not be marked as array (leaf inside array)", f.Path)
		}
	}
}

func TestFieldsCachedOnSecondCall(t *testing.T) {
	root := NewObjectNode()
	root.SetChild("x", NewLeafNode(Integer))

	f1 := root.Fields()
	f2 := root.Fields()

	// Cached: same length and first element should be identical.
	if len(f1) != len(f2) {
		t.Fatalf("expected same length, got %d vs %d", len(f1), len(f2))
	}
	if len(f1) > 0 && f1[0].Path != f2[0].Path {
		t.Error("expected Fields() to return cached result on second call")
	}
}

func TestFieldsPolymorphic(t *testing.T) {
	root := NewObjectNode()
	leaf := NewLeafNode(String)
	leaf.Types().Add(Integer)
	root.SetChild("val", leaf)

	m := root.FieldsMap()
	f := m["$.val"]
	if len(f.Types) != 2 {
		t.Fatalf("expected 2 types, got %d", len(f.Types))
	}
}

func TestFieldsMap(t *testing.T) {
	root := NewObjectNode()
	root.SetChild("a", NewLeafNode(Boolean))
	nested := NewObjectNode()
	nested.SetChild("b", NewLeafNode(Long))
	root.SetChild("n", nested)

	m := root.FieldsMap()
	if len(m) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m))
	}
	if _, ok := m["$.a"]; !ok {
		t.Error("missing $.a")
	}
	if _, ok := m["$.n.b"]; !ok {
		t.Error("missing $.n.b")
	}
}

func TestFieldsArrayMaxWidth(t *testing.T) {
	root := NewObjectNode()
	arrNode := NewArrayNode(NewLeafNode(Integer))
	arrNode.Info().Observe(5)
	root.SetChild("nums", arrNode)

	m := root.FieldsMap()
	f := m["$.nums[*]"]
	if f.MaxWidth != 5 {
		t.Errorf("expected MaxWidth 5, got %d", f.MaxWidth)
	}
}
