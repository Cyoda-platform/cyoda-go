package schema_test

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

func TestRoundTripFlatObject(t *testing.T) {
	node := schema.NewObjectNode()
	node.SetChild("name", schema.NewLeafNode(schema.String))
	node.SetChild("age", schema.NewLeafNode(schema.Integer))

	data, err := schema.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	restored, err := schema.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.Child("name") == nil {
		t.Error("expected 'name' child after round-trip")
	}
	nameTypes := restored.Child("name").Types().Types()
	if len(nameTypes) != 1 || nameTypes[0] != schema.String {
		t.Errorf("expected [STRING], got %v", nameTypes)
	}
	if restored.Child("age") == nil {
		t.Error("expected 'age' child after round-trip")
	}
}

func TestRoundTripNestedWithArray(t *testing.T) {
	elem := schema.NewLeafNode(schema.String)
	arr := schema.NewArrayNode(elem)

	inner := schema.NewObjectNode()
	inner.SetChild("city", schema.NewLeafNode(schema.String))

	root := schema.NewObjectNode()
	root.SetChild("tags", arr)
	root.SetChild("address", inner)

	data, err := schema.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	restored, err := schema.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tags := restored.Child("tags")
	if tags == nil || tags.Kind() != schema.KindArray {
		t.Fatal("expected 'tags' as ARRAY")
	}
	if tags.Element() == nil {
		t.Fatal("expected element descriptor for tags")
	}
	addr := restored.Child("address")
	if addr == nil || addr.Child("city") == nil {
		t.Fatal("expected 'address.city'")
	}
}

func TestRoundTripPolymorphic(t *testing.T) {
	node := schema.NewObjectNode()
	leaf := schema.NewLeafNode(schema.Integer)
	leaf.Types().Add(schema.String)
	node.SetChild("score", leaf)

	data, err := schema.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	restored, err := schema.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	types := restored.Child("score").Types().Types()
	if len(types) != 2 {
		t.Fatalf("expected 2 polymorphic types, got %d", len(types))
	}
}
