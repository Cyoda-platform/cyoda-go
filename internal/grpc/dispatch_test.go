package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
	events "github.com/cyoda-platform/cyoda-go/api/grpc/events"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

const testTenantID = common.TenantID("tenant-1")

func setupTestDispatcher(t *testing.T) (*ProcessorDispatcher, *MemberRegistry, string, chan *cepb.CloudEvent) {
	t.Helper()
	registry := NewMemberRegistry()
	sentCh := make(chan *cepb.CloudEvent, 10)
	memberID := registry.Register(testTenantID, []string{"python"}, func(ce *cepb.CloudEvent) error {
		sentCh <- ce
		return nil
	})
	uuids := common.NewTestUUIDGenerator()
	dispatcher := NewProcessorDispatcher(registry, uuids)
	return dispatcher, registry, memberID, sentCh
}

func testContext() context.Context {
	return common.WithUserContext(context.Background(), &common.UserContext{
		UserID:   "user-1",
		UserName: "test-user",
		Tenant:   common.Tenant{ID: testTenantID, Name: "Test Tenant"},
	})
}

func testEntity() *common.Entity {
	return &common.Entity{
		Meta: common.EntityMeta{
			ID:       "entity-123",
			TenantID: testTenantID,
		},
		Data: []byte(`{"foo":"bar"}`),
	}
}

