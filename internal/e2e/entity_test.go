package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// createEntityE2E creates a single entity via the REST API and returns the entity ID.
// It asserts that the creation succeeded (200 OK) and that at least one entity ID is returned.
func createEntityE2E(t *testing.T, entityName string, modelVersion int, payload string) string {
	t.Helper()
	path := fmt.Sprintf("/api/entity/JSON/%s/%d", entityName, modelVersion)
	resp := doAuth(t, http.MethodPost, path, payload)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createEntity %s/%d: expected 200, got %d: %s", entityName, modelVersion, resp.StatusCode, body)
	}

	// Response is an array of EntityTransactionResponse objects:
	// [{"transactionId": "...", "entityIds": ["uuid1", ...]}]
	var results []map[string]any
	if err := json.Unmarshal([]byte(body), &results); err != nil {
		t.Fatalf("createEntity %s/%d: failed to parse JSON response: %v\nbody: %s", entityName, modelVersion, err, body)
	}
	if len(results) == 0 {
		t.Fatalf("createEntity %s/%d: expected at least one result, got empty array\nbody: %s", entityName, modelVersion, body)
	}

	entityIDs, ok := results[0]["entityIds"].([]any)
	if !ok || len(entityIDs) == 0 {
		t.Fatalf("createEntity %s/%d: expected entityIds array in response, got: %v", entityName, modelVersion, results[0])
	}

	entityID, ok := entityIDs[0].(string)
	if !ok || entityID == "" {
		t.Fatalf("createEntity %s/%d: expected non-empty string entityId, got: %v", entityName, modelVersion, entityIDs[0])
	}
	return entityID
}
