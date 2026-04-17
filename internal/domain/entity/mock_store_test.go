package entity_test

import (
	"context"
	"iter"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// failingStoreFactory is a mock that returns a failingEntityStore from EntityStore().
type failingStoreFactory struct {
	err error
}

func (f *failingStoreFactory) EntityStore(_ context.Context) (spi.EntityStore, error) {
	return &failingEntityStore{err: f.err}, nil
}
func (f *failingStoreFactory) ModelStore(_ context.Context) (spi.ModelStore, error) {
	return nil, f.err
}
func (f *failingStoreFactory) KeyValueStore(_ context.Context) (spi.KeyValueStore, error) {
	return nil, f.err
}
func (f *failingStoreFactory) MessageStore(_ context.Context) (spi.MessageStore, error) {
	return nil, f.err
}
func (f *failingStoreFactory) WorkflowStore(_ context.Context) (spi.WorkflowStore, error) {
	return nil, f.err
}
func (f *failingStoreFactory) StateMachineAuditStore(_ context.Context) (spi.StateMachineAuditStore, error) {
	return nil, f.err
}
func (f *failingStoreFactory) AsyncSearchStore(_ context.Context) (spi.AsyncSearchStore, error) {
	return nil, f.err
}
func (f *failingStoreFactory) TransactionManager(_ context.Context) (spi.TransactionManager, error) {
	return nil, f.err
}
func (f *failingStoreFactory) Close() error { return nil }

// failingEntityStore always returns the configured error from Get/GetAsAt.
type failingEntityStore struct {
	err error
}

func (s *failingEntityStore) Save(_ context.Context, _ *spi.Entity) (int64, error) {
	return 0, s.err
}
func (s *failingEntityStore) SaveAll(_ context.Context, _ iter.Seq[*spi.Entity]) ([]int64, error) {
	return nil, s.err
}
func (s *failingEntityStore) CompareAndSave(_ context.Context, _ *spi.Entity, _ string) (int64, error) {
	return 0, s.err
}
func (s *failingEntityStore) Get(_ context.Context, _ string) (*spi.Entity, error) {
	return nil, s.err
}
func (s *failingEntityStore) GetAsAt(_ context.Context, _ string, _ time.Time) (*spi.Entity, error) {
	return nil, s.err
}
func (s *failingEntityStore) GetAll(_ context.Context, _ spi.ModelRef) ([]*spi.Entity, error) {
	return nil, s.err
}
func (s *failingEntityStore) GetAllAsAt(_ context.Context, _ spi.ModelRef, _ time.Time) ([]*spi.Entity, error) {
	return nil, s.err
}
func (s *failingEntityStore) Delete(_ context.Context, _ string) error {
	return s.err
}
func (s *failingEntityStore) DeleteAll(_ context.Context, _ spi.ModelRef) error {
	return s.err
}
func (s *failingEntityStore) Exists(_ context.Context, _ string) (bool, error) {
	return false, s.err
}
func (s *failingEntityStore) Count(_ context.Context, _ spi.ModelRef) (int64, error) {
	return 0, s.err
}
func (s *failingEntityStore) CountByState(_ context.Context, _ spi.ModelRef, _ []string) (map[string]int64, error) {
	return nil, s.err
}
func (s *failingEntityStore) GetVersionHistory(_ context.Context, _ string) ([]spi.EntityVersion, error) {
	return nil, s.err
}
