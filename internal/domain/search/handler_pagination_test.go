package search_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Regression tests for issue #98: async pagination parameters on
// GET /api/search/async/{jobId} must reject out-of-bound / overflow-prone
// values the same way the sync path does. Validation must happen BEFORE
// job lookup — confirmed by asserting the response body surfaces the
// pagination error rather than a generic "job not found".

// TestGetAsyncResults_PageSizeExceedsCap_RejectedBeforeJobLookup — the sync
// path caps pageSize at 10_000 (handler.go sync branch, line ~62); the
// async branch previously only checked for negatives, so pageSize=100_000
// was accepted.
func TestGetAsyncResults_PageSizeExceedsCap_RejectedBeforeJobLookup(t *testing.T) {
	srv := newTestServer(t)
	jobID := uuid.New().String()

	resp := doGetAsyncResults(t, srv.URL, jobID, "pageSize=100000")
	defer resp.Body.Close()

	body := readBodyStr(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pageSize=100000 → got %d, want 400; body: %s", resp.StatusCode, body)
	}
	// The error must come from pagination validation, not from the
	// downstream job lookup. "job not found" text means validation was
	// skipped.
	if strings.Contains(body, "job "+jobID+" not found") || strings.Contains(body, "job not found") {
		t.Errorf("validation should reject before job lookup, but got job-not-found response: %s", body)
	}
	if !strings.Contains(strings.ToLower(body), "pagesize") {
		t.Errorf("expected pageSize error in body, got: %s", body)
	}
}

// TestGetAsyncResults_PageNumberTimesPageSizeOverflow_RejectedBeforeJobLookup —
// `opts.Offset = pageNumber * pageSize` is a plain int multiplication
// which can wrap with attacker-supplied values. The handler must detect
// and reject overflow before reaching the job lookup.
func TestGetAsyncResults_PageNumberTimesPageSizeOverflow_RejectedBeforeJobLookup(t *testing.T) {
	srv := newTestServer(t)
	jobID := uuid.New().String()

	resp := doGetAsyncResults(t, srv.URL, jobID, "pageNumber=9223372036854775807", "pageSize=1000")
	defer resp.Body.Close()

	body := readBodyStr(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pageNumber=MaxInt64, pageSize=1000 → got %d, want 400; body: %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "job "+jobID+" not found") || strings.Contains(body, "job not found") {
		t.Errorf("validation should reject before job lookup, but got job-not-found response: %s", body)
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "overflow") && !strings.Contains(lower, "pagenumber") {
		t.Errorf("expected overflow/pageNumber error in body, got: %s", body)
	}
}

// TestGetAsyncResults_PageNumberExceedsCap_RejectedBeforeJobLookup — even
// when the multiplication pageNumber × pageSize fits in int64, an absurd
// pageNumber by itself is a sign of misuse (e.g. attacker probing). The
// async path should enforce an explicit pageNumber upper cap consistent
// with the sync path's pageSize cap. With pageSize=10000 (the cap) and
// pageNumber=MaxInt32 the product is ~2.1e13 which fits in int64, so the
// overflow check alone would NOT reject — only an explicit pageNumber cap
// catches this (issue #68 item 10).
func TestGetAsyncResults_PageNumberExceedsCap_RejectedBeforeJobLookup(t *testing.T) {
	srv := newTestServer(t)
	jobID := uuid.New().String()

	resp := doGetAsyncResults(t, srv.URL, jobID, "pageNumber=2147483647", "pageSize=10000")
	defer resp.Body.Close()

	body := readBodyStr(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pageNumber=MaxInt32, pageSize=10000 → got %d, want 400; body: %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "job "+jobID+" not found") || strings.Contains(body, "job not found") {
		t.Errorf("validation should reject before job lookup, but got job-not-found response: %s", body)
	}
	if !strings.Contains(strings.ToLower(body), "pagenumber") {
		t.Errorf("expected pageNumber error in body, got: %s", body)
	}
}

func readBodyStr(t *testing.T, resp *http.Response) string {
	t.Helper()
	return string(readBody(t, resp))
}
