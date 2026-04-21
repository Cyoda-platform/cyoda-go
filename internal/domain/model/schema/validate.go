package schema

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// ErrorKind classifies a ValidationError so handlers can branch on
// specific failure modes without matching error message text.
type ErrorKind int

const (
	// ErrKindGeneric covers validation failures that do not map to a
	// more specific kind (type mismatches, shape mismatches).
	ErrKindGeneric ErrorKind = iota

	// ErrKindUnknownElement fires when a data document carries a field
	// that the validating schema does not declare. In practice this is
	// the "stale schema" signal handlers use to decide whether to
	// refresh from authoritative storage and retry (see
	// internal/domain/entity/handler.go).
	ErrKindUnknownElement
)

// ValidationError describes a single validation failure at a specific path.
type ValidationError struct {
	Path    string
	Message string
	Kind    ErrorKind
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// HasUnknownSchemaElement reports whether any of the validation
// errors in errs classify as ErrKindUnknownElement — the stale-schema
// signal. Handlers use this to decide whether to force a cache
// refresh and re-validate once before surfacing a 4xx to the client.
func HasUnknownSchemaElement(errs []ValidationError) bool {
	for _, e := range errs {
		if e.Kind == ErrKindUnknownElement {
			return true
		}
	}
	return false
}

// Validate checks whether data conforms to the given model schema.
// It returns a slice of validation errors; an empty slice means the data is valid.
func Validate(model *ModelNode, data any) []ValidationError {
	return validateNode(model, data, "")
}

func validateNode(model *ModelNode, data any, path string) []ValidationError {
	switch model.Kind() {
	case KindObject:
		return validateObject(model, data, path)
	case KindArray:
		return validateArray(model, data, path)
	case KindLeaf:
		return validateLeaf(model, data, path)
	default:
		return []ValidationError{{Path: path, Message: fmt.Sprintf("unknown node kind %v", model.Kind())}}
	}
}

func validateObject(model *ModelNode, data any, path string) []ValidationError {
	obj, ok := data.(map[string]any)
	if !ok {
		return []ValidationError{{Path: path, Message: fmt.Sprintf("expected object, got %T", data)}}
	}

	var errs []ValidationError
	children := model.Children()
	for name, childModel := range children {
		childPath := joinPath(path, name)
		val, exists := obj[name]
		if !exists {
			// Missing fields are accepted — model describes known structure, not required fields.
			continue
		}
		errs = append(errs, validateNode(childModel, val, childPath)...)
	}
	// Extra fields in data that are not in the model are rejected.
	for name := range obj {
		if _, known := children[name]; !known {
			errs = append(errs, ValidationError{
				Path:    joinPath(path, name),
				Message: "unexpected field not present in model",
				Kind:    ErrKindUnknownElement,
			})
		}
	}
	return errs
}

func validateArray(model *ModelNode, data any, path string) []ValidationError {
	arr, ok := data.([]any)
	if !ok {
		return []ValidationError{{Path: path, Message: fmt.Sprintf("expected array, got %T", data)}}
	}

	elem := model.Element()
	if elem == nil {
		return nil
	}

	var errs []ValidationError
	for i, item := range arr {
		elemPath := fmt.Sprintf("%s[%d]", path, i)
		errs = append(errs, validateNode(elem, item, elemPath)...)
	}
	return errs
}

func validateLeaf(model *ModelNode, data any, path string) []ValidationError {
	if data == nil {
		// Null is compatible with any type.
		return nil
	}
	dataType := inferDataType(data)
	modelTypes := model.Types().Types()
	for _, mt := range modelTypes {
		if IsAssignableTo(dataType, mt) {
			return nil
		}
	}
	return []ValidationError{{
		Path:    path,
		Message: fmt.Sprintf("value of type %s is not compatible with %v", dataType, modelTypes),
	}}
}

// inferDataType maps a Go value (typically from JSON decoding with
// UseNumber) to a DataType using the same classifier the walker uses.
// This ensures validation sees the same classification as schema
// inference.
func inferDataType(v any) DataType {
	switch n := v.(type) {
	case bool:
		return Boolean
	case json.Number:
		d, err := ParseDecimal(string(n))
		if err != nil {
			// Malformed — conservatively say String (validation will fail).
			return String
		}
		stripped := d.StripTrailingZeros()
		if stripped.Scale() <= 0 {
			var bigVal *big.Int
			if stripped.Scale() == 0 {
				bigVal = stripped.Unscaled()
			} else {
				factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-stripped.Scale())), nil)
				bigVal = new(big.Int).Mul(stripped.Unscaled(), factor)
			}
			return ClassifyInteger(bigVal)
		}
		return ClassifyDecimal(stripped)
	case string:
		return String
	case nil:
		return Null
	default:
		// No float64/int/int64 fallbacks. Callers must use json.UseNumber.
		// If something leaks through, map to String so validation fails noisily.
		return String
	}
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}
