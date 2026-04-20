package schema

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type pathRel string

const (
	relDisjoint pathRel = "disjoint"
	relEqual    pathRel = "equal"
	relPrefix   pathRel = "prefix"
)

// sample bundles a base tree and two ops to be compared under commutativity.
type sample struct {
	name string
	base func() *ModelNode
	opA  func(t *testing.T) SchemaOp
	opB  func(t *testing.T) SchemaOp
}

// TestCommutativity_ByKindPairAndPathRelationship covers
// kind × kind × path-relationship axes. Symmetric pairs are covered
// once (e.g. property+broaden = broaden+property). Impossible combos
// (equal-path across incompatible kinds) are skipped.
func TestCommutativity_ByKindPairAndPathRelationship(t *testing.T) {
	samples := commutativitySamples(t)
	for _, s := range samples {
		t.Run(s.name, func(t *testing.T) {
			assertCommutative(t, s.base(), s.opA(t), s.opB(t))
		})
	}
}

func commutativitySamples(t *testing.T) []sample {
	t.Helper()
	return []sample{
		// ---- property × property ----
		{
			name: "property×property/disjoint",
			base: func() *ModelNode {
				root := NewObjectNode()
				root.SetChild("name", NewLeafNode(String))
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				return NewAddProperty("", "a1", marshalLeaf(t, Integer))
			},
			opB: func(t *testing.T) SchemaOp {
				return NewAddProperty("", "a2", marshalLeaf(t, String))
			},
		},
		{
			name: "property×property/equal",
			base: func() *ModelNode { return NewObjectNode() },
			// Same name, different subtree leaf type: both calls target the
			// same child; Merge must be associative/idempotent under union.
			opA: func(t *testing.T) SchemaOp {
				return NewAddProperty("", "x", marshalLeaf(t, String))
			},
			opB: func(t *testing.T) SchemaOp {
				return NewAddProperty("", "x", marshalLeaf(t, Integer))
			},
		},
		{
			name: "property×property/prefix",
			// Pre-seed /addr with a placeholder child so resolvePath
			// succeeds in both orders. A merges a new leaf /addr/zip;
			// B adds a sibling property /addr/city.
			base: func() *ModelNode {
				root := NewObjectNode()
				addr := NewObjectNode()
				addr.SetChild("_seed", NewLeafNode(String))
				root.SetChild("addr", addr)
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				return NewAddProperty("addr", "zip", marshalLeaf(t, String))
			},
			opB: func(t *testing.T) SchemaOp {
				return NewAddProperty("addr", "city", marshalLeaf(t, String))
			},
		},

		// ---- broaden × broaden ----
		{
			name: "broaden×broaden/disjoint",
			base: func() *ModelNode {
				root := NewObjectNode()
				root.SetChild("a", NewLeafNode(String))
				root.SetChild("b", NewLeafNode(String))
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				op, err := NewBroadenType("a", []DataType{Null})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
			opB: func(t *testing.T) SchemaOp {
				op, err := NewBroadenType("b", []DataType{Null})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
		},
		{
			name: "broaden×broaden/equal",
			base: func() *ModelNode {
				root := NewObjectNode()
				root.SetChild("x", NewLeafNode(String))
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				op, err := NewBroadenType("x", []DataType{Null})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
			opB: func(t *testing.T) SchemaOp {
				op, err := NewBroadenType("x", []DataType{Boolean})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
		},

		// ---- add_array_item_type × add_array_item_type ----
		{
			name: "arrayItem×arrayItem/disjoint",
			base: func() *ModelNode {
				root := NewObjectNode()
				root.SetChild("tags", NewArrayNode(NewLeafNode(Integer)))
				root.SetChild("other", NewArrayNode(NewLeafNode(String)))
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				op, err := NewAddArrayItemType("tags", []DataType{String})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
			opB: func(t *testing.T) SchemaOp {
				op, err := NewAddArrayItemType("other", []DataType{Null})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
		},
		{
			name: "arrayItem×arrayItem/equal",
			base: func() *ModelNode {
				root := NewObjectNode()
				root.SetChild("tags", NewArrayNode(NewLeafNode(Integer)))
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				op, err := NewAddArrayItemType("tags", []DataType{String})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
			opB: func(t *testing.T) SchemaOp {
				op, err := NewAddArrayItemType("tags", []DataType{Null})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
		},

		// ---- property × broaden (cross-kind, disjoint sibling paths) ----
		{
			name: "property×broaden/disjoint",
			base: func() *ModelNode {
				root := NewObjectNode()
				root.SetChild("age", NewLeafNode(String))
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				return NewAddProperty("", "email", marshalLeaf(t, String))
			},
			opB: func(t *testing.T) SchemaOp {
				op, err := NewBroadenType("age", []DataType{Null})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
		},

		// ---- property × broaden (prefix: property merges more types into
		// /addr/zip's LEAF; broaden widens the same leaf with NULL) ----
		{
			name: "property×broaden/prefix",
			base: func() *ModelNode {
				root := NewObjectNode()
				addr := NewObjectNode()
				addr.SetChild("zip", NewLeafNode(String))
				root.SetChild("addr", addr)
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				// Merge INTEGER into /addr/zip's existing LEAF.
				return NewAddProperty("addr", "zip", marshalLeaf(t, Integer))
			},
			opB: func(t *testing.T) SchemaOp {
				op, err := NewBroadenType("addr/zip", []DataType{Null})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
		},

		// ---- broaden × arrayItem (disjoint: widen leaf in one subtree,
		// widen array-element in another) ----
		{
			name: "broaden×arrayItem/disjoint",
			base: func() *ModelNode {
				root := NewObjectNode()
				root.SetChild("age", NewLeafNode(String))
				root.SetChild("tags", NewArrayNode(NewLeafNode(Integer)))
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				op, err := NewBroadenType("age", []DataType{Null})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
			opB: func(t *testing.T) SchemaOp {
				op, err := NewAddArrayItemType("tags", []DataType{String})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
		},

		// ---- property × arrayItem (disjoint: add sibling object property;
		// widen element type of an unrelated array) ----
		{
			name: "property×arrayItem/disjoint",
			base: func() *ModelNode {
				root := NewObjectNode()
				root.SetChild("tags", NewArrayNode(NewLeafNode(Integer)))
				return root
			},
			opA: func(t *testing.T) SchemaOp {
				return NewAddProperty("", "name", marshalLeaf(t, String))
			},
			opB: func(t *testing.T) SchemaOp {
				op, err := NewAddArrayItemType("tags", []DataType{String})
				if err != nil {
					t.Fatalf("%v", err)
				}
				return op
			},
		},
	}
}

// Silence unused-const warnings on pathRel enum. The strings inform
// sample naming; they don't appear in sample structs directly.
var _ = []pathRel{relDisjoint, relEqual, relPrefix}

func assertCommutative(t *testing.T, base *ModelNode, opA, opB SchemaOp) {
	t.Helper()
	dA, err := MarshalDelta([]SchemaOp{opA})
	if err != nil {
		t.Fatalf("MarshalDelta A: %v", err)
	}
	dB, err := MarshalDelta([]SchemaOp{opB})
	if err != nil {
		t.Fatalf("MarshalDelta B: %v", err)
	}

	ab, err := Apply(base, dA)
	if err != nil {
		t.Fatalf("Apply A: %v", err)
	}
	ab, err = Apply(ab, dB)
	if err != nil {
		t.Fatalf("Apply B-after-A: %v", err)
	}

	ba, err := Apply(base, dB)
	if err != nil {
		t.Fatalf("Apply B: %v", err)
	}
	ba, err = Apply(ba, dA)
	if err != nil {
		t.Fatalf("Apply A-after-B: %v", err)
	}

	abBytes, err := Marshal(ab)
	if err != nil {
		t.Fatalf("Marshal ab: %v", err)
	}
	baBytes, err := Marshal(ba)
	if err != nil {
		t.Fatalf("Marshal ba: %v", err)
	}
	if string(abBytes) != string(baBytes) {
		t.Errorf("not commutative:\nA then B: %s\nB then A: %s", abBytes, baBytes)
	}
}

// ---- helpers ----

func marshalLeaf(t *testing.T, dt DataType) []byte {
	t.Helper()
	leaf := NewLeafNode(dt)
	return mustMarshalNode(t, leaf)
}

// Ensure SchemaDelta type is in scope in this file.
var _ spi.SchemaDelta
