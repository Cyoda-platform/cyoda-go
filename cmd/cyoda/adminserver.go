package main

import (
	"net/http"
	"sync/atomic"

	"github.com/cyoda-platform/cyoda-go/internal/admin"
	"github.com/cyoda-platform/cyoda-go/internal/api/middleware"
	"github.com/cyoda-platform/cyoda-go/internal/observability"
)

// newAdminHandler assembles the admin surface (/livez, /readyz, /metrics)
// under the same panic recovery the API surface has. One policy per door: a
// panic in a probe or a metrics scrape is contained by ticket and latches the
// node unhealthy like any other. /livez still answers, so the node is
// drained, not restarted.
func newAdminHandler(readiness func() error, metricsBearer string, healthFlag *atomic.Bool) http.Handler {
	h := admin.NewHandler(admin.Options{
		Readiness:          readiness,
		MetricsBearerToken: metricsBearer,
		MetricsHandler:     observability.MetricsHandler(),
	})
	return middleware.Recovery(healthFlag)(h)
}
