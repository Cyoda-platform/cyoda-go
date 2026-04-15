package workflow_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/app"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// newTestServer creates an App with default config and returns an httptest.Server.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := app.DefaultConfig()
	cfg.ContextPath = ""
	a := app.New(cfg)
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// importModel creates a model so that the workflow endpoints have something to reference.
func importModel(t *testing.T, base, entityName string, version int) {
	t.Helper()
	url := base + "/model/import/JSON/SAMPLE_DATA/" + entityName + "/" + strconv.Itoa(version)
	resp, err := http.Post(url, "application/json", strings.NewReader(`{"name":"test"}`))
	if err != nil {
		t.Fatalf("model import failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("model import expected 200, got %d", resp.StatusCode)
	}
}

func doWorkflowImport(t *testing.T, base, entityName string, version int, body string) *http.Response {
	t.Helper()
	url := base + "/model/" + entityName + "/" + strconv.Itoa(version) + "/workflow/import"
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("workflow import request failed: %v", err)
	}
	return resp
}

func doWorkflowExport(t *testing.T, base, entityName string, version int) *http.Response {
	t.Helper()
	url := base + "/model/" + entityName + "/" + strconv.Itoa(version) + "/workflow/export"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("workflow export request failed: %v", err)
	}
	return resp
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to parse JSON: %v\nbody: %s", err, data)
	}
	return result
}

func readWorkflows(t *testing.T, resp *http.Response) []spi.WorkflowDefinition {
	t.Helper()
	body := readJSON(t, resp)
	wfRaw, err := json.Marshal(body["workflows"])
	if err != nil {
		t.Fatalf("failed to marshal workflows: %v", err)
	}
	var wfs []spi.WorkflowDefinition
	if err := json.Unmarshal(wfRaw, &wfs); err != nil {
		t.Fatalf("failed to parse workflows: %v\nraw: %s", err, wfRaw)
	}
	return wfs
}

func TestImportAndExport(t *testing.T) {
	srv := newTestServer(t)
	importModel(t, srv.URL, "Order", 1)

	body := `{
		"importMode": "MERGE",
		"workflows": [
			{
				"version": "1.0",
				"name": "order-flow",
				"initialState": "NEW",
				"active": true,
				"states": {
					"NEW": {
						"transitions": [
							{"name": "approve", "next": "APPROVED", "manual": true}
						]
					},
					"APPROVED": {
						"transitions": []
					}
				}
			}
		]
	}`

	resp := doWorkflowImport(t, srv.URL, "Order", 1, body)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("import expected 200, got %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	// Export and verify round-trip.
	exportResp := doWorkflowExport(t, srv.URL, "Order", 1)
	if exportResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(exportResp.Body)
		exportResp.Body.Close()
		t.Fatalf("export expected 200, got %d: %s", exportResp.StatusCode, b)
	}

	wfs := readWorkflows(t, exportResp)
	if len(wfs) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfs))
	}
	if wfs[0].Name != "order-flow" {
		t.Errorf("expected name order-flow, got %s", wfs[0].Name)
	}
	if wfs[0].InitialState != "NEW" {
		t.Errorf("expected initialState NEW, got %s", wfs[0].InitialState)
	}
	if !wfs[0].Active {
		t.Error("expected workflow to be active")
	}
	if len(wfs[0].States) != 2 {
		t.Errorf("expected 2 states, got %d", len(wfs[0].States))
	}
}

func TestImportMerge(t *testing.T) {
	srv := newTestServer(t)
	importModel(t, srv.URL, "Order", 1)

	// Import WF-A.
	bodyA := `{
		"importMode": "MERGE",
		"workflows": [
			{
				"version": "1.0",
				"name": "wf-a",
				"initialState": "S1",
				"active": true,
				"states": {"S1": {"transitions": []}}
			}
		]
	}`
	resp := doWorkflowImport(t, srv.URL, "Order", 1, bodyA)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import A: expected 200, got %d", resp.StatusCode)
	}

	// Import WF-B with MERGE → both should be present.
	bodyB := `{
		"importMode": "MERGE",
		"workflows": [
			{
				"version": "1.0",
				"name": "wf-b",
				"initialState": "S2",
				"active": true,
				"states": {"S2": {"transitions": []}}
			}
		]
	}`
	resp = doWorkflowImport(t, srv.URL, "Order", 1, bodyB)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import B: expected 200, got %d", resp.StatusCode)
	}

	wfs := readWorkflows(t, doWorkflowExport(t, srv.URL, "Order", 1))
	if len(wfs) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(wfs))
	}
	names := map[string]bool{}
	for _, wf := range wfs {
		names[wf.Name] = true
	}
	if !names["wf-a"] || !names["wf-b"] {
		t.Errorf("expected wf-a and wf-b, got %v", names)
	}
}

