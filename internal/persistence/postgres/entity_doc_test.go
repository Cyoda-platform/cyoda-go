package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

var testTime = time.Date(2026, 3, 28, 10, 0, 0, 123456000, time.UTC)

func testEntity() *common.Entity {
	return &common.Entity{
		Meta: common.EntityMeta{
			ID:                      "ent-001",
			TenantID:                "tenant-abc",
			ModelRef:                common.ModelRef{EntityName: "Order", ModelVersion: "1"},
			State:                   "APPROVED",
			Version:                 5,
			CreationDate:            testTime,
			LastModifiedDate:        testTime,
			TransactionID:           "tx-456",
			ChangeType:              "UPDATED",
			ChangeUser:              "user-123",
			TransitionForLatestSave: "approve",
		},
		Data: []byte(`{"name":"Acme Corp","amount":1500,"status":"active"}`),
	}
}

func TestEntityDoc_MarshalRoundTrip(t *testing.T) {
	ent := testEntity()
	validTime := testTime
	txTime := testTime.Add(time.Second)
	wallClock := testTime.Add(2 * time.Second)

	raw, err := marshalEntityDoc(ent, validTime, txTime, wallClock, false)
	if err != nil {
		t.Fatalf("marshalEntityDoc: %v", err)
	}

	got, err := unmarshalEntityDoc(raw)
	if err != nil {
		t.Fatalf("unmarshalEntityDoc: %v", err)
	}

	if got.Meta.ID != ent.Meta.ID {
		t.Errorf("ID = %q, want %q", got.Meta.ID, ent.Meta.ID)
	}
	if got.Meta.TenantID != ent.Meta.TenantID {
		t.Errorf("TenantID = %q, want %q", got.Meta.TenantID, ent.Meta.TenantID)
	}
	if got.Meta.ModelRef != ent.Meta.ModelRef {
		t.Errorf("ModelRef = %+v, want %+v", got.Meta.ModelRef, ent.Meta.ModelRef)
	}
	if got.Meta.State != ent.Meta.State {
		t.Errorf("State = %q, want %q", got.Meta.State, ent.Meta.State)
	}
	if got.Meta.Version != ent.Meta.Version {
		t.Errorf("Version = %d, want %d", got.Meta.Version, ent.Meta.Version)
	}
	if !got.Meta.CreationDate.Equal(ent.Meta.CreationDate) {
		t.Errorf("CreationDate = %v, want %v", got.Meta.CreationDate, ent.Meta.CreationDate)
	}
	if !got.Meta.LastModifiedDate.Equal(ent.Meta.LastModifiedDate) {
		t.Errorf("LastModifiedDate = %v, want %v", got.Meta.LastModifiedDate, ent.Meta.LastModifiedDate)
	}
	if got.Meta.TransactionID != ent.Meta.TransactionID {
		t.Errorf("TransactionID = %q, want %q", got.Meta.TransactionID, ent.Meta.TransactionID)
	}
	if got.Meta.ChangeType != ent.Meta.ChangeType {
		t.Errorf("ChangeType = %q, want %q", got.Meta.ChangeType, ent.Meta.ChangeType)
	}
	if got.Meta.ChangeUser != ent.Meta.ChangeUser {
		t.Errorf("ChangeUser = %q, want %q", got.Meta.ChangeUser, ent.Meta.ChangeUser)
	}
	if got.Meta.TransitionForLatestSave != ent.Meta.TransitionForLatestSave {
		t.Errorf("TransitionForLatestSave = %q, want %q", got.Meta.TransitionForLatestSave, ent.Meta.TransitionForLatestSave)
	}
}

