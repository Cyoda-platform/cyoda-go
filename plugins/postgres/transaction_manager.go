package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

const submitTimeTTL = 1 * time.Hour

// TransactionManager implements spi.TransactionManager backed by PostgreSQL
// with SERIALIZABLE isolation. Each Begin() acquires a real pgx.Tx,
// registers it in the txRegistry, and allocates a *txState for read/write
// bookkeeping used by Commit.
type TransactionManager struct {
	pool     *pgxpool.Pool
	registry *txRegistry
	uuids    spi.UUIDGenerator
	mu       sync.Mutex
	// submitTimes records the database timestamp captured at commit time.
	// Evicted after submitTimeTTL.
	submitTimes map[string]time.Time
	// tenants records the tenant for each active transaction so Join can
	// reconstruct the TransactionState without requiring tenant in the
	// joining context.
	tenants    map[string]spi.TenantID
	txStatesMu sync.Mutex
	txStates   map[string]*txState
}

// NewTransactionManager creates a new PostgreSQL-backed TransactionManager.
func NewTransactionManager(pool *pgxpool.Pool, uuids spi.UUIDGenerator) *TransactionManager {
	return &TransactionManager{
		pool:        pool,
		registry:    newTxRegistry(),
		uuids:       uuids,
		submitTimes: make(map[string]time.Time),
		tenants:     make(map[string]spi.TenantID),
		txStates:    make(map[string]*txState),
	}
}

// Begin starts a new SERIALIZABLE transaction and returns the transaction ID
// and a context carrying the TransactionState.
func (tm *TransactionManager) Begin(ctx context.Context) (string, context.Context, error) {
	tenantID, err := resolveTenant(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("Begin: %w", err)
	}

	txID := uuid.UUID(tm.uuids.NewTimeUUID()).String()

	pgxTx, err := tm.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", nil, fmt.Errorf("Begin: failed to start transaction: %w", err)
	}

	// Set the current tenant for RLS policies. We use set_config(name, value, is_local)
	// rather than `SET LOCAL app.current_tenant = $1` because PostgreSQL's SET statement
	// does not accept bound parameters under pgx's extended-query protocol.
	if _, err := pgxTx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", string(tenantID)); err != nil {
		_ = pgxTx.Rollback(ctx)
		return "", nil, fmt.Errorf("Begin: failed to set tenant: %w", err)
	}

	tm.registry.Register(txID, pgxTx)

	tm.mu.Lock()
	tm.tenants[txID] = tenantID
	tm.mu.Unlock()

	tm.txStatesMu.Lock()
	tm.txStates[txID] = newTxState(tenantID)
	tm.txStatesMu.Unlock()

	txSpiState := &spi.TransactionState{
		ID:       txID,
		TenantID: tenantID,
	}

	return txID, spi.WithTransaction(ctx, txSpiState), nil
}

// Commit commits the transaction and records its submit time.
// Returns spi.ErrConflict on serialization failure (PostgreSQL error 40001).
func (tm *TransactionManager) Commit(ctx context.Context, txID string) error {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return fmt.Errorf("Commit: transaction %s not found", txID)
	}

	// Capture the database timestamp before committing.
	// If the transaction is already in an aborted state (e.g. an earlier Exec
	// returned 40001 and left the tx aborted), the SELECT will fail with
	// SQLSTATE 25P02 (in_failed_sql_transaction). In that case we rollback
	// and surface ErrConflict, since the abort was most likely caused by a
	// serialization failure. We use time.Now() as a stand-in; it is never
	// stored on an error path.
	var submitTime time.Time
	if tsErr := pgxTx.QueryRow(ctx, "SELECT CURRENT_TIMESTAMP").Scan(&submitTime); tsErr != nil {
		tm.registry.Remove(txID)
		tm.removeTenant(txID)
		tm.removeTxState(txID)
		// Only classify as ErrConflict when the probe fails specifically because
		// the transaction is already in an aborted state (SQLSTATE 25P02:
		// in_failed_sql_transaction). Any other error (context cancellation,
		// network failure, etc.) is returned as-is so callers are not misled
		// into treating a transient infrastructure error as a retryable conflict.
		var pgErr *pgconn.PgError
		if errors.As(tsErr, &pgErr) && pgErr.Code == pgerrcode.InFailedSQLTransaction {
			_ = pgxTx.Rollback(context.Background())
			return fmt.Errorf("%w: Commit: transaction aborted: %w", spi.ErrConflict, tsErr)
		}
		// For non-25P02 errors (e.g. network failures, context deadline exceeded)
		// roll back with a fresh context so we don't leak the connection, then
		// return the raw error without wrapping it as ErrConflict.
		_ = pgxTx.Rollback(context.Background())
		return fmt.Errorf("Commit: failed to capture submit time: %w", tsErr)
	}

	if err := pgxTx.Commit(ctx); err != nil {
		// On commit failure the transaction is already aborted server-side, but
		// the pgx.Tx still holds the connection. Rollback explicitly to release
		// it back to the pool; ignore the rollback error (tx is already invalid).
		_ = pgxTx.Rollback(ctx)
		tm.registry.Remove(txID)
		tm.removeTenant(txID)
		tm.removeTxState(txID)
		return classifyError(fmt.Errorf("Commit: %w", err))
	}

	tm.registry.Remove(txID)
	tm.removeTenant(txID)
	tm.removeTxState(txID)

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
	tm.removeTxState(txID)

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

	txState := &spi.TransactionState{
		ID:       txID,
		TenantID: tenantID,
	}
	return spi.WithTransaction(ctx, txState), nil
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
	defer tm.mu.Unlock()
	delete(tm.tenants, txID)
}

// removeTxState removes the txState entry for a completed transaction.
func (tm *TransactionManager) removeTxState(txID string) {
	tm.txStatesMu.Lock()
	defer tm.txStatesMu.Unlock()
	delete(tm.txStates, txID)
}

// lookupTxState returns the txState for the given txID.
func (tm *TransactionManager) lookupTxState(txID string) (*txState, bool) {
	tm.txStatesMu.Lock()
	defer tm.txStatesMu.Unlock()
	s, ok := tm.txStates[txID]
	return s, ok
}

// Savepoint creates a named savepoint within the given PostgreSQL transaction.
func (tm *TransactionManager) Savepoint(ctx context.Context, txID string) (string, error) {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return "", fmt.Errorf("Savepoint: transaction %s not found", txID)
	}
	spID := uuid.UUID(tm.uuids.NewTimeUUID()).String()
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

// classifyError maps PostgreSQL errors that mean "this transaction was fully
// rolled back by the database before any external work stuck — a retry on a
// fresh snapshot is safe" to spi.ErrConflict. Everything else passes through.
//
// Retryable codes:
//   - serialization_failure (40001) — SSI detected an r/w dependency cycle
//   - deadlock_detected (40P01) — deadlock victim chosen by the server
//
// Both sentinels stay reachable: spi.ErrConflict satisfies handler-level
// errors.Is checks, and the original *pgconn.PgError stays in the chain so
// observability and logging can type-assert via errors.As.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == pgerrcode.SerializationFailure || pgErr.Code == pgerrcode.DeadlockDetected) {
		return fmt.Errorf("%w: %w", spi.ErrConflict, err)
	}
	return err
}
