package memory_test

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/plugins/memory"
)

func TestSMAuditRecord(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := ctxWithTenant("tenant-A")
	store, err := factory.StateMachineAuditStore(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := []spi.StateMachineEvent{
		{EventType: spi.SMEventStarted, EntityID: "e-1", TimeUUID: "t1", Details: "started", TransactionID: "tx-1"},
		{EventType: spi.SMEventTransitionMade, EntityID: "e-1", TimeUUID: "t2", State: "APPROVED", Details: "transition", TransactionID: "tx-1"},
		{EventType: spi.SMEventFinished, EntityID: "e-1", TimeUUID: "t3", Details: "done", TransactionID: "tx-1"},
	}
	for _, ev := range events {
		if err := store.Record(ctx, "e-1", ev); err != nil {
			t.Fatalf("record failed: %v", err)
		}
	}

	got, err := store.GetEvents(ctx, "e-1")
	if err != nil {
		t.Fatalf("get events failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	if got[0].EventType != spi.SMEventStarted {
		t.Errorf("expected first event START, got %s", got[0].EventType)
	}
	if got[1].State != "APPROVED" {
		t.Errorf("expected state APPROVED, got %s", got[1].State)
	}

	// Verify deep copy — mutating returned value must not affect stored data.
	got[0].Details = "mutated"
	got2, _ := store.GetEvents(ctx, "e-1")
	if got2[0].Details == "mutated" {
		t.Error("store returned a shallow copy — mutation leaked")
	}
}

func TestSMAuditFilterByTransaction(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := ctxWithTenant("tenant-A")
	store, _ := factory.StateMachineAuditStore(ctx)

	_ = store.Record(ctx, "e-1", spi.StateMachineEvent{EventType: spi.SMEventStarted, TransactionID: "tx-1", Details: "a"})
	_ = store.Record(ctx, "e-1", spi.StateMachineEvent{EventType: spi.SMEventTransitionMade, TransactionID: "tx-2", Details: "b"})
	_ = store.Record(ctx, "e-1", spi.StateMachineEvent{EventType: spi.SMEventFinished, TransactionID: "tx-1", Details: "c"})

	got, err := store.GetEventsByTransaction(ctx, "e-1", "tx-1")
	if err != nil {
		t.Fatalf("filter failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events for tx-1, got %d", len(got))
	}
	if got[0].Details != "a" || got[1].Details != "c" {
		t.Errorf("unexpected filtered events: %+v", got)
	}

	got2, err := store.GetEventsByTransaction(ctx, "e-1", "tx-2")
	if err != nil {
		t.Fatalf("filter failed: %v", err)
	}
	if len(got2) != 1 {
		t.Fatalf("expected 1 event for tx-2, got %d", len(got2))
	}
}

func TestSMAuditTenantIsolation(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctxA := ctxWithTenant("tenant-A")
	ctxB := ctxWithTenant("tenant-B")
	storeA, _ := factory.StateMachineAuditStore(ctxA)
	storeB, _ := factory.StateMachineAuditStore(ctxB)

	_ = storeA.Record(ctxA, "e-1", spi.StateMachineEvent{EventType: spi.SMEventStarted, Details: "tenant-A event"})

	// SPI contract: tenant B sees empty slice, not an error.
	got, err := storeB.GetEvents(ctxB, "e-1")
	if err != nil {
		t.Fatalf("expected nil error for tenant isolation, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice for tenant B, got %d events", len(got))
	}
}
