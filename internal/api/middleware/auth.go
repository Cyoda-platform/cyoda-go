package middleware

import (
	"log/slog"
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
				// Log the specific failure reason for operators; the
				// client body stays generic so distinct failure modes
				// cannot be distinguished via the response (issue #100).
				slog.Warn("HTTP auth failed",
					"pkg", "api/middleware",
					"method", r.Method,
					"path", r.URL.Path,
					"reason", err.Error(),
				)
				common.WriteError(w, r, common.Operational(http.StatusUnauthorized, common.ErrCodeUnauthorized, "authentication failed"))
				return
			}
			ctx := spi.WithUserContext(r.Context(), uc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
