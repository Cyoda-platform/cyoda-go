package mock

import (
	"context"
	"net/http"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

type AuthenticationService struct {
	DefaultUser *common.UserContext
}

func NewAuthenticationService(defaultUser *common.UserContext) *AuthenticationService {
	return &AuthenticationService{DefaultUser: defaultUser}
}

func (s *AuthenticationService) Authenticate(ctx context.Context, r *http.Request) (*common.UserContext, error) {
	// Return a defensive copy so concurrent requests cannot mutate the shared default.
	uc := *s.DefaultUser
	roles := make([]string, len(s.DefaultUser.Roles))
	copy(roles, s.DefaultUser.Roles)
	uc.Roles = roles
	return &uc, nil
}
