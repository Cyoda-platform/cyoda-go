// Package commontest holds test-only helpers shared across domain packages.
// Helpers here parse / assert against contracts defined in
// internal/common (RFC 9457 problem-detail envelopes, AppError shape).
package commontest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ExpectErrorCode parses an RFC 9457 problem-detail body from resp and
// asserts that `properties.errorCode` matches want. The response body is
// consumed and re-buffered so callers can still close it (or read it again).
func ExpectErrorCode(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	var pd struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(body, &pd); err != nil {
		t.Fatalf("decode problem detail: %v; body: %s", err, string(body))
	}
	got, _ := pd.Properties["errorCode"].(string)
	if got != want {
		t.Errorf("expected errorCode %q, got %q; body: %s", want, got, string(body))
	}
}
