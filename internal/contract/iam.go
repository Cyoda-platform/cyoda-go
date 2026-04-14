package contract

import (
	"context"
	"net/http"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type AuthenticationService interface {
	Authenticate(ctx context.Context, r *http.Request) (*spi.UserContext, error)
}

type AuthorizationService interface {
	HasRole(ctx context.Context, user *spi.UserContext, role string) bool
	CheckAccess(ctx context.Context, user *spi.UserContext, resource string, operation string) error
}
