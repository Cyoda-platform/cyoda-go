package common

import (
	"context"
	"sync"
	"time"
)

// TransactionState holds the state of an active SSI transaction.
// All processor execution is sequential (no goroutines) — see
// docs/superpowers/specs/2026-04-01-workflow-processor-execution-design.md.
// SAVEPOINTs snapshot/restore these maps for ASYNC_NEW_TX rollback isolation.
type TransactionState struct {
	ID           string
	TenantID     TenantID
	SnapshotTime time.Time
	ReadSet      map[string]bool    // entity IDs read
	WriteSet     map[string]bool    // entity IDs written
	Buffer       map[string]*Entity // staged writes
	Deletes      map[string]bool    // staged deletes
	RolledBack   bool               // set on rollback to prevent further operations
	OpMu         sync.RWMutex       // read lock for operations, write lock for commit/rollback
	Closed       bool               // set after commit or rollback completes
}

const txContextKey contextKey = "transaction"

// WithTransaction returns a new context carrying the given transaction state.
func WithTransaction(ctx context.Context, tx *TransactionState) context.Context {
	return context.WithValue(ctx, txContextKey, tx)
}

// GetTransaction returns the transaction state from the context, or nil if none.
func GetTransaction(ctx context.Context) *TransactionState {
	tx, _ := ctx.Value(txContextKey).(*TransactionState)
	return tx
}
