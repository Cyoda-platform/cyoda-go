package entity

import (
	"context"
	"strings"
	"sync"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// refreshingStore is a ModelStore fake that implements RefreshAndGet
// with a queue: each Get returns the head of getQueue; each
// RefreshAndGet returns the head of refreshQueue.
type refreshingStore struct {
	mu           sync.Mutex
	getQueue     []*spi.ModelDescriptor
	refreshQueue []*spi.ModelDescriptor
	getCount     int
	refreshCount int
}

func (s *refreshingStore) Get(_ context.Context, _ spi.ModelRef) (*spi.ModelDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCount++
	if len(s.getQueue) == 0 {
		// Fallback to last refresh result if queue drained.
		if len(s.refreshQueue) > 0 {
			return s.refreshQueue[0], nil
		}
		return nil, nil
	}
	d := s.getQueue[0]
	s.getQueue = s.getQueue[1:]
	return d, nil
}

func (s *refreshingStore) RefreshAndGet(_ context.Context, _ spi.ModelRef) (*spi.ModelDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshCount++
	if len(s.refreshQueue) == 0 {
		return nil, nil
	}
	d := s.refreshQueue[0]
	s.refreshQueue = s.refreshQueue[1:]
	return d, nil
}

// Satisfy the rest of spi.ModelStore with no-ops.
func (s *refreshingStore) Save(context.Context, *spi.ModelDescriptor) error     { return nil }
func (s *refreshingStore) GetAll(context.Context) ([]spi.ModelRef, error)       { return nil, nil }
func (s *refreshingStore) Delete(context.Context, spi.ModelRef) error           { return nil }
func (s *refreshingStore) Lock(context.Context, spi.ModelRef) error             { return nil }
func (s *refreshingStore) Unlock(context.Context, spi.ModelRef) error           { return nil }
func (s *refreshingStore) IsLocked(context.Context, spi.ModelRef) (bool, error) { return true, nil }
func (s *refreshingStore) SetChangeLevel(context.Context, spi.ModelRef, spi.ChangeLevel) error {
	return nil
}
func (s *refreshingStore) ExtendSchema(context.Context, spi.ModelRef, spi.SchemaDelta) error {
	return nil
}

// Compile-time check that refreshingStore satisfies the SPI contract.
var _ spi.ModelStore = (*refreshingStore)(nil)

func (s *refreshingStore) GetCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCount
}

func (s *refreshingStore) RefreshCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshCount
}

// buildDescriptorWithFields constructs a LOCKED descriptor whose Schema
// encodes an object node with the given named string-leaf children.
func buildDescriptorWithFields(t *testing.T, ref spi.ModelRef, fields ...string) *spi.ModelDescriptor {
	t.Helper()
	node := schema.NewObjectNode()
	for _, f := range fields {
		node.SetChild(f, schema.NewLeafNode(schema.String))
	}
	raw, err := schema.Marshal(node)
	if err != nil {
		t.Fatalf("schema.Marshal: %v", err)
	}
	return &spi.ModelDescriptor{
		Ref:    ref,
		State:  spi.ModelLocked,
		Schema: raw,
	}
}

func TestValidateWithRefresh_NoErrors_NoRefresh(t *testing.T) {
	h := &Handler{}
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	desc := buildDescriptorWithFields(t, ref, "a")
	ms := &refreshingStore{getQueue: []*spi.ModelDescriptor{desc}}

	err := h.ValidateWithRefresh(context.Background(), ms, ref, map[string]any{"a": "x"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ms.RefreshCount() != 0 {
		t.Errorf("expected 0 refreshes on clean validation, got %d", ms.RefreshCount())
	}
}

func TestValidateWithRefresh_StaleSchema_RefreshesOnce_ThenSucceeds(t *testing.T) {
	h := &Handler{}
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	stale := buildDescriptorWithFields(t, ref, "a")
	fresh := buildDescriptorWithFields(t, ref, "a", "b")
	ms := &refreshingStore{
		getQueue:     []*spi.ModelDescriptor{stale},
		refreshQueue: []*spi.ModelDescriptor{fresh},
	}

	// Data references 'b' — stale rejects, fresh accepts.
	err := h.ValidateWithRefresh(context.Background(), ms, ref, map[string]any{"a": "x", "b": "y"})
	if err != nil {
		t.Fatalf("expected pass after refresh, got %v", err)
	}
	if ms.RefreshCount() != 1 {
		t.Errorf("expected exactly 1 refresh, got %d", ms.RefreshCount())
	}
}

func TestValidateWithRefresh_RefreshStillStale_ReturnsErrors(t *testing.T) {
	h := &Handler{}
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	stale := buildDescriptorWithFields(t, ref, "a")
	stillStale := buildDescriptorWithFields(t, ref, "a")
	ms := &refreshingStore{
		getQueue:     []*spi.ModelDescriptor{stale},
		refreshQueue: []*spi.ModelDescriptor{stillStale},
	}

	err := h.ValidateWithRefresh(context.Background(), ms, ref, map[string]any{"a": "x", "b": "y"})
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected validation failure after refresh, got %v", err)
	}
	if ms.RefreshCount() != 1 {
		t.Errorf("expected exactly 1 refresh (bounded), got %d", ms.RefreshCount())
	}
}

func TestValidateWithRefresh_TypeMismatch_NoRefresh(t *testing.T) {
	// Non-unknown-element validation failure — must not trigger refresh.
	h := &Handler{}
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	desc := buildDescriptorWithFields(t, ref, "a") // 'a' is String
	ms := &refreshingStore{getQueue: []*spi.ModelDescriptor{desc}}

	// Data for 'a' is the wrong type — bool not string.
	//
	// Updated for A.1: the value-based classifier in validate.go only
	// recognizes json.Number for numerics; raw Go ints leak through the
	// default branch as String, which would coincidentally satisfy the
	// String schema and mask the test's intent. Bool is classified
	// unambiguously via inferDataType and mismatches String reliably.
	err := h.ValidateWithRefresh(context.Background(), ms, ref, map[string]any{"a": true})
	if err == nil {
		t.Fatal("expected type-mismatch error")
	}
	if ms.RefreshCount() != 0 {
		t.Errorf("non-unknown-element failures must not refresh, got %d", ms.RefreshCount())
	}
}

func TestValidateWithRefresh_NoRefreshInterface_ReturnsErrorsDirect(t *testing.T) {
	h := &Handler{}
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	// recordingModelStore (from handler_validate_or_extend_test.go) does
	// NOT implement RefreshAndGet — the wrapper must surface the original
	// errors without attempting refresh.
	desc := buildDescriptorWithFields(t, ref, "a")
	ms := &recordingModelStore{descriptor: desc}

	err := h.ValidateWithRefresh(context.Background(), ms, ref, map[string]any{"a": "x", "b": "y"})
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected direct validation failure (no refresh available), got %v", err)
	}
}
