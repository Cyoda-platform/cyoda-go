package admin

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// requireBearer returns an http.Handler that gates next behind a static
// Bearer token. It serves 401 Unauthorized with WWW-Authenticate: Bearer
// on missing header, non-Bearer scheme, or wrong token.
//
// Comparison uses crypto/subtle.ConstantTimeCompare, which is
// constant-time between equal-length inputs. The chart-managed token
// has a fixed length (48-char alphanumeric from randAlphaNum 48), so
// length-distinction timing is not exploitable in practice.
//
// The admin listener binds to 0.0.0.0 in the Helm target so kubelet and
// Prometheus can reach it; that makes /metrics reachable by any in-cluster
// pod by default. This middleware closes that exposure for /metrics while
// /livez and /readyz stay unauth (kubelet probes carry no bearer).
//
// Callers must only wrap /metrics when expected != "" — the outer
// switch in admin.NewHandler enforces that. For defense in depth,
// the middleware itself rejects an empty-expected invocation: an empty
// bearer token would otherwise match "Authorization: Bearer " (empty
// token after prefix strip) and unconditionally admit the request.
func requireBearer(expected string, next http.Handler) http.Handler {
	expectedBytes := []byte(expected)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(expectedBytes) == 0 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cyoda metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
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
