package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"

	"github.com/cyoda-platform/cyoda-go/internal/api/middleware"
	"github.com/cyoda-platform/cyoda-go/internal/auth"
)

// stubAuthService implements contract.AuthenticationService for testing the
// Auth middleware's behaviour on failure. It returns a preconfigured error.
type stubAuthService struct {
	err error
}

func (s *stubAuthService) Authenticate(_ context.Context, _ *http.Request) (*spi.UserContext, error) {
	return nil, s.err
}

// TestAuthMiddleware_ResponseBodyIsGenericForEveryFailureMode is the
// regression test for issue #100. Clients must not be able to distinguish
// between distinct auth failure modes via the HTTP response body — all
// failures yield the same generic "authentication failed" text.
func TestAuthMiddleware_ResponseBodyIsGenericForEveryFailureMode(t *testing.T) {
	cases := []error{
		fmt.Errorf("%w: missing Authorization header", auth.ErrAuthenticationFailed),
		fmt.Errorf("%w: invalid Authorization header: expected Bearer scheme", auth.ErrAuthenticationFailed),
		fmt.Errorf("%w: empty bearer token", auth.ErrAuthenticationFailed),
		fmt.Errorf("%w: token validation failed: signature verification failed", auth.ErrAuthenticationFailed),
	}

	for _, authErr := range cases {
		authErr := authErr
		t.Run(authErr.Error(), func(t *testing.T) {
			mw := middleware.Auth(&stubAuthService{err: authErr})
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("inner handler reached despite auth failure")
			}))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "authentication failed") {
				t.Errorf("body = %q; expected generic \"authentication failed\"", body)
			}
			// The specific reason (per branch) must NOT appear in the body —
			// that's the enumeration risk the fix closes.
			specific := strings.TrimPrefix(authErr.Error(), "authentication failed: ")
			if specific != "" && strings.Contains(body, specific) {
				t.Errorf("response body leaked reason %q: %q", specific, body)
			}
		})
	}
}

// TestAuthMiddleware_LogsSpecificFailureReason pins that the HTTP auth
// middleware emits an operator-visible log with the specific failure reason,
// so collapsing client-facing messages does not also collapse observability.
func TestAuthMiddleware_LogsSpecificFailureReason(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	authErr := fmt.Errorf("%w: missing Authorization header", auth.ErrAuthenticationFailed)
	mw := middleware.Auth(&stubAuthService{err: authErr})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	handler.ServeHTTP(rec, req)

	logs := logBuf.String()
	if !strings.Contains(logs, "missing Authorization header") {
		t.Errorf("expected log to include specific reason \"missing Authorization header\"; got: %s", logs)
	}
	if !errors.Is(authErr, auth.ErrAuthenticationFailed) {
		t.Fatalf("test-fixture assertion: authErr must wrap ErrAuthenticationFailed")
	}
}
