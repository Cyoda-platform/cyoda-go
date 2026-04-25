// Package externalapi contains parity Run* functions that implement the
// External API Scenario Dictionary scenarios against cyoda-go's HTTP API.
//
// Registration: each file calls parity.Register in an init() function.
// Backend test packages (memory, sqlite, postgres) must blank-import this
// package to trigger the side effect:
//
//	import _ "github.com/cyoda-platform/cyoda-go/e2e/parity/externalapi"
//
// This avoids the import cycle that would result from parity/registry.go
// importing this package (which itself imports parity for BackendFixture).
package externalapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/errorcontract"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

func init() {
	// External API scenario suite — tranche 1 (issue #118)
	// 01-model-lifecycle
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_01_01_RegisterModel", Fn: RunExternalAPI_01_01_RegisterModel},
		parity.NamedTest{Name: "ExternalAPI_01_02_UpsertExtendsSchema", Fn: RunExternalAPI_01_02_UpsertExtendsSchema},
		parity.NamedTest{Name: "ExternalAPI_01_03_UpsertIncompatibleType", Fn: RunExternalAPI_01_03_UpsertIncompatibleType},
		parity.NamedTest{Name: "ExternalAPI_01_04_ReregisterIdempotent", Fn: RunExternalAPI_01_04_ReregisterIdempotent},
		parity.NamedTest{Name: "ExternalAPI_01_05_LockModel", Fn: RunExternalAPI_01_05_LockModel},
		parity.NamedTest{Name: "ExternalAPI_01_06_UnlockModel", Fn: RunExternalAPI_01_06_UnlockModel},
		parity.NamedTest{Name: "ExternalAPI_01_07_LockTwiceRejected", Fn: RunExternalAPI_01_07_LockTwiceRejected},
		parity.NamedTest{Name: "ExternalAPI_01_08_DeleteModel", Fn: RunExternalAPI_01_08_DeleteModel},
		parity.NamedTest{Name: "ExternalAPI_01_09_ListModelsEmpty", Fn: RunExternalAPI_01_09_ListModelsEmpty},
		parity.NamedTest{Name: "ExternalAPI_01_10_ListModelsNonEmpty", Fn: RunExternalAPI_01_10_ListModelsNonEmpty},
		parity.NamedTest{Name: "ExternalAPI_01_11_ExportMetadataViews", Fn: RunExternalAPI_01_11_ExportMetadataViews},
		parity.NamedTest{Name: "ExternalAPI_01_12_NobelLaureatesSample", Fn: RunExternalAPI_01_12_NobelLaureatesSample},
		parity.NamedTest{Name: "ExternalAPI_01_13_LEISample", Fn: RunExternalAPI_01_13_LEISample},
	)
}

// RunExternalAPI_01_01_RegisterModel — dictionary 01/01.
func RunExternalAPI_01_01_RegisterModel(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	if err := c.ImportModel(t, "simple1", 1, `{"key1": 123}`); err != nil {
		t.Fatalf("CreateModelFromSample: %v", err)
	}
	raw, err := c.ExportModel(t, "SIMPLE_VIEW", "simple1", 1)
	if err != nil {
		t.Fatalf("ExportModel: %v", err)
	}
	// SIMPLE_VIEW export shape: {"currentState":"UNLOCKED","model":{"$":{".key1":"INTEGER",...}}}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	model, ok := got["model"].(map[string]any)
	if !ok {
		t.Fatalf("export missing model key: %v", got)
	}
	dollarVal, ok := model["$"].(map[string]any)
	if !ok {
		t.Fatalf("export model missing $ key: %v", model)
	}
	if dollarVal[".key1"] != "INTEGER" {
		t.Errorf(".key1 type: got %v, want INTEGER", dollarVal[".key1"])
	}
}

