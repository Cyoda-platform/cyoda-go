package schema

import (
	"errors"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// TestExtend_RejectsLeafToObjectKindMismatch asserts that attempting to
// extend a LEAF node at path P with an OBJECT node at the same path
// returns an error, rather than silently producing an OBJECT-with-
// primitive-types that Apply cannot replay. The error wraps the
// ErrPolymorphicSlot sentinel so the handler can surface a clear
// polymorphism-specific message.
func TestExtend_RejectsLeafToObjectKindMismatch(t *testing.T) {
	existing := NewObjectNode()
	existing.SetChild("f0", NewLeafNode(Integer))

	incoming := NewObjectNode()
	incomingF0 := NewObjectNode()
	incomingF0.SetChild("k0", NewLeafNode(Double))
	incoming.SetChild("f0", incomingF0)

	_, err := Extend(existing, incoming, spi.ChangeLevelStructural)
	if err == nil {
		t.Fatal("Extend accepted LEAF->OBJECT kind change without error")
	}
	if !errors.Is(err, ErrPolymorphicSlot) {
		t.Errorf("unexpected error: %v; want ErrPolymorphicSlot", err)
	}
}

// TestExtend_RejectsObjectToLeafKindMismatch — inverse case.
func TestExtend_RejectsObjectToLeafKindMismatch(t *testing.T) {
	existingF0 := NewObjectNode()
	existingF0.SetChild("k0", NewLeafNode(Double))
	existing := NewObjectNode()
	existing.SetChild("f0", existingF0)

	incoming := NewObjectNode()
	incoming.SetChild("f0", NewLeafNode(Integer))

	_, err := Extend(existing, incoming, spi.ChangeLevelStructural)
	if err == nil {
		t.Fatal("Extend accepted OBJECT->LEAF kind change without error")
	}
	if !errors.Is(err, ErrPolymorphicSlot) {
		t.Errorf("unexpected error: %v; want ErrPolymorphicSlot", err)
	}
}