func TestEntityDoc_MarshalWithNilData(t *testing.T) {
	ent := testEntity()
	ent.Data = nil

	raw, err := marshalEntityDoc(ent, testTime, testTime, testTime, false)
	if err != nil {
		t.Fatalf("marshalEntityDoc: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}

	// Should have only _meta key
	if len(doc) != 1 {
		t.Errorf("expected 1 key (_meta), got %d keys: %v", len(doc), keys(doc))
	}
	if _, ok := doc["_meta"]; !ok {
		t.Error("missing _meta key")
	}

	// Round-trip: unmarshalled Data should be empty or represent empty object
	got, err := unmarshalEntityDoc(raw)
	if err != nil {
		t.Fatalf("unmarshalEntityDoc: %v", err)
	}
	if len(got.Data) != 0 {
		// If there are no domain keys, Data should be nil/empty or "{}"
		var m map[string]any
		if err := json.Unmarshal(got.Data, &m); err != nil {
			t.Fatalf("unmarshal data: %v", err)
		}
		if len(m) != 0 {
			t.Errorf("expected empty domain data, got %v", m)
		}
	}
}

func TestEntityDoc_MarshalPreservesDomainData(t *testing.T) {
	ent := testEntity()
	raw, err := marshalEntityDoc(ent, testTime, testTime, testTime, false)
	if err != nil {
		t.Fatalf("marshalEntityDoc: %v", err)
	}

	got, err := unmarshalEntityDoc(raw)
	if err != nil {
		t.Fatalf("unmarshalEntityDoc: %v", err)
	}

	// Parse both original and round-tripped domain data
	var original, roundTripped map[string]any
	if err := json.Unmarshal(ent.Data, &original); err != nil {
		t.Fatalf("unmarshal original data: %v", err)
	}
	if err := json.Unmarshal(got.Data, &roundTripped); err != nil {
		t.Fatalf("unmarshal round-tripped data: %v", err)
	}

	// Verify "amount" is a number, not a string
	if amt, ok := roundTripped["amount"].(float64); !ok || amt != 1500 {
		t.Errorf("amount = %v (%T), want 1500 (float64)", roundTripped["amount"], roundTripped["amount"])
	}
	if name, ok := roundTripped["name"].(string); !ok || name != "Acme Corp" {
		t.Errorf("name = %v, want %q", roundTripped["name"], "Acme Corp")
	}
	if status, ok := roundTripped["status"].(string); !ok || status != "active" {
		t.Errorf("status = %v, want %q", roundTripped["status"], "active")
	}

	// _meta must NOT appear in domain data
	if _, ok := roundTripped["_meta"]; ok {
		t.Error("_meta should not appear in unmarshalled entity.Data")
	}
}

func TestEntityDoc_MetaFieldsPresent(t *testing.T) {
	ent := testEntity()
	raw, err := marshalEntityDoc(ent, testTime, testTime, testTime, false)
	if err != nil {
		t.Fatalf("marshalEntityDoc: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}

	metaRaw, ok := doc["_meta"]
	if !ok {
		t.Fatal("missing _meta key in document")
	}

	var meta map[string]any
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}

	requiredKeys := []string{
		"id", "tenant_id", "model_name", "model_version",
		"version", "state", "valid_time", "transaction_time",
		"wall_clock_time", "creation_date", "last_modified_date",
		"change_type", "change_user", "transaction_id", "transition", "deleted",
	}
	for _, k := range requiredKeys {
		if _, ok := meta[k]; !ok {
			t.Errorf("missing _meta key: %q", k)
		}
	}
}

func TestEntityDoc_DeletedFlag(t *testing.T) {
	ent := testEntity()
	raw, err := marshalEntityDoc(ent, testTime, testTime, testTime, true)
	if err != nil {
		t.Fatalf("marshalEntityDoc: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}

	var meta entityMeta
	if err := json.Unmarshal(doc["_meta"], &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if !meta.Deleted {
		t.Error("expected deleted=true in _meta")
	}
}

func TestEntityDoc_UnmarshalEntityVersion(t *testing.T) {
	ent := testEntity()
	validTime := testTime
	txTime := testTime.Add(time.Second)
	wallClock := testTime.Add(2 * time.Second)

	raw, err := marshalEntityDoc(ent, validTime, txTime, wallClock, false)
	if err != nil {
		t.Fatalf("marshalEntityDoc: %v", err)
	}

	ver, err := unmarshalEntityVersion(raw, 5, validTime)
	if err != nil {
		t.Fatalf("unmarshalEntityVersion: %v", err)
	}

	if ver.Version != 5 {
		t.Errorf("Version = %d, want 5", ver.Version)
	}
	if !ver.Timestamp.Equal(validTime) {
		t.Errorf("Timestamp = %v, want %v", ver.Timestamp, validTime)
	}
	if ver.Entity == nil {
		t.Fatal("Entity is nil")
	}
	if ver.Entity.Meta.ID != ent.Meta.ID {
		t.Errorf("Entity.Meta.ID = %q, want %q", ver.Entity.Meta.ID, ent.Meta.ID)
	}
	if ver.ChangeType != ent.Meta.ChangeType {
		t.Errorf("ChangeType = %q, want %q", ver.ChangeType, ent.Meta.ChangeType)
	}
	if ver.User != ent.Meta.ChangeUser {
		t.Errorf("User = %q, want %q", ver.User, ent.Meta.ChangeUser)
	}
	if ver.Deleted {
		t.Error("expected deleted=false")
	}
}

func keys(m map[string]json.RawMessage) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
