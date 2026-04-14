package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ChiMux adapts a chi.Router to satisfy the generated api.ServeMux interface.
// The generated code registers routes using "METHOD /path" patterns (Go 1.22
// style). Go's standard http.ServeMux panics when overlapping wildcard
// segments appear (e.g. /model/{entityName}/… vs /model/export/…).  Chi
// handles those patterns without conflict.
type ChiMux struct {
	r chi.Router
}

// NewChiMux returns a ChiMux backed by a fresh chi router.
func NewChiMux() *ChiMux {
	return &ChiMux{r: chi.NewRouter()}
}

// HandleFunc parses the "METHOD /path" pattern and registers it on chi.
func (c *ChiMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		// No method prefix — register for all methods.
		c.r.HandleFunc(pattern, handler)
		return
	}
	c.r.MethodFunc(method, path, handler)
}

// ServeHTTP delegates to the underlying chi router.
func (c *ChiMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.r.ServeHTTP(w, r)
}