// RunExternalAPI_01_02_UpsertExtendsSchema — dictionary 01/02.
func RunExternalAPI_01_02_UpsertExtendsSchema(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	if err := c.ImportModel(t, "merged", 1, `{"a": 1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.ImportModel(t, "merged", 1, `{"a": 2, "b": "hello"}`); err != nil {
		t.Fatalf("update: %v", err)
	}
	raw, err := c.ExportModel(t, "SIMPLE_VIEW", "merged", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// SIMPLE_VIEW export shape: {"currentState":"UNLOCKED","model":{"$":{...fields...}}}
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	model, _ := got["model"].(map[string]any)
	dollar, _ := model["$"].(map[string]any)
	for _, field := range []string{".a", ".b"} {
		if _, ok := dollar[field]; !ok {
			t.Errorf("export missing field %q in $: %v", field, dollar)
		}
	}
}

// RunExternalAPI_01_03_UpsertIncompatibleType — dictionary 01/03.
func RunExternalAPI_01_03_UpsertIncompatibleType(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	if err := c.ImportModel(t, "types", 1, `{"price": 13}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Upsert with a different scalar type on same field is accepted
	// (YAML scenario notes: "import is accepted and the field type is
	// adjusted"). Lock-time rejection is a separate tranche-2 concern.
	if err := c.ImportModel(t, "types", 1, `{"price": "expensive"}`); err != nil {
		t.Fatalf("update with incompatible type: %v", err)
	}
}

// RunExternalAPI_01_04_ReregisterIdempotent — dictionary 01/04.
func RunExternalAPI_01_04_ReregisterIdempotent(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	if err := c.ImportModel(t, "idemp", 1, `{"k": 1}`); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Re-register same schema: idempotent, no error.
	if err := c.ImportModel(t, "idemp", 1, `{"k": 1}`); err != nil {
		t.Fatalf("re-register: %v", err)
	}
}

// RunExternalAPI_01_05_LockModel — dictionary 01/05.
func RunExternalAPI_01_05_LockModel(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	if err := c.ImportModel(t, "lockme", 1, `{"k": 1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.LockModel(t, "lockme", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Proof of lock: list models and confirm state is LOCKED.
	models, err := c.ListModels(t)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, m := range models {
		if m.ModelName == "lockme" && m.ModelVersion == 1 {
			found = true
			if m.CurrentState != "LOCKED" {
				t.Errorf("model state: got %q, want LOCKED", m.CurrentState)
			}
		}
	}
	if !found {
		t.Error("model lockme/1 not found in list")
	}
}

// RunExternalAPI_01_06_UnlockModel — dictionary 01/06.
func RunExternalAPI_01_06_UnlockModel(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	_ = c.ImportModel(t, "unlockme", 1, `{"k": 1}`)
	_ = c.LockModel(t, "unlockme", 1)
	if err := c.UnlockModel(t, "unlockme", 1); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	models, _ := c.ListModels(t)
	for _, m := range models {
		if m.ModelName == "unlockme" && m.ModelVersion == 1 {
			if m.CurrentState != "UNLOCKED" {
				t.Errorf("state: got %q, want UNLOCKED", m.CurrentState)
			}
		}
	}
}

// RunExternalAPI_01_07_LockTwiceRejected — dictionary 01/07 (negative).
func RunExternalAPI_01_07_LockTwiceRejected(t *testing.T, fixture parity.BackendFixture) {
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("locktwice", 1, `{"k": 1}`); err != nil {
		t.Fatalf("CreateModelFromSample: %v", err)
	}
	if err := d.LockModel("locktwice", 1); err != nil {
		t.Fatalf("first LockModel: %v", err)
	}
	// Second lock attempt: must be rejected with 409 + a recognisable error code.
	status, body, err := d.LockModelRaw("locktwice", 1)
	if err != nil {
		t.Fatalf("LockModelRaw on second attempt: %v", err)
	}
	if status == http.StatusOK {
		t.Fatal("second LockModel should have failed but returned 200")
	}
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusConflict,
		ErrorCode:  "CONFLICT",
	})
}

// RunExternalAPI_01_08_DeleteModel — dictionary 01/08.
func RunExternalAPI_01_08_DeleteModel(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	_ = c.ImportModel(t, "toremove", 1, `{"k": 1}`)
	if err := c.DeleteModel(t, "toremove", 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	models, _ := c.ListModels(t)
	for _, m := range models {
		if m.ModelName == "toremove" && m.ModelVersion == 1 {
			t.Errorf("model still present after delete: %+v", m)
		}
	}
}

// RunExternalAPI_01_09_ListModelsEmpty — dictionary 01/09.
func RunExternalAPI_01_09_ListModelsEmpty(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	models, err := c.ListModels(t)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Fresh tenant — expect zero models.
	if len(models) != 0 {
		t.Errorf("fresh tenant: got %d models, want 0 (%+v)", len(models), models)
	}
}

// RunExternalAPI_01_10_ListModelsNonEmpty — dictionary 01/10.
func RunExternalAPI_01_10_ListModelsNonEmpty(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	for _, name := range []string{"a", "b", "c"} {
		_ = c.ImportModel(t, name, 1, `{"k": 1}`)
	}
	models, err := c.ListModels(t)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) < 3 {
		t.Errorf("got %d models, want ≥3", len(models))
	}
	names := map[string]bool{}
	for _, m := range models {
		names[m.ModelName] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !names[want] {
			t.Errorf("list missing model %q", want)
		}
	}
}

// RunExternalAPI_01_11_ExportMetadataViews — dictionary 01/11.
func RunExternalAPI_01_11_ExportMetadataViews(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	_ = c.ImportModel(t, "mdviews", 1, `{"k": 123}`)
	for _, view := range []string{"SIMPLE_VIEW"} {
		// JSON_SCHEMA view may or may not be wired today — only SIMPLE_VIEW
		// is asserted to exist in tranche-1. Expansion is tranche-2 work.
		raw, err := c.ExportModel(t, view, "mdviews", 1)
		if err != nil {
			t.Fatalf("export %s: %v", view, err)
		}
		if len(raw) == 0 {
			t.Errorf("export %s returned empty", view)
		}
	}
}

// RunExternalAPI_01_12_NobelLaureatesSample — dictionary 01/12.
func RunExternalAPI_01_12_NobelLaureatesSample(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	// Representative nested sample — keep small to avoid flakiness;
	// the scenario is about nesting depth, not sample size.
	sample := `{
		"year": 2020,
		"category": "Physics",
		"laureates": [
			{"id": "1001", "firstname": "Alice", "surname": "A", "motivation": "x"},
			{"id": "1002", "firstname": "Bob",   "surname": "B", "motivation": "y"}
		]
	}`
	if err := c.ImportModel(t, "nobel", 1, sample); err != nil {
		t.Fatalf("create: %v", err)
	}
	raw, err := c.ExportModel(t, "SIMPLE_VIEW", "nobel", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// Assert that the nested array path is present in the exported view.
	if !strings.Contains(string(raw), "laureates") {
		t.Errorf("export missing nested array field: %s", string(raw))
	}
}

// RunExternalAPI_01_13_LEISample — dictionary 01/13.
func RunExternalAPI_01_13_LEISample(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	sample := `{
		"lei":"549300MLUDYVRQOOXS22",
		"legalName":{"value":"ACME"},
		"entityStatus":"ACTIVE"
	}`
	if err := c.ImportModel(t, "lei", 1, sample); err != nil {
		t.Fatalf("create: %v", err)
	}
	raw, err := c.ExportModel(t, "SIMPLE_VIEW", "lei", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(string(raw), "legalName") {
		t.Errorf("export missing nested object field: %s", string(raw))
	}
}
