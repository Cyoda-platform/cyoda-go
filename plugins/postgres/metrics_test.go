package postgres_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/cyoda-platform/cyoda-go/plugins/postgres"
)

// The callback reports the pool's current state under backend="postgres",
// and unregistering stops it.
//
// This lives in the postgres_test package (not postgres) so it can share
// newTestPool (migrate_test.go) — which carries the pgx v5.9.1
// HealthCheckPeriod-hang workaround — instead of duplicating pool
// construction. registerPoolMetrics and meterName are unexported production
// symbols reached here through the export_test.go idiom the rest of this
// plugin already uses (RegisterPoolMetricsForTest, MeterNameForTest).
func TestRegisterPoolMetrics_ReportsPoolStat(t *testing.T) {
	pool := newTestPool(t)
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	unregister, err := postgres.RegisterPoolMetricsForTest(mp.Meter(postgres.MeterNameForTest), pool)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(context.Background()) // one acquired connection
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			found[m.Name] = true
			if m.Name == "cyoda.storage.pool.connections" {
				g := m.Data.(metricdata.Gauge[int64])
				var acquired int64 = -1
				for _, dp := range g.DataPoints {
					state, _ := dp.Attributes.Value(attribute.Key("state"))
					backend, _ := dp.Attributes.Value(attribute.Key("backend"))
					if backend.AsString() != "postgres" {
						t.Fatalf("data point without backend=postgres: %v", dp.Attributes)
					}
					if state.AsString() == "acquired" {
						acquired = dp.Value
					}
				}
				if acquired < 1 {
					t.Fatalf("acquired connections = %d, want >= 1", acquired)
				}
			}
		}
	}
	for _, want := range []string{
		"cyoda.storage.pool.connections", "cyoda.storage.pool.max_connections",
		"cyoda.storage.pool.acquires", "cyoda.storage.pool.empty_acquires",
		"cyoda.storage.pool.canceled_acquires", "cyoda.storage.pool.acquire_duration",
		"cyoda.storage.pool.empty_acquire_wait",
	} {
		if !found[want] {
			t.Errorf("instrument %s not reported", want)
		}
	}

	unregister()
	rm = metricdata.ResourceMetrics{}
	_ = reader.Collect(context.Background(), &rm)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "cyoda.storage.pool.connections" && len(m.Data.(metricdata.Gauge[int64]).DataPoints) > 0 {
				t.Fatal("callback still reporting after unregister")
			}
		}
	}
}
