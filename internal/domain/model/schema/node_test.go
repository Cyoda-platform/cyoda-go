package schema_test

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

func TestNewObjectNode(t *testing.T) {
	node := schema.NewObjectNode()
	if node.Kind() != schema.KindObject {
		t.Errorf("expected OBJECT, got %v", node.Kind())
	}
	if len(node.Children()) != 0 {
		t.Error("new object node should have no children")
	}
}

func TestNewLeafNode(t *testing.T) {
	node := schema.NewLeafNode(schema.String)
	if node.Kind() != schema.KindLeaf {
		t.Errorf("expected LEAF, got %v", node.Kind())
	}
	types := node.Types().Types()
	if len(types) != 1 || types[0] != schema.String {
		t.Errorf("expected [STRING], got %v", types)
	}
}

func TestNewArrayNode(t *testing.T) {
	elem := schema.NewLeafNode(schema.Integer)
	node := schema.NewArrayNode(elem)
	if node.Kind() != schema.KindArray {
		t.Errorf("expected ARRAY, got %v", node.Kind())
	}
	if node.Element() == nil {
		t.Fatal("array node should have an element descriptor")
	}
}

func TestObjectNodeAddChild(t *testing.T) {
	root := schema.NewObjectNode()
	child := schema.NewLeafNode(schema.String)
	root.SetChild("name", child)

	got := root.Child("name")
	if got == nil {
		t.Fatal("expected child 'name'")
	}
	if got.Kind() != schema.KindLeaf {
		t.Errorf("expected LEAF, got %v", got.Kind())
	}
}

func TestObserveElement(t *testing.T) {
	info := schema.NewArrayInfo()
	info.ObserveElement(0, schema.String)
	info.ObserveElement(1, schema.Integer)
	info.ObserveElement(2, schema.Boolean)

	elems := info.Elements()
	if len(elems) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(elems))
	}
	if types := elems[0].Types(); len(types) != 1 || types[0] != schema.String {
		t.Errorf("element 0: expected [STRING], got %v", types)
	}
	if types := elems[1].Types(); len(types) != 1 || types[0] != schema.Integer {
		t.Errorf("element 1: expected [INTEGER], got %v", types)
	}
	if types := elems[2].Types(); len(types) != 1 || types[0] != schema.Boolean {
		t.Errorf("element 2: expected [BOOLEAN], got %v", types)
	}
}

func TestElementsReturnsCopy(t *testing.T) {
	info := schema.NewArrayInfo()
	info.ObserveElement(0, schema.String)

	elems := info.Elements()
	elems[0] = nil // mutate returned slice

	original := info.Elements()
	if original[0] == nil {
		t.Error("mutating returned Elements slice should not affect the original")
	}
}

func TestIsUniformTrue(t *testing.T) {
	info := schema.NewArrayInfo()
	info.ObserveElement(0, schema.String)
	info.ObserveElement(1, schema.String)
	info.ObserveElement(2, schema.String)

	if !info.IsUniform() {
		t.Error("expected IsUniform true when all positions have same type")
	}
}

func TestIsUniformFalse(t *testing.T) {
	info := schema.NewArrayInfo()
	info.ObserveElement(0, schema.String)
	info.ObserveElement(1, schema.Integer)

	if info.IsUniform() {
		t.Error("expected IsUniform false when positions have different types")
	}
}

func TestIsUniformEmpty(t *testing.T) {
	info := schema.NewArrayInfo()
	if !info.IsUniform() {
		t.Error("expected IsUniform true on fresh ArrayInfo")
	}
}

func TestArrayNodeInfo(t *testing.T) {
	elem := schema.NewLeafNode(schema.Integer)
	node := schema.NewArrayNode(elem)
	if node.Info() == nil {
		t.Fatal("expected non-nil Info() on array node")
	}
}

func TestArrayInfoTracksWidth(t *testing.T) {
	info := schema.NewArrayInfo()
	info.Observe(3)
	if info.MaxWidth() != 3 {
		t.Errorf("expected max width 3, got %d", info.MaxWidth())
	}
	info.Observe(5)
	if info.MaxWidth() != 5 {
		t.Errorf("expected max width 5, got %d", info.MaxWidth())
	}
}
