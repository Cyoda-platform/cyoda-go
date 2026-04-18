package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The admin listener binds to 0.0.0.0 in the Helm target so kubelet and
// Prometheus can reach it; that makes /metrics reachable by any in-cluster
// pod by default. Bearer-token auth on /metrics closes that exposure while
// keeping /livez and /readyz unauthenticated (kubelet probes can't carry
// bearers, so auth on them would brick the readiness contract).

func TestMetricsAuth_NoTokenConfigured_Unauthenticated(t *testing.T) {
	// When no bearer is configured (desktop/docker default), /metrics is
	// served without requiring auth — preserves the backward-compat surface.
	h := NewHandler(Options{
		Readiness: func() error { return nil },
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics without configured auth should return 200; got %d", w.Code)
	}
}

func TestMetricsAuth_MissingHeader_Returns401(t *testing.T) {
	h := NewHandler(Options{
		Readiness:          func() error { return nil },
		MetricsBearerToken: "valid-token-xyz",
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("metrics without Authorization header should return 401; got %d", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Errorf("401 response must carry WWW-Authenticate: Bearer ...; got %q", got)
	}
}

func TestMetricsAuth_WrongScheme_Returns401(t *testing.T) {
	h := NewHandler(Options{
		Readiness:          func() error { return nil },
		MetricsBearerToken: "valid-token-xyz",
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("metrics with non-Bearer scheme should return 401; got %d", w.Code)
	}
}

func TestMetricsAuth_WrongToken_Returns401(t *testing.T) {
	h := NewHandler(Options{
		Readiness:          func() error { return nil },
		MetricsBearerToken: "valid-token-xyz",
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("metrics with wrong bearer should return 401; got %d", w.Code)
	}
}

func TestMetricsAuth_CorrectToken_Returns200(t *testing.T) {
	h := NewHandler(Options{
		Readiness:          func() error { return nil },
		MetricsBearerToken: "valid-token-xyz",
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer valid-token-xyz")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics with correct bearer should return 200; got %d", w.Code)
	}
}

// Probe endpoints must stay unauth regardless of bearer config — kubelet
// has no way to present a bearer, so any auth on these endpoints would
// brick the readiness contract across all Helm installs.

func TestMetricsAuth_LivezStaysUnauthenticated(t *testing.T) {
	h := NewHandler(Options{
		Readiness:          func() error { return nil },
		MetricsBearerToken: "valid-token-xyz",
	})
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("livez must stay unauth even when /metrics requires bearer; got %d", w.Code)
	}
}

func TestMetricsAuth_ReadyzStaysUnauthenticated(t *testing.T) {
	h := NewHandler(Options{
		Readiness:          func() error { return nil },
		MetricsBearerToken: "valid-token-xyz",
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("readyz must stay unauth even when /metrics requires bearer; got %d", w.Code)
	}
}

// Constant-time comparison matters for credential comparisons, but the
// property is hard to observe at the unit-test layer. We assert the
// positive/negative cases above work; the implementation is expected to
// use crypto/subtle.ConstantTimeCompare — reviewer verifies.
