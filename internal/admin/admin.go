// Package admin provides the unauthenticated admin HTTP listener for
// /livez, /readyz, and /metrics. Must never bind to a public interface —
// callers are responsible for choosing CYODA_ADMIN_BIND_ADDRESS.
package admin

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Options struct {
	// Readiness returns nil when the instance is ready to serve, or a
	// non-nil error describing why it isn't. Called synchronously on
	// every /readyz probe — keep it cheap.
	Readiness func() error
}

func NewHandler(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if err := opts.Readiness(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}
