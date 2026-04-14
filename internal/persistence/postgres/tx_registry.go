package postgres

import (
	"sync"

	"github.com/jackc/pgx/v5"
)

// txRegistry is a thread-safe in-process map from transaction ID to pgx.Tx.
// Used by TransactionManager to track active database transactions.
type txRegistry struct {
	mu   sync.RWMutex
	txns map[string]pgx.Tx
}

func newTxRegistry() *txRegistry {
	return &txRegistry{txns: make(map[string]pgx.Tx)}
}

// Register adds a transaction to the registry.
func (r *txRegistry) Register(txID string, tx pgx.Tx) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.txns[txID] = tx
}

// Lookup returns the transaction for the given ID, or false if not found.
func (r *txRegistry) Lookup(txID string) (pgx.Tx, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tx, ok := r.txns[txID]
	return tx, ok
}

// Remove deletes a transaction from the registry.
func (r *txRegistry) Remove(txID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.txns, txID)
}
