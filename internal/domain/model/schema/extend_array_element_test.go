package schema

import (
	"strings"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// TestExtend_ArrayElementKindMismatch_Rejected is a regression probe: the
// element of an ARRAY changed kind between two ingested records (e.g.
// first record has [{"k":1}], second record has ["str"]). Prior to this
// fix, checkElementWidening only covered same-kind pairs; kind-mismatched
// array elements fell through as "no change", then Merge silently absorbed
// the mismatched element into the existing kind — dropping the new kind
// and its TypeSet without any error or change-level check.
//
// Expected: same contract as root-level kind mismatch — reject with a
// clear error, with the same isNullOnlyLeaf carve-out.
func TestExtend_ArrayElementKindMismatch_Rejected(t *testing.T) {
	cases := []struct {
		name            string
		existingElem    *ModelNode
		incomingElem    *ModelNode
		wantErrContains string
	}{
		{
			name:            "OBJECT elem vs LEAF[String] elem",
			existingElem:    NewObjectNode(),
			incomingElem:    NewLeafNode(String),
			wantErrContains: "kind mismatch",
		},
		{
			name:            "LEAF[String] elem vs OBJECT elem",
			existingElem:    NewLeafNode(String),
			incomingElem:    NewObjectNode(),
			wantErrContains: "kind mismatch",
		},
		{
			name:            "OBJECT elem vs ARRAY elem",
			existingElem:    NewObjectNode(),
			incomingElem:    NewArrayNode(NewLeafNode(String)),
			wantErrContains: "kind mismatch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			existing := NewObjectNode()
			existing.SetChild("items", NewArrayNode(tc.existingElem))
			incoming := NewObjectNode()
			incoming.SetChild("items", NewArrayNode(tc.incomingElem))

			_, err := Extend(existing, incoming, spi.ChangeLevelType)
			if err == nil {
				t.Fatal("array element kind mismatch must reject, not silently absorb")
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Errorf("unexpected error: %v; want to contain %q", err, tc.wantErrContains)
			}
		})
	}
}

// TestExtend_ArrayElementNullableMarker_Accepted — the nullable-marker
// exception that applies at the root kind check must ALSO apply at the
// array-element level. ARRAY[OBJECT] + ARRAY[LEAF[NULL]] is a nullable
// array element (e.g. [null, null] against [{"k":1}]) and must succeed.
func TestExtend_ArrayElementNullableMarker_Accepted(t *testing.T) {
	existing := NewObjectNode()
	existing.SetChild("items", NewArrayNode(NewObjectNode()))

	incoming := NewObjectNode()
	incoming.SetChild("items", NewArrayNode(NewLeafNode(Null)))

	got, err := Extend(existing, incoming, spi.ChangeLevelType)
	if err != nil {
		t.Fatalf("ARRAY[OBJECT] + ARRAY[LEAF[NULL]] must succeed (nullable marker): %v", err)
	}
	items := got.Child("items")
	if items == nil || items.Kind() != KindArray {
		t.Fatalf("items child missing or wrong kind: %v", items)
	}
	elem := items.Element()
	if elem == nil {
		t.Fatal("array element nil after merge")
	}
	if elem.Kind() != KindObject {
		t.Errorf("merged element kind = %s, want %s", elem.Kind(), KindObject)
	}
	hasNull := false
	for _, dt := range elem.Types().Types() {
		if dt == Null {
			hasNull = true
			break
		}
	}
	if !hasNull {
		t.Errorf("element TypeSet = %v, want NULL after nullable-marker merge", elem.Types().Types())
	}
}
