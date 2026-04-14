package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const submitTimeTTL = 1 * time.Hour

// TransactionManager implements spi.TransactionManager backed by PostgreSQL
// with SERIALIZABLE isolation. Each Begin() acquires a real pgx.Tx and
// registers it in the txRegistry so that stores can look it up by ID.
type TransactionManager struct {
	pool     *pgxpool.Pool
	registry *txRegistry
	uuids    common.UUIDGenerator
	mu       sync.Mutex
	// submitTimes records the database timestamp captured at commit time.
	// Evicted after submitTimeTTL.
	submitTimes map[string]time.Time
	// tenants records the tenant for each active transaction so Join can
	// reconstruct the TransactionState without requiring tenant in the
	// joining context.
	tenants map[string]common.TenantID
}

// NewTransactionManager creates a new PostgreSQL-backed TransactionManager.
func NewTransactionManager(pool *pgxpool.Pool, uuids common.UUIDGenerator) *TransactionManager {
	return &TransactionManager{
		pool:        pool,
		registry:    newTxRegistry(),
		uuids:       uuids,
		submitTimes: make(map[string]time.Time),
		tenants:     make(map[string]common.TenantID),
	}
}

// Begin starts a new SERIALIZABLE transaction and returns the transaction ID
// and a context carrying the TransactionState.
func (tm *TransactionManager) Begin(ctx context.Context) (string, context.Context, error) {
	tenantID, err := resolveTenant(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("Begin: %w", err)
	}

	txID := tm.uuids.NewTimeUUID().String()

	pgxTx, err := tm.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", nil, fmt.Errorf("Begin: failed to start transaction: %w", err)
	}

	// Set the current tenant for RLS policies.
	if _, err := pgxTx.Exec(ctx, "SET LOCAL app.current_tenant = $1", string(tenantID)); err != nil {
		_ = pgxTx.Rollback(ctx)
		return "", nil, fmt.Errorf("Begin: failed to set tenant: %w", err)
	}

	tm.registry.Register(txID, pgxTx)

	tm.mu.Lock()
	tm.tenants[txID] = tenantID
	tm.mu.Unlock()

	txState := &common.TransactionState{
		ID:       txID,
		TenantID: tenantID,
	}

	return txID, common.WithTransaction(ctx, txState), nil
}

// Commit commits the transaction and records its submit time.
// Returns common.ErrConflict on serialization failure (PostgreSQL error 40001).
func (tm *TransactionManager) Commit(ctx context.Context, txID string) error {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return fmt.Errorf("Commit: transaction %s not found", txID)
	}

	// Capture the database timestamp before committing.
	var submitTime time.Time
	if err := pgxTx.QueryRow(ctx, "SELECT CURRENT_TIMESTAMP").Scan(&submitTime); err != nil {
		// If we can't get the time, the tx may already be in a failed state.
		// Try to rollback and return the error.
		_ = pgxTx.Rollback(ctx)
		tm.registry.Remove(txID)
		tm.removeTenant(txID)
		return classifyError(fmt.Errorf("Commit: failed to get submit time: %w", err))
	}

	if err := pgxTx.Commit(ctx); err != nil {
		tm.registry.Remove(txID)
		tm.removeTenant(txID)
		return classifyError(fmt.Errorf("Commit: %w", err))
	}

	tm.registry.Remove(txID)
	tm.removeTenant(txID)

	tm.mu.Lock()
	tm.submitTimes[txID] = submitTime
	evictBefore := time.Now().Add(-submitTimeTTL)
	for id, t := range tm.submitTimes {
		if t.Before(evictBefore) {
			delete(tm.submitTimes, id)
		}
	}
	tm.mu.Unlock()

	return nil
}

// Rollback aborts the transaction.
func (tm *TransactionManager) Rollback(ctx context.Context, txID string) error {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return fmt.Errorf("Rollback: transaction %s not found", txID)
	}

	err := pgxTx.Rollback(ctx)
	tm.registry.Remove(txID)
	tm.removeTenant(txID)

	if err != nil {
		return fmt.Errorf("Rollback: %w", err)
	}
	return nil
}

// Join attaches to an existing transaction, returning a context carrying its
// TransactionState.
func (tm *TransactionManager) Join(ctx context.Context, txID string) (context.Context, error) {
	_, ok := tm.registry.Lookup(txID)
	if !ok {
		return nil, fmt.Errorf("Join: transaction %s not found", txID)
	}

	tm.mu.Lock()
	tenantID := tm.tenants[txID]
	tm.mu.Unlock()

	txState := &common.TransactionState{
		ID:       txID,
		TenantID: tenantID,
	}
	return common.WithTransaction(ctx, txState), nil
}

// GetSubmitTime returns the database timestamp recorded when the transaction
// was committed.
func (tm *TransactionManager) GetSubmitTime(_ context.Context, txID string) (time.Time, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	t, ok := tm.submitTimes[txID]
	if !ok {
		return time.Time{}, fmt.Errorf("GetSubmitTime: transaction %s has no submit time (not yet committed or unknown)", txID)
	}
	return t, nil
}

// LookupTx exposes the registry lookup for use in tests and by the store
// layer (resolveQuerier). Production code should prefer resolveQuerier.
func (tm *TransactionManager) LookupTx(txID string) (pgx.Tx, bool) {
	return tm.registry.Lookup(txID)
}

// removeTenant cleans up the tenant mapping for a completed transaction.
func (tm *TransactionManager) removeTenant(txID string) {
	tm.mu.Lock()
	delete(tm.tenants, txID)
	tm.mu.Unlock()
}

// Savepoint creates a named savepoint within the given PostgreSQL transaction.
func (tm *TransactionManager) Savepoint(ctx context.Context, txID string) (string, error) {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return "", fmt.Errorf("Savepoint: transaction %s not found", txID)
	}
	spID := tm.uuids.NewTimeUUID().String()
	spName := "sp_" + spID
	if _, err := pgxTx.Exec(ctx, "SAVEPOINT "+pgx.Identifier{spName}.Sanitize()); err != nil {
		return "", fmt.Errorf("Savepoint: %w", err)
	}
	return spID, nil
}

// RollbackToSavepoint rolls back all work done since the named savepoint.
func (tm *TransactionManager) RollbackToSavepoint(ctx context.Context, txID string, savepointID string) error {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return fmt.Errorf("RollbackToSavepoint: transaction %s not found", txID)
	}
	spName := "sp_" + savepointID
	if _, err := pgxTx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+pgx.Identifier{spName}.Sanitize()); err != nil {
		return fmt.Errorf("RollbackToSavepoint: %w", err)
	}
	return nil
}

// ReleaseSavepoint releases a savepoint, merging its work into the parent transaction.
func (tm *TransactionManager) ReleaseSavepoint(ctx context.Context, txID string, savepointID string) error {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return fmt.Errorf("ReleaseSavepoint: transaction %s not found", txID)
	}
	spName := "sp_" + savepointID
	if _, err := pgxTx.Exec(ctx, "RELEASE SAVEPOINT "+pgx.Identifier{spName}.Sanitize()); err != nil {
		return fmt.Errorf("ReleaseSavepoint: %w", err)
	}
	return nil
}

// classifyError checks whether an error is a PostgreSQL serialization failure
// (error code 40001) and returns common.ErrConflict if so.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40001" {
		return common.ErrConflict
	}
	return err
}
