package search

import (
	"errors"
	"testing"

	"github.com/cyoda-platform/cyoda-go-spi/predicate"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// buildDoubleModel returns a minimal ModelNode with a single DOUBLE leaf at $.price.
func buildDoubleModel() *schema.ModelNode {
	node := schema.NewObjectNode()
	node.SetChild("price", schema.NewLeafNode(schema.Double))
	return node
}

// ---------------------------------------------------------------------------
// BETWEEN — composite array values
// ---------------------------------------------------------------------------

// TestValidateConditionTypes_Between_TypeMismatch verifies that a BETWEEN
// condition with string values against a DOUBLE field is rejected as a type
// mismatch (HTTP 400 CONDITION_TYPE_MISMATCH path).
func TestValidateConditionTypes_Between_TypeMismatch(t *testing.T) {
	model := buildDoubleModel()
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.price",
		OperatorType: "BETWEEN",
		Value:        []any{"abc", "def"},
	}
	err := ValidateConditionValueTypes(model, cond)
	if err == nil {
		t.Fatal("expected error for string values against DOUBLE BETWEEN condition, got nil")
	}
	if !errors.Is(err, errConditionTypeMismatch) {
		t.Errorf("expected errConditionTypeMismatch sentinel, got: %v", err)
	}
}

// TestValidateConditionTypes_Between_ValidIntegers verifies that a BETWEEN
// condition with numeric values against a DOUBLE field is accepted.
func TestValidateConditionTypes_Between_ValidIntegers(t *testing.T) {
	model := buildDoubleModel()
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.price",
		OperatorType: "BETWEEN",
		// float64 is what json.Unmarshal produces for numbers
		Value: []any{float64(10), float64(20)},
	}
	err := ValidateConditionValueTypes(model, cond)
	if err != nil {
		t.Fatalf("expected no error for numeric BETWEEN values against DOUBLE field, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Mixed-type array values (IN semantics)
// ---------------------------------------------------------------------------

// TestValidateConditionTypes_In_TypeMismatch verifies that an array containing
// a string element against a DOUBLE field is rejected.
func TestValidateConditionTypes_In_TypeMismatch(t *testing.T) {
	model := buildDoubleModel()
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.price",
		OperatorType: "EQUALS",
		Value:        []any{float64(1), "abc", float64(3)},
	}
	err := ValidateConditionValueTypes(model, cond)
	if err == nil {
		t.Fatal("expected error for mixed-type array value against DOUBLE field, got nil")
	}
	if !errors.Is(err, errConditionTypeMismatch) {
		t.Errorf("expected errConditionTypeMismatch sentinel, got: %v", err)
	}
}

// TestValidateConditionTypes_In_AllInts verifies that an array of all-numeric
// values against a DOUBLE field is accepted.
func TestValidateConditionTypes_In_AllInts(t *testing.T) {
	model := buildDoubleModel()
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.price",
		OperatorType: "EQUALS",
		Value:        []any{float64(1), float64(2), float64(3)},
	}
	err := ValidateConditionValueTypes(model, cond)
	if err != nil {
		t.Fatalf("expected no error for all-numeric array value against DOUBLE field, got: %v", err)
	}
}

// TestValidateConditionTypes_EmptyArray_Accepted verifies that an empty array
// value is accepted without error (no elements to mismatch).
func TestValidateConditionTypes_EmptyArray_Accepted(t *testing.T) {
	model := buildDoubleModel()
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.price",
		OperatorType: "BETWEEN",
		Value:        []any{},
	}
	err := ValidateConditionValueTypes(model, cond)
	if err != nil {
		t.Fatalf("expected no error for empty array value, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Object values — never valid for any operator
// ---------------------------------------------------------------------------

// TestValidateConditionTypes_ObjectValue_Rejects verifies that an object value
// (map[string]any) is rejected for any operator type against any field.
func TestValidateConditionTypes_ObjectValue_Rejects(t *testing.T) {
	model := buildDoubleModel()
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.price",
		OperatorType: "EQUALS",
		Value:        map[string]any{"foo": float64(1)},
	}
	err := ValidateConditionValueTypes(model, cond)
	if err == nil {
		t.Fatal("expected error for object value against any operator, got nil")
	}
	if !errors.Is(err, errConditionTypeMismatch) {
		t.Errorf("expected errConditionTypeMismatch sentinel, got: %v", err)
	}
}

// TestValidateConditionTypes_ObjectValue_StringField_Rejects verifies that
// object values are rejected even for string fields (object is never valid
// for any search operator).
func TestValidateConditionTypes_ObjectValue_StringField_Rejects(t *testing.T) {
	node := schema.NewObjectNode()
	node.SetChild("name", schema.NewLeafNode(schema.String))
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        map[string]any{"nested": "value"},
	}
	err := ValidateConditionValueTypes(node, cond)
	if err == nil {
		t.Fatal("expected error for object value against string field, got nil")
	}
	if !errors.Is(err, errConditionTypeMismatch) {
		t.Errorf("expected errConditionTypeMismatch sentinel, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Null element inside array — accepted (null compatible with any type)
// ---------------------------------------------------------------------------

// TestValidateConditionTypes_ArrayWithNullElement_Accepted verifies that a nil
// element inside an array value is accepted (null is compatible with any type).
func TestValidateConditionTypes_ArrayWithNullElement_Accepted(t *testing.T) {
	model := buildDoubleModel()
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.price",
		OperatorType: "BETWEEN",
		Value:        []any{float64(10), nil},
	}
	err := ValidateConditionValueTypes(model, cond)
	if err != nil {
		t.Fatalf("expected no error for array with null element, got: %v", err)
	}
}
