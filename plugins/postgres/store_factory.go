package postgres

import (
	"context"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"

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

// setTransactionManager wires the plugin's own TM into the factory. The
// field is written exactly once, at construction time, by initTransactionManager
// (same package) — so reads in resolveRaw are safe without synchronization
// because the construction return establishes happens-before for every
// subsequent caller. Keep this unexported: there is no legitimate external
// caller, and opening it would invite a race the factory isn't designed for.
func (f *StoreFactory) setTransactionManager(tm *TransactionManager) {
	f.tm = tm
}

// Pool returns the underlying connection pool.
func (f *StoreFactory) Pool() *pgxpool.Pool {
	return f.pool
}

func resolveTenant(ctx context.Context) (spi.TenantID, error) {
	uc := spi.GetUserContext(ctx)
	if uc == nil {
		return "", fmt.Errorf("no user context in request — tenant cannot be resolved")
	}
	if uc.Tenant.ID == "" {
		return "", fmt.Errorf("user context has no tenant")
	}
	return uc.Tenant.ID, nil
}

// resolveRaw returns the concrete pgx querier for the given context —
// the active pgx.Tx when one is in context, otherwise the pool. Stores
// never hold the result directly; they hold a ctxQuerier that re-resolves
// on every call, so the choice tracks the call-time context rather than
// the store-construction context.
func (f *StoreFactory) resolveRaw(ctx context.Context) Querier {
	if f.tm != nil {
		if tx := spi.GetTransaction(ctx); tx != nil {
			if pgxTx, ok := f.tm.LookupTx(tx.ID); ok {
				return pgxTx
			}
		}
	}
	return f.pool
}

// querier returns the per-call-resolving Querier used by all stores.
func (f *StoreFactory) querier() Querier {
	return &ctxQuerier{factory: f}
}

func (f *StoreFactory) EntityStore(ctx context.Context) (spi.EntityStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &entityStore{q: f.querier(), tenantID: tid}, nil
}

func (f *StoreFactory) ModelStore(ctx context.Context) (spi.ModelStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &modelStore{q: f.querier(), tenantID: tid}, nil
}

func (f *StoreFactory) KeyValueStore(ctx context.Context) (spi.KeyValueStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &kvStore{q: f.querier(), tenantID: tid}, nil
}

func (f *StoreFactory) MessageStore(ctx context.Context) (spi.MessageStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &messageStore{q: f.querier(), tenantID: tid}, nil
}

func (f *StoreFactory) WorkflowStore(ctx context.Context) (spi.WorkflowStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	kv := &kvStore{q: f.querier(), tenantID: tid}
	return &workflowStore{kv: kv}, nil
}

func (f *StoreFactory) StateMachineAuditStore(ctx context.Context) (spi.StateMachineAuditStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &smAuditStore{q: f.querier(), tenantID: tid}, nil
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

// TransactionManager implements spi.StoreFactory.
// Returns the TM configured on the factory. Errors if none was set.
func (f *StoreFactory) TransactionManager(ctx context.Context) (spi.TransactionManager, error) {
	if f.tm == nil {
		return nil, fmt.Errorf("postgres: TransactionManager not configured on StoreFactory")
	}
	return f.tm, nil
}

// newStoreFactory is the unexported constructor called by Plugin.NewFactory.
func newStoreFactory(pool *pgxpool.Pool) *StoreFactory {
	return NewStoreFactory(pool)
}

// InitTransactionManager installs a TransactionManager on the factory using
// the given UUID generator. It must be called before the factory is used to
// manage transactions; until it is called, TransactionManager() returns an
// error. Calling it more than once overwrites the previous manager.
//
// The internal alias initTransactionManager (same body, unexported) remains
// for use within the package; external callers — including test packages —
// should call this exported form.
func (f *StoreFactory) InitTransactionManager(uuids spi.UUIDGenerator) {
	tm := NewTransactionManager(f.pool, uuids)
	f.setTransactionManager(tm)
}

// initTransactionManager is the in-package alias used by Plugin.NewFactory.
func (f *StoreFactory) initTransactionManager(uuids spi.UUIDGenerator) {
	f.InitTransactionManager(uuids)
}
