package predicate

import (
	"encoding/json"
	"fmt"
)

// maxParseDepth limits recursive nesting of group conditions to prevent
// stack exhaustion from deeply nested payloads.
const maxParseDepth = 50

// ParseCondition parses a JSON-encoded condition into a Condition value.
// It inspects the "type" field to determine the concrete type and delegates
// to the appropriate parser. GroupCondition's "conditions" array is parsed
// recursively.
func ParseCondition(body []byte) (Condition, error) {
	return parseConditionWithDepth(body, 0)
}

func parseConditionWithDepth(body []byte, depth int) (Condition, error) {
	if depth > maxParseDepth {
		return nil, fmt.Errorf("condition nesting exceeds maximum depth of %d", maxParseDepth)
	}

	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse condition: %w", err)
	}

	switch envelope.Type {
	case "simple":
		return parseSimple(body)
	case "lifecycle":
		return parseLifecycle(body)
	case "group":
		return parseGroupWithDepth(body, depth)
	case "array":
		return parseArray(body)
	case "function":
		return &FunctionCondition{}, nil
	default:
		return nil, fmt.Errorf("unknown condition type: %q", envelope.Type)
	}
}

// coalesce returns the first non-empty string.
func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseSimple(body []byte) (*SimpleCondition, error) {
	var raw struct {
		JsonPath     string `json:"jsonPath"`
		OperatorType string `json:"operatorType"`
		Operator     string `json:"operator"`  // alias per Cyoda schema
		Operation    string `json:"operation"` // legacy alias (backwards compat)
		Value        any    `json:"value"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse simple condition: %w", err)
	}
	op := coalesce(raw.OperatorType, raw.Operator, raw.Operation)
	return &SimpleCondition{
		JsonPath:     raw.JsonPath,
		OperatorType: op,
		Value:        raw.Value,
	}, nil
}

func parseLifecycle(body []byte) (*LifecycleCondition, error) {
	var raw struct {
		Field        string `json:"field"`
		OperatorType string `json:"operatorType"`
		Operator     string `json:"operator"`  // alias per Cyoda schema
		Operation    string `json:"operation"` // legacy alias (backwards compat)
		Value        any    `json:"value"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse lifecycle condition: %w", err)
	}
	op := coalesce(raw.OperatorType, raw.Operator, raw.Operation)
	return &LifecycleCondition{
		Field:        raw.Field,
		OperatorType: op,
		Value:        raw.Value,
	}, nil
}

func parseGroupWithDepth(body []byte, depth int) (*GroupCondition, error) {
	var raw struct {
		Operator   string            `json:"operator"`
		Conditions []json.RawMessage `json:"conditions"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse group condition: %w", err)
	}

	conditions := make([]Condition, 0, len(raw.Conditions))
	for i, c := range raw.Conditions {
		parsed, err := parseConditionWithDepth(c, depth+1)
		if err != nil {
			return nil, fmt.Errorf("failed to parse group condition[%d]: %w", i, err)
		}
		conditions = append(conditions, parsed)
	}
	return &GroupCondition{
		Operator:   raw.Operator,
		Conditions: conditions,
	}, nil
}

func parseArray(body []byte) (*ArrayCondition, error) {
	var raw struct {
		JsonPath string `json:"jsonPath"`
		Values   []any  `json:"values"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse array condition: %w", err)
	}
	return &ArrayCondition{
		JsonPath: raw.JsonPath,
		Values:   raw.Values,
	}, nil
}
