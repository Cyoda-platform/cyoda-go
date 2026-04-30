package openapivalidator

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddleware_ForwardsResponseAndCaptures(t *testing.T) {
	v := newFixtureValidator(t)
	defaultCollector = newCollector() // reset for test

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"transactionId":"abc","entityIds":["e1"]}`)
	})
	wrapped := NewMiddleware(v)(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/single", nil)
	wrapped.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("forwarded status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"transactionId"`) {
		t.Errorf("body not forwarded: %q", rec.Body.String())
	}
	exercised := defaultCollector.exerciseSet()
	if !exercised["getSingle"] {
		t.Errorf("getSingle not recorded as exercised: %v", exercised)
	}
	if got := len(defaultCollector.drain()); got != 0 {
		t.Errorf("expected 0 mismatches, got %d", got)
	}
}

func TestMiddleware_RecordsMismatchOnDriftedResponse(t *testing.T) {
	v := newFixtureValidator(t)
	defaultCollector = newCollector()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `[{"transactionId":"abc","entityIds":["e1"]}]`) // array, but spec says single object
	})
	wrapped := NewMiddleware(v)(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/single", nil)
	wrapped.ServeHTTP(rec, req)

	mismatches := defaultCollector.drain()
	if len(mismatches) == 0 {
		t.Fatal("expected mismatch for array-vs-object response, got none")
	}
	if mismatches[0].Operation != "getSingle" {
		t.Errorf("operation = %q, want getSingle", mismatches[0].Operation)
	}
}

// In ModeRecord, the middleware never calls t.Errorf on the captured *T,
// even if a -run filter is active.
func TestMiddleware_RecordModeNeverFailsTest(t *testing.T) {
	if Mode != ModeRecord {
		t.Skip("test assumes default Mode == ModeRecord; skipped after the commit-11 flip")
	}
	v := newFixtureValidator(t)
	defaultCollector = newCollector()

	// Force the run-filter detection to true for this test by passing a
	// pretend filter via flag.
	if err := flag.Set("test.run", "Forced"); err != nil {
		t.Fatalf("set test.run: %v", err)
	}
	defer flag.Set("test.run", "")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `[{"oops":"array"}]`)
	})
	wrapped := NewMiddleware(v)(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/single", nil)
	ctx := WithTestT(req.Context(), t) // captured *T is the surrounding test
	req = req.WithContext(ctx)
	wrapped.ServeHTTP(rec, req)

	// In record mode this test must NOT fail despite the drift. Verified by
	// the fact that the test reaches this point without t.Errorf having
	// fired (would have marked it failed).
	if t.Failed() {
		t.Fatal("middleware called t.Errorf in record mode; should only fire in enforce mode")
	}
}
