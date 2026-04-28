package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// updateEntityE2E updates an entity via the REST API.
func updateEntityE2E(t *testing.T, entityID, transition, payload string) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/JSON/%s/%s", entityID, transition)
	resp := doAuth(t, http.MethodPut, path, payload)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("updateEntity %s/%s: expected 200, got %d: %s", entityID, transition, resp.StatusCode, body)
	}
}

// getEntityData retrieves an entity and returns the parsed data map.
// If pointInTime is non-empty, it's appended as a query parameter.
func getEntityData(t *testing.T, entityID, pointInTime string) map[string]any {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s", entityID)
	if pointInTime != "" {
		path += "?pointInTime=" + pointInTime
	}
	resp := doAuth(t, http.MethodGet, path, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getEntity %s (pit=%s): expected 200, got %d: %s", entityID, pointInTime, resp.StatusCode, body)
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("failed to parse entity response: %v", err)
	}

	// data can be a map or a JSON string — handle both.
	switch d := envelope["data"].(type) {
	case map[string]any:
		return d
	case string:
		var data map[string]any
		if err := json.Unmarshal([]byte(d), &data); err != nil {
			t.Fatalf("failed to parse entity data string: %v", err)
		}
		return data
	default:
		t.Fatalf("unexpected data type: %T", envelope["data"])
		return nil
	}
}

// getEntityAtTransactionID issues GET /api/entity/{id}?transactionId=<tx>
// and returns the (status, body) pair. Used by tests that need to assert
// both positive (200 + at-tx snapshot) and negative (404 + ENTITY_NOT_FOUND)
// outcomes for the transactionId-scoped GET path. Issue #150.
func getEntityAtTransactionID(t *testing.T, entityID, txID string) (int, string) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s?transactionId=%s", entityID, txID)
	resp := doAuth(t, http.MethodGet, path, "")
	return resp.StatusCode, readBody(t, resp)
}
