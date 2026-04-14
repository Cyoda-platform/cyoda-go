package client

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// roundTrip decodes the given fixture file with DisallowUnknownFields
// into v, then re-encodes v and asserts the re-encoded JSON is
// semantically equal to the original. Any drift between the parity
// type and the canonical wire shape — extra fields in the fixture not
// declared by v, OR fields the parity type adds that are absent from
// the fixture — fails the test.
func roundTrip(t *testing.T, fixtureName string, v any) {
	t.Helper()
	path := filepath.Join("testdata", fixtureName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %s into %T: %v", path, v, err)
	}

	// Re-encode and compare structurally. We compare via map[string]any
	// rather than byte-for-byte because field ordering and whitespace
	// can differ.
	reEncoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-encode %T: %v", v, err)
	}

	var origNorm, reNorm any
	if err := json.Unmarshal(raw, &origNorm); err != nil {
		t.Fatalf("unmarshal original %s for comparison: %v", path, err)
	}
	if err := json.Unmarshal(reEncoded, &reNorm); err != nil {
		t.Fatalf("unmarshal re-encoded for comparison: %v", err)
	}

	// reflect.DeepEqual on the parsed maps catches any structural
	// difference (added or removed fields, type mismatches, value
	// changes).
	if !jsonEqual(origNorm, reNorm) {
		origPretty, _ := json.MarshalIndent(origNorm, "", "  ")
		reEncPretty, _ := json.MarshalIndent(reNorm, "", "  ")
		t.Errorf("%s: round-trip differs.\n--- original ---\n%s\n--- re-encoded ---\n%s",
			path, string(origPretty), string(reEncPretty))
	}
}

// jsonEqual compares two parsed-JSON values structurally. Equivalent
// to reflect.DeepEqual for the subset of types json.Unmarshal produces
// (map[string]any, []any, string, float64, bool, nil).
func jsonEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			bvv, ok := bv[k]
			if !ok {
				return false
			}
			if !jsonEqual(v, bvv) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// TestJSONEqual_DetectsDisjointKeysWithNilValues guards against a latent
// bug in jsonEqual where two maps with the same length but different
// keys, both containing nil values, would compare equal because the
// iteration over the first map's keys returns the zero value (nil) for
// the missing keys in the second map. Disjoint key sets must be
// detected as inequality even when both sides contain only nils.
func TestJSONEqual_DetectsDisjointKeysWithNilValues(t *testing.T) {
	a := map[string]any{"foo": nil}
	b := map[string]any{"bar": nil}
	if jsonEqual(a, b) {
		t.Error("jsonEqual must detect disjoint key sets even when values are nil; got equal")
	}
}

func TestEntityResult_GetOne_RoundTrip(t *testing.T) {
	var v EntityResult
	roundTrip(t, "entity_result_get_one.json", &v)
	// Spot-check the canonical-required fields decoded.
	if v.Type != "ENTITY" {
		t.Errorf("Type: got %q, want \"ENTITY\"", v.Type)
	}
	if v.Meta.ID == "" {
		t.Error("Meta.ID is empty")
	}
	if v.Meta.State == "" {
		t.Error("Meta.State is empty (canonical-required)")
	}
	if v.Meta.CreationDate.IsZero() {
		t.Error("Meta.CreationDate is zero (canonical-required)")
	}
	if v.Meta.LastUpdateTime.IsZero() {
		t.Error("Meta.LastUpdateTime is zero (canonical-required)")
	}
	if v.Meta.ModelKey == nil {
		t.Error("Meta.ModelKey is nil — getOneEntity should populate it (deviation A2)")
	}
}

func TestEntityResult_Search_RoundTrip(t *testing.T) {
	var v EntityResult
	roundTrip(t, "entity_result_search.json", &v)
	// Per deviation A2, search responses do NOT include modelKey.
	if v.Meta.ModelKey != nil {
		t.Errorf("Meta.ModelKey should be nil on search results (deviation A2), got %+v", v.Meta.ModelKey)
	}
}

func TestEntityChangeMeta_RoundTrip(t *testing.T) {
	var v EntityChangeMeta
	roundTrip(t, "entity_change_meta.json", &v)
	if v.User == "" {
		t.Error("User is empty (canonical-required)")
	}
	if v.ChangeType == "" {
		t.Error("ChangeType is empty (canonical-required)")
	}
	if v.TimeOfChange.IsZero() {
		t.Error("TimeOfChange is zero (canonical-required)")
	}
}

func TestEntityModelDtoList_RoundTrip(t *testing.T) {
	var v []EntityModelDto
	roundTrip(t, "entity_model_dto_list.json", &v)
	if len(v) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(v))
	}
	if v[0].ModelName != "nobel-prize" {
		t.Errorf("v[0].ModelName: got %q, want \"nobel-prize\"", v[0].ModelName)
	}
	if v[0].CurrentState != "LOCKED" {
		t.Errorf("v[0].CurrentState: got %q, want \"LOCKED\"", v[0].CurrentState)
	}
}

func TestEntityTransactionInfo_RoundTrip(t *testing.T) {
	var v EntityTransactionInfo
	roundTrip(t, "entity_transaction_info.json", &v)
	if len(v.EntityIDs) != 1 {
		t.Fatalf("expected 1 entity ID, got %d", len(v.EntityIDs))
	}
}

func TestPagedEntityResults_RoundTrip(t *testing.T) {
	var v PagedEntityResults
	roundTrip(t, "paged_entity_results.json", &v)
	if len(v.Content) != 1 {
		t.Fatalf("expected 1 content element, got %d", len(v.Content))
	}
	if v.Page.Size != 20 {
		t.Errorf("Page.Size: got %d, want 20", v.Page.Size)
	}
}

func TestAsyncSearchStatus_RoundTrip(t *testing.T) {
	var v AsyncSearchStatus
	roundTrip(t, "async_search_status.json", &v)
	if v.SearchJobStatus != "SUCCESSFUL" {
		t.Errorf("SearchJobStatus: got %q, want \"SUCCESSFUL\"", v.SearchJobStatus)
	}
	if v.FinishTime == nil {
		t.Error("FinishTime is nil for a SUCCESSFUL job")
	}
}
