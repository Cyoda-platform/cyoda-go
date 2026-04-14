package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// --- Search helpers ---

func directSearch(t *testing.T, entityName string, modelVersion int, condition string) (int, []map[string]any) {
	t.Helper()
	path := fmt.Sprintf("/api/search/direct/%s/%d", entityName, modelVersion)
	resp := doAuth(t, http.MethodPost, path, condition)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	// Per canonical openapi-entity-search.yml, sync search returns
	// application/x-ndjson — a stream of JSON objects, one per line.
	var results []map[string]any
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode ndjson line %q: %v", line, err)
		}
		results = append(results, entry)
	}
	return resp.StatusCode, results
}

func setupSearchModel(t *testing.T, model string) {
	t.Helper()
	setupModelWithWorkflow(t, model, `{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1", "name": "search-wf", "initialState": "NONE", "active": true,
			"states": {
				"NONE": {"transitions": [{"name": "init", "next": "CREATED", "manual": false}]},
				"CREATED": {"transitions": [{"name": "approve", "next": "APPROVED", "manual": true}]},
				"APPROVED": {}
			}
		}]
	}`)
}

// --- Test 7.7: Search with string operators ---

func TestSearch_StringOperators(t *testing.T) {
	const model = "e2e-search-7"
	setupSearchModel(t, model)

	createEntityE2E(t, model, 1, `{"name":"Alice Johnson","amount":100,"status":"active"}`)
	createEntityE2E(t, model, 1, `{"name":"Bob Smith","amount":50,"status":"active"}`)
	createEntityE2E(t, model, 1, `{"name":"Alice Williams","amount":75,"status":"active"}`)

	// STARTS_WITH "Alice"
	cond := `{"type":"simple","jsonPath":"$.name","operatorType":"STARTS_WITH","value":"Alice"}`
	_, results := directSearch(t, model, 1, cond)
	if len(results) != 2 {
		t.Errorf("expected 2 results starting with Alice, got %d", len(results))
	}

	// CONTAINS "Smith"
	cond = `{"type":"simple","jsonPath":"$.name","operatorType":"CONTAINS","value":"Smith"}`
	_, results = directSearch(t, model, 1, cond)
	if len(results) != 1 {
		t.Errorf("expected 1 result containing Smith, got %d", len(results))
	}
}

// --- Test 7.8: Search with OR group ---

func TestSearch_ORGroup(t *testing.T) {
	const model = "e2e-search-8"
	setupSearchModel(t, model)

	createEntityE2E(t, model, 1, `{"name":"Alice","amount":10,"status":"draft"}`)
	createEntityE2E(t, model, 1, `{"name":"Bob","amount":200,"status":"active"}`)
	createEntityE2E(t, model, 1, `{"name":"Carol","amount":50,"status":"active"}`)

	// OR: name == "Alice" OR amount > 100
	cond := `{
		"type": "group",
		"operator": "OR",
		"conditions": [
			{"type":"simple","jsonPath":"$.name","operatorType":"EQUALS","value":"Alice"},
			{"type":"simple","jsonPath":"$.amount","operatorType":"GREATER_THAN","value":100}
		]
	}`
	status, results := directSearch(t, model, 1, cond)
	if status != http.StatusOK {
		t.Fatalf("search: expected 200, got %d", status)
	}

	// Alice (name match) + Bob (amount > 100) = 2
	if len(results) != 2 {
		names := make([]string, 0)
		for _, r := range results {
			if d, ok := r["data"].(map[string]any); ok {
				if n, ok := d["name"].(string); ok {
					names = append(names, n)
				}
			}
		}
		t.Errorf("expected 2 results (Alice + Bob), got %d: %v", len(results), strings.Join(names, ", "))
	}
}