// extractRequestID parses the request ID from a sent CloudEvent.
// Returns an error instead of calling t.Fatal so it is safe to call from goroutines.
func extractRequestID(ce *cepb.CloudEvent) (string, error) {
	_, payload, err := ParseCloudEvent(ce)
	if err != nil {
		return "", fmt.Errorf("failed to parse cloud event: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return "", fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	rid, ok := m["requestId"].(string)
	if !ok {
		return "", fmt.Errorf("requestId not found in payload")
	}
	return rid, nil
}

func TestDispatchProcessor_HappyPath(t *testing.T) {
	dispatcher, registry, memberID, sentCh := setupTestDispatcher(t)
	ctx := testContext()
	entity := testEntity()

	processor := common.ProcessorDefinition{
		Name: "my-proc",
		Config: common.ProcessorConfig{
			AttachEntity:         true,
			CalculationNodesTags: "python",
			ResponseTimeoutMs:    5000,
		},
	}

	// Goroutine to respond.
	// Note: uses t.Error (not t.Fatal) because t.Fatal calls runtime.Goexit
	// which has undefined behavior when called from a non-test goroutine.
	go func() {
		ce := <-sentCh
		if ce.Type != EntityProcessorCalculationRequest {
			t.Errorf("expected event type %s, got %s", EntityProcessorCalculationRequest, ce.Type)
		}
		reqID, err := extractRequestID(ce)
		if err != nil {
			t.Errorf("extractRequestID: %v", err)
			return
		}

		// Verify payload is attached with data and meta.
		_, payload, _ := ParseCloudEvent(ce)
		var m map[string]any
		json.Unmarshal(payload, &m)
		payloadObj, ok := m["payload"].(map[string]any)
		if !ok {
			t.Error("expected payload to be present when AttachEntity=true")
			return
		}
		if _, ok := payloadObj["data"]; !ok {
			t.Error("expected payload.data to be present")
		}
		meta, ok := payloadObj["meta"].(map[string]any)
		if !ok {
			t.Error("expected payload.meta to be present (EntityMetadata)")
			return
		}
		if meta["id"] != entity.Meta.ID {
			t.Errorf("expected meta.id=%s, got %v", entity.Meta.ID, meta["id"])
		}
		if _, ok := meta["state"]; !ok {
			t.Error("expected meta.state to be present")
		}

		// Verify payload matches the generated typed struct schema.
		var typedReq events.EntityProcessorCalculationRequestJson
		if err := json.Unmarshal(payload, &typedReq); err != nil {
			t.Errorf("sent processor request doesn't match schema: %v", err)
			return
		}
		if typedReq.ProcessorName != "my-proc" {
			t.Errorf("expected processorName my-proc, got %s", typedReq.ProcessorName)
		}

		// Verify auth context extension attributes on the CloudEvent.
		if ce.Attributes == nil {
			t.Error("expected CloudEvent attributes (auth context)")
			return
		}
		authType, ok := ce.Attributes["authtype"]
		if !ok {
			t.Error("expected authtype attribute")
			return
		}
		if authType.GetCeString() != "user" {
			t.Errorf("expected authtype=user, got %s", authType.GetCeString())
		}
		authId, ok := ce.Attributes["authid"]
		if !ok {
			t.Error("expected authid attribute")
			return
		}
		if authId.GetCeString() != "user-1" {
			t.Errorf("expected authid=user-1, got %s", authId.GetCeString())
		}

		member := registry.Get(memberID)
		member.CompleteRequest(reqID, &ProcessingResponse{
			Payload: json.RawMessage(`{"data":{"foo":"updated"}}`),
			Success: true,
		})
	}()

	result, err := dispatcher.DispatchProcessor(ctx, entity, processor, "wf1", "t1", "tx-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var data map[string]any
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatalf("failed to unmarshal result data: %v", err)
	}
	if data["foo"] != "updated" {
		t.Errorf("expected foo=updated, got %v", data["foo"])
	}
	if result.Meta.ID != entity.Meta.ID {
		t.Error("meta should be preserved")
	}
}

func TestDispatchProcessor_NoMember(t *testing.T) {
	registry := NewMemberRegistry()
	uuids := common.NewTestUUIDGenerator()
	dispatcher := NewProcessorDispatcher(registry, uuids)
	ctx := testContext()
	entity := testEntity()

	processor := common.ProcessorDefinition{
		Name: "my-proc",
		Config: common.ProcessorConfig{
			CalculationNodesTags: "java",
		},
	}

	_, err := dispatcher.DispatchProcessor(ctx, entity, processor, "wf1", "t1", "tx-1")
	if err == nil {
		t.Fatal("expected error for missing member")
	}
	if !errors.Is(err, ErrNoMatchingMember) {
		t.Errorf("expected ErrNoMatchingMember, got: %s", err)
	}
}

func TestDispatchProcessor_Timeout(t *testing.T) {
	dispatcher, _, _, _ := setupTestDispatcher(t)
	ctx := testContext()
	entity := testEntity()

	processor := common.ProcessorDefinition{
		Name: "my-proc",
		Config: common.ProcessorConfig{
			CalculationNodesTags: "python",
			ResponseTimeoutMs:    1, // 1ms timeout
		},
	}

	_, err := dispatcher.DispatchProcessor(ctx, entity, processor, "wf1", "t1", "tx-1")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if got := err.Error(); got != "processor dispatch timed out after 1ms" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestDispatchProcessor_NoAttachEntity(t *testing.T) {
	dispatcher, registry, memberID, sentCh := setupTestDispatcher(t)
	ctx := testContext()
	entity := testEntity()

	processor := common.ProcessorDefinition{
		Name: "my-proc",
		Config: common.ProcessorConfig{
			AttachEntity:         false,
			CalculationNodesTags: "python",
			ResponseTimeoutMs:    5000,
		},
	}

	go func() {
		ce := <-sentCh
		reqID, err := extractRequestID(ce)
		if err != nil {
			t.Errorf("extractRequestID: %v", err)
			return
		}

		// Verify payload is NOT attached.
		_, payload, _ := ParseCloudEvent(ce)
		var m map[string]any
		json.Unmarshal(payload, &m)
		if _, ok := m["payload"]; ok {
			t.Error("expected no payload when AttachEntity=false")
		}

		member := registry.Get(memberID)
		member.CompleteRequest(reqID, &ProcessingResponse{
			Success: true,
		})
	}()

	result, err := dispatcher.DispatchProcessor(ctx, entity, processor, "wf1", "t1", "tx-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With no payload in response, original entity is returned.
	if result != entity {
		t.Error("expected original entity when response has no payload")
	}
}

func TestDispatchCriteria_MatchesTrue(t *testing.T) {
	dispatcher, registry, memberID, sentCh := setupTestDispatcher(t)
	ctx := testContext()
	entity := testEntity()

	criterion := json.RawMessage(`{
		"name": "my-criteria",
		"config": {
			"calculationNodesTags": "python",
			"attachEntity": true,
			"responseTimeoutMs": 5000
		}
	}`)

	go func() {
		ce := <-sentCh
		if ce.Type != EntityCriteriaCalculationRequest {
			t.Errorf("expected event type %s, got %s", EntityCriteriaCalculationRequest, ce.Type)
		}
		reqID, err := extractRequestID(ce)
		if err != nil {
			t.Errorf("extractRequestID: %v", err)
			return
		}

		matchesTrue := true
		member := registry.Get(memberID)
		member.CompleteRequest(reqID, &ProcessingResponse{
			Success: true,
			Matches: &matchesTrue,
		})
	}()

	result, err := dispatcher.DispatchCriteria(ctx, entity, criterion, "transition", "wf1", "t1", "proc1", "tx-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected matches=true")
	}
}

func TestDispatchCriteria_MatchesFalse(t *testing.T) {
	dispatcher, registry, memberID, sentCh := setupTestDispatcher(t)
	ctx := testContext()
	entity := testEntity()

	criterion := json.RawMessage(`{
		"name": "my-criteria",
		"config": {
			"calculationNodesTags": "python",
			"responseTimeoutMs": 5000
		}
	}`)

	go func() {
		ce := <-sentCh
		reqID, err := extractRequestID(ce)
		if err != nil {
			t.Errorf("extractRequestID: %v", err)
			return
		}

		matchesFalse := false
		member := registry.Get(memberID)
		member.CompleteRequest(reqID, &ProcessingResponse{
			Success: true,
			Matches: &matchesFalse,
		})
	}()

	result, err := dispatcher.DispatchCriteria(ctx, entity, criterion, "transition", "wf1", "t1", "", "tx-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected matches=false")
	}
}
