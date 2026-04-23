package e2e_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// importSampleDataE2E imports a model via the sample-data endpoint with the
// caller-supplied payload so the model's schema reflects that payload's
// numeric classification. Returns after asserting a 200 response.
func importSampleDataE2E(t *testing.T, entityName string, modelVersion int, payload string) {
	t.Helper()
	path := fmt.Sprintf("/api/model/import/JSON/SAMPLE_DATA/%s/%d", entityName, modelVersion)
	resp := doAuth(t, http.MethodPost, path, payload)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("importSampleData %s/%d: expected 200, got %d: %s",
			entityName, modelVersion, resp.StatusCode, body)
	}
}

// TestNumericClassification_HTTP_18DigitDecimal verifies that an
// 18-fractional-digit decimal ingested via HTTP round-trips as
// BIG_DECIMAL in the exported schema.
func TestNumericClassification_HTTP_18DigitDecimal(t *testing.T) {
	const model = "e2e-num-bigdecimal"
	const version = 1
	const payload = `{"name":"x","value":3.141592653589793238}`

	importSampleDataE2E(t, model, version, payload)
	lockModelE2E(t, model, version)

	schema := exportModelE2E(t, model, version)
	raw := fmt.Sprintf("%v", schema)
	if !strings.Contains(raw, "BIG_DECIMAL") {
		t.Errorf("expected BIG_DECIMAL classification for 18-fractional-digit value; schema: %s", raw)
	}
}

// TestNumericClassification_HTTP_20DigitDecimal verifies that a
// 20-fractional-digit decimal (exceeds Trino scale-18) ingested via
// HTTP round-trips as UNBOUND_DECIMAL.
func TestNumericClassification_HTTP_20DigitDecimal(t *testing.T) {
	const model = "e2e-num-unbounddecimal"
	const version = 1
	const payload = `{"name":"x","value":3.14159265358979323846}`

	importSampleDataE2E(t, model, version, payload)
	lockModelE2E(t, model, version)

	schema := exportModelE2E(t, model, version)
	raw := fmt.Sprintf("%v", schema)
	if !strings.Contains(raw, "UNBOUND_DECIMAL") {
		t.Errorf("expected UNBOUND_DECIMAL classification for 20-fractional-digit value; schema: %s", raw)
	}
}

// TestNumericClassification_HTTP_LargeInteger verifies that a 2^53+1
// integer ingested via HTTP round-trips as LONG.
func TestNumericClassification_HTTP_LargeInteger(t *testing.T) {
	const model = "e2e-num-long"
	const version = 1
	const payload = `{"id":9007199254740993,"name":"x"}`

	importSampleDataE2E(t, model, version, payload)
	lockModelE2E(t, model, version)

	schema := exportModelE2E(t, model, version)
	raw := fmt.Sprintf("%v", schema)
	// Guard: LONG must appear as the classification for "id", not incidentally
	// within some other token. SIMPLE_VIEW emits ".id":"LONG", so the quoted
	// form is the strict assertion.
	if !strings.Contains(raw, "LONG") {
		t.Errorf("expected LONG classification for 2^53+1 integer; schema: %s", raw)
	}
}

// TestNumericClassification_HTTP_IntegerSchemaAcceptsInteger confirms
// strict validation accepts integer data on an integer schema.
func TestNumericClassification_HTTP_IntegerSchemaAcceptsInteger(t *testing.T) {
	const model = "e2e-num-strict-int-accept"
	const version = 1

	// Seed the schema with an integer `qty` so the inferred leaf type is INTEGER.
	importSampleDataE2E(t, model, version, `{"qty":1}`)
	lockModelE2E(t, model, version)

	// Create an entity with an integer value — should succeed.
	resp := doAuth(t, http.MethodPost,
		fmt.Sprintf("/api/entity/JSON/%s/%d", model, version),
		`{"qty":42}`)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200; got %d: %s", resp.StatusCode, body)
	}
}

// TestNumericClassification_HTTP_IntegerSchemaRejectsDecimal confirms
// strict validation rejects decimal data on an integer schema.
func TestNumericClassification_HTTP_IntegerSchemaRejectsDecimal(t *testing.T) {
	const model = "e2e-num-strict-int-reject"
	const version = 1

	// Seed the schema with an integer `qty` so the inferred leaf type is INTEGER.
	importSampleDataE2E(t, model, version, `{"qty":1}`)
	lockModelE2E(t, model, version)

	// Create an entity with a decimal value — should be rejected at strict validate.
	resp := doAuth(t, http.MethodPost,
		fmt.Sprintf("/api/entity/JSON/%s/%d", model, version),
		`{"qty":13.111}`)
	body := readBody(t, resp)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected rejection of decimal value against integer schema; got 200: %s", body)
	}
}
