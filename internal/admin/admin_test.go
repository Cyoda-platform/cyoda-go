package admin

import (
	"net/http"
	"net/http/httptest"
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
		Readiness: func() error { return errNotReady },
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
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("metrics: missing Content-Type")
	}
}

var errNotReady = &readyErr{msg: "not ready"}

type readyErr struct{ msg string }

func (e *readyErr) Error() string { return e.msg }
