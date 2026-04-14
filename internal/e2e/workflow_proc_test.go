package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// --- Helpers ---

// getEntityState retrieves an entity and returns its state from the meta envelope.
func getEntityState(t *testing.T, entityID string) string {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s", entityID)
	resp := doAuth(t, http.MethodGet, path, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getEntity %s: expected 200, got %d: %s", entityID, resp.StatusCode, body)
	}
	var result map[string]any
	json.Unmarshal([]byte(body), &result)
	meta, _ := result["meta"].(map[string]any)
	state, _ := meta["state"].(string)
	return state
}

// getSMAuditEvents retrieves state machine audit events for an entity.
func getSMAuditEvents(t *testing.T, entityID string) []map[string]any {
	t.Helper()
	path := fmt.Sprintf("/api/audit/entity/%s?eventType=StateMachine", entityID)
	resp := doAuth(t, http.MethodGet, path, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit GET %s: expected 200, got %d: %s", entityID, resp.StatusCode, body)
	}
	var auditResp map[string]any
	json.Unmarshal([]byte(body), &auditResp)
	items, _ := auditResp["items"].([]any)
	var events []map[string]any
	for _, item := range items {
		if ev, ok := item.(map[string]any); ok {
			events = append(events, ev)
		}
	}
	return events
}

// setupModelWithWorkflow imports a model, locks it, and imports a workflow.
func setupModelWithWorkflow(t *testing.T, entityName string, workflowJSON string) {
	t.Helper()
	importModelE2E(t, entityName, 1)
	lockModelE2E(t, entityName, 1)
	status, body := importWorkflowE2E(t, entityName, 1, workflowJSON)
	if status != http.StatusOK {
		t.Fatalf("workflow import for %s: expected 200, got %d: %s", entityName, status, body)
	}
}

// --- Test 1.6: Loopback after data update ---

func TestWorkflowProc_Loopback(t *testing.T) {
	const model = "e2e-wfproc-6"

	// Criteria that checks if amount > 100.
	procSvc.RegisterCriteria("high-value", func(ctx context.Context, entity *spi.Entity, criterion json.RawMessage) (bool, error) {
		var data map[string]any
		json.Unmarshal(entity.Data, &data)
		amount, _ := data["amount"].(float64)
		return amount > 100, nil
	})
	defer procSvc.Reset()

	wf := `{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1", "name": "loopback-wf", "initialState": "NONE", "active": true,
			"states": {
				"NONE": {"transitions": [{"name": "init", "next": "CREATED", "manual": false}]},
				"CREATED": {"transitions": [{"name": "auto-promote", "next": "PREMIUM", "manual": false,
					"criterion": {"type": "function", "function": {"name": "high-value"}}
				}]},
				"PREMIUM": {}
			}
		}]
	}`
	setupModelWithWorkflow(t, model, wf)

	// Create with amount=50 — should stay CREATED.
	entityID := createEntityE2E(t, model, 1, `{"name":"Test","amount":50,"status":"new"}`)
	state := getEntityState(t, entityID)
	if state != "CREATED" {
		t.Fatalf("expected CREATED with amount=50, got %s", state)
	}

	// Update with amount=200 via loopback (PUT without transition name).
	path := fmt.Sprintf("/api/entity/JSON/%s", entityID)
	resp := doAuth(t, http.MethodPut, path, `{"name":"Test","amount":200,"status":"new"}`)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback update: expected 200, got %d: %s", resp.StatusCode, body)
	}

	// After loopback, criteria should match and entity moves to PREMIUM.
	state = getEntityState(t, entityID)
	if state != "PREMIUM" {
		t.Errorf("expected PREMIUM after loopback with amount=200, got %s", state)
	}
}

// --- Test 1.8: Default workflow fallback ---

