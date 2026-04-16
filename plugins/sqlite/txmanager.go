package sqlite

import (
	"context"
	"fmt"
	"sync"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

const submitTimeTTL = 1 * time.Hour

// committedTx records a committed transaction in the in-memory log.
type committedTx struct {
	id         string
	submitTime time.Time
	writeSet   map[string]bool
}

// savepointSnapshot holds a deep copy of transaction state at savepoint time.
type savepointSnapshot struct {
	buffer   map[string]*spi.Entity
	readSet  map[string]bool
	writeSet map[string]bool
	deletes  map[string]bool
}

// transactionManager implements spi.TransactionManager with application-layer
// Serializable Snapshot Isolation. In-memory committedLog tracks conflicts;
// SQLite is the persistence layer.
//
// Full implementation will be added in a later task. This stub provides the
// type so store_factory.go compiles.
type transactionManager struct {
	factory        *StoreFactory
	uuids          spi.UUIDGenerator
	commitMu       sync.Mutex
	mu             sync.Mutex
	active         map[string]*spi.TransactionState
	committedLog   []committedTx
	committing     map[string]bool
	submitTimes    map[string]time.Time
	savepoints     map[string]map[string]savepointSnapshot
	lastSubmitTime int64
}

func newTransactionManager(factory *StoreFactory, uuids spi.UUIDGenerator) *transactionManager {
	tm := &transactionManager{
		factory:     factory,
		uuids:       uuids,
		active:      make(map[string]*spi.TransactionState),
		committing:  make(map[string]bool),
		submitTimes: make(map[string]time.Time),
		savepoints:  make(map[string]map[string]savepointSnapshot),
	}
	tm.seedLastSubmitTime()
	return tm
}

func (m *transactionManager) seedLastSubmitTime() {
	var maxTime int64
	err := m.factory.db.QueryRow("SELECT COALESCE(MAX(submit_time), 0) FROM entity_versions").Scan(&maxTime)
	if err != nil {
		// Table may not exist yet (pre-migration). Safe to start at 0.
		return
	}
	m.lastSubmitTime = maxTime
}

func (m *transactionManager) Begin(ctx context.Context) (string, context.Context, error) {
	return "", nil, fmt.Errorf("sqlite tx manager: not yet implemented")
}

func (m *transactionManager) Commit(_ context.Context, _ string) error {
	return fmt.Errorf("sqlite tx manager: not yet implemented")
}

func (m *transactionManager) Rollback(_ context.Context, _ string) error {
	return fmt.Errorf("sqlite tx manager: not yet implemented")
}

func (m *transactionManager) Join(_ context.Context, _ string) (context.Context, error) {
	return nil, fmt.Errorf("sqlite tx manager: not yet implemented")
}

func (m *transactionManager) GetSubmitTime(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, fmt.Errorf("sqlite tx manager: not yet implemented")
}

func (m *transactionManager) Savepoint(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("sqlite tx manager: not yet implemented")
}

func (m *transactionManager) RollbackToSavepoint(_ context.Context, _, _ string) error {
	return fmt.Errorf("sqlite tx manager: not yet implemented")
}

func (m *transactionManager) ReleaseSavepoint(_ context.Context, _, _ string) error {
	return fmt.Errorf("sqlite tx manager: not yet implemented")
}
