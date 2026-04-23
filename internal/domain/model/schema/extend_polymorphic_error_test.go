package schema

import (
	"errors"
	"strings"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// TestExtend_KindMismatch_WrapsPolymorphicSlotSentinel pins the programmatic
// contract the handler layer relies on: kind-mismatch errors from Extend
// must be detectable via errors.Is(err, ErrPolymorphicSlot) so callers can
// produce a clearer user-facing message distinct from real change-level
// violations.
func TestExtend_KindMismatch_WrapsPolymorphicSlotSentinel(t *testing.T) {
	existing := NewObjectNode()
	existing.SetChild("x", NewArrayNode(NewLeafNode(String)))

	incoming := NewObjectNode()
	incoming.SetChild("x", NewLeafNode(Integer))

	_, err := Extend(existing, incoming, spi.ChangeLevelType)
	if err == nil {
		t.Fatal("kind mismatch must error")
	}
	if !errors.Is(err, ErrPolymorphicSlot) {
		t.Errorf("kind mismatch error must wrap ErrPolymorphicSlot for handler classification; got: %v", err)
	}
}

// TestExtend_ChangeLevelViolation_DoesNotWrapPolymorphicSentinel — genuine
// change-level violations (new field at TYPE level, etc.) must NOT wrap
// the polymorphic-slot sentinel, so the handler keeps saying "change level
// violation" for those cases where raising the level would solve it.
func TestExtend_ChangeLevelViolation_DoesNotWrapPolymorphicSentinel(t *testing.T) {
	existing := NewObjectNode()
	existing.SetChild("a", NewLeafNode(String))

	incoming := NewObjectNode()
	incoming.SetChild("a", NewLeafNode(String))
	incoming.SetChild("b", NewLeafNode(String)) // new field requires STRUCTURAL

	_, err := Extend(existing, incoming, spi.ChangeLevelType)
	if err == nil {
		t.Fatal("new field at TYPE level must error")
	}
	if errors.Is(err, ErrPolymorphicSlot) {
		t.Errorf("change-level error must NOT wrap polymorphic-slot sentinel; got: %v", err)
	}
}

// TestExtend_ArrayElementKindMismatch_WrapsPolymorphicSlotSentinel — the
// element-level kind mismatch uses the same contract.
func TestExtend_ArrayElementKindMismatch_WrapsPolymorphicSlotSentinel(t *testing.T) {
	existing := NewObjectNode()
	existing.SetChild("items", NewArrayNode(NewObjectNode()))

	incoming := NewObjectNode()
	incoming.SetChild("items", NewArrayNode(NewLeafNode(String)))

	_, err := Extend(existing, incoming, spi.ChangeLevelType)
	if err == nil {
		t.Fatal("array element kind mismatch must error")
	}
	if !errors.Is(err, ErrPolymorphicSlot) {
		t.Errorf("array element kind mismatch must wrap ErrPolymorphicSlot; got: %v", err)
	}
}

// TestExtend_KindMismatch_MessageNamesPolymorphism — the user-facing
// string must explicitly use the word "polymorphic" (so clients can search
// docs) and must NOT say "change level violation" (which is misleading —
// no level change would solve this).
func TestExtend_KindMismatch_MessageNamesPolymorphism(t *testing.T) {
	existing := NewObjectNode()
	existing.SetChild("x", NewArrayNode(NewLeafNode(String)))

	incoming := NewObjectNode()
	incoming.SetChild("x", NewLeafNode(Integer))

	_, err := Extend(existing, incoming, spi.ChangeLevelType)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "polymorphic") {
		t.Errorf("error must name polymorphism: %q", msg)
	}
	if strings.Contains(msg, "change level violation") {
		t.Errorf("error must NOT say 'change level violation' (misleading for kind mismatch): %q", msg)
	}
}
