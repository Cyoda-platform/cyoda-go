package middleware

import (
	"net/http"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/contract"
)

func Auth(authService contract.AuthenticationService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uc, err := authService.Authenticate(r.Context(), r)
			if err != nil {
				common.WriteError(w, r, common.Operational(http.StatusUnauthorized, common.ErrCodeUnauthorized, "authentication failed"))
				return
			}
			ctx := spi.WithUserContext(r.Context(), uc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
