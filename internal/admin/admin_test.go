package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_Livez_Returns200(t *testing.T) {
	h := NewHandler(Options{
		Readiness: func() error { return nil },
	})
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("livez: got %d, want 200", w.Code)
	}
}

func TestHandler_Readyz_Unready(t *testing.T) {
	h := NewHandler(Options{
		Readiness: func() error { return errors.New("not ready") },
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz: got %d, want 503", w.Code)
	}
}

func TestHandler_Readyz_Ready(t *testing.T) {
	h := NewHandler(Options{Readiness: func() error { return nil }})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("readyz: got %d, want 200", w.Code)
	}
}

func TestHandler_Metrics_ReturnsPrometheusFormat(t *testing.T) {
	h := NewHandler(Options{Readiness: func() error { return nil }})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics: got %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") && !strings.HasPrefix(ct, "application/openmetrics-text") {
		t.Fatalf("metrics: Content-Type %q is not a Prometheus exposition format", ct)
	}
}
