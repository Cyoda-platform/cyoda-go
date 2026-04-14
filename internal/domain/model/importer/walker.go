package importer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// WalkConfig controls schema discovery behavior.
type WalkConfig struct {
	// IntScope is the minimum integer type to infer (default: Integer).
	// Values narrower than this are widened. For example, if IntScope is Integer,
	// a value of 42 (which fits in Byte) is inferred as Integer.
	IntScope schema.DataType
	// DecimalScope is the minimum decimal type to infer (default: Double).
	DecimalScope schema.DataType
}

// DefaultWalkConfig returns the default walk configuration matching Cyoda Cloud behavior.
func DefaultWalkConfig() WalkConfig {
	return WalkConfig{
		IntScope:     schema.Integer,
		DecimalScope: schema.Double,
	}
}

// Walk converts a generic parsed data tree into a ModelNode schema tree
// using the default walk configuration.
func Walk(data any) (*schema.ModelNode, error) {
	return WalkWithConfig(data, DefaultWalkConfig())
}

// WalkWithConfig converts a generic parsed data tree into a ModelNode schema tree
// using the provided configuration.
func WalkWithConfig(data any, cfg WalkConfig) (*schema.ModelNode, error) {
	w := &walker{cfg: cfg}
	return w.walkValue(data)
}

type walker struct {
	cfg WalkConfig
}

func (w *walker) walkValue(v any) (*schema.ModelNode, error) {
	switch val := v.(type) {
	case map[string]any:
		return w.walkObject(val)
	case []any:
		return w.walkArray(val)
	case string:
		return schema.NewLeafNode(schema.String), nil
	case json.Number:
		return schema.NewLeafNode(w.clampNumeric(inferNumericTypeFromString(val.String()))), nil
	case float64:
		return schema.NewLeafNode(w.clampNumeric(inferNumericType(val))), nil
	case bool:
		return schema.NewLeafNode(schema.Boolean), nil
	case nil:
		return schema.NewLeafNode(schema.Null), nil
	default:
		return nil, fmt.Errorf("unsupported type: %T", v)
	}
}

func (w *walker) walkObject(obj map[string]any) (*schema.ModelNode, error) {
	node := schema.NewObjectNode()
	for key, val := range obj {
		child, err := w.walkValue(val)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		node.SetChild(key, child)
	}
	return node, nil
}

func (w *walker) walkArray(arr []any) (*schema.ModelNode, error) {
	if len(arr) == 0 {
		return schema.NewArrayNode(schema.NewLeafNode(schema.Null)), nil
	}

	var element *schema.ModelNode
	for i, item := range arr {
		child, err := w.walkValue(item)
		if err != nil {
			return nil, fmt.Errorf("index [%d]: %w", i, err)
		}
		element = schema.Merge(element, child)
	}

	node := schema.NewArrayNode(element)
	node.Info().Observe(len(arr))
	return node, nil
}

// clampNumeric widens a numeric type to at least the configured minimum scope.
func (w *walker) clampNumeric(dt schema.DataType) schema.DataType {
	if schema.NumericFamily(dt) == 1 && schema.NumericRank(dt) < schema.NumericRank(w.cfg.IntScope) {
		return w.cfg.IntScope
	}
	if schema.NumericFamily(dt) == 2 && schema.NumericRank(dt) < schema.NumericRank(w.cfg.DecimalScope) {
		return w.cfg.DecimalScope
	}
	return dt
}

// inferNumericTypeFromString classifies a numeric string (from json.Number)
// into the narrowest DataType that can represent it without precision loss.
func inferNumericTypeFromString(s string) schema.DataType {
	if strings.ContainsAny(s, ".eE") {
		// Decimal family
		_, err := strconv.ParseFloat(s, 64)
		if err != nil {
			// Overflow or precision loss — use BigDecimal
			return schema.BigDecimal
		}
		return schema.Double
	}
	// Integer family
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// Too large for int64 — BigInteger
		return schema.BigInteger
	}
	switch {
	case v >= -128 && v <= 127:
		return schema.Byte
	case v >= -32768 && v <= 32767:
		return schema.Short
	case v >= -2147483648 && v <= 2147483647:
		return schema.Integer
	default:
		return schema.Long
	}
}

func inferNumericType(f float64) schema.DataType {
	if f == float64(int64(f)) {
		v := int64(f)
		switch {
		case v >= -128 && v <= 127:
			return schema.Byte
		case v >= -32768 && v <= 32767:
			return schema.Short
		case v >= -2147483648 && v <= 2147483647:
			return schema.Integer
		default:
			return schema.Long
		}
	}
	return schema.Double
}
