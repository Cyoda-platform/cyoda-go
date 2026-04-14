package spi

import (
	"context"
	"net/http"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

type AuthenticationService interface {
	Authenticate(ctx context.Context, r *http.Request) (*common.UserContext, error)
}

type AuthorizationService interface {
	HasRole(ctx context.Context, user *common.UserContext, role string) bool
	CheckAccess(ctx context.Context, user *common.UserContext, resource string, operation string) error
}
