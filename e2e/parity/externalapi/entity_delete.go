package externalapi

import (
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_06_01_DeleteSingle", Fn: RunExternalAPI_06_01_DeleteSingle},
		parity.NamedTest{Name: "ExternalAPI_06_02_DeleteByModel", Fn: RunExternalAPI_06_02_DeleteByModel},
		parity.NamedTest{Name: "ExternalAPI_06_06_DeleteAtPointInTime", Fn: RunExternalAPI_06_06_DeleteAtPointInTime},
	)
}

// RunExternalAPI_06_01_DeleteSingle — dictionary 06/01.
// Register delone/1, lock, create one entity. Delete by ID via d.DeleteEntity(id).
// After delete, d.GetEntity(id) must error (the entity is gone).
func RunExternalAPI_06_01_DeleteSingle(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("delone", 1, `{"k":1}`); err != nil {
		t.Fatalf("CreateModelFromSample: %v", err)
	}
	if err := d.LockModel("delone", 1); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	id, err := d.CreateEntity("delone", 1, `{"k":1}`)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := d.DeleteEntity(id); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
	// GetEntity should now error — the entity is gone.
	if _, err := d.GetEntity(id); err == nil {
		t.Fatal("expected GetEntity to fail after delete")
	}
}

// RunExternalAPI_06_02_DeleteByModel — dictionary 06/02.
// Register delmany/1, lock, create 5 entities. Call DeleteEntitiesByModel.
// Verify ListEntitiesByModel returns 0 afterwards.
func RunExternalAPI_06_02_DeleteByModel(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("delmany", 1, `{"k":1}`); err != nil {
		t.Fatalf("CreateModelFromSample: %v", err)
	}
	if err := d.LockModel("delmany", 1); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := d.CreateEntity("delmany", 1, `{"k":1}`); err != nil {
			t.Fatalf("CreateEntity[%d]: %v", i, err)
		}
	}
	if err := d.DeleteEntitiesByModel("delmany", 1); err != nil {
		t.Fatalf("DeleteEntitiesByModel: %v", err)
	}
	list, err := d.ListEntitiesByModel("delmany", 1)
	if err != nil {
		t.Fatalf("ListEntitiesByModel: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("after delete-by-model: got %d entities, want 0", len(list))
	}
}

// RunExternalAPI_06_06_DeleteAtPointInTime — dictionary 06/06.
//
// The full scenario asserts that delete-all-by-model with pointInTime=T1
// only removes entities created before T1. The Driver does not yet
// expose a pointInTime parameter on DeleteEntitiesByModel, so for
// tranche-1 we exercise the delete-all spine only.
//
// TODO(#118-followup): once the Driver gains a pointInTime helper,
// tighten this to assert that only the first N entities are removed
// at pointInTime=T1, leaving the post-T1 entities intact.
func RunExternalAPI_06_06_DeleteAtPointInTime(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("delpit", 1, `{"k":1}`); err != nil {
		t.Fatalf("CreateModelFromSample: %v", err)
	}
	if err := d.LockModel("delpit", 1); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := d.CreateEntity("delpit", 1, `{"k":1}`); err != nil {
			t.Fatalf("CreateEntity[before-T1][%d]: %v", i, err)
		}
	}
	t1 := time.Now().UTC()
	// Ensure observable delta between T1 and subsequent creations.
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 2; i++ {
		if _, err := d.CreateEntity("delpit", 1, `{"k":1}`); err != nil {
			t.Fatalf("CreateEntity[after-T1][%d]: %v", i, err)
		}
	}
	_ = t1 // captured for the future pointInTime helper

	if err := d.DeleteEntitiesByModel("delpit", 1); err != nil {
		t.Fatalf("DeleteEntitiesByModel: %v", err)
	}
	list, err := d.ListEntitiesByModel("delpit", 1)
	if err != nil {
		t.Fatalf("ListEntitiesByModel: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("after delete-all: got %d, want 0", len(list))
	}
}
