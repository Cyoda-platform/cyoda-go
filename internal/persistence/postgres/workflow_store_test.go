package postgres_test

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/persistence/postgres"
)

func setupWorkflowTest(t *testing.T) *postgres.StoreFactory {
	t.Helper()
	pool := newTestPool(t)
	_ = postgres.MigrateDown(pool)
	if err := postgres.Migrate(pool); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	t.Cleanup(func() { _ = postgres.MigrateDown(pool) })
	return postgres.NewStoreFactory(pool)
}

func sampleWorkflows() []common.WorkflowDefinition {
	return []common.WorkflowDefinition{
		{
			Version:      "1",
			Name:         "default",
			InitialState: "NONE",
			Active:       true,
			States: map[string]common.StateDefinition{
				"NONE":    {Transitions: []common.TransitionDefinition{{Name: "create", Next: "CREATED"}}},
				"CREATED": {},
			},
		},
	}
}

func TestWorkflowStore_SaveAndGet(t *testing.T) {
	factory := setupWorkflowTest(t)
	ctx := ctxWithTenant("wf-tenant")

	store, err := factory.WorkflowStore(ctx)
	if err != nil {
		t.Fatalf("WorkflowStore: %v", err)
	}

	ref := common.ModelRef{EntityName: "Order", ModelVersion: "1"}
	wfs := sampleWorkflows()

	if err := store.Save(ctx, ref, wfs); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(got))
	}
	if got[0].Name != "default" {
		t.Errorf("expected name 'default', got %q", got[0].Name)
	}
	if got[0].InitialState != "NONE" {
		t.Errorf("expected initialState 'NONE', got %q", got[0].InitialState)
	}
	if len(got[0].States) != 2 {
		t.Errorf("expected 2 states, got %d", len(got[0].States))
	}
}

func TestWorkflowStore_SaveOverwrites(t *testing.T) {
	factory := setupWorkflowTest(t)
	ctx := ctxWithTenant("wf-tenant")
	store, _ := factory.WorkflowStore(ctx)

	ref := common.ModelRef{EntityName: "Order", ModelVersion: "1"}

	store.Save(ctx, ref, sampleWorkflows())

	updated := []common.WorkflowDefinition{{
		Version: "2", Name: "updated", InitialState: "START", Active: true,
		States: map[string]common.StateDefinition{"START": {}},
	}}
	store.Save(ctx, ref, updated)

	got, _ := store.Get(ctx, ref)
	if len(got) != 1 || got[0].Name != "updated" {
		t.Errorf("expected overwritten workflow 'updated', got %v", got)
	}
}

func TestWorkflowStore_GetNotFound(t *testing.T) {
	factory := setupWorkflowTest(t)
	ctx := ctxWithTenant("wf-tenant")
	store, _ := factory.WorkflowStore(ctx)

	ref := common.ModelRef{EntityName: "Nonexistent", ModelVersion: "1"}
	_, err := store.Get(ctx, ref)
	if err == nil {
		t.Fatal("expected error for nonexistent workflows")
	}
}

func TestWorkflowStore_Delete(t *testing.T) {
	factory := setupWorkflowTest(t)
	ctx := ctxWithTenant("wf-tenant")
	store, _ := factory.WorkflowStore(ctx)

	ref := common.ModelRef{EntityName: "Order", ModelVersion: "1"}
	store.Save(ctx, ref, sampleWorkflows())

	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Get(ctx, ref)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestWorkflowStore_DeleteNonexistent(t *testing.T) {
	factory := setupWorkflowTest(t)
	ctx := ctxWithTenant("wf-tenant")
	store, _ := factory.WorkflowStore(ctx)

	ref := common.ModelRef{EntityName: "Nonexistent", ModelVersion: "1"}
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete nonexistent should not error: %v", err)
	}
}

func TestWorkflowStore_TenantIsolation(t *testing.T) {
	factory := setupWorkflowTest(t)
	ctxA := ctxWithTenant("tenant-A")
	ctxB := ctxWithTenant("tenant-B")

	storeA, _ := factory.WorkflowStore(ctxA)
	storeB, _ := factory.WorkflowStore(ctxB)

	ref := common.ModelRef{EntityName: "Order", ModelVersion: "1"}
	storeA.Save(ctxA, ref, sampleWorkflows())

	_, err := storeB.Get(ctxB, ref)
	if err == nil {
		t.Fatal("tenant-B should not see tenant-A's workflows")
	}
}
