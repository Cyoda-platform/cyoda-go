package api

import (
	"net/http"
	"sync/atomic"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// RegisterHealthRoutes registers GET /health on the API listener.
// /health mirrors the node's readiness flag: 200 {"status":"UP"} while
// healthy, 503 {"status":"DOWN"} after any recovered panic — API, gRPC,
// admin, or a background loop doing engine work. The flag latches: nothing
// re-arms it, because the node's state after a panic is unverified. Read
// the ticket in the log, then replace the node.
//
// This is not the deployment probe. Deployment probes are /livez
// (unconditional) and /readyz (same flag) on the admin listener; /health is
// for humans and simple scripts.
func RegisterHealthRoutes(mux *http.ServeMux, healthFlag *atomic.Bool) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if !healthFlag.Load() {
			common.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "DOWN"})
			return
		}
		common.WriteJSON(w, http.StatusOK, map[string]string{"status": "UP"})
	})
}
