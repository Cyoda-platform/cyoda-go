package api

import (
	"net/http"
	"sync/atomic"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

func RegisterHealthRoutes(mux *http.ServeMux, healthFlag *atomic.Bool) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if !healthFlag.Load() {
			common.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "DOWN"})
			return
		}
		common.WriteJSON(w, http.StatusOK, map[string]string{"status": "UP"})
	})
}
