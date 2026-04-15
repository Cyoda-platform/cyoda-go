package observability_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/cyoda-platform/cyoda-go/observability"
)

// setupTracer installs an in-memory tracer provider and W3C propagator.
func setupTracer(t *testing.T) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
}

func TestInjectTraceContext_RoundTrip(t *testing.T) {
	setupTracer(t)
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test.parent")
	defer span.End()
	wantTraceID := span.SpanContext().TraceID()

	headers := map[string]string{}
	observability.InjectTraceContext(ctx, headers)

	if headers["traceparent"] == "" {
		t.Fatalf("expected traceparent header, got headers=%v", headers)
	}

	ctx2 := observability.ExtractTraceContext(context.Background(), headers)
	sc := trace.SpanContextFromContext(ctx2)
	if !sc.IsValid() {
		t.Fatalf("expected valid span context after extract, got %v", sc)
	}
	if sc.TraceID() != wantTraceID {
		t.Errorf("trace id = %s, want %s", sc.TraceID(), wantTraceID)
	}
}

func TestInjectTraceContext_NilMap(t *testing.T) {
	setupTracer(t)
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test.parent")
	defer span.End()
	// Must not panic.
	observability.InjectTraceContext(ctx, nil)
}

func TestExtractTraceContext_NilMap(t *testing.T) {
	setupTracer(t)
	base := context.Background()
	got := observability.ExtractTraceContext(base, nil)
	if got != base {
		t.Errorf("expected base ctx unchanged when headers is nil")
	}
}

func TestExtractTraceContext_EmptyMap(t *testing.T) {
	setupTracer(t)
	base := context.Background()
	got := observability.ExtractTraceContext(base, map[string]string{})
	if sc := trace.SpanContextFromContext(got); sc.IsValid() {
		t.Errorf("expected no valid span context from empty headers")
	}
}

func TestExtractTraceContext_PreservesCancellation(t *testing.T) {
	setupTracer(t)
	base, cancel := context.WithCancel(context.Background())

	tracer := otel.Tracer("test")
	tmpCtx, span := tracer.Start(context.Background(), "test.parent")
	defer span.End()
	headers := map[string]string{}
	observability.InjectTraceContext(tmpCtx, headers)

	got := observability.ExtractTraceContext(base, headers)
	if got.Err() != nil {
		t.Fatalf("ctx already cancelled before cancel()")
	}
	cancel()
	if got.Err() == nil {
		t.Errorf("cancel on base ctx did not propagate to extracted ctx")
	}
}
