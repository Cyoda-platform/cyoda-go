package schema

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidationError describes a single validation failure at a specific path.
type ValidationError struct {
	Path    string
	Message string
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
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
		if isCompatible(dataType, mt) {
			return nil
		}
	}

	return []ValidationError{{
		Path:    path,
		Message: fmt.Sprintf("value of type %s is not compatible with %v", dataType, modelTypes),
	}}
}

// inferDataType maps a Go value (typically from JSON decoding) to a DataType.
func inferDataType(v any) DataType {
	switch n := v.(type) {
	case bool:
		return Boolean
	case json.Number:
		// JSON/XML importers preserve numeric leaves as json.Number.
		// Classify as Double when the literal carries a fractional or
		// exponent component, Long otherwise. Distinguishing finer
		// integer widths here would only be used as a "numeric vs
		// non-numeric" signal by isCompatible, so Long is sufficient.
		if strings.ContainsAny(string(n), ".eE") {
			return Double
		}
		return Long
	case float64:
		// Fallback for JSON numbers decoded WITHOUT UseNumber() (legacy paths)
		// and for caller-constructed trees with hand-typed float64 values.
		// Intentionally lossy: an integer-valued float64 (e.g. 2.0) is
		// classified as Double here, while the same integer literal via
		// UseNumber → json.Number("2") would be classified as Long. This
		// asymmetry is harmless under isCompatible's numeric-vs-non-numeric
		// signal and is preserved to avoid broadening the float64 branch's
		// scope (which would also affect non-importer call sites).
		return Double
	case int:
		return Integer
	case int64:
		return Long
	case string:
		return String
	case nil:
		return Null
	default:
		// Fallback — should not normally occur for JSON-decoded data.
		return String
	}
}

// isCompatible returns true if a data value of type dataT is acceptable
// where the model declares modelT.
func isCompatible(dataT, modelT DataType) bool {
	if dataT == modelT {
		return true
	}
	if dataT == Null {
		return true
	}
	// Numeric data values are compatible with any numeric model type.
	if isNumeric(dataT) && isNumeric(modelT) {
		return true
	}
	return false
}

// isNumeric returns true if dt is a numeric DataType.
func isNumeric(dt DataType) bool {
	switch dt {
	case Byte, Short, Integer, Long, BigInteger, UnboundInteger,
		Float, Double, BigDecimal, UnboundDecimal:
		return true
	default:
		return false
	}
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}
