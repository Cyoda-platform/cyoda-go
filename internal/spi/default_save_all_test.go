package spi_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/spi"
)

// mockEntityStore is a minimal mock for testing DefaultSaveAll.
type mockEntityStore struct {
	saveFunc func(ctx context.Context, entity *common.Entity) (int64, error)
	calls    []*common.Entity
}

func (m *mockEntityStore) Save(ctx context.Context, entity *common.Entity) (int64, error) {
	m.calls = append(m.calls, entity)
	return m.saveFunc(ctx, entity)
}

// Stubs for the rest of EntityStore — not exercised by DefaultSaveAll.
func (m *mockEntityStore) CompareAndSave(context.Context, *common.Entity, string) (int64, error) {
	return 0, nil
}
func (m *mockEntityStore) SaveAll(context.Context, iter.Seq[*common.Entity]) ([]int64, error) {
	return nil, nil
}
func (m *mockEntityStore) Get(context.Context, string) (*common.Entity, error)  { return nil, nil }
func (m *mockEntityStore) GetAsAt(_ context.Context, _ string, _ time.Time) (*common.Entity, error) {
	return nil, nil
}
func (m *mockEntityStore) GetAll(context.Context, common.ModelRef) ([]*common.Entity, error) {
	return nil, nil
}
func (m *mockEntityStore) GetAllAsAt(_ context.Context, _ common.ModelRef, _ time.Time) ([]*common.Entity, error) {
	return nil, nil
}
func (m *mockEntityStore) Delete(context.Context, string) error                      { return nil }
func (m *mockEntityStore) DeleteAll(context.Context, common.ModelRef) error          { return nil }
func (m *mockEntityStore) Exists(context.Context, string) (bool, error)              { return false, nil }
func (m *mockEntityStore) Count(context.Context, common.ModelRef) (int64, error)     { return 0, nil }
func (m *mockEntityStore) GetVersionHistory(context.Context, string) ([]common.EntityVersion, error) {
	return nil, nil
}

func TestDefaultSaveAll_SequentialOrder(t *testing.T) {
	var nextVersion int64
	store := &mockEntityStore{
		saveFunc: func(_ context.Context, _ *common.Entity) (int64, error) {
			nextVersion++
			return nextVersion, nil
		},
	}

	entities := []*common.Entity{
		{Meta: common.EntityMeta{ID: "a"}},
		{Meta: common.EntityMeta{ID: "b"}},
		{Meta: common.EntityMeta{ID: "c"}},
	}

	versions, err := spi.DefaultSaveAll(store, context.Background(), slices.Values(entities))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	for i, v := range versions {
		if v != int64(i+1) {
			t.Errorf("versions[%d] = %d, want %d", i, v, i+1)
		}
	}
	if len(store.calls) != 3 {
		t.Fatalf("expected 3 Save calls, got %d", len(store.calls))
	}
	for i, e := range store.calls {
		if e.Meta.ID != entities[i].Meta.ID {
			t.Errorf("call %d: got ID %q, want %q", i, e.Meta.ID, entities[i].Meta.ID)
		}
	}
}

func TestDefaultSaveAll_ErrorStopsEarly(t *testing.T) {
	callCount := 0
	boom := errors.New("boom")
	store := &mockEntityStore{
		saveFunc: func(_ context.Context, _ *common.Entity) (int64, error) {
			callCount++
			if callCount == 2 {
				return 0, boom
			}
			return int64(callCount), nil
		},
	}

	entities := []*common.Entity{
		{Meta: common.EntityMeta{ID: "a"}},
		{Meta: common.EntityMeta{ID: "b"}},
		{Meta: common.EntityMeta{ID: "c"}},
	}

	versions, err := spi.DefaultSaveAll(store, context.Background(), slices.Values(entities))
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom error, got %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 partial version, got %d", len(versions))
	}
	if versions[0] != 1 {
		t.Errorf("versions[0] = %d, want 1", versions[0])
	}
	if callCount != 2 {
		t.Errorf("expected 2 Save calls, got %d", callCount)
	}
}

func TestDefaultSaveAll_EmptyIterator(t *testing.T) {
	store := &mockEntityStore{
		saveFunc: func(_ context.Context, _ *common.Entity) (int64, error) {
			t.Fatal("Save should not be called for empty iterator")
			return 0, nil
		},
	}

	empty := func(yield func(*common.Entity) bool) {}
	versions, err := spi.DefaultSaveAll(store, context.Background(), empty)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected 0 versions, got %d", len(versions))
	}
}
