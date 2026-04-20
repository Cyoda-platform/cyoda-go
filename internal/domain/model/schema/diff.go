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
	if oldElem == nil || newElem == nil {
		return fmt.Errorf("array element missing at %q", displayPath(path))
	}
	if oldElem.Kind() != KindLeaf || newElem.Kind() != KindLeaf {
		// Catalog today only widens LEAF-element arrays. Object/array
		// elements would require add_property or deeper ops and are out
		// of scope until Extend produces them.
		return fmt.Errorf("array element at %q is not a leaf (got %s -> %s); beyond catalog",
			displayPath(path), oldElem.Kind(), newElem.Kind())
	}
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
