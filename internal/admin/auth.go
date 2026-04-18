package admin

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// requireBearer returns an http.Handler that gates next behind a static
// Bearer token. It serves 401 Unauthorized with WWW-Authenticate: Bearer
// when the header is absent, uses a non-Bearer scheme, or presents a
// wrong token. Comparison is constant-time to avoid leaking token length
// or prefix via timing.
//
// The admin listener binds to 0.0.0.0 in the Helm target so kubelet and
// Prometheus can reach it; that makes /metrics reachable by any in-cluster
// pod by default. This middleware closes that exposure for /metrics while
// /livez and /readyz stay unauth (kubelet probes carry no bearer).
//
// An empty expected token disables auth — caller should not wrap /metrics
// in that case; handler returns 401 defensively if reached with no token.
func requireBearer(expected string, next http.Handler) http.Handler {
	expectedBytes := []byte(expected)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const scheme = "Bearer "
		if !strings.HasPrefix(h, scheme) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cyoda metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		got := []byte(strings.TrimPrefix(h, scheme))
		if subtle.ConstantTimeCompare(got, expectedBytes) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cyoda metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
