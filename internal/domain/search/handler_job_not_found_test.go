package search_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Regression test for issue #93.
//
// All three async-search endpoints that lookup by job UUID must return
// `errorCode: SEARCH_JOB_NOT_FOUND` with HTTP 404 when the job does not
// exist — previously they returned `ENTITY_NOT_FOUND`, which collides with
// the entity-by-id error and breaks typed client classifiers.

func extractErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal problem body: %v; body=%s", err, string(body))
	}
	props, _ := doc["properties"].(map[string]any)
	code, _ := props["errorCode"].(string)
	return code
}

func TestGetAsyncStatus_UnknownJob_ReturnsSearchJobNotFound(t *testing.T) {
	srv := newTestServer(t)
	jobID := uuid.New().String()

	resp := doGetAsyncStatus(t, srv.URL, jobID)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, body)
	}
	if got := extractErrorCode(t, body); got != "SEARCH_JOB_NOT_FOUND" {
		t.Errorf("errorCode = %q, want %q; body: %s", got, "SEARCH_JOB_NOT_FOUND", body)
	}
}

func TestGetAsyncResults_UnknownJob_ReturnsSearchJobNotFound(t *testing.T) {
	srv := newTestServer(t)
	jobID := uuid.New().String()

	resp := doGetAsyncResults(t, srv.URL, jobID)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, body)
	}
	if got := extractErrorCode(t, body); got != "SEARCH_JOB_NOT_FOUND" {
		t.Errorf("errorCode = %q, want %q; body: %s", got, "SEARCH_JOB_NOT_FOUND", body)
	}
}

func TestCancelAsync_UnknownJob_ReturnsSearchJobNotFound(t *testing.T) {
	srv := newTestServer(t)
	jobID := uuid.New().String()

	resp := doCancelAsync(t, srv.URL, jobID)
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, body)
	}
	if got := extractErrorCode(t, body); got != "SEARCH_JOB_NOT_FOUND" {
		t.Errorf("errorCode = %q, want %q; body: %s", got, "SEARCH_JOB_NOT_FOUND", body)
	}
}
