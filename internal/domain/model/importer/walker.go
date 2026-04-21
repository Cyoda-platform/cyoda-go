package importer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// WalkConfig is retained as an empty struct for backward compatibility
// across the refactor. Scope fields (IntScope, DecimalScope) were
// removed in A.1 Task 13 along with the BYTE/SHORT/FLOAT DataTypes.
type WalkConfig struct{}

// DefaultWalkConfig returns an empty WalkConfig.
func DefaultWalkConfig() WalkConfig { return WalkConfig{} }

// Walk converts a generic parsed data tree into a ModelNode schema tree.
func Walk(data any) (*schema.ModelNode, error) {
	return WalkWithConfig(data, DefaultWalkConfig())
}

// WalkWithConfig applies the default walk.
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
		// Transitional: coarse classification pending A.1 Task 15.
		// Any integer literal → Integer/BigInteger only; fractionals → Double.
		s := val.String()
		if strings.ContainsAny(s, ".eE") {
			return schema.NewLeafNode(schema.Double), nil
		}
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			return schema.NewLeafNode(schema.BigInteger), nil
		}
		return schema.NewLeafNode(schema.Integer), nil
	case float64:
		return schema.NewLeafNode(schema.Double), nil
	case bool:
		return schema.NewLeafNode(schema.Boolean), nil
	case nil:
		return schema.NewLeafNode(schema.Null), nil
	default:
		return nil, fmt.Errorf("unsupported type: %T", v)
	}
}

func (w *walker) walkObject(m map[string]any) (*schema.ModelNode, error) {
	node := schema.NewObjectNode()
	for k, v := range m {
		child, err := w.walkValue(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		node.SetChild(k, child)
	}
	return node, nil
}

func (w *walker) walkArray(arr []any) (*schema.ModelNode, error) {
	if len(arr) == 0 {
		return schema.NewArrayNode(schema.NewLeafNode(schema.Null)), nil
	}
	var element *schema.ModelNode
	for _, item := range arr {
		child, err := w.walkValue(item)
		if err != nil {
			return nil, err
		}
		if element == nil {
			element = child
			continue
		}
		element = schema.Merge(element, child)
	}
	node := schema.NewArrayNode(element)
	node.Info().Observe(len(arr))
	return node, nil
}
