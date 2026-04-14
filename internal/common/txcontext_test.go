package common

import (
	"context"
	"testing"
)

func TestWithTransactionAndGetTransaction(t *testing.T) {
	tx := &TransactionState{
		ID:       "tx-001",
		TenantID: TenantID("tenant-1"),
		ReadSet:  map[string]bool{"e1": true},
		WriteSet: map[string]bool{"e2": true},
		Buffer:   map[string]*Entity{"e2": {Meta: EntityMeta{ID: "e2"}}},
		Deletes:  map[string]bool{"e3": true},
	}

	ctx := WithTransaction(context.Background(), tx)
	got := GetTransaction(ctx)

	if got == nil {
		t.Fatal("expected transaction state, got nil")
	}
	if got.ID != "tx-001" {
		t.Errorf("expected ID tx-001, got %s", got.ID)
	}
	if got.TenantID != TenantID("tenant-1") {
		t.Errorf("expected TenantID tenant-1, got %s", got.TenantID)
	}
	if !got.ReadSet["e1"] {
		t.Error("expected e1 in ReadSet")
	}
	if !got.WriteSet["e2"] {
		t.Error("expected e2 in WriteSet")
	}
	if got.Buffer["e2"] == nil || got.Buffer["e2"].Meta.ID != "e2" {
		t.Error("expected e2 in Buffer")
	}
	if !got.Deletes["e3"] {
		t.Error("expected e3 in Deletes")
	}
}

func TestGetTransactionReturnsNilWhenAbsent(t *testing.T) {
	ctx := context.Background()
	got := GetTransaction(ctx)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}
