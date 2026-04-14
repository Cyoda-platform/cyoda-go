package schema

import "sync/atomic"

// NodeKind indicates whether a ModelNode represents an object, array, or leaf.
type NodeKind int

const (
	// KindLeaf represents a leaf node holding one or more primitive DataTypes.
	KindLeaf NodeKind = iota
	// KindObject represents an object node with named children.
	KindObject
	// KindArray represents an array node with an element descriptor.
	KindArray
)

// String returns the canonical name of the NodeKind.
func (k NodeKind) String() string {
	switch k {
	case KindLeaf:
		return "LEAF"
	case KindObject:
		return "OBJECT"
	case KindArray:
		return "ARRAY"
	default:
		return "UNKNOWN"
	}
}

// ModelNode represents a node in the entity model tree.
type ModelNode struct {
	kind       NodeKind
	types      *TypeSet
	children   map[string]*ModelNode
	element    *ModelNode
	info       *ArrayInfo
	fieldCache atomic.Pointer[cachedFields]
}

// NewObjectNode returns an object node with an empty children map.
func NewObjectNode() *ModelNode {
	return &ModelNode{
		kind:     KindObject,
		types:    NewTypeSet(),
		children: make(map[string]*ModelNode),
	}
}

// NewLeafNode returns a leaf node seeded with the given DataType.
func NewLeafNode(dt DataType) *ModelNode {
	ts := NewTypeSet()
	ts.Add(dt)
	return &ModelNode{
		kind:  KindLeaf,
		types: ts,
	}
}

// NewArrayNode returns an array node whose elements are described by the given node.
func NewArrayNode(element *ModelNode) *ModelNode {
	return &ModelNode{
		kind:    KindArray,
		types:   NewTypeSet(),
		element: element,
		info:    NewArrayInfo(),
	}
}

// Kind returns the NodeKind of this node.
func (n *ModelNode) Kind() NodeKind { return n.kind }

// Types returns the TypeSet associated with this node.
func (n *ModelNode) Types() *TypeSet { return n.types }

// Element returns the element descriptor for array nodes, or nil.
func (n *ModelNode) Element() *ModelNode { return n.element }

// Info returns the ArrayInfo for array nodes, or nil.
func (n *ModelNode) Info() *ArrayInfo { return n.info }

// Children returns a shallow copy of the children map.
func (n *ModelNode) Children() map[string]*ModelNode {
	if n.children == nil {
		return nil
	}
	result := make(map[string]*ModelNode, len(n.children))
	for k, v := range n.children {
		result[k] = v
	}
	return result
}

// Child returns the named child, or nil if not found.
func (n *ModelNode) Child(name string) *ModelNode {
	if n.children == nil {
		return nil
	}
	return n.children[name]
}

// SetChild adds or replaces a named child on this node.
func (n *ModelNode) SetChild(name string, child *ModelNode) {
	if n.children == nil {
		n.children = make(map[string]*ModelNode)
	}
	n.children[name] = child
}

// ArrayInfo tracks observed array shapes for SIMPLE_VIEW support.
type ArrayInfo struct {
	maxWidth int
	elements []*TypeSet
}

// NewArrayInfo returns an empty ArrayInfo.
func NewArrayInfo() *ArrayInfo {
	return &ArrayInfo{}
}

// Observe records an observed array width, updating the maximum.
func (a *ArrayInfo) Observe(width int) {
	if width > a.maxWidth {
		a.maxWidth = width
	}
}

// MaxWidth returns the largest observed array width.
func (a *ArrayInfo) MaxWidth() int { return a.maxWidth }

// ObserveElement records a DataType at the given array index.
func (a *ArrayInfo) ObserveElement(index int, dt DataType) {
	for len(a.elements) <= index {
		a.elements = append(a.elements, NewTypeSet())
	}
	a.elements[index].Add(dt)
}

// Elements returns a copy of the per-position TypeSet slice.
func (a *ArrayInfo) Elements() []*TypeSet {
	result := make([]*TypeSet, len(a.elements))
	copy(result, a.elements)
	return result
}

// IsUniform returns true if all observed positions have the same type set.
func (a *ArrayInfo) IsUniform() bool {
	if len(a.elements) <= 1 {
		return true
	}
	first := a.elements[0]
	for _, ts := range a.elements[1:] {
		if !first.Equal(ts) {
			return false
		}
	}
	return true
}
