package search

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/cyoda-platform/cyoda-go-spi/predicate"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// skipTypeCheckOperators lists operators whose condition value is not compared
// against the field's DataType. IS_NULL and NOT_NULL don't use the value
// semantically; the value is always null and any type is acceptable.
var skipTypeCheckOperators = map[string]struct{}{
	"IS_NULL":  {},
	"NOT_NULL": {},
}

// ValidateConditionValueTypes walks a condition tree and checks that each
// simple clause's value is type-compatible with the field's DataType as
// declared in the model schema.
//
// The model's FieldsMap provides a lookup from JSONPath (e.g. "$.price") to
// a FieldDescriptor carrying the observed DataType(s). Conditions referencing
// unknown paths are accepted (the condition may traverse a path not yet seen
// in training data).
//
// Returns a non-nil error if any simple clause has a type-mismatched value.
// Polymorphic fields (>1 type) accept values matching any participating type.
// Null values are accepted for any field type.
func ValidateConditionValueTypes(model *schema.ModelNode, cond predicate.Condition) error {
	if model == nil || cond == nil {
		return nil
	}
	fm := model.FieldsMap()
	return walkConditionTypes(fm, cond)
}

func walkConditionTypes(fm map[string]schema.FieldDescriptor, cond predicate.Condition) error {
	if cond == nil {
		return nil
	}
	switch c := cond.(type) {
	case *predicate.SimpleCondition:
		return validateSimpleConditionType(fm, c)
	case *predicate.GroupCondition:
		for _, child := range c.Conditions {
			if err := walkConditionTypes(fm, child); err != nil {
				return err
			}
		}
		return nil
	case *predicate.LifecycleCondition:
		// Lifecycle conditions match entity metadata, not data fields.
		// No DataType constraint applies here.
		return nil
	case *predicate.ArrayCondition, *predicate.FunctionCondition:
		return nil
	default:
		return nil
	}
}

func validateSimpleConditionType(fm map[string]schema.FieldDescriptor, c *predicate.SimpleCondition) error {
	// Operators that don't perform value comparison bypass type checking.
	if _, skip := skipTypeCheckOperators[c.OperatorType]; skip {
		return nil
	}

	// Null values are compatible with any field type.
	if c.Value == nil {
		return nil
	}

	fd, ok := fm[c.JsonPath]
	if !ok {
		// Unknown path — no type constraint; accept.
		return nil
	}

	if len(fd.Types) == 0 {
		// No type information recorded — accept.
		return nil
	}

	// Branch on composite vs scalar values before calling inferValueDataType.
	switch v := c.Value.(type) {
	case []any:
		// Array values (e.g. BETWEEN [lo, hi], IN [a, b, c]): every element
		// must type-check against the field. An empty array is accepted (no
		// elements means nothing to mismatch).
		for i, elem := range v {
			if err := checkSingleValueType(fd, c.JsonPath, elem); err != nil {
				return fmt.Errorf("value[%d]: %w", i, err)
			}
		}
		return nil
	case map[string]any:
		// No search operator accepts an object value.
		_ = v
		return fmt.Errorf("condition value for field %q is an object, which is not valid for any operator type: %w",
			c.JsonPath, errConditionTypeMismatch)
	default:
		return checkSingleValueType(fd, c.JsonPath, c.Value)
	}
}

// errConditionTypeMismatch is the sentinel error for condition type mismatch.
// Handlers check errors.Is(err, errConditionTypeMismatch) to emit HTTP 400
// with ErrCodeConditionTypeMismatch.
var errConditionTypeMismatch = fmt.Errorf("condition type mismatch")

// checkSingleValueType checks whether a single scalar value is compatible with
// the field's TypeSet. Null values are accepted for any field type. String-only
// fields accept any value type (lexicographic comparison semantics).
func checkSingleValueType(fd schema.FieldDescriptor, jsonPath string, v any) error {
	if v == nil {
		return nil // null compatible with any type
	}

	valueType := inferValueDataType(v)
	if valueType == schema.Null {
		return nil // null compatible with any type
	}

	// Only enforce type compatibility when the field carries at least one
	// numeric or boolean type. String fields accept any comparison value
	// (numeric or string) to support lexicographic and coerced comparisons.
	// This matches the Cloud's InvalidTypesInClientConditionException semantics,
	// which targets "non-string value against a non-string field" mismatches.
	hasConstrainedType := false
	for _, ft := range fd.Types {
		if schema.IsNumeric(ft) || ft == schema.Boolean {
			hasConstrainedType = true
			break
		}
	}
	if !hasConstrainedType {
		return nil
	}

	for _, fieldType := range fd.Types {
		if schema.IsAssignableTo(valueType, fieldType) {
			return nil
		}
	}

	return fmt.Errorf("condition value type %s is not compatible with field %q (expected %v): %w",
		valueType, jsonPath, fd.Types, errConditionTypeMismatch)
}

// inferValueDataType infers the DataType of a condition value.
//
// Condition values come from predicate.ParseCondition which uses standard
// json.Unmarshal — numbers arrive as float64, not json.Number. We convert
// float64 to json.Number so the schema classifier can apply its full
// precision-based widening lattice, rather than defaulting to String.
func inferValueDataType(v any) schema.DataType {
	switch val := v.(type) {
	case []any, map[string]any:
		// Composite values (e.g. BETWEEN [lo, hi]) — skip type check.
		return schema.Null
	case float64:
		// Standard json.Unmarshal produces float64. Convert to json.Number
		// so InferDataType can classify it correctly.
		return schema.InferDataType(json.Number(strconv.FormatFloat(val, 'f', -1, 64)))
	}
	return schema.InferDataType(v)
}
