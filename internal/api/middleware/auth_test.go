package middleware_test

import (
	"bytes"
	"context"
	"errors"
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

// TestAuthMiddleware_ResponseBodyIsGenericForEveryFailureMode pins issues
// #100 / #68 item 12: regardless of which Authenticate failure mode the
// upstream auth service signalled, the HTTP response body must be the same
// generic "authentication failed" RFC 9457 problem-detail. No per-branch
// detail may leak into the body — that's the user-enumeration risk.
func TestAuthMiddleware_ResponseBodyIsGenericForEveryFailureMode(t *testing.T) {
	// The DelegatingAuthenticator now returns the bare sentinel for every
	// failure mode; that is the only error shape the middleware ever sees in
	// production. We still assert that even an exotic error wrapping the
	// sentinel produces the generic body — defence in depth against future
	// implementations of contract.AuthenticationService.
	cases := []error{
		auth.ErrAuthenticationFailed,
		errors.New("some-future-implementation-leak: token=abc.def.ghi"),
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
			// Body must never carry the auth implementation's raw error
			// string — that would re-open the enumeration channel.
			if authErr != auth.ErrAuthenticationFailed {
				if strings.Contains(body, "some-future-implementation-leak") || strings.Contains(body, "abc.def.ghi") {
					t.Errorf("response body leaked auth-impl detail: %q", body)
				}
			}
		})
	}
}

// TestAuthMiddleware_DoesNotDuplicateAuthLayerLog pins the
// "one event = one log line" rule for auth failures. The auth-layer
// implementation (DelegatingAuthenticator) is the source of truth for the
// per-failure structured WARN record; the middleware must not emit a
// duplicate WARN keyed on the now-uniform err.Error() string.
func TestAuthMiddleware_DoesNotDuplicateAuthLayerLog(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	mw := middleware.Auth(&stubAuthService{err: auth.ErrAuthenticationFailed})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	handler.ServeHTTP(rec, req)

	logs := logBuf.String()
	if strings.Contains(logs, "HTTP auth failed") {
		t.Errorf("middleware emitted a duplicate auth-failure log; the auth layer is the single source of truth: %s", logs)
	}
}
