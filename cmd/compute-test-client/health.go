package main

import (
	"net"
	"net/http"
	"sync/atomic"
)

// healthServer serves /healthz on a chosen port. The fixture uses this
// to verify the compute test client is alive and gRPC-connected before
// running scenarios.
type healthServer struct {
	listener net.Listener
	srv      *http.Server
	ready    *atomic.Bool
}

// newHealthServer constructs a health server bound to an ephemeral port.
// Returns the bound listener so the caller can read the chosen port via
// listener.Addr().
func newHealthServer() (*healthServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	hs := &healthServer{
		listener: ln,
		ready:    new(atomic.Bool),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !hs.ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"DOWN"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"UP"}`))
	})
	hs.srv = &http.Server{Handler: mux}
	return hs, nil
}

// start serves on the bound listener in a goroutine. The server stops
// when stop() is called.
func (h *healthServer) start() {
	go func() { _ = h.srv.Serve(h.listener) }()
}

func (h *healthServer) stop() {
	_ = h.srv.Close()
}

func (h *healthServer) addr() string {
	return h.listener.Addr().String()
}

func (h *healthServer) markReady() {
	h.ready.Store(true)
}
