package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

func TestValidateConforming(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("name", schema.NewLeafNode(schema.String))
	model.SetChild("age", schema.NewLeafNode(schema.Integer))

	data := map[string]any{"name": "Alice", "age": float64(30)}
	errs := schema.Validate(model, data)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateTypeMismatch(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("age", schema.NewLeafNode(schema.Integer))

	data := map[string]any{"age": "not a number"}
	errs := schema.Validate(model, data)
	if len(errs) == 0 {
		t.Error("expected validation error for type mismatch")
	}
}

func TestValidateExtraFieldRejected(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("name", schema.NewLeafNode(schema.String))

	data := map[string]any{"name": "Alice", "extra": "field"}
	errs := schema.Validate(model, data)
	if len(errs) == 0 {
		t.Error("expected validation error for extra field, got none")
	}
}

func TestValidateNestedObject(t *testing.T) {
	inner := schema.NewObjectNode()
	inner.SetChild("city", schema.NewLeafNode(schema.String))
	model := schema.NewObjectNode()
	model.SetChild("address", inner)

	data := map[string]any{
		"address": map[string]any{"city": 12345},
	}
	errs := schema.Validate(model, data)
	if len(errs) == 0 {
		t.Error("expected validation error for nested type mismatch")
	}
}

func TestValidatePolymorphicAcceptsBothTypes(t *testing.T) {
	leaf := schema.NewLeafNode(schema.Integer)
	leaf.Types().Add(schema.String)
	model := schema.NewObjectNode()
	model.SetChild("value", leaf)

	// Integer should pass
	errs := schema.Validate(model, map[string]any{"value": float64(42)})
	if len(errs) != 0 {
		t.Errorf("integer should be accepted: %v", errs)
	}

	// String should pass
	errs = schema.Validate(model, map[string]any{"value": "hello"})
	if len(errs) != 0 {
		t.Errorf("string should be accepted: %v", errs)
	}

	// Boolean should fail
	errs = schema.Validate(model, map[string]any{"value": true})
	if len(errs) == 0 {
		t.Error("boolean should be rejected for [INTEGER, STRING] field")
	}
}

func TestValidateArray(t *testing.T) {
	elemModel := schema.NewLeafNode(schema.String)
	arrModel := schema.NewArrayNode(elemModel)
	model := schema.NewObjectNode()
	model.SetChild("tags", arrModel)

	// Valid array
	errs := schema.Validate(model, map[string]any{"tags": []any{"a", "b"}})
	if len(errs) != 0 {
		t.Errorf("valid array should pass: %v", errs)
	}

	// Invalid element type
	errs = schema.Validate(model, map[string]any{"tags": []any{"a", float64(1)}})
	if len(errs) == 0 {
		t.Error("expected error for invalid array element type")
	}
}

func TestValidateWrongStructure(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("name", schema.NewLeafNode(schema.String))

	// Passing array where object expected
	errs := schema.Validate(model, []any{"not", "an", "object"})
	if len(errs) == 0 {
		t.Error("expected error for wrong top-level structure")
	}
}

func TestValidateNullCompatible(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("name", schema.NewLeafNode(schema.String))

	// Null should be compatible with any type
	errs := schema.Validate(model, map[string]any{"name": nil})
	if len(errs) != 0 {
		t.Errorf("null should be compatible with STRING: %v", errs)
	}
}

// TestValidateJSONNumberAgainstNumeric — XML and JSON importers both
// produce json.Number for numeric leaves (after issue #24 PR-2).
// inferDataType must classify json.Number as numeric, otherwise
// validation falsely rejects every numeric XML/JSON-imported field.
func TestValidateJSONNumberAgainstNumeric(t *testing.T) {
	model := schema.NewObjectNode()
	model.SetChild("age", schema.NewLeafNode(schema.Integer))
	model.SetChild("rate", schema.NewLeafNode(schema.Double))
	model.SetChild("big", schema.NewLeafNode(schema.Long))

	data := map[string]any{
		"age":  json.Number("30"),
		"rate": json.Number("3.14"),
		"big":  json.Number("9007199254740993"), // > 2^53
	}
	errs := schema.Validate(model, data)
	if len(errs) != 0 {
		t.Errorf("json.Number should be compatible with numeric model types, got: %v", errs)
	}
}
