package memory_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

func TestJoinActiveTransaction(t *testing.T) {
	_, tm := newTxManager(t)
	ctx := ctxWithTenant("tenant-A")

	txID, txCtx, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	// Join from a second context.
	joinCtx, err := tm.Join(ctx, txID)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	// Both contexts should carry the same TransactionState.
	txOrig := common.GetTransaction(txCtx)
	txJoined := common.GetTransaction(joinCtx)
	if txOrig != txJoined {
		t.Fatal("expected Join to return the same TransactionState pointer")
	}

	// Entity saved via joined context should be visible in the transaction buffer.
	txJoined.Buffer["e-join"] = &common.Entity{
		Meta: common.EntityMeta{
			ID:         "e-join",
			TenantID:   "tenant-A",
			ChangeType: "CREATED",
		},
		Data: []byte(`{"joined":true}`),
	}
	txJoined.WriteSet["e-join"] = true

	if _, ok := txOrig.Buffer["e-join"]; !ok {
		t.Fatal("entity saved via joined context not visible in original tx buffer")
	}

	// Commit should succeed and flush the entity.
	if err := tm.Commit(ctx, txID); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

func TestJoinNonExistentTransaction(t *testing.T) {
	_, tm := newTxManager(t)
	ctx := ctxWithTenant("tenant-A")

	_, err := tm.Join(ctx, "nonexistent-tx-id")
	if err == nil {
		t.Fatal("expected error joining non-existent transaction")
	}
	if !strings.Contains(err.Error(), "transaction not found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestJoinRolledBackTransaction(t *testing.T) {
	_, tm := newTxManager(t)
	ctx := ctxWithTenant("tenant-A")

	txID, _, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	if err := tm.Rollback(ctx, txID); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	_, err = tm.Join(ctx, txID)
	if err == nil {
		t.Fatal("expected error joining rolled-back transaction")
	}
	if !strings.Contains(err.Error(), "closed") && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestJoinCommittedTransaction(t *testing.T) {
	_, tm := newTxManager(t)
	ctx := ctxWithTenant("tenant-A")

	txID, _, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	if err := tm.Commit(ctx, txID); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	_, err = tm.Join(ctx, txID)
	if err == nil {
		t.Fatal("expected error joining committed transaction")
	}
	if !strings.Contains(err.Error(), "closed") && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestJoinTenantMismatch(t *testing.T) {
	_, tm := newTxManager(t)
	ctxA := ctxWithTenant("tenant-A")
	ctxB := ctxWithTenant("tenant-B")

	txID, _, err := tm.Begin(ctxA)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	_, err = tm.Join(ctxB, txID)
	if err == nil {
		t.Fatal("expected error on tenant mismatch join")
	}
	if !strings.Contains(err.Error(), "tenant mismatch") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// Clean up.
	_ = tm.Rollback(ctxA, txID)
}

func TestJoinConcurrentOperationAndCommit(t *testing.T) {
	factory, tm := newTxManager(t)
	ctx := ctxWithTenant("tenant-A")

	txID, _, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	// Join from a goroutine that holds the read lock during a slow operation.
	joinCtx, err := tm.Join(ctx, txID)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	tx := common.GetTransaction(joinCtx)

	var wg sync.WaitGroup
	operationStarted := make(chan struct{})
	operationDone := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Acquire read lock to simulate an in-flight operation.
		tx.OpMu.RLock()
		close(operationStarted)
		// Hold the lock for 100ms to simulate work.
		time.Sleep(100 * time.Millisecond)
		// Write entity while holding the lock.
		tx.Buffer["e-concurrent"] = &common.Entity{
			Meta: common.EntityMeta{
				ID:         "e-concurrent",
				TenantID:   "tenant-A",
				ChangeType: "CREATED",
			},
			Data: []byte(`{"concurrent":true}`),
		}
		tx.WriteSet["e-concurrent"] = true
		tx.OpMu.RUnlock()
		close(operationDone)
	}()

	// Wait for the goroutine to start its operation.
	<-operationStarted

	// Commit from the main goroutine — should block until the operation completes.
	commitStart := time.Now()
	if err := tm.Commit(ctx, txID); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	commitDuration := time.Since(commitStart)

	// The commit should have waited for the operation (at least ~100ms).
	if commitDuration < 50*time.Millisecond {
		t.Errorf("commit returned too quickly (%v), expected to wait for in-flight operation", commitDuration)
	}

	// Verify the entity written by the goroutine was committed.
	store, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatalf("EntityStore failed: %v", err)
	}
	got, err := store.Get(ctx, "e-concurrent")
	if err != nil {
		t.Fatalf("Get after commit failed: %v", err)
	}
	if string(got.Data) != `{"concurrent":true}` {
		t.Errorf("unexpected data: %s", got.Data)
	}

	wg.Wait()
	<-operationDone
}
