package driver_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
)

// TestNewRemote_UsesProvidedBaseURLAndToken verifies the remote constructor
// issues requests against the given base URL with the given bearer token —
// no fixture state leaks into the call path.
func TestNewRemote_UsesProvidedBaseURLAndToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	d := driver.NewRemote(t, srv.URL, "remote-jwt-token")
	if err := d.ListModelsDiscard(); err != nil {
		t.Fatalf("ListModelsDiscard: %v", err)
	}
	if !strings.Contains(gotAuth, "remote-jwt-token") {
		t.Errorf("Authorization header: got %q, want it to contain the token", gotAuth)
	}
}

func TestNewRemote_NoToken_EmptyBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	d := driver.NewRemote(t, srv.URL, "")
	_ = d.ListModelsDiscard()
	if gotAuth != "Bearer " && gotAuth != "" {
		t.Errorf("empty token should produce empty or bare Bearer header, got %q", gotAuth)
	}
	_ = io.Discard
}
