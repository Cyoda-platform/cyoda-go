package mock

import (
	"context"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

type AuthorizationService struct{}

func NewAuthorizationService() *AuthorizationService {
	return &AuthorizationService{}
}

func (s *AuthorizationService) HasRole(ctx context.Context, user *common.UserContext, role string) bool {
	return true
}

func (s *AuthorizationService) CheckAccess(ctx context.Context, user *common.UserContext, resource string, operation string) error {
	return nil
}
