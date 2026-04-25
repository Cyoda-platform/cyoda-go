// Package errorcontract defines a normalised, cross-language view of an
// HTTP error response. cyoda-go emits RFC 9457 Problem Details on the
// wire; this package parses that shape into a struct that any Cyoda
// implementation (cyoda-go, cyoda-cloud) is required to map into — no
// matter the precise wire format.
package errorcontract

import (
	"encoding/json"
	"fmt"
)

// ExpectedError is the test-side, language-neutral view of an error
// response. Zero-value fields are treated as "don't assert".
type ExpectedError struct {
	HTTPStatus int
	ErrorCode  string       // empty = not asserted
	Fields     []ErrorField // nil = not asserted
}

// ErrorField is one entry in the optional per-field diagnostic array.
type ErrorField struct {
	Path          string
	Value         any
	EntityName    string
	EntityVersion int
}

// TB is the subset of *testing.T we need. Accepting an interface makes
// the matcher testable via a fake without the real testing harness.
type TB interface {
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Helper()
}

// Match asserts that httpStatus + body satisfy want. Parses body as
// RFC 9457 Problem Details (the cyoda-go wire format) and normalises:
//
//	properties.errorCode  -> ErrorCode
//	properties.fields     -> Fields
//
// A zero ErrorCode or nil Fields in want skips that sub-assertion.
func Match(t TB, httpStatus int, body []byte, want ExpectedError) {
	t.Helper()

	if httpStatus != want.HTTPStatus {
		t.Errorf("http_status mismatch: got %d, want %d", httpStatus, want.HTTPStatus)
	}

	if len(body) == 0 {
		if want.ErrorCode != "" || len(want.Fields) > 0 {
			t.Errorf("body empty but want ErrorCode=%q or Fields=%d", want.ErrorCode, len(want.Fields))
		}
		return
	}

	var rfc rfc9457
	if err := json.Unmarshal(body, &rfc); err != nil {
		t.Errorf("body not valid JSON: %v", err)
		return
	}

	if want.ErrorCode != "" && rfc.Properties.ErrorCode != want.ErrorCode {
		t.Errorf("error_code mismatch: got %q, want %q", rfc.Properties.ErrorCode, want.ErrorCode)
	}

	if want.Fields != nil {
		if len(rfc.Properties.Fields) != len(want.Fields) {
			t.Errorf("fields length: got %d, want %d", len(rfc.Properties.Fields), len(want.Fields))
			return
		}
		for i, got := range rfc.Properties.Fields {
			w := want.Fields[i]
			if got.Path != w.Path {
				t.Errorf("fields[%d].path: got %q, want %q", i, got.Path, w.Path)
			}
			if fmt.Sprint(got.Value) != fmt.Sprint(w.Value) {
				t.Errorf("fields[%d].value: got %v, want %v", i, got.Value, w.Value)
			}
			if got.EntityName != w.EntityName {
				t.Errorf("fields[%d].entityName: got %q, want %q", i, got.EntityName, w.EntityName)
			}
			if got.EntityVersion != w.EntityVersion {
				t.Errorf("fields[%d].entityVersion: got %d, want %d", i, got.EntityVersion, w.EntityVersion)
			}
		}
	}
}

type rfc9457 struct {
	Type       string            `json:"type"`
	Title      string            `json:"title"`
	Status     int               `json:"status"`
	Detail     string            `json:"detail,omitempty"`
	Instance   string            `json:"instance,omitempty"`
	Properties rfc9457Properties `json:"properties,omitempty"`
}

type rfc9457Properties struct {
	ErrorCode string       `json:"errorCode"`
	Retryable bool         `json:"retryable,omitempty"`
	Fields    []ErrorField `json:"fields,omitempty"`
}
