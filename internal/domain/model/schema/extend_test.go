package schema_test

import (
	"strings"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

func TestExtendStructuralNewField(t *testing.T) {
	// STRUCTURAL allows new fields
	existing := schema.NewObjectNode()
	existing.SetChild("name", schema.NewLeafNode(schema.String))
	incoming := schema.NewObjectNode()
	incoming.SetChild("name", schema.NewLeafNode(schema.String))
	incoming.SetChild("age", schema.NewLeafNode(schema.Integer))
	result, err := schema.Extend(existing, incoming, spi.ChangeLevelStructural)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Object().Child("age") == nil {
		t.Error("expected 'age' field")
	}
}

func TestExtendTypeRejectsNewField(t *testing.T) {
	// TYPE does not allow new fields
	existing := schema.NewObjectNode()
	existing.SetChild("name", schema.NewLeafNode(schema.String))
	incoming := schema.NewObjectNode()
	incoming.SetChild("name", schema.NewLeafNode(schema.String))
	incoming.SetChild("age", schema.NewLeafNode(schema.Integer))
	_, err := schema.Extend(existing, incoming, spi.ChangeLevelType)
	if err == nil {
		t.Error("expected error: TYPE should reject new fields")
	}
}

func TestExtendTypeAllowsTypeWidening(t *testing.T) {
	existing := schema.NewObjectNode()
	existing.SetChild("value", schema.NewLeafNode(schema.Integer))
	incoming := schema.NewObjectNode()
	incoming.SetChild("value", schema.NewLeafNode(schema.String))
	result, err := schema.Extend(existing, incoming, spi.ChangeLevelType)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	types := result.Object().Child("value").DeclaredTypes()
	if len(types) != 2 {
		t.Errorf("expected polymorphic, got %v", types)
	}
}

func TestExtendArrayElementsAllowsElementWidening(t *testing.T) {
	existingArr := schema.NewArrayNode(schema.NewLeafNode(schema.Integer))
	existing := schema.NewObjectNode()
	existing.SetChild("scores", existingArr)
	incomingArr := schema.NewArrayNode(schema.NewLeafNode(schema.String))
	incoming := schema.NewObjectNode()
	incoming.SetChild("scores", incomingArr)
	result, err := schema.Extend(existing, incoming, spi.ChangeLevelArrayElements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elemTypes := result.Object().Child("scores").Array().Element().DeclaredTypes()
	if len(elemTypes) != 2 {
		t.Errorf("expected widened, got %v", elemTypes)
	}
}

func TestExtendArrayElementsRejectsLeafTypeWidening(t *testing.T) {
	existing := schema.NewObjectNode()
	existing.SetChild("value", schema.NewLeafNode(schema.Integer))
	incoming := schema.NewObjectNode()
	incoming.SetChild("value", schema.NewLeafNode(schema.String))
	_, err := schema.Extend(existing, incoming, spi.ChangeLevelArrayElements)
	if err == nil {
		t.Error("expected error")
	}
}

func TestExtendArrayLengthAllowsWidthChange(t *testing.T) {
	existingArr := schema.NewArrayNode(schema.NewLeafNode(schema.String))
	existingArr.ObserveArrayWidth(3)
	existing := schema.NewObjectNode()
	existing.SetChild("tags", existingArr)
	incomingArr := schema.NewArrayNode(schema.NewLeafNode(schema.String))
	incomingArr.ObserveArrayWidth(5)
	incoming := schema.NewObjectNode()
	incoming.SetChild("tags", incomingArr)
	result, err := schema.Extend(existing, incoming, spi.ChangeLevelArrayLength)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Object().Child("tags").Array().MaxWidth() != 5 {
		t.Error("expected width 5")
	}
}

func TestExtendArrayLengthRejectsElementTypeChange(t *testing.T) {
	existingArr := schema.NewArrayNode(schema.NewLeafNode(schema.Integer))
	existing := schema.NewObjectNode()
	existing.SetChild("scores", existingArr)
	incomingArr := schema.NewArrayNode(schema.NewLeafNode(schema.String))
	incoming := schema.NewObjectNode()
	incoming.SetChild("scores", incomingArr)
	_, err := schema.Extend(existing, incoming, spi.ChangeLevelArrayLength)
	if err == nil {
		t.Error("expected error")
	}
}

func TestExtendEmptyLevelRejectsAll(t *testing.T) {
	existing := schema.NewObjectNode()
	existing.SetChild("name", schema.NewLeafNode(schema.String))
	incoming := schema.NewObjectNode()
	incoming.SetChild("name", schema.NewLeafNode(schema.String))
	incoming.SetChild("extra", schema.NewLeafNode(schema.Integer))
	_, err := schema.Extend(existing, incoming, "")
	if err == nil {
		t.Error("expected error: empty level rejects all changes")
	}
}

func TestExtendConformingDataNoChange(t *testing.T) {
	existing := schema.NewObjectNode()
	existing.SetChild("name", schema.NewLeafNode(schema.String))
	incoming := schema.NewObjectNode()
	incoming.SetChild("name", schema.NewLeafNode(schema.String))
	result, err := schema.Extend(existing, incoming, spi.ChangeLevelArrayLength)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Object().Child("name") == nil {
		t.Error("expected 'name' preserved")
	}
}

// Extend takes the document now, not a pre-walked model. The two write doors
// share one traversal, so they cannot disagree about what a leaf accepts.
func TestExtend_TakesTheDocument(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("amount", schema.NewLeafNode(schema.Double))

	// Held at every level, including the most restrictive.
	for _, level := range []spi.ChangeLevel{
		spi.ChangeLevelArrayLength, spi.ChangeLevelArrayElements,
		spi.ChangeLevelType, spi.ChangeLevelStructural,
	} {
		got, err := schema.Extend(model, map[string]any{"amount": num("2147483648")}, level)
		if err != nil {
			t.Fatalf("level %s: %v", level, err)
		}
		types := got.Object().Child("amount").DeclaredTypes()
		if len(types) != 1 || types[0] != schema.Double {
			t.Errorf("level %s: amount = %v, want [DOUBLE] unchanged", level, types)
		}
	}
}

// A value the field does not hold still costs its level.
func TestExtend_UnheldValueStillGated(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("amount", schema.NewLeafNode(schema.Double))

	_, err := schema.Extend(model, map[string]any{"amount": num("9007199254740993")}, spi.ChangeLevelArrayLength)
	if err == nil {
		t.Fatal("a 16-significant-digit value is not held by DOUBLE; it must be gated")
	}
	if !strings.Contains(err.Error(), "requires TYPE level") {
		t.Errorf("error must name the level it needs, got %q", err)
	}

	got, err := schema.Extend(model, map[string]any{"amount": num("9007199254740993")}, spi.ChangeLevelType)
	if err != nil {
		t.Fatalf("at TYPE the change is permitted: %v", err)
	}
	if types := got.Object().Child("amount").DeclaredTypes(); len(types) != 1 || types[0] != schema.UnboundDecimal {
		t.Errorf("amount = %v, want [UNBOUND_DECIMAL]", types)
	}
}

// The defect: a verdict about one leaf must not widen another.
func TestExtend_AGatedChangeElsewhereDoesNotWidenAHeldLeaf(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("x", schema.NewLeafNode(schema.Double))
	model.SetChild("y", schema.NewLeafNode(schema.Integer))

	got, err := schema.Extend(model,
		map[string]any{"x": num("2147483648"), "y": num("1.5")}, spi.ChangeLevelType)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if types := got.Object().Child("x").DeclaredTypes(); len(types) != 1 || types[0] != schema.Double {
		t.Errorf("x = %v, want [DOUBLE] — only y changed", types)
	}
}