func TestWorkflowProc_DefaultWorkflowFallback(t *testing.T) {
	const model = "e2e-wfproc-8"

	// No workflow imported — default NONE→CREATED→DELETED applies.
	importModelE2E(t, model, 1)
	lockModelE2E(t, model, 1)

	entityID := createEntityE2E(t, model, 1, `{"name":"Test","amount":10,"status":"new"}`)

	state := getEntityState(t, entityID)
	if state != "CREATED" {
		t.Errorf("expected CREATED from default workflow, got %s", state)
	}
}

// --- Test 1.9: Cascade depth limit ---

func TestWorkflowProc_CascadeDepthLimit(t *testing.T) {
	const model = "e2e-wfproc-9"

	// Use criteria-gated transitions that always match — this bypasses the
	// static loop detection at import time but still loops at runtime.
	procSvc.RegisterCriteria("loop-gate", func(ctx context.Context, entity *spi.Entity, criterion json.RawMessage) (bool, error) {
		return true, nil
	})
	defer procSvc.Reset()

	wf := `{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1", "name": "loop-wf", "initialState": "A", "active": true,
			"states": {
				"A": {"transitions": [{"name": "to-b", "next": "B", "manual": false,
					"criterion": {"type": "function", "function": {"name": "loop-gate"}}
				}]},
				"B": {"transitions": [{"name": "to-a", "next": "A", "manual": false,
					"criterion": {"type": "function", "function": {"name": "loop-gate"}}
				}]}
			}
		}]
	}`

	importModelE2E(t, model, 1)
	lockModelE2E(t, model, 1)
	status, body := importWorkflowE2E(t, model, 1, wf)
	if status != http.StatusOK {
		t.Fatalf("workflow import: expected 200, got %d: %s", status, body)
	}

	// Entity creation should fail due to cascade depth exceeded.
	path := fmt.Sprintf("/api/entity/JSON/%s/%d", model, 1)
	resp := doAuth(t, http.MethodPost, path, `{"name":"Test","amount":10,"status":"new"}`)
	respBody := readBody(t, resp)

	// Should return an error (500 or 400) — not hang.
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected error for infinite cascade, but got 200: %s", respBody)
	}
}

// --- Test 1.10: Processor modifies entity data ---

func TestWorkflowProc_ProcessorModifiesData(t *testing.T) {
	const model = "e2e-wfproc-10"

	procSvc.RegisterProcessor("compute-total", func(ctx context.Context, entity *spi.Entity, proc spi.ProcessorDefinition) (*spi.Entity, error) {
		var data map[string]any
		json.Unmarshal(entity.Data, &data)
		amount, _ := data["amount"].(float64)
		data["total"] = amount * 1.1 // add 10% tax
		updated, _ := json.Marshal(data)
		return &spi.Entity{Meta: entity.Meta, Data: updated}, nil
	})
	defer procSvc.Reset()

	wf := `{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1", "name": "modify-wf", "initialState": "NONE", "active": true,
			"states": {
				"NONE": {"transitions": [{"name": "init", "next": "CREATED", "manual": false,
					"processors": [{"type": "calculator", "name": "compute-total", "executionMode": "SYNC",
						"config": {"attachEntity": true, "calculationNodesTags": ""}}]
				}]},
				"CREATED": {}
			}
		}]
	}`
	setupModelWithWorkflow(t, model, wf)

	entityID := createEntityE2E(t, model, 1, `{"name":"Test","amount":100,"status":"new"}`)

	data := getEntityData(t, entityID, "")
	total, _ := data["total"].(float64)
	if total < 109.99 || total > 110.01 {
		t.Errorf("expected total≈110, got %v", data["total"])
	}
}

// --- Test 1.11: Multiple processors on single transition ---

