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
