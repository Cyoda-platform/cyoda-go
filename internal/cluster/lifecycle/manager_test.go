package lifecycle_test

import (
	"context"
	"sync"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/lifecycle"
)

// fakeTM records Rollback calls for reaper-integration verification.
type fakeTM struct {
	mu       sync.Mutex
	rolled   []string
	rollback func(txID string) error
}

func (f *fakeTM) Begin(context.Context) (string, context.Context, error) {
	return "", nil, nil
}
func (f *fakeTM) Commit(context.Context, string) error { return nil }
func (f *fakeTM) Rollback(_ context.Context, txID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rolled = append(f.rolled, txID)
	if f.rollback != nil {
		return f.rollback(txID)
	}
	return nil
}
func (f *fakeTM) Join(context.Context, string) (context.Context, error) {
	return nil, nil
}
func (f *fakeTM) GetSubmitTime(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}
func (f *fakeTM) Savepoint(context.Context, string) (string, error)         { return "", nil }
func (f *fakeTM) RollbackToSavepoint(context.Context, string, string) error { return nil }
func (f *fakeTM) ReleaseSavepoint(context.Context, string, string) error    { return nil }

var _ spi.TransactionManager = (*fakeTM)(nil)

func TestReapExpired_CallsTMRollback(t *testing.T) {
	tm := &fakeTM{}
	m := lifecycle.NewManager(5 * time.Minute)
	m.SetTransactionManager(tm)

	m.Register(context.Background(), "tx-1", "node-a", 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	reaped, err := m.ReapExpired(context.Background())
	if err != nil {
		t.Fatalf("ReapExpired: %v", err)
	}
	if reaped != 1 {
		t.Fatalf("reaped = %d, want 1", reaped)
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if len(tm.rolled) != 1 || tm.rolled[0] != "tx-1" {
		t.Errorf("TM.Rollback calls = %v, want [tx-1]", tm.rolled)
	}
}

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
