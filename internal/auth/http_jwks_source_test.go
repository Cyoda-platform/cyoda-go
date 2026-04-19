package auth_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/auth"
)

// testTransportTrusting builds an http.Transport whose TLS config trusts the
// given httptest.Server's self-signed cert AND pins MinVersion to TLS 1.3
// (mirroring production). Tests use this to exercise the real TLS hardening
// against httptest servers.
func testTransportTrusting(srv *httptest.Server) *http.Transport {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    pool,
		},
	}
}

// jwksJSONBody returns a minimal but well-formed JWKS body for tests that
// don't care about the keys themselves — only the HTTP-layer behavior.
func jwksJSONBody(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(struct {
		Keys []any `json:"keys"`
	}{Keys: []any{}})
	if err != nil {
		t.Fatalf("marshal jwks body: %v", err)
	}
	return b
}

func TestHTTPJWKSSource_RejectsTLS12OnlyEndpoint(t *testing.T) {
	body := jwksJSONBody(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	srv.TLS = &tls.Config{MaxVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()

	src := auth.NewHTTPJWKSSourceWithTransportForTesting(srv.URL, 5*time.Minute, testTransportTrusting(srv))
	_, err := src.GetKey("any")
	if err == nil {
		t.Fatal("expected handshake failure against TLS 1.2-only endpoint, got nil")
	}
	// Should be a TLS handshake / protocol version failure. Don't overfit on
	// the exact message, but require it to NOT look like a 404 or body error.
	msg := err.Error()
	if strings.Contains(msg, "status") || strings.Contains(msg, "invalid JWKS JSON") {
		t.Fatalf("expected TLS/handshake error, got HTTP-level error: %v", err)
	}
}

func TestHTTPJWKSSource_RejectsHTMLContentType(t *testing.T) {
	body := jwksJSONBody(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := auth.NewHTTPJWKSSourceWithTransportForTesting(srv.URL, 5*time.Minute, testTransportTrusting(srv))
	_, err := src.GetKey("any")
	if err == nil {
		t.Fatal("expected content-type rejection, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "content-type") {
		t.Fatalf("expected error to mention content-type, got: %v", err)
	}
}

func TestHTTPJWKSSource_AcceptsApplicationJSON(t *testing.T) {
	body := jwksJSONBody(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := auth.NewHTTPJWKSSourceWithTransportForTesting(srv.URL, 5*time.Minute, testTransportTrusting(srv))
	// Empty key set → GetKey returns ErrKeyNotFound, NOT a transport/content-type
	// error. Confirms the fetch succeeded.
	_, err := src.GetKey("any-kid")
	if err == nil {
		t.Fatal("expected ErrKeyNotFound for empty JWKS, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "content-type") {
		t.Fatalf("unexpected content-type error for valid application/json: %v", err)
	}
}

func TestHTTPJWKSSource_AcceptsJWKSetJSONWithCharset(t *testing.T) {
	body := jwksJSONBody(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/jwk-set+json; charset=utf-8")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := auth.NewHTTPJWKSSourceWithTransportForTesting(srv.URL, 5*time.Minute, testTransportTrusting(srv))
	_, err := src.GetKey("any-kid")
	if err == nil {
		t.Fatal("expected ErrKeyNotFound for empty JWKS, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "content-type") {
		t.Fatalf("unexpected content-type error for application/jwk-set+json: %v", err)
	}
}

func TestHTTPJWKSSource_RejectsNon200Status(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := auth.NewHTTPJWKSSourceWithTransportForTesting(srv.URL, 5*time.Minute, testTransportTrusting(srv))
	_, err := src.GetKey("any")
	if err == nil {
		t.Fatal("expected status-code rejection, got nil")
	}
}
