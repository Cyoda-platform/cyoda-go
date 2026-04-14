package memory_test

import (
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/persistence/memory"
)

func TestModelStoreSaveAndGetDescriptor(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := ctxWithTenant("tenant-A")
	store, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now := time.Now()
	desc := &spi.ModelDescriptor{
		Ref:         spi.ModelRef{EntityName: "Order", ModelVersion: "1"},
		State:       spi.ModelUnlocked,
		ChangeLevel: spi.ChangeLevelStructural,
		UpdateDate:  now,
		Schema:      []byte(`{"type":"object"}`),
	}

	if err := store.Save(ctx, desc); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	got, err := store.Get(ctx, desc.Ref)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	if got.Ref != desc.Ref {
		t.Errorf("ref mismatch: got %v, want %v", got.Ref, desc.Ref)
	}
	if got.State != spi.ModelUnlocked {
		t.Errorf("state mismatch: got %v, want %v", got.State, spi.ModelUnlocked)
	}
	if got.ChangeLevel != spi.ChangeLevelStructural {
		t.Errorf("change level mismatch: got %v, want %v", got.ChangeLevel, spi.ChangeLevelStructural)
	}
	if !got.UpdateDate.Equal(now) {
		t.Errorf("update date mismatch: got %v, want %v", got.UpdateDate, now)
	}
	if string(got.Schema) != `{"type":"object"}` {
		t.Errorf("schema mismatch: got %s", got.Schema)
	}

	// Verify defensive copy: mutating returned schema doesn't affect store
	got.Schema[0] = 'X'
	got2, _ := store.Get(ctx, desc.Ref)
	if got2.Schema[0] != '{' {
		t.Error("Get must return a defensive copy of Schema")
	}
}

func TestModelStoreLockUnlock(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := ctxWithTenant("tenant-A")
	store, _ := factory.ModelStore(ctx)

	ref := spi.ModelRef{EntityName: "Order", ModelVersion: "1"}
	desc := &spi.ModelDescriptor{
		Ref:    ref,
		State:  spi.ModelUnlocked,
		Schema: []byte(`{}`),
	}
	store.Save(ctx, desc)

	if err := store.Lock(ctx, ref); err != nil {
		t.Fatalf("lock failed: %v", err)
	}
	locked, err := store.IsLocked(ctx, ref)
	if err != nil {
		t.Fatalf("isLocked failed: %v", err)
	}
	if !locked {
		t.Error("expected model to be locked")
	}

	// Verify Get reflects locked state
	got, _ := store.Get(ctx, ref)
	if got.State != spi.ModelLocked {
		t.Errorf("expected LOCKED state via Get, got %v", got.State)
	}

	if err := store.Unlock(ctx, ref); err != nil {
		t.Fatalf("unlock failed: %v", err)
	}
	locked, _ = store.IsLocked(ctx, ref)
	if locked {
		t.Error("expected model to be unlocked")
	}
}

func TestModelStoreSetChangeLevel(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := ctxWithTenant("tenant-A")
	store, _ := factory.ModelStore(ctx)

	ref := spi.ModelRef{EntityName: "Order", ModelVersion: "1"}
	desc := &spi.ModelDescriptor{
		Ref:         ref,
		State:       spi.ModelUnlocked,
		ChangeLevel: spi.ChangeLevelStructural,
		Schema:      []byte(`{}`),
	}
	store.Save(ctx, desc)

	if err := store.SetChangeLevel(ctx, ref, spi.ChangeLevelType); err != nil {
		t.Fatalf("setChangeLevel failed: %v", err)
	}

	got, _ := store.Get(ctx, ref)
	if got.ChangeLevel != spi.ChangeLevelType {
		t.Errorf("expected TYPE, got %v", got.ChangeLevel)
	}
}

func TestModelStoreTenantIsolation(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctxA := ctxWithTenant("tenant-A")
	ctxB := ctxWithTenant("tenant-B")
	storeA, _ := factory.ModelStore(ctxA)
	storeB, _ := factory.ModelStore(ctxB)

	ref := spi.ModelRef{EntityName: "Order", ModelVersion: "1"}
	desc := &spi.ModelDescriptor{
		Ref:    ref,
		State:  spi.ModelUnlocked,
		Schema: []byte(`{"tenant":"A"}`),
	}
	storeA.Save(ctxA, desc)

	_, err := storeB.Get(ctxB, ref)
	if err == nil {
		t.Error("tenant-B should not see tenant-A's model")
	}
}

func TestModelStoreDeleteDescriptor(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := ctxWithTenant("tenant-A")
	store, _ := factory.ModelStore(ctx)

	ref := spi.ModelRef{EntityName: "Order", ModelVersion: "1"}
	desc := &spi.ModelDescriptor{
		Ref:    ref,
		State:  spi.ModelUnlocked,
		Schema: []byte(`{}`),
	}
	store.Save(ctx, desc)

	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	_, err := store.Get(ctx, ref)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestModelStoreGetAllDescriptors(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := ctxWithTenant("tenant-A")
	store, _ := factory.ModelStore(ctx)

	ref1 := spi.ModelRef{EntityName: "Order", ModelVersion: "1"}
	ref2 := spi.ModelRef{EntityName: "Product", ModelVersion: "1"}

	store.Save(ctx, &spi.ModelDescriptor{Ref: ref1, Schema: []byte(`{}`)})
	store.Save(ctx, &spi.ModelDescriptor{Ref: ref2, Schema: []byte(`{}`)})

	refs, err := store.GetAll(ctx)
	if err != nil {
		t.Fatalf("getAll failed: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}

	found := map[string]bool{}
	for _, r := range refs {
		found[r.EntityName] = true
	}
	if !found["Order"] || !found["Product"] {
		t.Errorf("expected Order and Product in refs, got %v", refs)
	}
}
