package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/api/middleware"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

func TestRecoveryMiddlewareCatchesPanic(t *testing.T) {
	common.SetErrorResponseMode("sanitized")
	healthFlag := &atomic.Bool{}
	healthFlag.Store(true)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something terrible")
	})

	wrapped := middleware.Recovery(healthFlag)(handler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	wrapped.ServeHTTP(w, r)

	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
	if healthFlag.Load() {
		t.Error("expected health flag to be false after FATAL")
	}
	var pd map[string]any
	json.NewDecoder(w.Body).Decode(&pd)
	if pd["ticket"] == nil || pd["ticket"] == "" {
		t.Error("expected ticket UUID in panic response")
	}
}

func TestRecoveryMiddlewarePassesThrough(t *testing.T) {
	healthFlag := &atomic.Bool{}
	healthFlag.Store(true)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	wrapped := middleware.Recovery(healthFlag)(handler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	wrapped.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !healthFlag.Load() {
		t.Error("health flag should remain true")
	}
}

func TestRecoveryMiddlewareHealthFlagStaysFalse(t *testing.T) {
	healthFlag := &atomic.Bool{}
	healthFlag.Store(true)

	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// First request panics
	wrapped := middleware.Recovery(healthFlag)(panicHandler)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	// Health flag is now false
	if healthFlag.Load() {
		t.Fatal("expected unhealthy after panic")
	}

	// Second request is OK but flag stays false
	wrapped2 := middleware.Recovery(healthFlag)(okHandler)
	w2 := httptest.NewRecorder()
	wrapped2.ServeHTTP(w2, httptest.NewRequest("GET", "/", nil))

	if healthFlag.Load() {
		t.Error("health flag should stay false — no auto-recovery")
	}
}

func TestRecoveryMiddleware_VerboseMode_NoStackTraceInResponse(t *testing.T) {
	common.SetErrorResponseMode("verbose")
	defer common.SetErrorResponseMode("sanitized")

	healthFlag := &atomic.Bool{}
	healthFlag.Store(true)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something terrible")
	})

	wrapped := middleware.Recovery(healthFlag)(handler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	wrapped.ServeHTTP(w, r)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	body := w.Body.String()
	if strings.Contains(body, "goroutine") {
		t.Error("response body must not contain 'goroutine' (stack trace leaked)")
	}
	if strings.Contains(body, ".go:") {
		t.Error("response body must not contain file paths like '.go:' (stack trace leaked)")
	}
	if strings.Contains(body, "runtime/debug") {
		t.Error("response body must not contain 'runtime/debug' (stack trace leaked)")
	}
}
