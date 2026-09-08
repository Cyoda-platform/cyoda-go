package main

import (
	"net/http"

	"github.com/cyoda-platform/cyoda-go/app"
)

// newHTTPServer is the one place an http.Server is built for this binary, so
// the API server and the admin server carry the same receive-side timeouts.
func newHTTPServer(addr string, handler http.Handler, t app.HTTPConfig) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: t.ReadHeaderTimeout,
		ReadTimeout:       t.ReadTimeout,
		WriteTimeout:      t.WriteTimeout,
		IdleTimeout:       t.IdleTimeout,
	}
}
