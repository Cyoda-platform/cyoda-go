package schema_test

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// A value whose classified type is ASSIGNABLE to a type the leaf already
// declares does not change the model's admitted value space, so it must not
// consume any ChangeLevel permission. The write path classifies a whole
// number as INTEGER by value alone; against a DOUBLE-declared leaf that is
// not a type change — INTEGER widens to DOUBLE in the lattice Merge and
// Validate both already use — and the model below TYPE level must still
// accept it.
func TestExtend_AssignableScalar_IsNotATypeChange(t *testing.T) {
	for _, level := range []spi.ChangeLevel{
		spi.ChangeLevelArrayLength,
		spi.ChangeLevelArrayElements,
		spi.ChangeLevelType,
		spi.ChangeLevelStructural,
	} {
		t.Run(string(level), func(t *testing.T) {
			existing := schema.NewObjectNode()
			existing.SetChild("amount", schema.NewLeafNode(schema.Double))
			doc := map[string]any{"amount": num("42")}

			result, err := schema.Extend(existing, doc, level)
			if err != nil {
				t.Fatalf("a whole number is assignable to a DOUBLE leaf, it must not need permission: %v", err)
			}
			got := result.Object().Child("amount").DeclaredTypes()
			if len(got) != 1 || got[0] != schema.Double {
				t.Errorf("the leaf must still declare exactly [DOUBLE], got %v", got)
			}
		})
	}
}

// The converse must keep costing what it costs: a value with a fractional
// part is not assignable to an INTEGER-declared leaf, so it is a genuine
// type change and stays gated.
func TestExtend_NonAssignableScalar_StillRequiresTypeLevel(t *testing.T) {
	existing := schema.NewObjectNode()
	existing.SetChild("count", schema.NewLeafNode(schema.Integer))
	doc := map[string]any{"count": num("1.5")}

	if _, err := schema.Extend(existing, doc, spi.ChangeLevelArrayLength); err == nil {
		t.Fatal("a fractional value into an INTEGER leaf widens the declared set; it must stay a gated type change")
	}
	result, err := schema.Extend(existing, doc, spi.ChangeLevelType)
	if err != nil {
		t.Fatalf("TYPE level permits the widening: %v", err)
	}
	got := result.Object().Child("count").DeclaredTypes()
	if len(got) != 1 || got[0] != schema.Double {
		t.Errorf("expected the collapsed [DOUBLE], got %v", got)
	}
}

// The same gate governs an array's element, where the level in play is
// ARRAY_ELEMENTS. A whole number into a DOUBLE-declared element is not a
// change there either, so even ARRAY_LENGTH must accept it.
func TestExtend_AssignableArrayElement_IsNotATypeChange(t *testing.T) {
	existing := schema.NewObjectNode()
	existing.SetChild("amounts", schema.NewArrayNode(schema.NewLeafNode(schema.Double)))
	doc := map[string]any{"amounts": []any{num("42")}}

	result, err := schema.Extend(existing, doc, spi.ChangeLevelArrayLength)
	if err != nil {
		t.Fatalf("a whole number is assignable to a DOUBLE element: %v", err)
	}
	got := result.Object().Child("amounts").Array().Element().DeclaredTypes()
	if len(got) != 1 || got[0] != schema.Double {
		t.Errorf("the element must still declare exactly [DOUBLE], got %v", got)
	}
}

// A guard, not a regression probe: this passed before the gate changed and
// must keep passing. A JSON null against a leaf that already declares a
// scalar admits it directly (Admit's null case: model.Scalar() != nil) and
// records no change at all, so it never reaches the leaf gate, and the level
// comparison the old test's incoming-Null-leaf label implied is unreachable
// under the new rule for exactly this reason — null costs nothing here
// because the branch is never entered, not through assignability.
func TestExtend_NullIntoDeclaredScalar_CostsNothing(t *testing.T) {
	existing := schema.NewObjectNode()
	existing.SetChild("amount", schema.NewLeafNode(schema.Double))
	doc := map[string]any{"amount": nil}

	result, err := schema.Extend(existing, doc, spi.ChangeLevelArrayLength)
	if err != nil {
		t.Fatalf("null is assignable to any declared type: %v", err)
	}
	got := result.Object().Child("amount").DeclaredTypes()
	if len(got) != 1 || got[0] != schema.Double {
		t.Errorf("the leaf must still declare exactly [DOUBLE], got %v", got)
	}
}

// The boundary the relaxation stops at, and the reason it is not "whole
// numbers are free on a DOUBLE leaf": classification is by magnitude, and
// only INTEGER widens into DOUBLE. A whole number past 2^31 classifies LONG,
// whose 2^63 range exceeds DOUBLE's 53-bit mantissa — the lattice refuses
// that conversion deliberately — so it is a real type change, refused below
// TYPE and collapsing the leaf to UNBOUND_DECIMAL at it. Pinning this stops a
// later "any whole number is fine" simplification from silently reshaping
// stored data.
func TestExtend_WholeNumberPastIntegerRange_IsStillATypeChange(t *testing.T) {
	build := func() (*schema.ModelNode, *schema.ModelNode) {
		existing := schema.NewObjectNode()
		existing.SetChild("amount", schema.NewLeafNode(schema.Double))
		incoming := schema.NewObjectNode()
		incoming.SetChild("amount", schema.NewLeafNode(schema.Long))
		return existing, incoming
	}

	existing, incoming := build()
	if _, err := schema.Extend(existing, incoming, spi.ChangeLevelArrayLength); err == nil {
		t.Fatal("LONG does not widen into DOUBLE; it must stay a gated type change")
	}

	existing, incoming = build()
	result, err := schema.Extend(existing, incoming, spi.ChangeLevelType)
	if err != nil {
		t.Fatalf("TYPE level permits it: %v", err)
	}
	got := result.Object().Child("amount").DeclaredTypes()
	if len(got) != 1 || got[0] != schema.UnboundDecimal {
		t.Errorf("DOUBLE and LONG share no common type below UNBOUND_DECIMAL, got %v", got)
	}
}
