package workflow

import (
	"encoding/json"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

func TestWorkflowWithoutActiveField_DefaultsToActive(t *testing.T) {
	engine, factory := setupEngine(t)
	ctx := ctxWithTenant(testTenant)
	modelRef := spi.ModelRef{EntityName: "OboSigningKey", ModelVersion: "1"}

	// Import a workflow without "active" field.
	// The import handler defaults Active to true before saving.
	// Simulate the handler's behavior:
	wfJSON := `[{"version":"1.0","name":"obo-signing-key-lifecycle","initialState":"ACTIVE","states":{"ACTIVE":{"transitions":[]}}}]`
	var workflows []spi.WorkflowDefinition
	if err := json.Unmarshal([]byte(wfJSON), &workflows); err != nil {
		t.Fatalf("failed to parse workflow: %v", err)
	}

	// Verify Active defaults to false from JSON unmarshal.
	if workflows[0].Active {
		t.Fatal("precondition: expected Active=false from JSON without active field")
	}

	// Apply the same defaulting the handler does.
	for i := range workflows {
		workflows[i].Active = true
	}

	wfStore, err := factory.WorkflowStore(ctx)
	if err != nil {
		t.Fatalf("failed to get workflow store: %v", err)
	}
	if err := wfStore.Save(ctx, modelRef, workflows); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Create entity — should use the imported workflow (not default).
	entity := makeEntity("obo-active-1", modelRef, map[string]any{"keyId": "test"})
	result, err := engine.Execute(ctx, entity, "")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success")
	}
	// The imported workflow has initialState "ACTIVE" and no transitions,
	// so entity should end in "ACTIVE" (not "CREATED" from default).
	if entity.Meta.State != "ACTIVE" {
		t.Fatalf("expected state ACTIVE from imported workflow, got %s (probably fell back to default)", entity.Meta.State)
	}
}
