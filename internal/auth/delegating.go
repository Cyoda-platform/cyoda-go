package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// ErrAuthenticationFailed is the generic client-facing auth failure sentinel.
// All Authenticate branches wrap this via `%w` so callers can detect auth
// failure with errors.Is and so that err.Error() always begins with the same
// string — preventing enumeration-style probing that could otherwise tell
// "no token sent" apart from "token is wrong" (issue #100).
var ErrAuthenticationFailed = errors.New("authentication failed")

// DelegatingAuthenticator implements contract.AuthenticationService by delegating
// token validation to a JWKSValidator.
type DelegatingAuthenticator struct {
	validator *JWKSValidator
}

// NewDelegatingAuthenticator creates a new DelegatingAuthenticator.
func NewDelegatingAuthenticator(validator *JWKSValidator) *DelegatingAuthenticator {
	return &DelegatingAuthenticator{validator: validator}
}

// Authenticate extracts a Bearer token from the Authorization header and validates it.
func (a *DelegatingAuthenticator) Authenticate(_ context.Context, r *http.Request) (*spi.UserContext, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("%w: missing Authorization header", ErrAuthenticationFailed)
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("%w: invalid Authorization header: expected Bearer scheme", ErrAuthenticationFailed)
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return nil, fmt.Errorf("%w: empty bearer token", ErrAuthenticationFailed)
	}

	uc, err := a.validator.Validate(token)
	if err != nil {
		return nil, fmt.Errorf("%w: token validation failed: %w", ErrAuthenticationFailed, err)
	}

	return uc, nil
}
