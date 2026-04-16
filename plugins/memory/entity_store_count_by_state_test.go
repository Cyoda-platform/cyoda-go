package memory_test

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"

	"github.com/cyoda-platform/cyoda-go/plugins/memory"
)

// TestCountByState_EmptyStatesSliceShortCircuits verifies the SPI contract:
// an empty (non-nil) states slice returns an empty map without iterating
// storage. Specifically, this guards against accidental "no filter" semantics
// when len(states) == 0.
func TestCountByState_EmptyStatesSliceShortCircuits(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := ctxWithTenant("tenant-cbs")

	es, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}

	mref := spi.ModelRef{EntityName: "Order", ModelVersion: "1"}

	// Save an entity to prove the early-exit ignores it.
	_, err = es.Save(ctx, &spi.Entity{
		Meta: spi.EntityMeta{
			ID:       "e-001",
			TenantID: "tenant-cbs",
			ModelRef: mref,
			State:    "NEW",
		},
		Data: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := es.CountByState(ctx, mref, []string{})
	if err != nil {
		t.Fatalf("CountByState: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty map for empty states slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for empty states slice, got %v", got)
	}
}
