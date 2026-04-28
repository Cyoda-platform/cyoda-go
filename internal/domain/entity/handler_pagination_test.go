package entity_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Regression tests for PR #149 follow-up: GET /entity/{name}/{version} must
// reject out-of-bound pagination parameters with the same caps as the
// search endpoints. Validation must happen BEFORE the storage lookup —
// asserted by passing an unknown entity name (would otherwise be a 404)
// alongside oversized params and confirming the response is a 400 with a
// pagination message rather than a not-found.

// TestGetAllEntities_PageSizeExceedsCap_RejectedBeforeStorage — pageSize
// above MaxPageSize must surface 400 even when the entity model is
// unknown.
func TestGetAllEntities_PageSizeExceedsCap_RejectedBeforeStorage(t *testing.T) {
	srv := newTestServer(t)
	resp := doGetAllEntitiesRaw(t, srv.URL, "UnknownModel", 1, "pageSize=100000&pageNumber=0")
	defer resp.Body.Close()
	body := string(readBody(t, resp))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pageSize=100000 → got %d, want 400; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(strings.ToLower(body), "pagesize") {
		t.Errorf("expected pageSize error in body, got: %s", body)
	}
}

// TestGetAllEntities_PageNumberExceedsCap_RejectedBeforeStorage — even
// though int32×int32 cannot overflow Go's int64-promoted product on
// supported platforms, an absurd pageNumber that would let an attacker
// blow past realistic snapshots must be capped consistently with the
// async-search path.
func TestGetAllEntities_PageNumberExceedsCap_RejectedBeforeStorage(t *testing.T) {
	srv := newTestServer(t)
	// MaxPageNumber = MaxInt32 / MaxPageSize = 2147483647 / 10000 = 214748;
	// pageNumber=2147483647 is well above it.
	resp := doGetAllEntitiesRaw(t, srv.URL, "UnknownModel", 1, "pageSize=10&pageNumber=2147483647")
	defer resp.Body.Close()
	body := string(readBody(t, resp))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pageNumber=MaxInt32 → got %d, want 400; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(strings.ToLower(body), "pagenumber") {
		t.Errorf("expected pageNumber error in body, got: %s", body)
	}
}

// doGetAllEntitiesRaw lets a test pass the query string verbatim so it
// can supply oversized values that would not round-trip through the
// integer-typed helper.
func doGetAllEntitiesRaw(t *testing.T, base, entityName string, version int, query string) *http.Response {
	t.Helper()
	url := fmt.Sprintf("%s/entity/%s/%d?%s", base, entityName, version, query)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get all entities request failed: %v", err)
	}
	return resp
}
