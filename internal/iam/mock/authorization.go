package mock

import (
	"context"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type AuthorizationService struct{}

func NewAuthorizationService() *AuthorizationService {
	return &AuthorizationService{}
}

func (s *AuthorizationService) HasRole(ctx context.Context, user *spi.UserContext, role string) bool {
	return true
}

func (s *AuthorizationService) CheckAccess(ctx context.Context, user *spi.UserContext, resource string, operation string) error {
	return nil
}
