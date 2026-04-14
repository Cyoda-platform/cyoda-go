package middleware

import (
	"net/http"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/spi"
)

func Auth(authService spi.AuthenticationService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uc, err := authService.Authenticate(r.Context(), r)
			if err != nil {
				common.WriteError(w, r, common.Operational(http.StatusUnauthorized, common.ErrCodeUnauthorized, "authentication failed"))
				return
			}
			ctx := common.WithUserContext(r.Context(), uc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
