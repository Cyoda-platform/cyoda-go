package exporter

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// JSONSchemaExporter exports a ModelNode tree as a JSON Schema document
// wrapped in the standard Cyoda envelope: {currentState, model}.
type JSONSchemaExporter struct {
	currentState string
}

// NewJSONSchemaExporter returns a new JSONSchemaExporter.
func NewJSONSchemaExporter(currentState string) *JSONSchemaExporter {
	return &JSONSchemaExporter{currentState: currentState}
}

// Export converts the ModelNode tree into a JSON Schema byte slice
// wrapped in {currentState, model}.
func (e *JSONSchemaExporter) Export(node *schema.ModelNode) ([]byte, error) {
	s := e.convert(node)
	envelope := map[string]any{
		"currentState": e.currentState,
		"model":        s,
	}
	return json.Marshal(envelope)
}

func (e *JSONSchemaExporter) convert(node *schema.ModelNode) map[string]any {
	switch node.Kind() {
	case schema.KindObject:
		return e.convertObject(node)
	case schema.KindArray:
		return e.convertArray(node)
	case schema.KindLeaf:
		return e.convertLeaf(node)
	default:
		return map[string]any{}
	}
}

func (e *JSONSchemaExporter) convertObject(node *schema.ModelNode) map[string]any {
	props := make(map[string]any)
	// Sort children keys for deterministic output.
	children := node.Children()
	keys := make([]string, 0, len(children))
	for k := range children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		props[k] = e.convert(children[k])
	}
	result := map[string]any{
		"type":       "object",
		"properties": props,
	}
	return result
}

func (e *JSONSchemaExporter) convertArray(node *schema.ModelNode) map[string]any {
	result := map[string]any{
		"type": "array",
	}
	if elem := node.Element(); elem != nil {
		result["items"] = e.convert(elem)
	}
	return result
}

func (e *JSONSchemaExporter) convertLeaf(node *schema.ModelNode) map[string]any {
	ts := node.Types()
	types := ts.Types()
	if len(types) == 0 {
		return map[string]any{}
	}
	if len(types) == 1 {
		return jsonSchemaType(types[0])
	}
	// Polymorphic: use oneOf
	oneOf := make([]any, 0, len(types))
	for _, dt := range types {
		oneOf = append(oneOf, jsonSchemaType(dt))
	}
	return map[string]any{"oneOf": oneOf}
}

// jsonSchemaType maps a DataType to a JSON Schema type descriptor.
func jsonSchemaType(dt schema.DataType) map[string]any {
	switch dt {
	case schema.Integer, schema.Long,
		schema.BigInteger, schema.UnboundInteger:
		return map[string]any{"type": "integer"}

	case schema.Double, schema.BigDecimal, schema.UnboundDecimal:
		return map[string]any{"type": "number"}

	case schema.String, schema.Character:
		return map[string]any{"type": "string"}

	case schema.Boolean:
		return map[string]any{"type": "boolean"}

	case schema.Null:
		return map[string]any{"type": "null"}

	case schema.LocalDate:
		return map[string]any{"type": "string", "format": "date"}
	case schema.LocalDateTime, schema.ZonedDateTime:
		return map[string]any{"type": "string", "format": "date-time"}
	case schema.LocalTime:
		return map[string]any{"type": "string", "format": "time"}
	case schema.Year:
		return map[string]any{"type": "string", "format": "year"}
	case schema.YearMonth:
		return map[string]any{"type": "string", "format": "year-month"}

	case schema.UUIDType, schema.TimeUUIDType:
		return map[string]any{"type": "string", "format": "uuid"}

	case schema.ByteArray:
		return map[string]any{"type": "string", "format": "byte"}

	default:
		return map[string]any{"type": fmt.Sprintf("unknown(%d)", dt)}
	}
}
