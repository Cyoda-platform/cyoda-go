package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"

	"github.com/cyoda-platform/cyoda-go/plugins/sqlite"
)

// TestCountByState_SQLite_EmptyStatesShortCircuits verifies the SPI contract
// that a non-nil empty states slice returns an empty map without querying.
func TestCountByState_SQLite_EmptyStatesShortCircuits(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cbs_empty.db")

	factory, err := sqlite.NewStoreFactoryForTest(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.NewStoreFactoryForTest: %v", err)
	}
	t.Cleanup(func() { factory.Close() })

	ctx := spi.WithUserContext(context.Background(), &spi.UserContext{
		UserID: "test-user",
		Tenant: spi.Tenant{ID: "tenant-cbs", Name: "Tenant"},
		Roles:  []string{"ROLE_USER"},
	})

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
