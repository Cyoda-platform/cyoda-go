package externalapi

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_07_01_GetEntityAtPointInTime", Fn: RunExternalAPI_07_01_GetEntityAtPointInTime},
		parity.NamedTest{Name: "ExternalAPI_07_02_GetEntityByTransactionID", Fn: RunExternalAPI_07_02_GetEntityByTransactionID},
		parity.NamedTest{Name: "ExternalAPI_07_03_ChangeHistoryFull", Fn: RunExternalAPI_07_03_ChangeHistoryFull},
		parity.NamedTest{Name: "ExternalAPI_07_04_ChangeHistoryAtPointInTime", Fn: RunExternalAPI_07_04_ChangeHistoryAtPointInTime},
		parity.NamedTest{Name: "ExternalAPI_07_05_ChangeHistoryNonExistent", Fn: RunExternalAPI_07_05_ChangeHistoryNonExistent},
	)
}

// RunExternalAPI_07_01_GetEntityAtPointInTime — dictionary 07/01.
// GET entity at three different points in time returns three states.
func RunExternalAPI_07_01_GetEntityAtPointInTime(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("pit1", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.LockModel("pit1", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("pit1", 1, `{"k":1}`)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	t1 := time.Now().UTC()
	time.Sleep(100 * time.Millisecond)

	if err := d.UpdateEntityData(id, `{"k":2}`); err != nil {
		t.Fatalf("update@t2: %v", err)
	}
	t2 := time.Now().UTC()
	time.Sleep(100 * time.Millisecond)

	if err := d.UpdateEntityData(id, `{"k":3}`); err != nil {
		t.Fatalf("update@t3: %v", err)
	}

	gotT1, err := d.GetEntityAt(id, t1)
	if err != nil {
		t.Fatalf("GetEntityAt(t1): %v", err)
	}
	if gotT1.Data["k"] != float64(1) {
		t.Errorf("at t1: got k=%v, want 1", gotT1.Data["k"])
	}
	gotT2, err := d.GetEntityAt(id, t2)
	if err != nil {
		t.Fatalf("GetEntityAt(t2): %v", err)
	}
	if gotT2.Data["k"] != float64(2) {
		t.Errorf("at t2: got k=%v, want 2", gotT2.Data["k"])
	}
	gotNow, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("GetEntity(now): %v", err)
	}
	if gotNow.Data["k"] != float64(3) {
		t.Errorf("at now: got k=%v, want 3", gotNow.Data["k"])
	}
}

// RunExternalAPI_07_02_GetEntityByTransactionID — dictionary 07/02.
// GET /entity/{id}?transactionId=<tx>. Skipped pending parity-client
// surface addition; tracked alongside tranche-2 follow-up.
func RunExternalAPI_07_02_GetEntityByTransactionID(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: parity client does not yet expose transactionId-scoped GET; tracked alongside tranche-2 follow-up issue")
}

// RunExternalAPI_07_03_ChangeHistoryFull — dictionary 07/03.
// Full change history lists CREATE + N UPDATEs.
func RunExternalAPI_07_03_ChangeHistoryFull(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("pit3", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.LockModel("pit3", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("pit3", 1, `{"k":1}`)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	if err := d.UpdateEntityData(id, `{"k":2}`); err != nil {
		t.Fatalf("update1: %v", err)
	}
	if err := d.UpdateEntityData(id, `{"k":3}`); err != nil {
		t.Fatalf("update2: %v", err)
	}
	changes, err := d.GetEntityChanges(id)
	if err != nil {
		t.Fatalf("GetEntityChanges: %v", err)
	}
	if len(changes) < 3 {
		t.Errorf("changes: got %d, want >= 3 (1 CREATE + 2 UPDATE)", len(changes))
	}
	// The API returns changes newest-first; the oldest entry (CREATE) is last.
	last := changes[len(changes)-1]
	if last.ChangeType != "CREATED" {
		t.Errorf("changes[last].changeType: got %q, want CREATED", last.ChangeType)
	}
}

// RunExternalAPI_07_04_ChangeHistoryAtPointInTime — dictionary 07/04.
// Skipped: parity client's GetEntityChanges has no pointInTime variant.
func RunExternalAPI_07_04_ChangeHistoryAtPointInTime(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: parity client does not yet expose pointInTime-scoped change history")
}

// RunExternalAPI_07_05_ChangeHistoryNonExistent — dictionary 07/05 (NEGATIVE).
// Stopgap until GetEntityChangesRaw lands in Phase 5.
func RunExternalAPI_07_05_ChangeHistoryNonExistent(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	bogus := uuid.New()
	_, err := d.GetEntityChanges(bogus)
	if err == nil {
		t.Fatal("expected error for non-existent entity")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error: %v", err)
	}
}
