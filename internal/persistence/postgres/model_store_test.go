package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/persistence/postgres"
)

func setupModelTest(t *testing.T) *postgres.StoreFactory {
	t.Helper()
	pool := newTestPool(t)
	_ = postgres.MigrateDown(pool)
	if err := postgres.Migrate(pool); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	t.Cleanup(func() { _ = postgres.MigrateDown(pool) })
	return postgres.NewStoreFactory(pool)
}

func makeDescriptor(name, version string) *common.ModelDescriptor {
	return &common.ModelDescriptor{
		Ref:         common.ModelRef{EntityName: name, ModelVersion: version},
		State:       common.ModelUnlocked,
		ChangeLevel: common.ChangeLevelArrayElements,
		UpdateDate:  time.Now().UTC().Truncate(time.Millisecond),
		Schema:      []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`),
	}
}

func TestModelStore_SaveAndGet(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")

	store, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}

	desc := makeDescriptor("Widget", "1")
	if err := store.Save(ctx, desc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, desc.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Ref != desc.Ref {
		t.Errorf("Ref: got %v, want %v", got.Ref, desc.Ref)
	}
	if got.State != desc.State {
		t.Errorf("State: got %v, want %v", got.State, desc.State)
	}
	if got.ChangeLevel != desc.ChangeLevel {
		t.Errorf("ChangeLevel: got %v, want %v", got.ChangeLevel, desc.ChangeLevel)
	}
	if string(got.Schema) != string(desc.Schema) {
		t.Errorf("Schema: got %q, want %q", got.Schema, desc.Schema)
	}
}

func TestModelStore_SaveOverwrite(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	desc := makeDescriptor("Widget", "1")
	store.Save(ctx, desc)

	desc2 := *desc
	desc2.State = common.ModelLocked
	desc2.ChangeLevel = common.ChangeLevelStructural
	if err := store.Save(ctx, &desc2); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}

	got, err := store.Get(ctx, desc.Ref)
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if got.State != common.ModelLocked {
		t.Errorf("State after overwrite: got %v, want %v", got.State, common.ModelLocked)
	}
	if got.ChangeLevel != common.ChangeLevelStructural {
		t.Errorf("ChangeLevel after overwrite: got %v, want %v", got.ChangeLevel, common.ChangeLevelStructural)
	}
}

func TestModelStore_GetNotFound(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	_, err := store.Get(ctx, common.ModelRef{EntityName: "NoSuch", ModelVersion: "1"})
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
	if !errors.Is(err, common.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestModelStore_GetAll(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	store.Save(ctx, makeDescriptor("Alpha", "1"))
	store.Save(ctx, makeDescriptor("Beta", "2"))

	refs, err := store.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}

	refSet := make(map[string]bool)
	for _, r := range refs {
		refSet[r.EntityName+"."+r.ModelVersion] = true
	}
	if !refSet["Alpha.1"] {
		t.Error("missing Alpha.1")
	}
	if !refSet["Beta.2"] {
		t.Error("missing Beta.2")
	}
}

func TestModelStore_GetAllEmpty(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	refs, err := store.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll empty: %v", err)
	}
	if refs == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}

func TestModelStore_Delete(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	desc := makeDescriptor("Widget", "1")
	store.Save(ctx, desc)

	if err := store.Delete(ctx, desc.Ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Get(ctx, desc.Ref)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestModelStore_DeleteNonexistent(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	if err := store.Delete(ctx, common.ModelRef{EntityName: "NoSuch", ModelVersion: "1"}); err != nil {
		t.Fatalf("Delete nonexistent should not error, got: %v", err)
	}
}

func TestModelStore_LockUnlock(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	desc := makeDescriptor("Widget", "1")
	store.Save(ctx, desc)

	// Initially unlocked
	locked, err := store.IsLocked(ctx, desc.Ref)
	if err != nil {
		t.Fatalf("IsLocked: %v", err)
	}
	if locked {
		t.Error("expected unlocked after save")
	}

	// Lock it
	if err := store.Lock(ctx, desc.Ref); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	locked, err = store.IsLocked(ctx, desc.Ref)
	if err != nil {
		t.Fatalf("IsLocked after lock: %v", err)
	}
	if !locked {
		t.Error("expected locked after Lock()")
	}

	// Unlock it
	if err := store.Unlock(ctx, desc.Ref); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	locked, err = store.IsLocked(ctx, desc.Ref)
	if err != nil {
		t.Fatalf("IsLocked after unlock: %v", err)
	}
	if locked {
		t.Error("expected unlocked after Unlock()")
	}
}

func TestModelStore_LockNotFound(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	err := store.Lock(ctx, common.ModelRef{EntityName: "NoSuch", ModelVersion: "1"})
	if err == nil {
		t.Fatal("expected error locking nonexistent model")
	}
	if !errors.Is(err, common.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestModelStore_UnlockNotFound(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	err := store.Unlock(ctx, common.ModelRef{EntityName: "NoSuch", ModelVersion: "1"})
	if err == nil {
		t.Fatal("expected error unlocking nonexistent model")
	}
	if !errors.Is(err, common.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestModelStore_IsLockedNotFound(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	_, err := store.IsLocked(ctx, common.ModelRef{EntityName: "NoSuch", ModelVersion: "1"})
	if err == nil {
		t.Fatal("expected error for IsLocked on nonexistent model")
	}
	if !errors.Is(err, common.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestModelStore_SetChangeLevel(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	desc := makeDescriptor("Widget", "1")
	store.Save(ctx, desc)

	if err := store.SetChangeLevel(ctx, desc.Ref, common.ChangeLevelStructural); err != nil {
		t.Fatalf("SetChangeLevel: %v", err)
	}

	got, err := store.Get(ctx, desc.Ref)
	if err != nil {
		t.Fatalf("Get after SetChangeLevel: %v", err)
	}
	if got.ChangeLevel != common.ChangeLevelStructural {
		t.Errorf("ChangeLevel: got %v, want %v", got.ChangeLevel, common.ChangeLevelStructural)
	}
}

func TestModelStore_SetChangeLevelNotFound(t *testing.T) {
	factory := setupModelTest(t)
	ctx := ctxWithTenant("model-tenant")
	store, _ := factory.ModelStore(ctx)

	err := store.SetChangeLevel(ctx, common.ModelRef{EntityName: "NoSuch", ModelVersion: "1"}, common.ChangeLevelStructural)
	if err == nil {
		t.Fatal("expected error for SetChangeLevel on nonexistent model")
	}
	if !errors.Is(err, common.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestModelStore_TenantIsolation(t *testing.T) {
	factory := setupModelTest(t)
	ctxA := ctxWithTenant("tenant-A")
	ctxB := ctxWithTenant("tenant-B")

	storeA, _ := factory.ModelStore(ctxA)
	storeB, _ := factory.ModelStore(ctxB)

	desc := makeDescriptor("SharedName", "1")
	storeA.Save(ctxA, desc)

	// Tenant B cannot see tenant A's model
	_, err := storeB.Get(ctxB, desc.Ref)
	if err == nil {
		t.Fatal("tenant-B should not see tenant-A's model")
	}

	// Tenant B GetAll should be empty
	refs, err := storeB.GetAll(ctxB)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("tenant-B should see 0 refs, got %d", len(refs))
	}

	// Tenant A still sees it
	refs, err = storeA.GetAll(ctxA)
	if err != nil {
		t.Fatalf("GetAll tenant-A: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("tenant-A should see 1 ref, got %d", len(refs))
	}
}
