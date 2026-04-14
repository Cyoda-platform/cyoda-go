package schema_test

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

func TestMergeDisjointChildren(t *testing.T) {
	a := schema.NewObjectNode()
	a.SetChild("name", schema.NewLeafNode(schema.String))

	b := schema.NewObjectNode()
	b.SetChild("age", schema.NewLeafNode(schema.Integer))

	merged := schema.Merge(a, b)
	if merged.Child("name") == nil {
		t.Error("expected 'name' child after merge")
	}
	if merged.Child("age") == nil {
		t.Error("expected 'age' child after merge")
	}
}

func TestMergeOverlappingChildrenUnionTypes(t *testing.T) {
	a := schema.NewObjectNode()
	a.SetChild("score", schema.NewLeafNode(schema.Integer))

	b := schema.NewObjectNode()
	b.SetChild("score", schema.NewLeafNode(schema.String))

	merged := schema.Merge(a, b)
	score := merged.Child("score")
	if score == nil {
		t.Fatal("expected 'score' child after merge")
	}
	types := score.Types().Types()
	if len(types) != 2 {
		t.Fatalf("expected 2 types (polymorphic), got %d: %v", len(types), types)
	}
}

func TestMergeNestedObjects(t *testing.T) {
	a := schema.NewObjectNode()
	addr := schema.NewObjectNode()
	addr.SetChild("city", schema.NewLeafNode(schema.String))
	a.SetChild("address", addr)

	b := schema.NewObjectNode()
	addr2 := schema.NewObjectNode()
	addr2.SetChild("zip", schema.NewLeafNode(schema.String))
	b.SetChild("address", addr2)

	merged := schema.Merge(a, b)
	mergedAddr := merged.Child("address")
	if mergedAddr == nil {
		t.Fatal("expected 'address' child")
	}
	if mergedAddr.Child("city") == nil {
		t.Error("expected 'city' under address")
	}
	if mergedAddr.Child("zip") == nil {
		t.Error("expected 'zip' under address")
	}
}

func TestMergeArrayElementTypes(t *testing.T) {
	elemA := schema.NewLeafNode(schema.Integer)
	a := schema.NewObjectNode()
	a.SetChild("tags", schema.NewArrayNode(elemA))

	elemB := schema.NewLeafNode(schema.String)
	b := schema.NewObjectNode()
	b.SetChild("tags", schema.NewArrayNode(elemB))

	merged := schema.Merge(a, b)
	tags := merged.Child("tags")
	if tags == nil {
		t.Fatal("expected 'tags' child")
	}
	if tags.Element() == nil {
		t.Fatal("expected array element descriptor")
	}
	types := tags.Element().Types().Types()
	if len(types) != 2 {
		t.Fatalf("expected 2 element types, got %d", len(types))
	}
}

func TestMergeKindConflict(t *testing.T) {
	// Object node with a child.
	obj := schema.NewObjectNode()
	obj.SetChild("x", schema.NewLeafNode(schema.String))

	// Array node with an element.
	arr := schema.NewArrayNode(schema.NewLeafNode(schema.Integer))

	merged := schema.Merge(obj, arr)

	if merged.Kind() != schema.KindObject {
		t.Errorf("expected KindObject after object+array merge, got %v", merged.Kind())
	}
	if merged.Element() == nil {
		t.Error("expected element to be preserved after object+array merge")
	}
	if merged.Child("x") == nil {
		t.Error("expected child 'x' to be preserved after object+array merge")
	}
}

func TestMergeArrayInfo(t *testing.T) {
	// First array: width 3, position 0=Integer, position 1=String.
	a := schema.NewArrayNode(schema.NewLeafNode(schema.Integer))
	a.Info().Observe(3)
	a.Info().ObserveElement(0, schema.Integer)
	a.Info().ObserveElement(1, schema.String)

	// Second array: width 5, position 0=String, position 2=Boolean.
	b := schema.NewArrayNode(schema.NewLeafNode(schema.Integer))
	b.Info().Observe(5)
	b.Info().ObserveElement(0, schema.String)
	b.Info().ObserveElement(2, schema.Boolean)

	merged := schema.Merge(a, b)
	info := merged.Info()
	if info == nil {
		t.Fatal("expected merged ArrayInfo to be non-nil")
	}
	if info.MaxWidth() != 5 {
		t.Errorf("expected maxWidth 5, got %d", info.MaxWidth())
	}
	elems := info.Elements()
	if len(elems) != 3 {
		t.Fatalf("expected 3 per-position type sets, got %d", len(elems))
	}
	// Position 0: union of Integer and String → 2 types.
	if len(elems[0].Types()) != 2 {
		t.Errorf("position 0: expected 2 types, got %d: %v", len(elems[0].Types()), elems[0].Types())
	}
	// Position 1: String only (from a).
	if len(elems[1].Types()) != 1 {
		t.Errorf("position 1: expected 1 type, got %d: %v", len(elems[1].Types()), elems[1].Types())
	}
	// Position 2: Boolean only (from b).
	if len(elems[2].Types()) != 1 {
		t.Errorf("position 2: expected 1 type, got %d: %v", len(elems[2].Types()), elems[2].Types())
	}
}

func TestMergeNilInputs(t *testing.T) {
	node := schema.NewObjectNode()
	node.SetChild("x", schema.NewLeafNode(schema.String))

	if got := schema.Merge(nil, node); got.Child("x") == nil {
		t.Error("Merge(nil, node) should return node's structure")
	}
	if got := schema.Merge(node, nil); got.Child("x") == nil {
		t.Error("Merge(node, nil) should return node's structure")
	}
	if got := schema.Merge(nil, nil); got != nil {
		t.Error("Merge(nil, nil) should return nil")
	}
}