func TestImportReplace(t *testing.T) {
	srv := newTestServer(t)
	importModel(t, srv.URL, "Order", 1)

	// Import WF-A.
	bodyA := `{
		"importMode": "MERGE",
		"workflows": [
			{
				"version": "1.0",
				"name": "wf-a",
				"initialState": "S1",
				"active": true,
				"states": {"S1": {"transitions": []}}
			}
		]
	}`
	resp := doWorkflowImport(t, srv.URL, "Order", 1, bodyA)
	resp.Body.Close()

	// Import WF-B with REPLACE → only WF-B.
	bodyB := `{
		"importMode": "REPLACE",
		"workflows": [
			{
				"version": "1.0",
				"name": "wf-b",
				"initialState": "S2",
				"active": true,
				"states": {"S2": {"transitions": []}}
			}
		]
	}`
	resp = doWorkflowImport(t, srv.URL, "Order", 1, bodyB)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import B: expected 200, got %d", resp.StatusCode)
	}

	wfs := readWorkflows(t, doWorkflowExport(t, srv.URL, "Order", 1))
	if len(wfs) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfs))
	}
	if wfs[0].Name != "wf-b" {
		t.Errorf("expected wf-b, got %s", wfs[0].Name)
	}
}

func TestImportActivate(t *testing.T) {
	srv := newTestServer(t)
	importModel(t, srv.URL, "Order", 1)

	// Import WF-A.
	bodyA := `{
		"importMode": "MERGE",
		"workflows": [
			{
				"version": "1.0",
				"name": "wf-a",
				"initialState": "S1",
				"active": true,
				"states": {"S1": {"transitions": []}}
			}
		]
	}`
	resp := doWorkflowImport(t, srv.URL, "Order", 1, bodyA)
	resp.Body.Close()

	// Import WF-B with ACTIVATE → WF-A inactive, WF-B active.
	bodyB := `{
		"importMode": "ACTIVATE",
		"workflows": [
			{
				"version": "1.0",
				"name": "wf-b",
				"initialState": "S2",
				"active": true,
				"states": {"S2": {"transitions": []}}
			}
		]
	}`
	resp = doWorkflowImport(t, srv.URL, "Order", 1, bodyB)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import B: expected 200, got %d", resp.StatusCode)
	}

	wfs := readWorkflows(t, doWorkflowExport(t, srv.URL, "Order", 1))
	if len(wfs) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(wfs))
	}

	wfMap := map[string]spi.WorkflowDefinition{}
	for _, wf := range wfs {
		wfMap[wf.Name] = wf
	}

	if wfMap["wf-a"].Active {
		t.Error("expected wf-a to be inactive after ACTIVATE import")
	}
	if !wfMap["wf-b"].Active {
		t.Error("expected wf-b to be active after ACTIVATE import")
	}
}

func TestExportEmpty_Returns404(t *testing.T) {
	srv := newTestServer(t)
	importModel(t, srv.URL, "Order", 1)

	resp := doWorkflowExport(t, srv.URL, "Order", 1)
	if resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, b)
	}

	body := readJSON(t, resp)
	detail, _ := body["detail"].(string)
	if !strings.Contains(detail, common.ErrCodeWorkflowNotFound) {
		t.Errorf("expected error code %s in detail, got: %s", common.ErrCodeWorkflowNotFound, detail)
	}
}

