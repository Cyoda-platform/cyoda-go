package schema

// Merge combines two ModelNode trees into a new tree.
// Both inputs are consumed and must not be used after the call (Ownership Rule 7).
// When one input is nil, the non-nil input is returned directly. The caller must
// not retain a separate reference to the input, per Ownership Rule 7.
// Returns nil if both inputs are nil.
func Merge(a, b *ModelNode) *ModelNode {
	if a == nil && b == nil {
		return nil
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	result := NewObjectNode()
	result.kind = mergeKind(a.kind, b.kind)
	result.types = Union(a.types, b.types)

	for name, child := range a.children {
		result.children[name] = child
	}
	for name, child := range b.children {
		if existing, ok := result.children[name]; ok {
			result.children[name] = Merge(existing, child)
		} else {
			result.children[name] = child
		}
	}

	// Merge array elements
	result.element = Merge(a.element, b.element)

	// Merge array info
	result.info = mergeArrayInfo(a.info, b.info)

	return result
}

func mergeKind(a, b NodeKind) NodeKind {
	if a == b {
		return a
	}
	if a == KindLeaf {
		return b
	}
	if b == KindLeaf {
		return a
	}
	// Object + Array (or vice-versa): we promote to KindObject but the merged
	// node preserves both children and element. Downstream exporters should
	// check element != nil independently of Kind().
	return KindObject
}

func mergeArrayInfo(a, b *ArrayInfo) *ArrayInfo {
	if a == nil && b == nil {
		return nil
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	result := NewArrayInfo()
	if a.maxWidth > b.maxWidth {
		result.maxWidth = a.maxWidth
	} else {
		result.maxWidth = b.maxWidth
	}
	maxLen := len(a.elements)
	if len(b.elements) > maxLen {
		maxLen = len(b.elements)
	}
	for i := 0; i < maxLen; i++ {
		var merged *TypeSet
		switch {
		case i < len(a.elements) && i < len(b.elements):
			merged = Union(a.elements[i], b.elements[i])
		case i < len(a.elements):
			merged = a.elements[i]
		default:
			merged = b.elements[i]
		}
		result.elements = append(result.elements, merged)
	}
	return result
}
