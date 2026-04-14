package parity

import (
	"net/http"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// RunTenantIsolationEntities verifies that entities created by tenant A
// are invisible, unmodifiable, and audit-invisible to tenant B.
//
// Ports three internal/e2e tests:
//   - TestTenantIsolation_EntitiesInvisible   (GET cross-tenant -> 404)
//   - TestTenantIsolation_DeleteReturns404    (DELETE cross-tenant -> 404, no leakage)
//   - TestTenantIsolation_AuditInvisible      (GET audit cross-tenant -> 404 or empty)
//
// After all cross-tenant operations by B, verifies tenant A can still
// retrieve the entity (200) — the entity was not damaged by B's attempts.
func RunTenantIsolationEntities(t *testing.T, fixture BackendFixture) {
	tenantA := fixture.NewTenant(t)
	tenantB := fixture.NewTenant(t)
	clientA := client.NewClient(fixture.BaseURL(), tenantA.Token)
	clientB := client.NewClient(fixture.BaseURL(), tenantB.Token)

	const modelName = "iso-entity-test"
	const modelVersion = 1

	// Tenant A: set up model + workflow + entity.
	setupSimpleWorkflow(t, clientA, modelName, modelVersion)

	entityID, err := clientA.CreateEntity(t, modelName, modelVersion,
		`{"name":"TenantA","amount":10,"status":"new"}`)
	if err != nil {
		t.Fatalf("CreateEntity (tenant A): %v", err)
	}

	// Tenant B: GET entity by ID -> expect 404 (entity invisible).
	status, _ := clientB.GetEntityRaw(t, entityID)
	if status != http.StatusNotFound {
		t.Errorf("tenant B GET entity: expected 404, got %d", status)
	}

	// Tenant B: DELETE entity -> expect 404 (no existence leakage).
	status, _ = clientB.DeleteEntityRaw(t, entityID)
	if status != http.StatusNotFound {
		t.Errorf("tenant B DELETE entity: expected 404, got %d", status)
	}

	// Tenant B: GET audit events for entity -> expect 404 or 200 with empty items.
	status, err = clientB.GetAuditEventsRaw(t, entityID)
	if status == http.StatusOK {
		// If 200, verify items are empty by doing a full decode.
		auditResp, auditErr := clientB.GetAuditEvents(t, entityID)
		if auditErr != nil {
			t.Fatalf("tenant B GetAuditEvents decode: %v", auditErr)
		}
		if len(auditResp.Items) > 0 {
			t.Error("tenant B should not see tenant A's audit events")
		}
	} else if status != http.StatusNotFound {
		t.Errorf("tenant B GET audit: expected 404 or 200-with-empty-items, got %d (err: %v)", status, err)
	}
	// 404 is acceptable — no existence leakage.

	// Verify tenant A can still GET the entity (200) — not damaged by B.
	got, err := clientA.GetEntity(t, entityID)
	if err != nil {
		t.Fatalf("tenant A GET entity after B's attempts: %v", err)
	}
	if got.Data["name"] != "TenantA" {
		t.Errorf("tenant A entity data.name: got %v, want \"TenantA\"", got.Data["name"])
	}
}

// RunTenantIsolationModels verifies that models created by tenant A are
// invisible to tenant B, and that tenant B can independently create and
// lock a model with the same name.
//
// Ports two internal/e2e tests:
//   - TestTenantIsolation_ModelsInvisible          (export cross-tenant -> 404)
//   - TestTenantIsolation_SameModelNameIndependent  (same name, independent lifecycle)
func RunTenantIsolationModels(t *testing.T, fixture BackendFixture) {
	tenantA := fixture.NewTenant(t)
	tenantB := fixture.NewTenant(t)
	clientA := client.NewClient(fixture.BaseURL(), tenantA.Token)
	clientB := client.NewClient(fixture.BaseURL(), tenantB.Token)

	const modelName = "iso-model-test"
	const modelVersion = 1

	// Tenant A: import and lock model.
	if err := clientA.ImportModel(t, modelName, modelVersion, `{"name":"TenantA","amount":100,"status":"new"}`); err != nil {
		t.Fatalf("ImportModel (tenant A): %v", err)
	}
	if err := clientA.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel (tenant A): %v", err)
	}

	// Tenant B: export tenant A's model -> expect 404 (model invisible).
	_, err := clientB.ExportModel(t, "SIMPLE_VIEW", modelName, modelVersion)
	if err == nil {
		t.Error("tenant B ExportModel: expected error (404), got success — model should be invisible")
	}

	// Tenant B: import a model with the SAME name -> expect 200 (independent lifecycle).
	if err := clientB.ImportModel(t, modelName, modelVersion, `{"name":"TenantB","amount":99,"status":"new"}`); err != nil {
		t.Fatalf("ImportModel (tenant B, same name): %v", err)
	}

	// Tenant B: lock same-name model -> expect 200 (independent).
	if err := clientB.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel (tenant B): %v", err)
	}

	// Verify tenant A can still export their own model (200).
	_, err = clientA.ExportModel(t, "SIMPLE_VIEW", modelName, modelVersion)
	if err != nil {
		t.Errorf("tenant A ExportModel after B's operations: %v", err)
	}
}
