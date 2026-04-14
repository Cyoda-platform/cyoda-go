package grpc

import (
	"encoding/json"
	"testing"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
)

func TestNewCloudEvent_ParseCloudEvent_RoundTrip(t *testing.T) {
	type testPayload struct {
		TransactionID string `json:"transactionId"`
		Name          string `json:"name"`
	}

	input := testPayload{TransactionID: "txn-123", Name: "alice"}

	ce, err := NewCloudEvent(EntityCreateRequest, input)
	if err != nil {
		t.Fatalf("NewCloudEvent returned error: %v", err)
	}

	if ce.Id == "" {
		t.Error("expected non-empty ID")
	}
	if ce.Source != "cyoda-go" {
		t.Errorf("expected source 'cyoda-go', got %q", ce.Source)
	}
	if ce.SpecVersion != "1.0" {
		t.Errorf("expected spec_version '1.0', got %q", ce.SpecVersion)
	}
	if ce.Type != EntityCreateRequest {
		t.Errorf("expected type %q, got %q", EntityCreateRequest, ce.Type)
	}

	eventType, payload, err := ParseCloudEvent(ce)
	if err != nil {
		t.Fatalf("ParseCloudEvent returned error: %v", err)
	}
	if eventType != EntityCreateRequest {
		t.Errorf("expected event type %q, got %q", EntityCreateRequest, eventType)
	}

	var result testPayload
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if result.TransactionID != "txn-123" {
		t.Errorf("expected transactionId 'txn-123', got %q", result.TransactionID)
	}
	if result.Name != "alice" {
		t.Errorf("expected name 'alice', got %q", result.Name)
	}
}

func TestExtractTransactionID_Present(t *testing.T) {
	payload := json.RawMessage(`{"transactionId":"txn-456","other":"value"}`)
	got := ExtractTransactionID(payload)
	if got != "txn-456" {
		t.Errorf("expected 'txn-456', got %q", got)
	}
}

func TestExtractTransactionID_Absent(t *testing.T) {
	payload := json.RawMessage(`{"other":"value"}`)
	got := ExtractTransactionID(payload)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestParseCloudEvent_Nil(t *testing.T) {
	_, _, err := ParseCloudEvent(nil)
	if err == nil {
		t.Fatal("expected error for nil CloudEvent")
	}
}

func TestParseCloudEvent_BinaryData(t *testing.T) {
	ce := &cepb.CloudEvent{
		Id:          "test-id",
		Source:      "test",
		SpecVersion: "1.0",
		Type:        "test.type",
		Data:        &cepb.CloudEvent_BinaryData{BinaryData: []byte(`{"key":"value"}`)},
	}

	eventType, payload, err := ParseCloudEvent(ce)
	if err != nil {
		t.Fatalf("unexpected error for binary_data: %v", err)
	}
	if eventType != "test.type" {
		t.Errorf("eventType = %q, want %q", eventType, "test.type")
	}
	if string(payload) != `{"key":"value"}` {
		t.Errorf("payload = %q, want %q", string(payload), `{"key":"value"}`)
	}
}

func TestExtractStringField(t *testing.T) {
	payload := json.RawMessage(`{"foo":"bar","count":42}`)

	if got := ExtractStringField(payload, "foo"); got != "bar" {
		t.Errorf("expected 'bar', got %q", got)
	}
	if got := ExtractStringField(payload, "missing"); got != "" {
		t.Errorf("expected empty string for missing field, got %q", got)
	}
	if got := ExtractStringField(payload, "count"); got != "" {
		t.Errorf("expected empty string for non-string field, got %q", got)
	}
}
