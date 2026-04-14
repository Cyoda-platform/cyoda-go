package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// setChangeLevelE2E sets the changeLevel on a model.
func setChangeLevelE2E(t *testing.T, entityName string, modelVersion int, level string) {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/changeLevel/%s", entityName, modelVersion, level)
	resp := doAuth(t, http.MethodPost, path, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setChangeLevel %s/%d/%s: expected 200, got %d: %s", entityName, modelVersion, level, resp.StatusCode, body)
	}
}

// exportModelE2E exports a model as SIMPLE_VIEW and returns the parsed body.
func exportModelE2E(t *testing.T, entityName string, modelVersion int) map[string]any {
	t.Helper()
	path := fmt.Sprintf("/api/model/export/SIMPLE_VIEW/%s/%d", entityName, modelVersion)
	resp := doAuth(t, http.MethodGet, path, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exportModel %s/%d: expected 200, got %d: %s", entityName, modelVersion, resp.StatusCode, body)
	}
	var result map[string]any
	json.Unmarshal([]byte(body), &result)
	return result
}

// --- Test 9.1: STRUCTURAL changeLevel — new field added ---

func TestModelExtension_Structural(t *testing.T) {
	const model = "e2e-ext-1"

	// Import model with base fields.
	importModelE2E(t, model, 1)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "STRUCTURAL")

	// Create entity with an extra field not in the original model.
	entityID := createEntityE2E(t, model, 1, `{"name":"Test","amount":100,"status":"new","extra_field":"hello"}`)

	// Verify entity was created with the extra field.
	data := getEntityData(t, entityID, "")
	if data["extra_field"] != "hello" {
		t.Errorf("expected extra_field=hello, got %v", data["extra_field"])
	}

	// Verify the model schema was extended.
	exported := exportModelE2E(t, model, 1)
	modelStr, _ := json.Marshal(exported)
	if len(modelStr) == 0 {
		t.Fatal("empty model export")
	}
	// The exported model should mention the new field somewhere.
	exportedJSON, _ := json.MarshalIndent(exported, "", "  ")
	if !jsonContainsKey(exported, "extra_field") {
		t.Errorf("expected model schema to contain extra_field after STRUCTURAL extension.\nexport: %s", string(exportedJSON))
	}
}

// --- Test 9.2: TYPE changeLevel — type promotion ---

func TestModelExtension_TypePromotion(t *testing.T) {
	const model = "e2e-ext-2"

	// Import with integer amount.
	importModelE2E(t, model, 1)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "TYPE")

	// Create entity with float where original had integer — type should widen.
	entityID := createEntityE2E(t, model, 1, `{"name":"Test","amount":99.5,"status":"new"}`)

	data := getEntityData(t, entityID, "")
	amount, _ := data["amount"].(float64)
	if amount != 99.5 {
		t.Errorf("expected amount=99.5, got %v", data["amount"])
	}
}

// --- Test 9.3: ARRAY_ELEMENTS changeLevel ---

func TestModelExtension_ArrayElements(t *testing.T) {
	const model = "e2e-ext-3"

	// Import model from sample with an array field.
	path := fmt.Sprintf("/api/model/import/JSON/SAMPLE_DATA/%s/1", model)
	resp := doAuth(t, http.MethodPost, path, `{"name":"Test","tags":["a","b"],"amount":10,"status":"new"}`)
	readBody(t, resp)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "ARRAY_ELEMENTS")

	// Create entity with tags containing a different type element.
	entityID := createEntityE2E(t, model, 1, `{"name":"Test","tags":["x","y","z"],"amount":10,"status":"new"}`)

	data := getEntityData(t, entityID, "")
	tags, _ := data["tags"].([]any)
	if len(tags) != 3 {
		t.Errorf("expected 3 tags, got %d", len(tags))
	}
}

// --- Test 9.4: ARRAY_LENGTH changeLevel — only length changes ---

func TestModelExtension_ArrayLength(t *testing.T) {
	const model = "e2e-ext-4"

	// Import model from sample with a 2-element array.
	path := fmt.Sprintf("/api/model/import/JSON/SAMPLE_DATA/%s/1", model)
	resp := doAuth(t, http.MethodPost, path, `{"name":"Test","items":[1,2],"amount":10,"status":"new"}`)
	readBody(t, resp)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "ARRAY_LENGTH")

	// Create entity with a longer array.
	entityID := createEntityE2E(t, model, 1, `{"name":"Test","items":[1,2,3,4,5],"amount":10,"status":"new"}`)

	data := getEntityData(t, entityID, "")
	items, _ := data["items"].([]any)
	if len(items) != 5 {
		t.Errorf("expected 5 items, got %d", len(items))
	}
}

// --- Test 9.5: Locked model without changeLevel rejects new field ---

func TestModelExtension_StrictRejectsNewField(t *testing.T) {
	const model = "e2e-ext-5"

	importModelE2E(t, model, 1)
	lockModelE2E(t, model, 1)
	// No changeLevel set — strict validation.

	// Try to create entity with an extra field.
	path := fmt.Sprintf("/api/entity/JSON/%s/%d", model, 1)
	resp := doAuth(t, http.MethodPost, path, `{"name":"Test","amount":10,"status":"new","forbidden_field":"oops"}`)
	body := readBody(t, resp)

	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected rejection for extra field without changeLevel, got 200: %s", body)
	}
}

// --- Test 9.6: Multiple entities progressively extend schema ---

func TestModelExtension_ProgressiveExtension(t *testing.T) {
	const model = "e2e-ext-6"

	importModelE2E(t, model, 1)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "STRUCTURAL")

	// First entity adds field_a.
	createEntityE2E(t, model, 1, `{"name":"First","amount":10,"status":"new","field_a":"value_a"}`)

	// Second entity adds field_b (field_a should already be in schema).
	createEntityE2E(t, model, 1, `{"name":"Second","amount":20,"status":"new","field_b":"value_b"}`)

	// Verify model has both new fields.
	exported := exportModelE2E(t, model, 1)
	if !jsonContainsKey(exported, "field_a") {
		t.Error("expected model to contain field_a")
	}
	if !jsonContainsKey(exported, "field_b") {
		t.Error("expected model to contain field_b")
	}
}

// jsonContainsKey recursively checks if a JSON structure contains a given key.
// In SIMPLE_VIEW format, field names are dot-prefixed (e.g., ".extra_field"),
// so we also check for that variant.
func jsonContainsKey(obj map[string]any, key string) bool {
	dotKey := "." + key
	for k, v := range obj {
		if k == key || k == dotKey {
			return true
		}
		switch child := v.(type) {
		case map[string]any:
			if jsonContainsKey(child, key) {
				return true
			}
		case []any:
			for _, item := range child {
				if m, ok := item.(map[string]any); ok {
					if jsonContainsKey(m, key) {
						return true
					}
				}
			}
		}
	}
	return false
}
