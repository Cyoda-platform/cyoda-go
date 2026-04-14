package memory

import (
	"context"
	"fmt"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// ModelStore is a tenant-scoped, in-memory implementation of spi.ModelStore.
type ModelStore struct {
	tenant  common.TenantID
	factory *StoreFactory
}

func cloneDescriptor(src *common.ModelDescriptor) *common.ModelDescriptor {
	cp := *src
	if src.Schema != nil {
		cp.Schema = make([]byte, len(src.Schema))
		copy(cp.Schema, src.Schema)
	}
	return &cp
}

func (s *ModelStore) Save(ctx context.Context, desc *common.ModelDescriptor) error {
	s.factory.modelMu.Lock()
	defer s.factory.modelMu.Unlock()
	if s.factory.modelData[s.tenant] == nil {
		s.factory.modelData[s.tenant] = make(map[common.ModelRef]*common.ModelDescriptor)
	}
	s.factory.modelData[s.tenant][desc.Ref] = cloneDescriptor(desc)
	return nil
}

func (s *ModelStore) Get(ctx context.Context, modelRef common.ModelRef) (*common.ModelDescriptor, error) {
	s.factory.modelMu.RLock()
	defer s.factory.modelMu.RUnlock()
	entry, ok := s.factory.modelData[s.tenant][modelRef]
	if !ok {
		return nil, fmt.Errorf("model %s not found", modelRef)
	}
	return cloneDescriptor(entry), nil
}

func (s *ModelStore) GetAll(ctx context.Context) ([]common.ModelRef, error) {
	s.factory.modelMu.RLock()
	defer s.factory.modelMu.RUnlock()
	var refs []common.ModelRef
	for ref := range s.factory.modelData[s.tenant] {
		refs = append(refs, ref)
	}
	return refs, nil
}

func (s *ModelStore) Delete(ctx context.Context, modelRef common.ModelRef) error {
	s.factory.modelMu.Lock()
	defer s.factory.modelMu.Unlock()
	delete(s.factory.modelData[s.tenant], modelRef)
	return nil
}

func (s *ModelStore) Lock(ctx context.Context, modelRef common.ModelRef) error {
	s.factory.modelMu.Lock()
	defer s.factory.modelMu.Unlock()
	entry, ok := s.factory.modelData[s.tenant][modelRef]
	if !ok {
		return fmt.Errorf("model %s not found", modelRef)
	}
	entry.State = common.ModelLocked
	return nil
}

func (s *ModelStore) Unlock(ctx context.Context, modelRef common.ModelRef) error {
	s.factory.modelMu.Lock()
	defer s.factory.modelMu.Unlock()
	entry, ok := s.factory.modelData[s.tenant][modelRef]
	if !ok {
		return fmt.Errorf("model %s not found", modelRef)
	}
	entry.State = common.ModelUnlocked
	return nil
}

func (s *ModelStore) IsLocked(ctx context.Context, modelRef common.ModelRef) (bool, error) {
	s.factory.modelMu.RLock()
	defer s.factory.modelMu.RUnlock()
	entry, ok := s.factory.modelData[s.tenant][modelRef]
	if !ok {
		return false, fmt.Errorf("model %s not found", modelRef)
	}
	return entry.State == common.ModelLocked, nil
}

func (s *ModelStore) SetChangeLevel(ctx context.Context, modelRef common.ModelRef, level common.ChangeLevel) error {
	s.factory.modelMu.Lock()
	defer s.factory.modelMu.Unlock()
	entry, ok := s.factory.modelData[s.tenant][modelRef]
	if !ok {
		return fmt.Errorf("model %s not found", modelRef)
	}
	entry.ChangeLevel = level
	return nil
}
