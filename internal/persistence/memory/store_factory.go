package memory

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/spi"
)

type StoreFactory struct {
	entityMu    sync.RWMutex
	modelMu     sync.RWMutex
	kvMu        sync.RWMutex
	msgMu       sync.RWMutex
	wfMu        sync.RWMutex
	smAuditMu   sync.RWMutex
	entityData  map[common.TenantID]map[string][]entityVersion
	modelData   map[common.TenantID]map[common.ModelRef]*common.ModelDescriptor
	kvData      map[common.TenantID]map[string]map[string][]byte
	msgData     map[common.TenantID]map[string]*messageEntry
	wfData      map[common.TenantID]map[common.ModelRef][]common.WorkflowDefinition
	smAudit     map[common.TenantID]map[string][]common.StateMachineEvent // tenantID -> entityID -> events
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
		entityData:  make(map[common.TenantID]map[string][]entityVersion),
		modelData:   make(map[common.TenantID]map[common.ModelRef]*common.ModelDescriptor),
		kvData:      make(map[common.TenantID]map[string]map[string][]byte),
		msgData:     make(map[common.TenantID]map[string]*messageEntry),
		wfData:      make(map[common.TenantID]map[common.ModelRef][]common.WorkflowDefinition),
		smAudit:     make(map[common.TenantID]map[string][]common.StateMachineEvent),
		blobDir:     blobDir,
		searchStore: NewAsyncSearchStore(),
	}
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
