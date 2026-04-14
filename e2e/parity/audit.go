package parity

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// RunAuditEntityHistory verifies that creating an entity with a workflow
// produces audit events (both EntityChange and StateMachine) that are
// retrievable via the audit REST API.
//
// Port of internal/e2e TestAudit_EntityCreationGeneratesEvents, using the
// discriminated-union audit types from Task 1.2b:
//   - GetAuditEvents -> EntityAuditEventsResponse -> []AuditEvent
//   - AsStateMachine() / AsEntityChange() for typed subtype assertions
//
// Replaces the queryDB(sm_audit_events) check with API-based assertions.
func RunAuditEntityHistory(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "audit-entity-history"
	const modelVersion = 1

	// Setup: import model, lock, import workflow with NONE->CREATED auto-transition.
	setupSimpleWorkflow(t, c, modelName, modelVersion)

	// Create entity (triggers workflow: NONE -> CREATED).
	entityID, err := c.CreateEntity(t, modelName, modelVersion,
		`{"name":"AuditTest","amount":100,"status":"draft"}`)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	// Get audit events via REST API.
	auditResp, err := c.GetAuditEvents(t, entityID)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}

	// Classify events by type.
	entityChangeCount := 0
	stateMachineCount := 0
	hasTransactionID := false
	hasStart := false
	hasFinish := false

	for i := range auditResp.Items {
		ev := &auditResp.Items[i]

		if ev.TransactionID != "" {
			hasTransactionID = true
		}

		switch ev.AuditEventType {
		case "EntityChange":
			entityChangeCount++
			ec, err := ev.AsEntityChange()
			if err != nil {
				t.Errorf("AsEntityChange on event %d: %v", i, err)
				continue
			}
			if ec.ChangeType == "" {
				t.Errorf("EntityChange event %d: changeType is empty", i)
			}

		case "StateMachine":
			stateMachineCount++
			sm, err := ev.AsStateMachine()
			if err != nil {
				t.Errorf("AsStateMachine on event %d: %v", i, err)
				continue
			}
			// cyoda-go emits STATE_MACHINE_START and STATE_MACHINE_FINISH
			// (see internal/common/types.go SMEventStarted/SMEventFinished).
			// NOTE: canonical openapi-audit.yml uses "STARTED"/"FINISHED" —
			// this is a known drift in cyoda-go.
			switch sm.EventType {
			case "STATE_MACHINE_START":
				hasStart = true
			case "STATE_MACHINE_FINISH":
				hasFinish = true
			}
		}
	}

	if entityChangeCount < 1 {
		t.Errorf("expected >= 1 EntityChange events, got %d", entityChangeCount)
	}
	if stateMachineCount < 2 {
		t.Errorf("expected >= 2 StateMachine events (START + FINISH), got %d", stateMachineCount)
	}
	if !hasStart {
		t.Error("missing STATE_MACHINE_START event")
	}
	if !hasFinish {
		t.Error("missing STATE_MACHINE_FINISH event")
	}
	if !hasTransactionID {
		t.Error("expected at least one audit event with a non-empty transactionId")
	}
}

// RunAuditWorkflowEvents verifies that audit events created during
// entity creation carry the transaction ID, enabling cross-referencing
// between entity versions and workflow events.
//
// Port of internal/e2e TestAudit_EventsHaveTransactionID.
func RunAuditWorkflowEvents(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "audit-workflow-events"
	const modelVersion = 1

	// Setup: import model, lock, import workflow.
	setupSimpleWorkflow(t, c, modelName, modelVersion)

	// Create entity.
	entityID, err := c.CreateEntity(t, modelName, modelVersion,
		`{"name":"TxAudit","amount":200,"status":"active"}`)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	// Get audit events.
	auditResp, err := c.GetAuditEvents(t, entityID)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}

	// At least one event should have a transactionId.
	hasTransactionID := false
	for _, ev := range auditResp.Items {
		if ev.TransactionID != "" {
			hasTransactionID = true
			break
		}
	}
	if !hasTransactionID {
		t.Error("expected at least one audit event with a transactionId for cross-referencing")
	}
}
