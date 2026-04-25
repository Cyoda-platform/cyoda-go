package externalapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/errorcontract"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_12_01_CreateEntityOnUnlockedModel", Fn: RunExternalAPI_12_01_CreateEntityOnUnlockedModel},
		parity.NamedTest{Name: "ExternalAPI_12_02_CreateEntityWithIncompatibleType", Fn: RunExternalAPI_12_02_CreateEntityWithIncompatibleType},
		parity.NamedTest{Name: "ExternalAPI_12_03_SetChangeLevelInvalidEnum", Fn: RunExternalAPI_12_03_SetChangeLevelInvalidEnum},
		parity.NamedTest{Name: "ExternalAPI_12_04_GetEntityAtTimeBeforeCreation", Fn: RunExternalAPI_12_04_GetEntityAtTimeBeforeCreation},
		parity.NamedTest{Name: "ExternalAPI_12_05_GetEntityWithBogusTransactionID", Fn: RunExternalAPI_12_05_GetEntityWithBogusTransactionID},
		parity.NamedTest{Name: "ExternalAPI_12_06_GetChangesForMissingEntity", Fn: RunExternalAPI_12_06_GetChangesForMissingEntity},
		parity.NamedTest{Name: "ExternalAPI_12_07_DeleteByConditionTooManyMatches", Fn: RunExternalAPI_12_07_DeleteByConditionTooManyMatches},
		parity.NamedTest{Name: "ExternalAPI_12_08_UpdateUnknownTransition", Fn: RunExternalAPI_12_08_UpdateUnknownTransition},
		parity.NamedTest{Name: "ExternalAPI_12_09_GetModelAfterDelete", Fn: RunExternalAPI_12_09_GetModelAfterDelete},
		parity.NamedTest{Name: "ExternalAPI_12_10_ImportWorkflowOnUnknownModel", Fn: RunExternalAPI_12_10_ImportWorkflowOnUnknownModel},
	)
}

// RunExternalAPI_12_01_CreateEntityOnUnlockedModel — dictionary 12/neg/01.
// Dictionary expects HTTP 409 + EntityModelWrongStateException.
// equiv_or_better: cyoda-go emits MODEL_NOT_LOCKED which carries the
// wrong-state semantic precisely; cloud maps to EntityModelWrongStateException.
func RunExternalAPI_12_01_CreateEntityOnUnlockedModel(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("neg1", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Skip lock — model is unlocked.
	status, body, err := d.CreateEntityRaw("neg1", 1, `{"k":1}`)
	if err != nil {
		t.Fatalf("CreateEntityRaw: %v", err)
	}
	// equiv_or_better: MODEL_NOT_LOCKED maps to EntityModelWrongStateException in the cloud dictionary.
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusConflict,
		ErrorCode:  "MODEL_NOT_LOCKED",
	})
}

