package postgres

import (
	"context"
	"fmt"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/spi"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StoreFactory implements spi.StoreFactory backed by PostgreSQL.
type StoreFactory struct {
	pool *pgxpool.Pool
	tm   *TransactionManager // may be nil if transactions not configured
}

// NewStoreFactory creates a new PostgreSQL-backed StoreFactory.
func NewStoreFactory(pool *pgxpool.Pool) *StoreFactory {
	return &StoreFactory{pool: pool}
}

// SetTransactionManager associates a TransactionManager with this factory.
// When set, resolveQuerier returns the active pgx.Tx for transactional operations.
func (f *StoreFactory) SetTransactionManager(tm *TransactionManager) {
	f.tm = tm
}

// Pool returns the underlying connection pool.
func (f *StoreFactory) Pool() *pgxpool.Pool {
	return f.pool
}

func resolveTenant(ctx context.Context) (common.TenantID, error) {
	uc := common.GetUserContext(ctx)
	if uc == nil {
		return "", fmt.Errorf("no user context in request — tenant cannot be resolved")
	}
	if uc.Tenant.ID == "" {
		return "", fmt.Errorf("user context has no tenant")
	}
	return uc.Tenant.ID, nil
}

// resolveQuerier returns the active pgx.Tx from context if a transaction is in
// progress, otherwise the pool.
func (f *StoreFactory) resolveQuerier(ctx context.Context) Querier {
	if f.tm != nil {
		if tx := common.GetTransaction(ctx); tx != nil {
			if pgxTx, ok := f.tm.LookupTx(tx.ID); ok {
				return pgxTx
			}
		}
	}
	return f.pool
}

func (f *StoreFactory) EntityStore(ctx context.Context) (spi.EntityStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &entityStore{q: f.resolveQuerier(ctx), tenantID: tid}, nil
}

func (f *StoreFactory) ModelStore(ctx context.Context) (spi.ModelStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &modelStore{q: f.resolveQuerier(ctx), tenantID: tid}, nil
}

func (f *StoreFactory) KeyValueStore(ctx context.Context) (spi.KeyValueStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &kvStore{q: f.resolveQuerier(ctx), tenantID: tid}, nil
}

func (f *StoreFactory) MessageStore(ctx context.Context) (spi.MessageStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &messageStore{q: f.resolveQuerier(ctx), tenantID: tid}, nil
}

func (f *StoreFactory) WorkflowStore(ctx context.Context) (spi.WorkflowStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	kv := &kvStore{q: f.resolveQuerier(ctx), tenantID: tid}
	return &workflowStore{kv: kv}, nil
}

func (f *StoreFactory) StateMachineAuditStore(ctx context.Context) (spi.StateMachineAuditStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &smAuditStore{q: f.resolveQuerier(ctx), tenantID: tid}, nil
}

func (f *StoreFactory) AsyncSearchStore(_ context.Context) (spi.AsyncSearchStore, error) {
	// AsyncSearchStore is a long-lived singleton — tenant is resolved per method call,
	// not at construction. This allows app.go to obtain the store at startup with
	// context.Background() (no tenant). ReapExpired also runs without tenant context.
	return &asyncSearchStore{pool: f.pool}, nil
}

func (f *StoreFactory) Close() error {
	f.pool.Close()
	return nil
}
