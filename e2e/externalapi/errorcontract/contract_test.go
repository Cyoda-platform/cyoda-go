package errorcontract_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/errorcontract"
)

func TestMatch_StatusAndErrorCode_RFC9457Body(t *testing.T) {
	body := []byte(`{
		"type":"about:blank","title":"Conflict","status":409,
		"detail":"already locked","instance":"/api/model/x/1/lock",
		"properties":{"errorCode":"MODEL_ALREADY_LOCKED","retryable":false}
	}`)
	errorcontract.Match(t, 409, body, errorcontract.ExpectedError{
		HTTPStatus: 409,
		ErrorCode:  "MODEL_ALREADY_LOCKED",
	})
}

func TestMatch_StatusMismatch_Fails(t *testing.T) {
	ft := &fakeT{}
	body := []byte(`{"type":"about:blank","title":"Bad Request","status":400,
		"properties":{"errorCode":"BAD"}}`)
	errorcontract.Match(ft, 400, body, errorcontract.ExpectedError{
		HTTPStatus: 409, ErrorCode: "BAD",
	})
	if !ft.failed {
		t.Fatal("expected Match to fail on status mismatch")
	}
}

func TestMatch_ErrorCodeMismatch_Fails(t *testing.T) {
	ft := &fakeT{}
	body := []byte(`{"type":"about:blank","status":400,
		"properties":{"errorCode":"GOT"}}`)
	errorcontract.Match(ft, 400, body, errorcontract.ExpectedError{
		HTTPStatus: 400, ErrorCode: "WANT",
	})
	if !ft.failed {
		t.Fatal("expected Match to fail on errorCode mismatch")
	}
}

func TestMatch_EmptyErrorCode_SkipsAssertion(t *testing.T) {
	body := []byte(`{"type":"about:blank","status":400,
		"properties":{"errorCode":"ANY"}}`)
	// Empty ErrorCode means "don't assert error_code".
	errorcontract.Match(t, 400, body, errorcontract.ExpectedError{HTTPStatus: 400})
}

func TestMatch_NilFields_SkipsFieldAssertion(t *testing.T) {
	body := []byte(`{"type":"about:blank","status":400,
		"properties":{"errorCode":"VALIDATION_FAILED",
			"fields":[{"path":"$.x","value":1,"entityName":"m","entityVersion":1}]}}`)
	errorcontract.Match(t, 400, body, errorcontract.ExpectedError{
		HTTPStatus: 400, ErrorCode: "VALIDATION_FAILED",
	})
}

func TestMatch_FieldsPresent_Asserted(t *testing.T) {
	body := []byte(`{"type":"about:blank","status":400,
		"properties":{"errorCode":"VALIDATION_FAILED",
			"fields":[{"path":"$.age","value":"abc","entityName":"family","entityVersion":1}]}}`)
	errorcontract.Match(t, 400, body, errorcontract.ExpectedError{
		HTTPStatus: 400, ErrorCode: "VALIDATION_FAILED",
		Fields: []errorcontract.ErrorField{
			{Path: "$.age", Value: "abc", EntityName: "family", EntityVersion: 1},
		},
	})
}

func TestMatch_MalformedBody_Fails(t *testing.T) {
	ft := &fakeT{}
	errorcontract.Match(ft, 500, []byte("not-json"), errorcontract.ExpectedError{
		HTTPStatus: 500, ErrorCode: "ANY",
	})
	if !ft.failed {
		t.Fatal("expected Match to fail on malformed body")
	}
}

func TestSchemaJSON_IsValidJSON(t *testing.T) {
	b, err := os.ReadFile("schema.json")
	if err != nil {
		t.Fatalf("read schema.json: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("schema.json is not valid JSON: %v", err)
	}
	if v["$schema"] == nil {
		t.Fatal("schema.json missing $schema key")
	}
}

// fakeT captures failure without the real testing harness aborting.
type fakeT struct {
	failed bool
	msgs   []string
}

func (f *fakeT) Errorf(format string, args ...any) { f.failed = true }
func (f *fakeT) Fatalf(format string, args ...any) { f.failed = true }
func (f *fakeT) Helper()                           {}
