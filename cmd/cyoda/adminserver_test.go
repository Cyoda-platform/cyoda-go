package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A panic on the admin surface is contained exactly like one on the API
// surface: 500 with a ticket, and the node latches unhealthy.
func TestAdminHandler_PanicIsContainedAndLatches(t *testing.T) {
	flag := &atomic.Bool{}
	flag.Store(true)
	h := newAdminHandler(func() error { panic("probe exploded") }, "", flag)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if flag.Load() {
		t.Fatal("admin panic must latch the health flag")
	}
	// /livez keeps answering (the node is drained, not restarted).
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/livez = %d after a recovered panic, want 200", w.Code)
	}
}
