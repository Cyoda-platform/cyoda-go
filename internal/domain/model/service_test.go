package model_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model"
)

// refreshingModelStore is a ModelStore fake that:
//   - returns getDescriptor from Get (the "stale" view)
//   - returns refreshDescriptor from RefreshAndGet (the "fresh" view)
//
// Save, Get, and RefreshAndGet calls are counted for assertions. Save
// also captures the last saved descriptor so tests can verify the
// import produced a new UNLOCKED descriptor.
type refreshingModelStore struct {
	mu sync.Mutex

	// getDescriptor is what Get returns. Typically a LOCKED stale value.
	getDescriptor *spi.ModelDescriptor
	getErr        error
	getCount      int

	// refreshDescriptor is what RefreshAndGet returns. nil models the
	// authoritative post-delete state where no model exists upstream.
	refreshDescriptor *spi.ModelDescriptor
	refreshErr        error
	refreshCount     int

	// saved is the last descriptor passed to Save, if any.
	saved     *spi.ModelDescriptor
	saveCount int
}

func (s *refreshingModelStore) Get(_ context.Context, _ spi.ModelRef) (*spi.ModelDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCount++
	return s.getDescriptor, s.getErr
}

func (s *refreshingModelStore) RefreshAndGet(_ context.Context, _ spi.ModelRef) (*spi.ModelDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshCount++
	return s.refreshDescriptor, s.refreshErr
}

func (s *refreshingModelStore) Save(_ context.Context, d *spi.ModelDescriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCount++
	s.saved = d
	return nil
}

func (s *refreshingModelStore) GetAll(context.Context) ([]spi.ModelRef, error)       { return nil, nil }
func (s *refreshingModelStore) Delete(context.Context, spi.ModelRef) error           { return nil }
func (s *refreshingModelStore) Lock(context.Context, spi.ModelRef) error             { return nil }
func (s *refreshingModelStore) Unlock(context.Context, spi.ModelRef) error           { return nil }
func (s *refreshingModelStore) IsLocked(context.Context, spi.ModelRef) (bool, error) { return true, nil }
func (s *refreshingModelStore) SetChangeLevel(context.Context, spi.ModelRef, spi.ChangeLevel) error {
	return nil
}
func (s *refreshingModelStore) ExtendSchema(context.Context, spi.ModelRef, spi.SchemaDelta) error {
	return nil
}

// Compile-time check that refreshingModelStore satisfies the SPI contract.
var _ spi.ModelStore = (*refreshingModelStore)(nil)

func (s *refreshingModelStore) GetCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCount
}

func (s *refreshingModelStore) RefreshCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshCount
}

func (s *refreshingModelStore) Saved() *spi.ModelDescriptor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved
}

// fakeStoreFactory satisfies spi.StoreFactory with the given ModelStore.
// All other stores return an error — ImportModel only touches ModelStore.
type fakeStoreFactory struct {
	modelStore spi.ModelStore
}

func (f *fakeStoreFactory) ModelStore(_ context.Context) (spi.ModelStore, error) {
	return f.modelStore, nil
}

var errUnused = errors.New("store not used by this test")

func (f *fakeStoreFactory) EntityStore(_ context.Context) (spi.EntityStore, error) {
	return nil, errUnused
}
func (f *fakeStoreFactory) KeyValueStore(_ context.Context) (spi.KeyValueStore, error) {
	return nil, errUnused
}
func (f *fakeStoreFactory) MessageStore(_ context.Context) (spi.MessageStore, error) {
	return nil, errUnused
}
func (f *fakeStoreFactory) WorkflowStore(_ context.Context) (spi.WorkflowStore, error) {
	return nil, errUnused
}
func (f *fakeStoreFactory) StateMachineAuditStore(_ context.Context) (spi.StateMachineAuditStore, error) {
	return nil, errUnused
}
func (f *fakeStoreFactory) AsyncSearchStore(_ context.Context) (spi.AsyncSearchStore, error) {
	return nil, errUnused
}
func (f *fakeStoreFactory) TransactionManager(_ context.Context) (spi.TransactionManager, error) {
	return nil, errUnused
}
func (f *fakeStoreFactory) Close() error { return nil }

