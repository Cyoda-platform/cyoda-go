package search_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSearch_ConditionTypeMismatch_Rejects verifies that a search condition
// whose value type does not match the field's locked DataType is rejected
// with HTTP 400 and error code CONDITION_TYPE_MISMATCH.
//
// Regression for the 14/06 parity finding: cyoda-go previously accepted
// string values against DOUBLE fields and silently returned empty results.
// The dictionary expects InvalidTypesInClientConditionException (HTTP 400).
func TestSearch_ConditionTypeMismatch_DirectSearch_Returns400(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "ordersWrong", 1, `{"price": 100.0}`)

	const badCondition = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.price","operatorType":"GREATER_OR_EQUAL","value":"abc"}
		]
	}`

	resp := doDirectSearch(t, srv.URL, "ordersWrong", 1, badCondition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("direct search: expected 400, got %d; body: %s", resp.StatusCode, body)
	}

	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("parse response body: %v; raw: %s", err, body)
	}
	props, _ := obj["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("expected properties in error response; body: %s", body)
	}
	errorCode, _ := props["errorCode"].(string)
	if errorCode != "CONDITION_TYPE_MISMATCH" {
		t.Errorf("errorCode = %q, want CONDITION_TYPE_MISMATCH; body: %s", errorCode, body)
	}
}

func TestSearch_ConditionTypeMismatch_AsyncSubmit_Returns400(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "ordersWrong2", 1, `{"price": 100.0}`)

	const badCondition = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.price","operatorType":"GREATER_OR_EQUAL","value":"abc"}
		]
	}`

	resp := doSubmitAsync(t, srv.URL, "ordersWrong2", 1, badCondition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("async submit: expected 400, got %d; body: %s", resp.StatusCode, body)
	}

	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("parse response body: %v; raw: %s", err, body)
	}
	props, _ := obj["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("expected properties in error response; body: %s", body)
	}
	errorCode, _ := props["errorCode"].(string)
	if errorCode != "CONDITION_TYPE_MISMATCH" {
		t.Errorf("errorCode = %q, want CONDITION_TYPE_MISMATCH; body: %s", errorCode, body)
	}
}

