package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// createMessageE2E creates a single message via the REST API and returns the message ID.
func createMessageE2E(t *testing.T, subject string, payload string) string {
	t.Helper()
	path := fmt.Sprintf("/api/message/new/%s", subject)
	body := fmt.Sprintf(`{"payload": %s, "meta-data": {"source": "e2e"}}`, payload)
	resp := doAuth(t, http.MethodPost, path, body)
	respBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createMessage subject=%s: expected 200, got %d: %s", subject, resp.StatusCode, respBody)
	}

	// Response is an array: [{"entityIds": ["uuid"], "transactionId": "uuid"}]
	var results []map[string]any
	if err := json.Unmarshal([]byte(respBody), &results); err != nil {
		t.Fatalf("createMessage subject=%s: failed to parse JSON response: %v\nbody: %s", subject, err, respBody)
	}
	if len(results) == 0 {
		t.Fatalf("createMessage subject=%s: expected at least one result, got empty array\nbody: %s", subject, respBody)
	}

	entityIDs, ok := results[0]["entityIds"].([]any)
	if !ok || len(entityIDs) == 0 {
		t.Fatalf("createMessage subject=%s: expected entityIds array in response, got: %v", subject, results[0])
	}

	messageID, ok := entityIDs[0].(string)
	if !ok || messageID == "" {
		t.Fatalf("createMessage subject=%s: expected non-empty string messageId, got: %v", subject, entityIDs[0])
	}
	return messageID
}

// TestMessage_DeleteBatch verifies that a batch delete removes the specified messages
// while leaving others intact.
func TestMessage_DeleteBatch(t *testing.T) {
	// 1. Create 3 messages.
	id1 := createMessageE2E(t, "e2e.batch", `{"seq": 1}`)
	id2 := createMessageE2E(t, "e2e.batch", `{"seq": 2}`)
	id3 := createMessageE2E(t, "e2e.batch", `{"seq": 3}`)

	// 2. Delete 2 by batch (id1 and id2).
	batchBody, err := json.Marshal([]string{id1, id2})
	if err != nil {
		t.Fatalf("failed to marshal batch IDs: %v", err)
	}
	delResp := doAuth(t, http.MethodDelete, "/api/message", string(batchBody))
	delBody := readBody(t, delResp)
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("deleteMessages batch: expected 200, got %d: %s", delResp.StatusCode, delBody)
	}

	// 3. GET remaining message (id3) — still exists.
	path3 := fmt.Sprintf("/api/message/%s", id3)
	getResp := doAuth(t, http.MethodGet, path3, "")
	getBody := readBody(t, getResp)
	if getResp.StatusCode != http.StatusOK {
		t.Errorf("getMessage %s (should exist): expected 200, got %d: %s", id3, getResp.StatusCode, getBody)
	}

	// 4. GET deleted messages — 404.
	for _, id := range []string{id1, id2} {
		p := fmt.Sprintf("/api/message/%s", id)
		r := doAuth(t, http.MethodGet, p, "")
		b := readBody(t, r)
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("getMessage %s (should be deleted): expected 404, got %d: %s", id, r.StatusCode, b)
		}
	}
}