// TestImportModel_StaleCacheAfterRemoteDelete_ProceedsAfterRefresh verifies
// that when the cached ModelStore.Get returns a stale LOCKED descriptor
// (e.g. because a peer's delete was broadcast on gossip but hasn't landed
// on this node yet), ImportModel consults RefreshAndGet to bypass the
// cache. The fresh authoritative state is "no model exists", so the import
// must proceed (Save) rather than reject with 409.
func TestImportModel_StaleCacheAfterRemoteDelete_ProceedsAfterRefresh(t *testing.T) {
	staleRef := spi.ModelRef{EntityName: "Dataset", ModelVersion: "1"}
	stale := &spi.ModelDescriptor{
		Ref:   staleRef,
		State: spi.ModelLocked,
		// No Schema — merging path not exercised; fresh path is nil.
	}
	ms := &refreshingModelStore{
		getDescriptor:     stale,
		refreshDescriptor: nil, // peer's delete propagated authoritatively
	}

	h := model.New(&fakeStoreFactory{modelStore: ms})

	// A trivial JSON sample — the importer only needs parseable sample data.
	result, err := h.ImportModel(context.Background(), model.ImportModelInput{
		EntityName:   "Dataset",
		ModelVersion: "1",
		Format:       "JSON",
		Converter:    "SAMPLE_DATA",
		Data:         []byte(`{"field":"value"}`),
	})
	if err != nil {
		t.Fatalf("ImportModel: expected success after cache refresh, got %v", err)
	}
	if result == nil || result.ModelID == "" {
		t.Fatalf("expected non-empty ModelID in result, got %+v", result)
	}
	if ms.RefreshCount() == 0 {
		t.Errorf("expected RefreshAndGet to be called at least once, got 0")
	}
	if ms.Saved() == nil {
		t.Fatal("expected ModelStore.Save to be called with new descriptor")
	}
	if ms.Saved().State != spi.ModelUnlocked {
		t.Errorf("expected saved descriptor State=UNLOCKED, got %s", ms.Saved().State)
	}
}

// TestLockModel_StaleCacheAfterRemoteDelete_404Not409 verifies that
// LockModel's existence pre-check goes through RefreshAndGet, not the
// cached Get. When a peer has deleted the model but this node still
// holds a stale LOCKED descriptor in its per-request cache, LockModel
// must observe the authoritative "gone" state and return 404
// (model-not-found), not 409 (already-locked).
//
// This test documents the pattern applied to LockModel, UnlockModel,
// DeleteModel, and SetChangeLevel — all four sites have the same shape
// and share the getModelFresh helper.
func TestLockModel_StaleCacheAfterRemoteDelete_404Not409(t *testing.T) {
	staleRef := spi.ModelRef{EntityName: "Dataset", ModelVersion: "1"}
	stale := &spi.ModelDescriptor{
		Ref:   staleRef,
		State: spi.ModelLocked,
	}
	ms := &refreshingModelStore{
		getDescriptor:     stale,
		refreshDescriptor: nil, // peer's delete propagated authoritatively
	}

	h := model.New(&fakeStoreFactory{modelStore: ms})

	_, err := h.LockModel(context.Background(), "Dataset", "1")
	if err == nil {
		t.Fatalf("LockModel: expected model-not-found error after refresh, got nil")
	}
	if ms.RefreshCount() == 0 {
		t.Errorf("expected RefreshAndGet to be called at least once, got 0")
	}
	// The error must be a 404 (MODEL_NOT_FOUND), not a 409 (already-locked
	// conflict). The exact error type comes from modelNotFound() in
	// handler.go — we match on its code/status rather than the message.
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *common.AppError, got %T: %v", err, err)
	}
	if appErr.Status != 404 {
		t.Errorf("expected HTTP 404 (model-not-found), got %d: %s", appErr.Status, appErr.Message)
	}
}

// TestExportModel_ClassifiesModelStoreErrors verifies that ExportModel
// distinguishes spi.ErrNotFound (a legitimate 404) from other infrastructure
// errors returned by ModelStore.Get (which must be 5xx). Blanket-mapping every
// Get error to 404 MODEL_NOT_FOUND hides real failures — a schema fold or a
// transient pgx connection blip would look indistinguishable from a genuine
// missing model.
func TestExportModel_ClassifiesModelStoreErrors(t *testing.T) {
	ref := spi.ModelRef{EntityName: "Dataset", ModelVersion: "1"}

	t.Run("ErrNotFound maps to 404", func(t *testing.T) {
		ms := &refreshingModelStore{getErr: spi.ErrNotFound}
		h := model.New(&fakeStoreFactory{modelStore: ms})

		_, err := h.ExportModel(context.Background(), "Dataset", "1", "JSON_SCHEMA")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var appErr *common.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected *common.AppError, got %T: %v", err, err)
		}
		if appErr.Status != 404 {
			t.Errorf("expected 404 for ErrNotFound, got %d: %s", appErr.Status, appErr.Message)
		}
	})

	t.Run("arbitrary error maps to 5xx", func(t *testing.T) {
		synthetic := errors.New("synthetic fold failure")
		ms := &refreshingModelStore{getErr: synthetic}
		h := model.New(&fakeStoreFactory{modelStore: ms})

		_, err := h.ExportModel(context.Background(), "Dataset", "1", "JSON_SCHEMA")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var appErr *common.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected *common.AppError, got %T: %v", err, err)
		}
		if appErr.Status == 404 {
			t.Errorf("non-ErrNotFound infra error must not be 404 MODEL_NOT_FOUND; got %d: %s", appErr.Status, appErr.Message)
		}
		if appErr.Status < 500 || appErr.Status >= 600 {
			t.Errorf("expected 5xx for non-ErrNotFound error, got %d: %s", appErr.Status, appErr.Message)
		}
		// The original error must be preserved in the chain for logging /
		// correlation via the ticket UUID.
		if !errors.Is(err, synthetic) {
			t.Errorf("expected wrapped error to satisfy errors.Is(synthetic), got %v", err)
		}
	})

	_ = ref // silence unused in case future expansion needs it
}
