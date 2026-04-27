package schema

import "testing"

func TestValidationError_ErrKindUnknownElement_SetForExtraField(t *testing.T) {
	model := NewObjectNode()
	model.SetChild("name", NewLeafNode(String))
	// Data has an extra field not in the schema.
	data := map[string]any{"name": "alice", "email": "a@b.c"}
	errs := Validate(model, data)
	if len(errs) == 0 {
		t.Fatal("expected at least one validation error")
	}
	found := false
	for _, e := range errs {
		if e.Kind == ErrKindUnknownElement {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ErrKindUnknownElement for extra field, got: %+v", errs)
	}
}

func TestValidationError_ErrKindGeneric_ForTypeMismatch(t *testing.T) {
	model := NewObjectNode()
	model.SetChild("age", NewLeafNode(Integer))
	data := map[string]any{"age": "not a number"}
	errs := Validate(model, data)
	if len(errs) == 0 {
		t.Fatal("expected type-mismatch error")
	}
	for _, e := range errs {
		if e.Kind == ErrKindUnknownElement {
			t.Errorf("type mismatch should NOT be ErrKindUnknownElement: %+v", e)
		}
	}
}

// TestValidationError_ErrKindIncompatibleType_LeafTypeMismatch asserts that
// scalar value-vs-type leaf mismatches (the canonical "incompatible type"
// signal — Cloud's FoundIncompatibleTypeWithEntityModelException) are
// classified as ErrKindIncompatibleType and carry the expected/actual
// DataType structure for downstream Props rendering.
func TestValidationError_ErrKindIncompatibleType_LeafTypeMismatch(t *testing.T) {
	model := NewObjectNode()
	model.SetChild("price", NewLeafNode(Integer))
	// price is INTEGER; submit a STRING value (unambiguously incompatible).
	data := map[string]any{"price": "not-a-number"}
	errs := Validate(model, data)
	if len(errs) == 0 {
		t.Fatal("expected type-mismatch error")
	}
	var match *ValidationError
	for i := range errs {
		if errs[i].Kind == ErrKindIncompatibleType {
			match = &errs[i]
			break
		}
	}
	if match == nil {
		t.Fatalf("expected at least one ErrKindIncompatibleType error, got: %+v", errs)
	}
	if match.Path != "price" {
		t.Errorf("path: got %q, want %q", match.Path, "price")
	}
	if match.ActualType != String {
		t.Errorf("ActualType: got %v, want %v", match.ActualType, String)
	}
	if len(match.ExpectedTypes) != 1 || match.ExpectedTypes[0] != Integer {
		t.Errorf("ExpectedTypes: got %v, want [INTEGER]", match.ExpectedTypes)
	}
}

// TestHasIncompatibleType_Match returns the first incompatible-type error
// from a slice, or nil when none is present. Mirrors HasUnknownSchemaElement
// shape so handlers can branch on classification.
func TestHasIncompatibleType_Match(t *testing.T) {
	errs := []ValidationError{
		{Path: "x", Message: "extra", Kind: ErrKindUnknownElement},
		{Path: "price", Message: "value of type STRING is not compatible with [INTEGER]", Kind: ErrKindIncompatibleType, ActualType: String, ExpectedTypes: []DataType{Integer}},
	}
	got := FirstIncompatibleType(errs)
	if got == nil {
		t.Fatal("expected non-nil match")
	}
	if got.Path != "price" {
		t.Errorf("path: got %q, want %q", got.Path, "price")
	}
}

func TestHasIncompatibleType_NoMatch(t *testing.T) {
	errs := []ValidationError{
		{Path: "x", Message: "extra", Kind: ErrKindUnknownElement},
	}
	if got := FirstIncompatibleType(errs); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestHasUnknownSchemaElement_Empty(t *testing.T) {
	if HasUnknownSchemaElement(nil) {
		t.Error("nil slice must not match")
	}
	if HasUnknownSchemaElement([]ValidationError{}) {
		t.Error("empty slice must not match")
	}
}

func TestHasUnknownSchemaElement_WithMatch(t *testing.T) {
	errs := []ValidationError{
		{Path: "name", Message: "type", Kind: ErrKindGeneric},
		{Path: "email", Message: "extra", Kind: ErrKindUnknownElement},
	}
	if !HasUnknownSchemaElement(errs) {
		t.Error("expected true when any error is ErrKindUnknownElement")
	}
}

func TestHasUnknownSchemaElement_NoMatch(t *testing.T) {
	errs := []ValidationError{
		{Path: "name", Message: "type", Kind: ErrKindGeneric},
	}
	if HasUnknownSchemaElement(errs) {
		t.Error("expected false when no ErrKindUnknownElement")
	}
}
