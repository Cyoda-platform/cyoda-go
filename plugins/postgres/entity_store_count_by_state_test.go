package postgres_test

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// TestCountByState_Postgres_EmptyStatesShortCircuits verifies the SPI contract
// that a non-nil empty states slice returns an empty map without querying the
// database.
func TestCountByState_Postgres_EmptyStatesShortCircuits(t *testing.T) {
	factory := setupEntityTest(t)
	ctx := ctxWithTenant("tenant-cbs-empty")

	es, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}

	mref := spi.ModelRef{EntityName: "Order", ModelVersion: "1"}

	// Save an entity to prove the early-exit ignores it.
	_, err = es.Save(ctx, &spi.Entity{
		Meta: spi.EntityMeta{
			ID:       "e-001",
			ModelRef: mref,
			State:    "NEW",
		},
		Data: []byte(`{"x":1}`),
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
