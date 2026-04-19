package auth

import (
	"net/http"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// requireAdmin gates administrative endpoints: the key pair handler, the
// trusted-key handler, and the M2M client handler. These routes are wrapped
// by the auth middleware, so a missing UserContext here means the middleware
// was bypassed or misconfigured — respond 401. A present UserContext lacking
// ROLE_ADMIN is a genuine authorization failure — respond 403.
//
// Returns true when the caller may proceed; otherwise writes the response
// and returns false.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	uc := spi.GetUserContext(r.Context())
	if uc == nil {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return false
	}
	if !spi.HasRole(uc.Roles, "ROLE_ADMIN") {
		http.Error(w, `{"error":"forbidden: requires ROLE_ADMIN"}`, http.StatusForbidden)
		return false
	}
	return true
}
