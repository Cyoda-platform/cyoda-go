package observability

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/cyoda-platform/cyoda-go"

var (
	tp       *sdktrace.TracerProvider
	mp       *sdkmetric.MeterProvider
	initOnce sync.Once
	initErr  error
	shutdownFn func(context.Context) error
)

// Init initializes the OpenTelemetry SDK with OTLP exporters.
// Returns a shutdown function that must be called on application exit.
// Uses standard OTel env vars: OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SERVICE_NAME.
//
// Init is guarded by sync.Once — subsequent calls return the existing shutdown
// function without re-initializing. To re-initialize, call the shutdown function
// first and then ResetInit (test-only).
func Init(ctx context.Context, serviceName, nodeID string) (func(context.Context) error, error) {
	initOnce.Do(func() {
		res, err := resource.New(ctx,
			resource.WithAttributes(
				semconv.ServiceName(serviceName),
				semconv.ServiceInstanceID(nodeID),
			),
		)
		if err != nil {
			initErr = fmt.Errorf("create OTel resource: %w", err)
			return
		}

		// Trace provider
		traceExporter, err := otlptracehttp.New(ctx)
		if err != nil {
			initErr = fmt.Errorf("create trace exporter: %w", err)
			return
		}

		// Seed the dynamic sampler from OTel env vars before constructing
		// the TracerProvider. Sampler.SetSampler should never fail here
		// because SamplerConfigFromEnv only returns valid configs, but
		// fall back to the safe default if it ever does.
		initialCfg := SamplerConfigFromEnv()
		if err := Sampler.SetSampler(initialCfg); err != nil {
			slog.Error("failed to set initial trace sampler, using default",
				"pkg", "observability", "error", err)
			_ = Sampler.SetSampler(SamplerConfig{Sampler: "always", ParentBased: true})
		}

		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(Sampler),
		)
		otel.SetTracerProvider(tp)

		// Metric provider
		metricExporter, err := otlpmetrichttp.New(ctx)
		if err != nil {
			initErr = fmt.Errorf("create metric exporter: %w", err)
			return
		}
		mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(mp)

		// Context propagation (W3C trace context + baggage)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))

		shutdownFn = func(ctx context.Context) error {
			if err := tp.Shutdown(ctx); err != nil {
				// Log but don't propagate — a missing backend (e.g. in tests) is not fatal.
				slog.Warn("OTel trace provider shutdown error", "pkg", "observability", "err", err)
			}
			if err := mp.Shutdown(ctx); err != nil {
				// Log but don't propagate — a missing backend (e.g. in tests) is not fatal.
				slog.Warn("OTel meter provider shutdown error", "pkg", "observability", "err", err)
			}
			return nil
		}
	})
	if initErr != nil {
		return nil, initErr
	}
	return shutdownFn, nil
}

// ResetInit resets the init guard so Init can be called again.
// This is intended for tests only.
func ResetInit() {
	initOnce = sync.Once{}
	initErr = nil
	shutdownFn = nil
	tp = nil
	mp = nil
}

// Tracer returns the application tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// Meter returns the application meter.
func Meter() metric.Meter {
	return otel.Meter(instrumentationName)
}
