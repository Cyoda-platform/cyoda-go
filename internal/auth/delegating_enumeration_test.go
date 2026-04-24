package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/auth"
)

// TestDelegatingAuthenticator_ErrorsAreWrappedAsAuthFailed is the regression
// test for issue #100. All four Authenticate failure branches must wrap a
// single sentinel `ErrAuthenticationFailed` so that:
//   - err.Error() always starts with the generic string "authentication failed"
//     (so if a caller ever surfaces err.Error() to the client, it can't be used
//     to distinguish "no token sent" from "token is wrong")
//   - errors.Is(err, auth.ErrAuthenticationFailed) returns true for every branch
//     so callers can detect auth failure generically
func TestDelegatingAuthenticator_ErrorsAreWrappedAsAuthFailed(t *testing.T) {
	validator := auth.NewJWKSValidator("http://localhost:0/jwks", "cyoda", 5*time.Minute)
	authn := auth.NewDelegatingAuthenticator(validator)

	cases := []struct {
		name         string
		setHeader    func(r *http.Request)
		reasonSubstr string
	}{
		{
			name:         "missing Authorization header",
			setHeader:    func(r *http.Request) {},
			reasonSubstr: "missing Authorization header",
		},
		{
			name: "non-Bearer scheme",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
			},
			reasonSubstr: "Bearer",
		},
		{
			name: "empty bearer token",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer ")
			},
			reasonSubstr: "empty",
		},
		{
			name: "invalid token value",
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer not.a.real.token")
			},
			reasonSubstr: "token validation failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			tc.setHeader(req)

			_, err := authn.Authenticate(context.Background(), req)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !errors.Is(err, auth.ErrAuthenticationFailed) {
				t.Errorf("errors.Is(err, ErrAuthenticationFailed) = false; err = %v", err)
			}
			if !strings.HasPrefix(err.Error(), "authentication failed") {
				t.Errorf("err.Error() = %q; must start with \"authentication failed\" so it is safe to surface", err.Error())
			}
			// The specific reason must remain in the chain (for logging),
			// it just must not be the PREFIX.
			if !strings.Contains(err.Error(), tc.reasonSubstr) {
				t.Errorf("err.Error() = %q; expected to contain reason %q for operator logs", err.Error(), tc.reasonSubstr)
			}
		})
	}
}
