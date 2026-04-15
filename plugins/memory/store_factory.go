package memory

import (
	"context"
	"fmt"
	"os"
	"sync"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type StoreFactory struct {
	entityMu    sync.RWMutex
	modelMu     sync.RWMutex
	kvMu        sync.RWMutex
	msgMu       sync.RWMutex
	wfMu        sync.RWMutex
	smAuditMu   sync.RWMutex
	entityData  map[spi.TenantID]map[string][]entityVersion
	modelData   map[spi.TenantID]map[spi.ModelRef]*spi.ModelDescriptor
	kvData      map[spi.TenantID]map[string]map[string][]byte
	msgData     map[spi.TenantID]map[string]*messageEntry
	wfData      map[spi.TenantID]map[spi.ModelRef][]spi.WorkflowDefinition
	smAudit     map[spi.TenantID]map[string][]spi.StateMachineEvent // tenantID -> entityID -> events
	blobDir     string
	txManager   *TransactionManager
	searchStore *AsyncSearchStore
}

func NewStoreFactory() *StoreFactory {
	blobDir, err := os.MkdirTemp("", "cyoda-go-blobs-*")
	if err != nil {
		panic(fmt.Sprintf("failed to create blob temp dir: %v", err))
	}
	return &StoreFactory{
		entityData:  make(map[spi.TenantID]map[string][]entityVersion),
		modelData:   make(map[spi.TenantID]map[spi.ModelRef]*spi.ModelDescriptor),
		kvData:      make(map[spi.TenantID]map[string]map[string][]byte),
		msgData:     make(map[spi.TenantID]map[string]*messageEntry),
		wfData:      make(map[spi.TenantID]map[spi.ModelRef][]spi.WorkflowDefinition),
		smAudit:     make(map[spi.TenantID]map[string][]spi.StateMachineEvent),
		blobDir:     blobDir,
		searchStore: NewAsyncSearchStore(),
	}
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

func (f *StoreFactory) EntityStore(ctx context.Context) (spi.EntityStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &EntityStore{tenant: tid, factory: f}, nil
}

func (f *StoreFactory) ModelStore(ctx context.Context) (spi.ModelStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &ModelStore{tenant: tid, factory: f}, nil
}

func (f *StoreFactory) KeyValueStore(ctx context.Context) (spi.KeyValueStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &KeyValueStore{tenant: tid, factory: f}, nil
}

func (f *StoreFactory) MessageStore(ctx context.Context) (spi.MessageStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &MessageStore{tenant: tid, factory: f}, nil
}

func (f *StoreFactory) WorkflowStore(ctx context.Context) (spi.WorkflowStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &WorkflowStore{tenant: tid, factory: f}, nil
}

func (f *StoreFactory) StateMachineAuditStore(ctx context.Context) (spi.StateMachineAuditStore, error) {
	tid, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &StateMachineAuditStore{tenant: tid, factory: f}, nil
}

func (f *StoreFactory) AsyncSearchStore(_ context.Context) (spi.AsyncSearchStore, error) {
	return f.searchStore, nil
}

func (f *StoreFactory) Close() error {
	return os.RemoveAll(f.blobDir)
}

// TransactionManager implements spi.StoreFactory.
// Returns the TM registered via NewTransactionManager. Errors if none is set.
func (f *StoreFactory) TransactionManager(ctx context.Context) (spi.TransactionManager, error) {
	tm := f.GetTransactionManager()
	if tm == nil {
		return nil, fmt.Errorf("memory: TransactionManager not initialized (call NewTransactionManager first)")
	}
	return tm, nil
}

// newStoreFactory is the unexported constructor called by Plugin.NewFactory.
func newStoreFactory() *StoreFactory {
	return NewStoreFactory()
}

// initTransactionManager installs a TransactionManager on the factory.
func (f *StoreFactory) initTransactionManager(uuids spi.UUIDGenerator) {
	f.NewTransactionManager(uuids)
}
