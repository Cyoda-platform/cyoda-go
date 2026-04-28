package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

// TestDeleteMessages_DELETEsWithJSONArrayBody verifies that DeleteMessages:
//   - issues DELETE /api/message (no path segment)
//   - sends the ID list as a JSON array body
//   - decodes the [{entityIds:[...], success:true}] response shape
//   - returns the deleted IDs from the first result
func TestDeleteMessages_DELETEsWithJSONArrayBody(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"entityIds":["m1","m2"],"success":true}]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	deleted, err := c.DeleteMessages(t, []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}

	// Method must be DELETE.
	if gotMethod != http.MethodDelete {
		t.Errorf("method: got %q, want DELETE", gotMethod)
	}
	// Path must be /api/message (no additional segment).
	if gotPath != "/api/message" {
		t.Errorf("path: got %q, want /api/message", gotPath)
	}
	// Body must be a JSON array of the supplied IDs.
	var bodyIDs []string
	if err := json.Unmarshal(gotBody, &bodyIDs); err != nil {
		t.Fatalf("request body is not a JSON array: %v; body=%q", err, gotBody)
	}
	sort.Strings(bodyIDs)
	want := []string{"m1", "m2"}
	if len(bodyIDs) != len(want) {
		t.Errorf("body IDs: got %v, want %v", bodyIDs, want)
	} else {
		for i := range bodyIDs {
			if bodyIDs[i] != want[i] {
				t.Errorf("body IDs[%d]: got %q, want %q", i, bodyIDs[i], want[i])
			}
		}
	}

	// Returned IDs must match the server's entityIds.
	sort.Strings(deleted)
	if len(deleted) != 2 || deleted[0] != "m1" || deleted[1] != "m2" {
		t.Errorf("returned deleted IDs: got %v, want [m1 m2]", deleted)
	}
}

// TestDeleteMessages_EmptyResultsError verifies that an empty results array
// from the server is treated as an error.
func TestDeleteMessages_EmptyResultsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.DeleteMessages(t, []string{"m1"})
	if err == nil {
		t.Fatal("expected error for empty results array, got nil")
	}
}
