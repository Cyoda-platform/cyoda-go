package memory

import (
	"context"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type KeyValueStore struct {
	tenant  spi.TenantID
	factory *StoreFactory
}

func (s *KeyValueStore) Put(ctx context.Context, namespace string, key string, value []byte) error {
	s.factory.kvMu.Lock()
	defer s.factory.kvMu.Unlock()
	if s.factory.kvData[s.tenant] == nil {
		s.factory.kvData[s.tenant] = make(map[string]map[string][]byte)
	}
	if s.factory.kvData[s.tenant][namespace] == nil {
		s.factory.kvData[s.tenant][namespace] = make(map[string][]byte)
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	s.factory.kvData[s.tenant][namespace][key] = cp
	return nil
}

func (s *KeyValueStore) Get(ctx context.Context, namespace string, key string) ([]byte, error) {
	s.factory.kvMu.RLock()
	defer s.factory.kvMu.RUnlock()
	ns, ok := s.factory.kvData[s.tenant][namespace]
	if !ok {
		return nil, fmt.Errorf("key %s not found in namespace %s", key, namespace)
	}
	val, ok := ns[key]
	if !ok {
		return nil, fmt.Errorf("key %s not found in namespace %s", key, namespace)
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	return cp, nil
}

func (s *KeyValueStore) Delete(ctx context.Context, namespace string, key string) error {
	s.factory.kvMu.Lock()
	defer s.factory.kvMu.Unlock()
	if ns, ok := s.factory.kvData[s.tenant][namespace]; ok {
		delete(ns, key)
	}
	return nil
}

func (s *KeyValueStore) List(ctx context.Context, namespace string) (map[string][]byte, error) {
	s.factory.kvMu.RLock()
	defer s.factory.kvMu.RUnlock()
	ns := s.factory.kvData[s.tenant][namespace]
	result := make(map[string][]byte, len(ns))
	for k, v := range ns {
		cp := make([]byte, len(v))
		copy(cp, v)
		result[k] = cp
	}
	return result, nil
}