func TestImportFullWorkflow(t *testing.T) {
	srv := newTestServer(t)
	importModel(t, srv.URL, "PaymentRequest", 1)

	body := `{
		"importMode": "MERGE",
		"workflows": [
			{
				"version": "1.0",
				"name": "payment-request-flow",
				"initialState": "PENDING_VALIDATION",
				"active": true,
				"states": {
					"PENDING_VALIDATION": {
						"transitions": [
							{
								"name": "validate",
								"next": "VALIDATED",
								"manual": false,
								"processors": [
									{"name": "validate-payment", "type": "FUNCTION"}
								]
							}
						]
					},
					"VALIDATED": {
						"transitions": [
							{
								"name": "approve",
								"next": "APPROVED",
								"manual": true
							},
							{
								"name": "reject",
								"next": "REJECTED",
								"manual": true
							}
						]
					},
					"APPROVED": {
						"transitions": [
							{
								"name": "process",
								"next": "PROCESSED",
								"manual": false,
								"processors": [
									{"name": "execute-payment", "type": "EXTERNAL_API"}
								]
							}
						]
					},
					"PROCESSED": {
						"transitions": []
					},
					"REJECTED": {
						"transitions": []
					}
				}
			}
		]
	}`

	resp := doWorkflowImport(t, srv.URL, "PaymentRequest", 1, body)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("import expected 200, got %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	wfs := readWorkflows(t, doWorkflowExport(t, srv.URL, "PaymentRequest", 1))
	if len(wfs) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfs))
	}

	wf := wfs[0]
	if wf.Name != "payment-request-flow" {
		t.Errorf("expected name payment-request-flow, got %s", wf.Name)
	}
	if wf.InitialState != "PENDING_VALIDATION" {
		t.Errorf("expected initialState PENDING_VALIDATION, got %s", wf.InitialState)
	}
	if len(wf.States) != 5 {
		t.Errorf("expected 5 states, got %d", len(wf.States))
	}

	// Verify PENDING_VALIDATION transitions.
	pv := wf.States["PENDING_VALIDATION"]
	if len(pv.Transitions) != 1 {
		t.Fatalf("PENDING_VALIDATION: expected 1 transition, got %d", len(pv.Transitions))
	}
	if pv.Transitions[0].Name != "validate" {
		t.Errorf("expected transition name validate, got %s", pv.Transitions[0].Name)
	}
	if pv.Transitions[0].Next != "VALIDATED" {
		t.Errorf("expected next VALIDATED, got %s", pv.Transitions[0].Next)
	}
	if len(pv.Transitions[0].Processors) != 1 {
		t.Fatalf("expected 1 processor, got %d", len(pv.Transitions[0].Processors))
	}
	if pv.Transitions[0].Processors[0].Name != "validate-payment" {
		t.Errorf("expected processor name validate-payment, got %s", pv.Transitions[0].Processors[0].Name)
	}

	// Verify VALIDATED has 2 manual transitions.
	v := wf.States["VALIDATED"]
	if len(v.Transitions) != 2 {
		t.Fatalf("VALIDATED: expected 2 transitions, got %d", len(v.Transitions))
	}
	for _, tr := range v.Transitions {
		if !tr.Manual {
			t.Errorf("expected transition %s to be manual", tr.Name)
		}
	}

	// Verify PROCESSED and REJECTED are terminal.
	for _, state := range []string{"PROCESSED", "REJECTED"} {
		s := wf.States[state]
		if len(s.Transitions) != 0 {
			t.Errorf("%s: expected 0 transitions, got %d", state, len(s.Transitions))
		}
	}
}

func TestImportDefaultMode(t *testing.T) {
	srv := newTestServer(t)
	importModel(t, srv.URL, "Order", 1)

	// No importMode specified → defaults to MERGE.
	body := `{
		"workflows": [
			{
				"version": "1.0",
				"name": "wf-default",
				"initialState": "S1",
				"active": true,
				"states": {"S1": {"transitions": []}}
			}
		]
	}`
	resp := doWorkflowImport(t, srv.URL, "Order", 1, body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	wfs := readWorkflows(t, doWorkflowExport(t, srv.URL, "Order", 1))
	if len(wfs) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfs))
	}
	if wfs[0].Name != "wf-default" {
		t.Errorf("expected wf-default, got %s", wfs[0].Name)
	}
}
