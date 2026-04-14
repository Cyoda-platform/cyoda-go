package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync/atomic"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

func Recovery(healthFlag *atomic.Bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					stack := string(debug.Stack())
					err := fmt.Errorf("panic: %v", rec)
					slog.Error("panic recovered", "pkg", "middleware", "err", err, "stack", stack)
					appErr := common.Fatal("internal server error", err)
					appErr.Detail = "panic recovered; check server logs for details"
					healthFlag.Store(false)
					common.WriteError(w, r, appErr)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
