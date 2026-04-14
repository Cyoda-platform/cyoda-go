package exporter

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// SimpleViewExporter exports a ModelNode tree as Cyoda's native SIMPLE_VIEW
// format: a JSON object with "currentState" and a path-based "model" map.
type SimpleViewExporter struct {
	state string
}

// NewSimpleViewExporter returns a SimpleViewExporter with the given lock state
// (typically "LOCKED" or "UNLOCKED").
func NewSimpleViewExporter(currentState string) *SimpleViewExporter {
	return &SimpleViewExporter{state: currentState}
}

// Export converts the ModelNode tree into a SIMPLE_VIEW JSON byte slice.
func (e *SimpleViewExporter) Export(node *schema.ModelNode) ([]byte, error) {
	model := make(map[string]map[string]any)
	e.walk(node, "$", model)

	result := map[string]any{
		"currentState": e.state,
		"model":        sortedModel(model),
	}
	return json.Marshal(result)
}

// walk recursively builds the path-based node map for an object node.
func (e *SimpleViewExporter) walk(node *schema.ModelNode, path string, model map[string]map[string]any) {
	if node.Kind() != schema.KindObject {
		return
	}

	descriptor := make(map[string]any)
	children := node.Children()

	// Sort child keys for deterministic output.
	keys := make([]string, 0, len(children))
	for k := range children {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		child := children[name]
		switch child.Kind() {
		case schema.KindLeaf:
			descriptor["."+name] = typeDescriptor(child.Types())

		case schema.KindArray:
			elem := child.Element()
			if elem == nil {
				continue
			}
			if elem.Kind() == schema.KindObject {
				// Array of objects: structural reference + recurse
				descriptor["#."+name] = "OBJECT"
				childPath := path + "." + name + "[*]"
				elemDesc := make(map[string]any)
				elemDesc["#"] = "ARRAY_ELEMENT"
				// Walk the element's children into this descriptor
				elemChildren := elem.Children()
				elemKeys := make([]string, 0, len(elemChildren))
				for k := range elemChildren {
					elemKeys = append(elemKeys, k)
				}
				sort.Strings(elemKeys)
				for _, ek := range elemKeys {
					ec := elemChildren[ek]
					switch ec.Kind() {
					case schema.KindLeaf:
						elemDesc["."+ek] = typeDescriptor(ec.Types())
					case schema.KindArray:
						e.handleArrayChild(ec, ek, childPath, elemDesc, model)
					case schema.KindObject:
						elemDesc["#."+ek] = "OBJECT"
						e.walk(ec, childPath+"."+ek, model)
					}
				}
				model[childPath] = elemDesc
			} else {
				// Array of primitives
				descriptor["."+name+"[*]"] = arrayTypeDescriptor(child)
			}

		case schema.KindObject:
			descriptor["#."+name] = "OBJECT"
			e.walk(child, path+"."+name, model)
		}
	}

	model[path] = descriptor
}

// handleArrayChild handles an array child within an array-of-objects element.
func (e *SimpleViewExporter) handleArrayChild(
	child *schema.ModelNode, name, parentPath string,
	parentDesc map[string]any, model map[string]map[string]any,
) {
	elem := child.Element()
	if elem == nil {
		return
	}
	if elem.Kind() == schema.KindObject {
		parentDesc["#."+name] = "OBJECT"
		childPath := parentPath + "." + name + "[*]"
		e.walk(elem, childPath, model)
		// Add ARRAY_ELEMENT marker
		if desc, ok := model[childPath]; ok {
			desc["#"] = "ARRAY_ELEMENT"
		}
	} else {
		parentDesc["."+name+"[*]"] = arrayTypeDescriptor(child)
	}
}

// typeDescriptor formats a TypeSet as a SIMPLE_VIEW type descriptor string.
func typeDescriptor(ts *schema.TypeSet) string {
	types := ts.Types()
	if len(types) == 0 {
		return "NULL"
	}
	if len(types) == 1 {
		return types[0].String()
	}
	// Polymorphic: "[TYPE1, TYPE2, ...]"
	names := make([]string, len(types))
	for i, dt := range types {
		names[i] = dt.String()
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// arrayTypeDescriptor formats an array node as a SIMPLE_VIEW array type string.
func arrayTypeDescriptor(arr *schema.ModelNode) string {
	elem := arr.Element()
	if elem == nil {
		return "NULL"
	}
	td := typeDescriptor(elem.Types())
	info := arr.Info()
	if info != nil && info.MaxWidth() > 0 {
		return fmt.Sprintf("(%s x %d)", td, info.MaxWidth())
	}
	return td
}

// sortedModel returns an ordered map representation for deterministic JSON output.
func sortedModel(model map[string]map[string]any) json.Marshaler {
	return &orderedModel{data: model}
}

type orderedModel struct {
	data map[string]map[string]any
}

func (m *orderedModel) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf strings.Builder
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		valJSON, err := json.Marshal(m.data[k])
		if err != nil {
			return nil, err
		}
		buf.Write(valJSON)
	}
	buf.WriteByte('}')
	return []byte(buf.String()), nil
}
