package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// DelegatingAuthenticator implements spi.AuthenticationService by delegating
// token validation to a JWKSValidator.
type DelegatingAuthenticator struct {
	validator *JWKSValidator
}

// NewDelegatingAuthenticator creates a new DelegatingAuthenticator.
func NewDelegatingAuthenticator(validator *JWKSValidator) *DelegatingAuthenticator {
	return &DelegatingAuthenticator{validator: validator}
}

// Authenticate extracts a Bearer token from the Authorization header and validates it.
func (a *DelegatingAuthenticator) Authenticate(_ context.Context, r *http.Request) (*common.UserContext, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("missing Authorization header")
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("invalid Authorization header: expected Bearer scheme")
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return nil, fmt.Errorf("empty bearer token")
	}

	uc, err := a.validator.Validate(token)
	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	return uc, nil
}
