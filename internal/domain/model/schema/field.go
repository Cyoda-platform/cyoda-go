package schema

import "sort"

// FieldDescriptor is a flat representation of a single leaf field in the model tree.
type FieldDescriptor struct {
	Path     string     // JSONPath-like: "$.name", "$.items[*].price"
	Types    []DataType
	IsArray  bool
	MaxWidth int
}

// cachedFields holds the lazily-computed flat field view.
type cachedFields struct {
	list    []FieldDescriptor
	byPath  map[string]FieldDescriptor
}

// Fields returns a flat list of all leaf fields, cached after first call.
func (n *ModelNode) Fields() []FieldDescriptor {
	if cached := n.fieldCache.Load(); cached != nil {
		return cached.list
	}
	cf := n.buildFieldCache()
	n.fieldCache.CompareAndSwap(nil, cf)
	return n.fieldCache.Load().list
}

// FieldsMap returns a map from path to FieldDescriptor, cached alongside Fields.
func (n *ModelNode) FieldsMap() map[string]FieldDescriptor {
	if cached := n.fieldCache.Load(); cached != nil {
		return cached.byPath
	}
	cf := n.buildFieldCache()
	n.fieldCache.CompareAndSwap(nil, cf)
	return n.fieldCache.Load().byPath
}

func (n *ModelNode) buildFieldCache() *cachedFields {
	var list []FieldDescriptor
	collectFields(n, "$", false, &list)
	// Sort for deterministic output
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })

	byPath := make(map[string]FieldDescriptor, len(list))
	for _, f := range list {
		byPath[f.Path] = f
	}
	return &cachedFields{list: list, byPath: byPath}
}

// collectFields walks the ModelNode tree recursively, appending leaf descriptors.
func collectFields(n *ModelNode, prefix string, inArray bool, out *[]FieldDescriptor) {
	switch n.kind {
	case KindLeaf:
		*out = append(*out, FieldDescriptor{
			Path:    prefix,
			Types:   n.types.Types(), // returns a copy
			IsArray: inArray,
		})
	case KindObject:
		// Sort child keys for deterministic order.
		keys := make([]string, 0, len(n.children))
		for k := range n.children {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectFields(n.children[k], prefix+"."+k, false, out)
		}
	case KindArray:
		if n.element != nil {
			arrayPath := prefix + "[*]"
			maxW := 0
			if n.info != nil {
				maxW = n.info.MaxWidth()
			}
			if n.element.kind == KindLeaf {
				*out = append(*out, FieldDescriptor{
					Path:     arrayPath,
					Types:    n.element.types.Types(),
					IsArray:  true,
					MaxWidth: maxW,
				})
			} else {
				// For arrays of objects/arrays, recurse with the array path prefix.
				// The inArray flag is false for nested fields inside array objects.
				collectFields(n.element, arrayPath, false, out)
			}
		}
	}
}