func TestWorkflowProc_MultipleProcessorsSameTransition(t *testing.T) {
	const model = "e2e-wfproc-11"

	procSvc.RegisterProcessor("step-1", func(ctx context.Context, entity *spi.Entity, proc spi.ProcessorDefinition) (*spi.Entity, error) {
		var data map[string]any
		json.Unmarshal(entity.Data, &data)
		data["step1"] = true
		updated, _ := json.Marshal(data)
		return &spi.Entity{Meta: entity.Meta, Data: updated}, nil
	})
	procSvc.RegisterProcessor("step-2", func(ctx context.Context, entity *spi.Entity, proc spi.ProcessorDefinition) (*spi.Entity, error) {
		var data map[string]any
		json.Unmarshal(entity.Data, &data)
		// step-2 should see step-1's output.
		if data["step1"] != true {
			return nil, fmt.Errorf("step-2 did not see step-1's output")
		}
		data["step2"] = true
		updated, _ := json.Marshal(data)
		return &spi.Entity{Meta: entity.Meta, Data: updated}, nil
	})
	defer procSvc.Reset()

	wf := `{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1", "name": "multi-proc-wf", "initialState": "NONE", "active": true,
			"states": {
				"NONE": {"transitions": [{"name": "init", "next": "CREATED", "manual": false,
					"processors": [
						{"type": "calculator", "name": "step-1", "executionMode": "SYNC",
							"config": {"attachEntity": true, "calculationNodesTags": ""}},
						{"type": "calculator", "name": "step-2", "executionMode": "SYNC",
							"config": {"attachEntity": true, "calculationNodesTags": ""}}
					]
				}]},
				"CREATED": {}
			}
		}]
	}`
	setupModelWithWorkflow(t, model, wf)

	entityID := createEntityE2E(t, model, 1, `{"name":"Test","amount":10,"status":"new"}`)

	data := getEntityData(t, entityID, "")
	if data["step1"] != true {
		t.Error("expected step1=true")
	}
	if data["step2"] != true {
		t.Error("expected step2=true")
	}
}

// --- Test 1.12: Audit trail for full workflow ---

func TestWorkflowProc_FullAuditTrail(t *testing.T) {
	const model = "e2e-wfproc-12"

	procSvc.RegisterProcessor("audit-proc", func(ctx context.Context, entity *spi.Entity, proc spi.ProcessorDefinition) (*spi.Entity, error) {
		return entity, nil // no-op processor
	})
	defer procSvc.Reset()

	wf := `{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1", "name": "audit-trail-wf", "initialState": "NONE", "active": true,
			"states": {
				"NONE": {"transitions": [{"name": "init", "next": "CREATED", "manual": false,
					"processors": [{"type": "calculator", "name": "audit-proc", "executionMode": "SYNC",
						"config": {"attachEntity": true, "calculationNodesTags": ""}}]
				}]},
				"CREATED": {"transitions": [{"name": "finish", "next": "DONE", "manual": false}]},
				"DONE": {}
			}
		}]
	}`
	setupModelWithWorkflow(t, model, wf)

	entityID := createEntityE2E(t, model, 1, `{"name":"Test","amount":10,"status":"new"}`)

	events := getSMAuditEvents(t, entityID)

	// Expect at minimum: START, WORKFLOW_FOUND, TRANSITION_MAKE (x2: init + finish), FINISH.
	eventTypes := make(map[string]int)
	for _, ev := range events {
		if et, ok := ev["eventType"].(string); ok {
			eventTypes[et]++
		}
	}

	if eventTypes["STATE_MACHINE_START"] < 1 {
		t.Error("missing STATE_MACHINE_START event")
	}
	if eventTypes["STATE_MACHINE_FINISH"] < 1 {
		t.Error("missing STATE_MACHINE_FINISH event")
	}
	if eventTypes["TRANSITION_MAKE"] < 2 {
		t.Errorf("expected >= 2 TRANSITION_MAKE events (init + finish), got %d", eventTypes["TRANSITION_MAKE"])
	}

	state := getEntityState(t, entityID)
	if state != "DONE" {
		t.Errorf("expected final state DONE, got %s", state)
	}
}
