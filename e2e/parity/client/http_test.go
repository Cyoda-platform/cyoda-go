package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Canonical v1 (time-based) UUID matching cyoda-go's UUIDGenerator
// convention per docs/milestones/m3-entity-crud/design.md decision #10.
const fixtureEntityID = "a9d92c64-3f49-11b2-ad21-125557264b03"

// TestHTTPClient_DisallowUnknownFields proves the parity HTTP client
// rejects responses containing fields that the value types do not
// declare. This is the guard rail that catches API drift: when cyoda
// adds a new field to EntityMetadata, the parity test set fails
// until the value types in types.go are updated.
//
// The guard combines two layers:
//   - The HTTP client uses json.Decoder.DisallowUnknownFields() on
//     every response (this test).
//   - The value types (types.go, audit.go) each use a flat-alias
//     pattern with their own DisallowUnknownFields enforcement for
//     embedded-type and discriminated-union cases (tested separately
//     in types_test.go and audit_test.go).
//
// The two layers together catch drift at every level — top-level
// envelope, nested objects, and discriminated-union subtypes.
func TestHTTPClient_DisallowUnknownFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Response shape mirrors a canonical entity envelope with the
		// modelKey populated (matches the GET /entity/{id} contract per
		// approved deviation A2). One bogus field ("futureField") is
		// added under meta to verify DisallowUnknownFields rejects it.
		_, _ = w.Write([]byte(`{
			"type": "ENTITY",
			"data": {"name": "alice"},
			"meta": {
				"id": "` + fixtureEntityID + `",
				"state": "DRAFT",
				"creationDate": "2026-04-09T10:00:00Z",
				"lastUpdateTime": "2026-04-09T10:00:00Z",
				"transactionId": "b1234567-3f49-11b2-ad21-125557264b03",
				"modelKey": {"name": "Person", "version": 1},
				"futureField": "should be rejected"
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "no-token-needed-for-this-test")

	_, err := c.GetEntity(t, uuid.MustParse(fixtureEntityID))
	if err == nil {
		t.Fatal("expected unmarshal error from unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "futureField") {
		t.Fatalf("error should mention the unknown field name, got: %v", err)
	}
}

// TestDecodeJSONResponse_ChunkedEmptyBody guards against the latent
// chunked-response bug in the original doJSON: net/http sets
// resp.ContentLength = -1 for chunked or unknown-length responses,
// and the original code's `ContentLength == 0` skip-condition would
// fall through and try to decode an empty body, returning io.EOF
// wrapped as a decoder error. The current decodeJSONResponse must
// treat io.EOF as "nothing to decode" cleanly.
func TestDecodeJSONResponse_ChunkedEmptyBody(t *testing.T) {
	// Synthesize a chunked-style response with ContentLength = -1 and
	// an empty body. Real net/http produces this whenever a server
	// uses Transfer-Encoding: chunked or omits Content-Length.
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: -1,
		Body:          io.NopCloser(strings.NewReader("")),
		Header:        make(http.Header),
	}
	defer resp.Body.Close()

	var out EntityResult
	if err := decodeJSONResponse(resp, &out); err != nil {
		t.Fatalf("decodeJSONResponse on chunked-empty body should succeed cleanly, got: %v", err)
	}
}

// TestDecodeJSONResponse_NilOutSkipsDecode verifies that passing
// out=nil drains the body and returns without trying to decode.
func TestDecodeJSONResponse_NilOutSkipsDecode(t *testing.T) {
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: 42,
		Body:          io.NopCloser(strings.NewReader(`{"any":"shape"}`)),
		Header:        make(http.Header),
	}
	defer resp.Body.Close()

	if err := decodeJSONResponse(resp, nil); err != nil {
		t.Fatalf("decodeJSONResponse with out=nil must succeed cleanly, got: %v", err)
	}
}