// TestSearch_ConditionTypeMatch_Accepted verifies that a search condition
// whose value type matches the field's DataType (INTEGER value against
// INTEGER field) is not rejected.
func TestSearch_ConditionTypeMatch_Accepted(t *testing.T) {
	srv := newTestServer(t)
	// Use 100 (integer) so the model field is classified as INTEGER.
	importAndLockModel(t, srv.URL, "ordersGood", 1, `{"price": 100}`)

	// Integer condition value against INTEGER model field — must be accepted.
	const goodCondition = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.price","operatorType":"GREATER_OR_EQUAL","value":50}
		]
	}`

	resp := doDirectSearch(t, srv.URL, "ordersGood", 1, goodCondition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("direct search with valid type: expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestSearch_ConditionTypeMatch_DoubleField_Accepted verifies that a DOUBLE
// condition value against a DOUBLE field is accepted.
func TestSearch_ConditionTypeMatch_DoubleField_Accepted(t *testing.T) {
	srv := newTestServer(t)
	// Use 100.5 (decimal) so the model field is classified as DOUBLE.
	importAndLockModel(t, srv.URL, "ordersDouble", 1, `{"price": 100.5}`)

	// Double condition value against DOUBLE model field — must be accepted.
	const goodCondition = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.price","operatorType":"GREATER_OR_EQUAL","value":50.5}
		]
	}`

	resp := doDirectSearch(t, srv.URL, "ordersDouble", 1, goodCondition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("direct search with double value on double field: expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestSearch_ConditionType_IntegerFieldWithStringValue_Rejects verifies that
// a STRING condition value against an INTEGER field is rejected.
func TestSearch_ConditionType_IntegerFieldWithStringValue_Rejects(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "stationsInt", 1, `{"station_id": 42}`)

	const condition = `{
		"type":"simple","jsonPath":"$.station_id","operatorType":"EQUALS","value":"text-value"
	}`
	resp := doDirectSearch(t, srv.URL, "stationsInt", 1, condition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("string on int field: expected 400, got %d; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "CONDITION_TYPE_MISMATCH") {
		t.Errorf("body missing CONDITION_TYPE_MISMATCH: %s", body)
	}
}

// TestSearch_ConditionType_UnknownField_Accepted verifies that a search
// condition referencing an unknown field path is not rejected by the
// type-checking pass (unknown paths have no type constraint).
func TestSearch_ConditionType_UnknownField_Accepted(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "simpleModel", 1, `{"name": "Alice"}`)

	// Unknown field "$.unknown" — no type constraint, should not reject.
	const condition = `{
		"type":"simple","jsonPath":"$.unknown","operatorType":"EQUALS","value":"whatever"
	}`
	resp := doDirectSearch(t, srv.URL, "simpleModel", 1, condition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown field path: expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestSearch_ConditionType_NullValue_Accepted verifies that a null condition
// value is not rejected (null is compatible with any type).
func TestSearch_ConditionType_NullValue_Accepted(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "nullableModel", 1, `{"price": 100.0}`)

	const condition = `{
		"type":"simple","jsonPath":"$.price","operatorType":"IS_NULL","value":null
	}`
	resp := doDirectSearch(t, srv.URL, "nullableModel", 1, condition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("null value: expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestSearch_ConditionType_LifecycleCondition_Accepted verifies that lifecycle
// conditions (not subject to data-field type checking) are accepted.
func TestSearch_ConditionType_LifecycleCondition_Accepted(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "lifecycleModel", 1, `{"price": 100.0}`)

	const condition = `{
		"type":"lifecycle","field":"state","operatorType":"EQUALS","value":"CREATED"
	}`
	resp := doDirectSearch(t, srv.URL, "lifecycleModel", 1, condition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lifecycle condition: expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestSearch_ConditionType_StringFieldWithStringValue_Accepted verifies that
// a string value against a STRING field passes type checking.
func TestSearch_ConditionType_StringFieldWithStringValue_Accepted(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "nameModel", 1, `{"name": "Alice"}`)

	const condition = `{
		"type":"simple","jsonPath":"$.name","operatorType":"EQUALS","value":"Bob"
	}`
	resp := doDirectSearch(t, srv.URL, "nameModel", 1, condition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("string-on-string: expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestSearch_IsNullNotNull_SkipTypeCheck verifies that IS_NULL / NOT_NULL
// operators bypass value-type checking (they don't use the value field).
func TestSearch_IsNullNotNull_SkipTypeCheck(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "isNullModel", 1, `{"price": 100.0}`)

	for _, op := range []string{"IS_NULL", "NOT_NULL"} {
		t.Run(op, func(t *testing.T) {
			condition := `{"type":"simple","jsonPath":"$.price","operatorType":"` + op + `","value":null}`
			resp := doDirectSearch(t, srv.URL, "isNullModel", 1, condition)
			defer resp.Body.Close()
			body := readBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: expected 200, got %d; body: %s", op, resp.StatusCode, body)
			}
		})
	}
}

// TestSearch_StringFieldWithNumericValue_Accepted verifies that a numeric
// condition value against a STRING-only field is accepted. String fields
// accept any comparison value (numeric or string) to support lexicographic
// and coerced comparisons. Only numeric/boolean field types are strictly
// validated against the value's DataType.
func TestSearch_StringFieldWithNumericValue_Accepted(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "strOnlyModel", 1, `{"name": "Alice"}`)

	const condition = `{
		"type":"simple","jsonPath":"$.name","operatorType":"EQUALS","value":42
	}`
	resp := doDirectSearch(t, srv.URL, "strOnlyModel", 1, condition)
	defer resp.Body.Close()
	body := readBody(t, resp)

	// Numeric value against STRING field is accepted (lexicographic comparison).
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("numeric on string field: expected 200 (accepted), got %d; body: %s", resp.StatusCode, body)
	}
}
