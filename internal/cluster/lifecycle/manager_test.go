package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/cluster/lifecycle"
)

func TestManager_RegisterAndIsAlive(t *testing.T) {
	m := lifecycle.NewManager(5 * time.Minute)
	ctx := context.Background()
	m.Register(ctx, "tx-1", "node-1", 30*time.Second)
	alive, nodeID, err := m.IsAlive(ctx, "tx-1")
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if !alive {
		t.Error("expected alive=true")
	}
	if nodeID != "node-1" {
		t.Errorf("nodeID = %q, want %q", nodeID, "node-1")
	}
}

func TestManager_UnknownTxNotAlive(t *testing.T) {
	m := lifecycle.NewManager(5 * time.Minute)
	ctx := context.Background()
	alive, _, err := m.IsAlive(ctx, "tx-unknown")
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if alive {
		t.Error("expected alive=false for unknown tx")
	}
}

func TestManager_ExpiredTxNotAlive(t *testing.T) {
	m := lifecycle.NewManager(5 * time.Minute)
	ctx := context.Background()
	m.Register(ctx, "tx-1", "node-1", 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	alive, _, err := m.IsAlive(ctx, "tx-1")
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if alive {
		t.Error("expected alive=false for expired tx")
	}
}

func TestManager_RecordOutcome(t *testing.T) {
	m := lifecycle.NewManager(5 * time.Minute)
	ctx := context.Background()
	m.Register(ctx, "tx-1", "node-1", 30*time.Second)
	m.RecordOutcome(ctx, "tx-1", lifecycle.OutcomeCommitted)
	outcome, found := m.GetOutcome(ctx, "tx-1")
	if !found {
		t.Fatal("expected to find outcome")
	}
	if outcome != lifecycle.OutcomeCommitted {
		t.Errorf("outcome = %v, want Committed", outcome)
	}
}

func TestManager_ReapExpired(t *testing.T) {
	m := lifecycle.NewManager(5 * time.Minute)
	ctx := context.Background()
	m.Register(ctx, "tx-1", "node-1", 1*time.Millisecond)
	m.Register(ctx, "tx-2", "node-1", 30*time.Second)
	time.Sleep(10 * time.Millisecond)
	reaped, err := m.ReapExpired(ctx)
	if err != nil {
		t.Fatalf("ReapExpired: %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped = %d, want 1", reaped)
	}
	alive, _, _ := m.IsAlive(ctx, "tx-2")
	if !alive {
		t.Error("tx-2 should still be alive")
	}
}

func TestManager_ListByNode(t *testing.T) {
	m := lifecycle.NewManager(5 * time.Minute)
	ctx := context.Background()
	m.Register(ctx, "tx-1", "node-1", 30*time.Second)
	m.Register(ctx, "tx-2", "node-1", 30*time.Second)
	m.Register(ctx, "tx-3", "node-2", 30*time.Second)
	txns := m.ListByNode(ctx, "node-1")
	if len(txns) != 2 {
		t.Errorf("len = %d, want 2", len(txns))
	}
}
