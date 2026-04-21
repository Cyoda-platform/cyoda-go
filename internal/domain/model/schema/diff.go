package schema

import (
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// Diff returns a SchemaDelta such that Apply(old, Diff(old, new)) ≡ new,
// provided new differs from old only by additive extension within the
// op catalog (add_property, broaden_type, add_array_item_type).
//
// Returns (nil, nil) on semantic no-op.
// Returns an error if the change is not expressible — kind changes,
// property removal, or a replacement of a sub-tree that would require
// a non-additive op.
func Diff(oldN, newN *ModelNode) (spi.SchemaDelta, error) {
	if oldN == nil || newN == nil {
		return nil, fmt.Errorf("schema.Diff: nil input")
	}
	var ops []SchemaOp
	if err := diffNode("", oldN, newN, &ops); err != nil {
		return nil, fmt.Errorf("schema.Diff: %w", err)
	}
	if len(ops) == 0 {
		return nil, nil
	}
	return MarshalDelta(ops)
}

func diffNode(path string, oldN, newN *ModelNode, ops *[]SchemaOp) error {
	if oldN.Kind() != newN.Kind() {
		return fmt.Errorf("kind change at %q: %s -> %s (not additive)",
			displayPath(path), oldN.Kind(), newN.Kind())
	}
	switch newN.Kind() {
	case KindLeaf:
		return diffLeaf(path, oldN, newN, ops)
	case KindObject:
		return diffObject(path, oldN, newN, ops)
	case KindArray:
		return diffArray(path, oldN, newN, ops)
	default:
		return fmt.Errorf("unknown kind at %q: %v", displayPath(path), newN.Kind())
	}
}

func diffLeaf(path string, oldN, newN *ModelNode, ops *[]SchemaOp) error {
	added := typeSetDifference(newN.Types(), oldN.Types())
	if len(added) == 0 {
		return nil
	}
	op, err := NewBroadenType(path, added)
	if err != nil {
		return fmt.Errorf("broaden_type at %q: %w", displayPath(path), err)
	}
	*ops = append(*ops, op)
	return nil
}

func diffObject(path string, oldN, newN *ModelNode, ops *[]SchemaOp) error {
	newChildren := newN.Children()
	for name, newChild := range newChildren {
		oldChild := oldN.Child(name)
		if oldChild == nil {
			raw, err := Marshal(newChild)
			if err != nil {
				return fmt.Errorf("marshal subtree %q: %w", joinSchemaPath(path, name), err)
			}
			*ops = append(*ops, NewAddProperty(path, name, raw))
			continue
		}
		if err := diffNode(joinSchemaPath(path, name), oldChild, newChild, ops); err != nil {
			return err
		}
	}
	for name := range oldN.Children() {
		if _, ok := newChildren[name]; !ok {
			return fmt.Errorf("property removal at %q is not additive", joinSchemaPath(path, name))
		}
	}
	return nil
}

func diffArray(path string, oldN, newN *ModelNode, ops *[]SchemaOp) error {
	oldElem := oldN.Element()
	newElem := newN.Element()
	// Both nil: no element ever observed — nothing to emit.
	if oldElem == nil && newElem == nil {
		return nil
	}
	// Incoming element disappeared — not additive.
	if newElem == nil {
		return fmt.Errorf("array element removed at %q", displayPath(path))
	}
	// Old was an empty array (no observed element yet). Treat this as an
	// "unobserved element" transitioning to a concrete one. Only the
	// LEAF case is expressible via the current op catalog — descending
	// into a synthesized OBJECT/ARRAY shell would require a non-additive
	// kind installation that A.3 tracks separately.
	if oldElem == nil {
		if newElem.Kind() != KindLeaf {
			return fmt.Errorf("array element materialization at %q requires LEAF element; got %s (extend to a LEAF element first)",
				displayPath(path), newElem.Kind())
		}
		op, err := NewAddArrayItemType(path, newElem.Types().Types())
		if err != nil {
			return fmt.Errorf("add_array_item_type at %q: %w", displayPath(path), err)
		}
		*ops = append(*ops, op)
		return nil
	}
	// LEAF-element arrays use the dedicated widening op (cheapest and
	// most common shape from schema.Extend at ChangeLevelArrayElements).
	if oldElem.Kind() == KindLeaf && newElem.Kind() == KindLeaf {
		added := typeSetDifference(newElem.Types(), oldElem.Types())
		if len(added) == 0 {
			return nil
		}
		op, err := NewAddArrayItemType(path, added)
		if err != nil {
			return fmt.Errorf("add_array_item_type at %q: %w", displayPath(path), err)
		}
		*ops = append(*ops, op)
		return nil
	}
	// OBJECT- or ARRAY-element: descend into the element using the "[]"
	// path segment. Subsequent ops (add_property, broaden_type, etc.)
	// carry paths like "parent/[]/child" that Apply's resolvePath
	// follows by traversing the array's Element().
	return diffNode(joinSchemaPath(path, "[]"), oldElem, newElem, ops)
}

// typeSetDifference returns the DataTypes present in `b` but not in `a`,
// in stable canonical-name order (so the resulting op payload is
// deterministic).
func typeSetDifference(b, a *TypeSet) []DataType {
	in := make(map[DataType]struct{})
	for _, dt := range a.Types() {
		in[dt] = struct{}{}
	}
	var added []DataType
	for _, dt := range b.Types() {
		if _, ok := in[dt]; !ok {
			added = append(added, dt)
		}
	}
	return added
}

// joinSchemaPath returns the slash-joined child path used by the op
// catalog (distinct from validate.go's dotted display paths).
func joinSchemaPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "/" + child
}

// displayPath renders an empty path as "(root)" for error messages.
func displayPath(p string) string {
	if p == "" {
		return "(root)"
	}
	return p
}
