package observability_test

import (
	"context"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/observability"
)

func TestInit_ReturnsShutdownFunc(t *testing.T) {
	observability.ResetInit()
	shutdown, err := observability.Init(context.Background(), "test-service", "node-test")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
	// Shutdown should not error
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// IM-22: Double-init must not leak providers. Second call returns early.
func TestInit_DoubleInitReturnsEarly(t *testing.T) {
	observability.ResetInit()
	ctx := context.Background()
	shutdown1, err := observability.Init(ctx, "test-service-1", "node-1")
	if err != nil {
		t.Fatalf("first Init: %v", err)
	}
	defer shutdown1(ctx)

	shutdown2, err := observability.Init(ctx, "test-service-2", "node-2")
	if err != nil {
		t.Fatalf("second Init: %v", err)
	}
	// Second init should return a valid (non-nil) shutdown function
	if shutdown2 == nil {
		t.Fatal("expected non-nil shutdown from second Init call")
	}
	// Calling shutdown2 should not panic or error
	if err := shutdown2(ctx); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

func TestInit_TracerAndMeterAvailable(t *testing.T) {
	observability.ResetInit()
	shutdown, err := observability.Init(context.Background(), "test-service", "node-test")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer shutdown(context.Background()) //nolint:errcheck

	tracer := observability.Tracer()
	if tracer == nil {
		t.Fatal("expected non-nil tracer")
	}

	meter := observability.Meter()
	if meter == nil {
		t.Fatal("expected non-nil meter")
	}
}
