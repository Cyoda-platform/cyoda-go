package mock

import (
	"context"
	"net/http"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type AuthenticationService struct {
	DefaultUser *spi.UserContext
}

func NewAuthenticationService(defaultUser *spi.UserContext) *AuthenticationService {
	return &AuthenticationService{DefaultUser: defaultUser}
}

func (s *AuthenticationService) Authenticate(ctx context.Context, r *http.Request) (*spi.UserContext, error) {
	// Return a defensive copy so concurrent requests cannot mutate the shared default.
	uc := *s.DefaultUser
	roles := make([]string, len(s.DefaultUser.Roles))
	copy(roles, s.DefaultUser.Roles)
	uc.Roles = roles
	return &uc, nil
}