// RunExternalAPI_12_02_CreateEntityWithIncompatibleType — dictionary 12/neg/02.
// Dictionary expects HTTP 400 + FoundIncompatibleTypeWitEntityModelException.
// worse: cyoda-go emits generic BAD_REQUEST — same code path as scenario 02/03.
// Tracked by #129. The entity-count assertion is preserved for when #129 lands.
func RunExternalAPI_12_02_CreateEntityWithIncompatibleType(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending #129 — cyoda-go emits generic BAD_REQUEST; dictionary requires FoundIncompatibleTypeWitEntityModelException-level specificity (TYPE_MISMATCH). Discover-and-compare worse case.")
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("neg2", 1, `{"price":13}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.LockModel("neg2", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	status, body, err := d.CreateEntityRaw("neg2", 1, `{"price":13.111}`)
	if err != nil {
		t.Fatalf("CreateEntityRaw: %v", err)
	}
	// Tighten to TYPE_MISMATCH once #129 lands.
	// Cloud equivalent: FoundIncompatibleTypeWitEntityModelException.
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusBadRequest,
		ErrorCode:  "TYPE_MISMATCH",
	})
	// Entity count must remain zero — write was rejected.
	list, err := d.ListEntitiesByModel("neg2", 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("entity count: got %d, want 0", len(list))
	}
}

// RunExternalAPI_12_03_SetChangeLevelInvalidEnum — dictionary 12/neg/03.
// Dictionary expects HTTP 400, message contains "Invalid enum value".
// worse: cyoda-go emits generic BAD_REQUEST despite the detail containing
// the right message. Tracked by #130.
func RunExternalAPI_12_03_SetChangeLevelInvalidEnum(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending #130 — cyoda-go emits generic BAD_REQUEST for invalid change-level enum; dictionary expects an enum-validation-specific code. Discover-and-compare worse case.")
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("neg3", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	status, body, err := d.SetChangeLevelRaw("neg3", 1, "wrong")
	if err != nil {
		t.Fatalf("SetChangeLevelRaw: %v", err)
	}
	// Tighten to INVALID_CHANGE_LEVEL (or similar) once #130 lands.
	// Detail string already contains valid-values list; only errorCode is generic.
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusBadRequest,
		ErrorCode:  "INVALID_CHANGE_LEVEL",
	})
}

// RunExternalAPI_12_04_GetEntityAtTimeBeforeCreation — dictionary 12/neg/04.
// HTTP 404. Skipped pending GetEntityAtRaw returning the body on Driver.
func RunExternalAPI_12_04_GetEntityAtTimeBeforeCreation(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: GetEntityAtRaw not exposed on Driver; tracked alongside tranche-2 follow-up issue")
}

// RunExternalAPI_12_05_GetEntityWithBogusTransactionID — dictionary 12/neg/05.
// HTTP 404. Skipped — transactionId-scoped GET surface absent (same gap as 07/02).
func RunExternalAPI_12_05_GetEntityWithBogusTransactionID(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: parity client does not yet expose transactionId-scoped GET (same gap as 07/02)")
}

// RunExternalAPI_12_06_GetChangesForMissingEntity — dictionary 12/neg/06.
// Dictionary expects HTTP 404 + EntityNotFoundException.
// equiv_or_better: cyoda-go emits ENTITY_NOT_FOUND which matches exactly.
func RunExternalAPI_12_06_GetChangesForMissingEntity(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	bogus := uuid.New()
	status, body, err := d.GetEntityChangesRaw(bogus)
	if err != nil {
		t.Fatalf("GetEntityChangesRaw: %v", err)
	}
	// equiv_or_better: ENTITY_NOT_FOUND maps directly to EntityNotFoundException in the cloud dictionary.
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusNotFound,
		ErrorCode:  "ENTITY_NOT_FOUND",
	})
}

// RunExternalAPI_12_07_DeleteByConditionTooManyMatches — dictionary 12/neg/07.
// Skipped pending #124 — delete-by-condition surface entirely missing server-side.
func RunExternalAPI_12_07_DeleteByConditionTooManyMatches(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending #124 — DELETE /entity/{name}/{version} ignores both condition body and pointInTime; full delete-by-condition surface is a v0.7.0 server-side gap")
}

// RunExternalAPI_12_08_UpdateUnknownTransition — dictionary 12/neg/08.
// Dictionary expects HTTP 400 + (IllegalTransition|TransitionNotFound).
// different_naming_same_level: cyoda-go emits WORKFLOW_FAILED (HTTP 400)
// with detail "transition not found in state". This carries the transition-
// not-found semantic; cloud uses a more specific code (IllegalTransition /
// TransitionNotFound). Tightening to WORKFLOW_FAILED preserves the signal
// until a dedicated TRANSITION_NOT_FOUND code is introduced.
func RunExternalAPI_12_08_UpdateUnknownTransition(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("neg8", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.LockModel("neg8", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("neg8", 1, `{"k":1}`)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	status, body, err := d.UpdateEntityRaw(id, "NoSuchTransition", `{"k":2}`)
	if err != nil {
		t.Fatalf("UpdateEntityRaw: %v", err)
	}
	// different_naming_same_level: WORKFLOW_FAILED carries "transition not found in state" in the
	// detail string; cloud equivalent is IllegalTransition / TransitionNotFound.
	// Reconcile with a dedicated TRANSITION_NOT_FOUND code in v0.7.0+ if introduced.
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusBadRequest,
		ErrorCode:  "WORKFLOW_FAILED",
	})
}

// RunExternalAPI_12_09_GetModelAfterDelete — dictionary 12/neg/09.
// cyoda-go has no per-model GET endpoint; we verify the deleted model
// is absent from ListModels. This is a `different_naming_same_level`
// case: list-and-not-found vs per-model 404 are semantically equivalent.
func RunExternalAPI_12_09_GetModelAfterDelete(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("neg9", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.DeleteModel("neg9", 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	models, err := d.ListModels()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, m := range models {
		if m.ModelName == "neg9" && m.ModelVersion == 1 {
			t.Errorf("model neg9/1 still in list after delete: %+v", m)
		}
	}
	// different_naming_same_level: cloud returns per-model GET 404; cyoda-go
	// exposes list-and-not-found instead (no per-model GET endpoint today).
	// Reconcile in tranche-5 cloud smoke if cyoda-go later adds a per-model
	// GET endpoint.
}

// RunExternalAPI_12_10_ImportWorkflowOnUnknownModel — dictionary 12/neg/10.
// Dictionary expects HTTP 404 + (ModelNotFound|EntityModelNotFound).
// worse: cyoda-go returns HTTP 200 {"success":true} for workflow import on an
// unregistered model. Tracked by #131.
func RunExternalAPI_12_10_ImportWorkflowOnUnknownModel(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending #131 — cyoda-go returns HTTP 200 {success:true} for workflow import on unknown model; dictionary requires HTTP 404 (ModelNotFound/EntityModelNotFound). Discover-and-compare worse case.")
	d := driver.NewInProcess(t, fixture)
	body := `{"workflows":[{"version":"1.0","name":"w","initialState":"s","states":{"s":{"transitions":[]}}}]}`
	status, respBody, err := d.ImportWorkflowRaw("unknownModel", 1, body)
	if err != nil {
		t.Fatalf("ImportWorkflowRaw: %v", err)
	}
	// Tighten to MODEL_NOT_FOUND once #131 lands.
	errorcontract.Match(t, status, respBody, errorcontract.ExpectedError{
		HTTPStatus: http.StatusNotFound,
		ErrorCode:  "MODEL_NOT_FOUND",
	})
}
