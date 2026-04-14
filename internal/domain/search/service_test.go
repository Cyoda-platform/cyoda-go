package search_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/search"
	"github.com/cyoda-platform/cyoda-go/internal/domain/search/predicate"
	"github.com/cyoda-platform/cyoda-go/internal/persistence/memory"
	"github.com/cyoda-platform/cyoda-go/internal/spi"
)

// helper: create a context with a UserContext for the given tenant.
func tenantCtx(tenantID string) context.Context {
	return common.WithUserContext(context.Background(), &common.UserContext{
		UserID:   "test-user",
		UserName: "Test User",
		Tenant: common.Tenant{
			ID:   common.TenantID(tenantID),
			Name: "Test Tenant",
		},
		Roles: []string{"ROLE_USER"},
	})
}

// helper: save an entity with JSON data, return its ID.
func saveEntity(t *testing.T, ctx context.Context, factory *memory.StoreFactory, modelRef common.ModelRef, id string, data []byte) {
	t.Helper()
	store, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}
	_, err = store.Save(ctx, &common.Entity{
		Meta: common.EntityMeta{
			ID:       id,
			ModelRef: modelRef,
			State:    "NEW",
		},
		Data: data,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestDirectSearchSimpleEquals(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	saveEntity(t, ctx, factory, ref, "e1", []byte(`{"name":"Alice","age":30}`))
	saveEntity(t, ctx, factory, ref, "e2", []byte(`{"name":"Bob","age":25}`))
	saveEntity(t, ctx, factory, ref, "e3", []byte(`{"name":"Alice","age":40}`))

	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Alice",
	}

	results, err := svc.Search(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Verify the matched entities are Alice
	for _, e := range results {
		if e.Meta.ID != "e1" && e.Meta.ID != "e3" {
			t.Errorf("unexpected entity ID: %s", e.Meta.ID)
		}
	}
}

func TestDirectSearchNoMatches(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	saveEntity(t, ctx, factory, ref, "e1", []byte(`{"name":"Alice"}`))

	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Nobody",
	}

	results, err := svc.Search(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestDirectSearchPointInTime(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	// Save original
	saveEntity(t, ctx, factory, ref, "e1", []byte(`{"name":"Alice"}`))

	snapshot := time.Now()
	time.Sleep(2 * time.Millisecond) // ensure time progresses

	// Update entity
	store, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Save(ctx, &common.Entity{
		Meta: common.EntityMeta{
			ID:       "e1",
			ModelRef: ref,
			State:    "UPDATED",
		},
		Data: []byte(`{"name":"Bob"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Search at old timestamp should find "Alice"
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Alice",
	}
	pit := snapshot
	results, err := svc.Search(ctx, ref, cond, search.SearchOptions{PointInTime: &pit})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result at point-in-time, got %d", len(results))
	}
	if results[0].Meta.ID != "e1" {
		t.Errorf("expected e1, got %s", results[0].Meta.ID)
	}

	// Search at current time for "Alice" should find nothing (entity is now "Bob")
	results, err = svc.Search(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for Alice at current time, got %d", len(results))
	}
}

func TestDirectSearchPagination(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "item", ModelVersion: "1"}

	for i := 0; i < 5; i++ {
		saveEntity(t, ctx, factory, ref,
			fmt.Sprintf("e%d", i),
			[]byte(fmt.Sprintf(`{"val":%d}`, i)),
		)
	}

	// Match all with a condition that always matches (val > -1)
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.val",
		OperatorType: "GREATER_THAN",
		Value:        float64(-1),
	}

	// No pagination: should get all 5
	all, err := svc.Search(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5, got %d", len(all))
	}

	// Limit=2, Offset=2: should get 2 results
	page, err := svc.Search(ctx, ref, cond, search.SearchOptions{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Fatalf("expected 2 results with limit=2,offset=2, got %d", len(page))
	}

	// Offset=4, Limit=10: should get 1 result (only 5 total)
	tail, err := svc.Search(ctx, ref, cond, search.SearchOptions{Limit: 10, Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 {
		t.Fatalf("expected 1 result with offset=4, got %d", len(tail))
	}
}

func TestAsyncLifecycle(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	saveEntity(t, ctx, factory, ref, "e1", []byte(`{"name":"Alice"}`))
	saveEntity(t, ctx, factory, ref, "e2", []byte(`{"name":"Bob"}`))

	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Alice",
	}

	jobID, err := svc.SubmitAsync(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatalf("SubmitAsync: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected non-empty job ID")
	}

	// Poll until SUCCESSFUL (with timeout)
	deadline := time.Now().Add(5 * time.Second)
	var status search.SearchJobStatus
	for time.Now().Before(deadline) {
		status, err = svc.GetAsyncStatus(ctx, jobID)
		if err != nil {
			t.Fatalf("GetAsyncStatus: %v", err)
		}
		if status.Status == "SUCCESSFUL" || status.Status == "FAILED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Status != "SUCCESSFUL" {
		t.Fatalf("expected SUCCESSFUL, got %s", status.Status)
	}
	if status.FinishTime == nil {
		t.Fatal("expected non-nil finish time")
	}

	page, err := svc.GetAsyncResults(ctx, jobID, search.ResultOptions{})
	if err != nil {
		t.Fatalf("GetAsyncResults: %v", err)
	}
	if len(page.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(page.Results))
	}
	if page.Results[0].Meta.ID != "e1" {
		t.Errorf("expected e1, got %s", page.Results[0].Meta.ID)
	}
	if page.Total != 1 {
		t.Errorf("expected total=1, got %d", page.Total)
	}
}

func TestAsyncCancel(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	// Create many entities to increase chance the goroutine is still running
	for i := 0; i < 100; i++ {
		saveEntity(t, ctx, factory, ref,
			fmt.Sprintf("e%d", i),
			[]byte(fmt.Sprintf(`{"name":"entity-%d"}`, i)),
		)
	}

	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "entity-0",
	}

	jobID, err := svc.SubmitAsync(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatalf("SubmitAsync: %v", err)
	}

	// Cancel immediately
	result, err := svc.CancelAsync(ctx, jobID)
	if err != nil {
		t.Fatalf("CancelAsync: %v", err)
	}

	// The job might already be done (it's fast), so cancellation may or may not succeed
	// But we should at least be able to get the status without error
	status, err := svc.GetAsyncStatus(ctx, jobID)
	if err != nil {
		t.Fatalf("GetAsyncStatus after cancel: %v", err)
	}
	if result.Cancelled {
		if status.Status != "CANCELLED" {
			t.Errorf("expected CANCELLED status after successful cancel, got %s", status.Status)
		}
		if result.CurrentStatus != "CANCELLED" {
			t.Errorf("expected CancelResult.CurrentStatus=CANCELLED, got %s", result.CurrentStatus)
		}
	} else {
		// Job completed before cancel — CurrentStatus should reflect that
		if result.CurrentStatus != "SUCCESSFUL" && result.CurrentStatus != "FAILED" {
			t.Errorf("expected SUCCESSFUL or FAILED for non-cancelled job, got %s", result.CurrentStatus)
		}
	}
}

func TestAsyncTenantIsolation(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctxA := tenantCtx("tenant-A")
	ctxB := tenantCtx("tenant-B")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	saveEntity(t, ctxA, factory, ref, "e1", []byte(`{"name":"Alice"}`))

	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Alice",
	}

	jobID, err := svc.SubmitAsync(ctxA, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for completion
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := svc.GetAsyncStatus(ctxA, jobID)
		if st.Status == "SUCCESSFUL" || st.Status == "FAILED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Tenant B should not see tenant A's job
	_, err = svc.GetAsyncStatus(ctxB, jobID)
	if err == nil {
		t.Fatal("expected error when querying tenant A's job from tenant B context")
	}

	_, err = svc.GetAsyncResults(ctxB, jobID, search.ResultOptions{})
	if err == nil {
		t.Fatal("expected error when getting results of tenant A's job from tenant B context")
	}

	_, cancelErr := svc.CancelAsync(ctxB, jobID)
	if cancelErr == nil {
		t.Fatal("expected error when cancelling tenant A's job from tenant B context")
	}
}

// I-2: SubmitAsync must populate SearchOpts on the job.
func TestSubmitAsyncPopulatesSearchOpts(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	saveEntity(t, ctx, factory, ref, "e1", []byte(`{"name":"Alice"}`))

	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Alice",
	}

	pit := time.Now().Add(-1 * time.Hour)
	opts := search.SearchOptions{
		Limit:       50,
		Offset:      10,
		PointInTime: &pit,
	}

	jobID, err := svc.SubmitAsync(ctx, ref, cond, opts)
	if err != nil {
		t.Fatalf("SubmitAsync: %v", err)
	}

	// Check the job in the store immediately (before goroutine finishes).
	job, err := searchStore.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if len(job.SearchOpts) == 0 {
		t.Fatal("SearchOpts should be populated on the job, got empty")
	}

	// Verify it deserializes back correctly.
	var decoded struct {
		Limit  int  `json:"limit"`
		Offset int  `json:"offset"`
	}
	if err := json.Unmarshal(job.SearchOpts, &decoded); err != nil {
		t.Fatalf("failed to unmarshal SearchOpts: %v", err)
	}
	if decoded.Limit != 50 {
		t.Errorf("SearchOpts.Limit = %d, want 50", decoded.Limit)
	}
	if decoded.Offset != 10 {
		t.Errorf("SearchOpts.Offset = %d, want 10", decoded.Offset)
	}
}

// I-3: Cancel-then-complete must not overwrite CANCELLED with SUCCESSFUL.
// We use a blocking search store wrapper to control timing deterministically.

// blockingSearchStore wraps spi.AsyncSearchStore and blocks SaveResults until released.
type blockingSearchStore struct {
	spi.AsyncSearchStore
	saveResultsGate chan struct{} // close to unblock SaveResults
}

func (b *blockingSearchStore) SaveResults(ctx context.Context, jobID string, entityIDs []string) error {
	<-b.saveResultsGate // block until gate is opened
	return b.AsyncSearchStore.SaveResults(ctx, jobID, entityIDs)
}

func TestCancelRaceDoesNotOverwriteCancelled(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	realStore, _ := factory.AsyncSearchStore(context.Background())

	gate := make(chan struct{})
	blockedStore := &blockingSearchStore{
		AsyncSearchStore: realStore,
		saveResultsGate:  gate,
	}

	svc := search.NewSearchService(factory, uuids, blockedStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	saveEntity(t, ctx, factory, ref, "e1", []byte(`{"name":"Alice"}`))

	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Alice",
	}

	jobID, err := svc.SubmitAsync(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatalf("SubmitAsync: %v", err)
	}

	// Wait for the goroutine to reach SaveResults (it will block on the gate).
	// Poll until the search goroutine has at least started (job is still RUNNING).
	time.Sleep(50 * time.Millisecond)

	// Cancel the job while the goroutine is blocked.
	result, err := svc.CancelAsync(ctx, jobID)
	if err != nil {
		t.Fatalf("CancelAsync: %v", err)
	}
	if !result.Cancelled {
		t.Fatal("expected cancel to succeed while goroutine is blocked")
	}

	// Now release the goroutine to proceed with SaveResults + UpdateJobStatus.
	close(gate)

	// Give the goroutine time to finish.
	time.Sleep(100 * time.Millisecond)

	// Final status must be CANCELLED, not SUCCESSFUL.
	status, err := svc.GetAsyncStatus(ctx, jobID)
	if err != nil {
		t.Fatalf("GetAsyncStatus: %v", err)
	}
	if status.Status != "CANCELLED" {
		t.Errorf("expected CANCELLED after cancel-then-complete race, got %s", status.Status)
	}
}

// captureSearchStore is an in-memory AsyncSearchStore that records which
// methods get called. Used by TestSubmitAsync_SelfExecutingStore_SkipsGoroutine.
type captureSearchStore struct {
	spi.AsyncSearchStore

	mu                sync.Mutex
	createJobCalls    int
	saveResultsCalls  int
	updateStatusCalls int
}

func newCaptureSearchStore(base spi.AsyncSearchStore) *captureSearchStore {
	return &captureSearchStore{AsyncSearchStore: base}
}

func (c *captureSearchStore) CreateJob(ctx context.Context, job *spi.SearchJob) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.createJobCalls++
	return c.AsyncSearchStore.CreateJob(ctx, job)
}

func (c *captureSearchStore) SaveResults(ctx context.Context, jobID string, ids []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saveResultsCalls++
	return c.AsyncSearchStore.SaveResults(ctx, jobID, ids)
}

func (c *captureSearchStore) UpdateJobStatus(ctx context.Context, jobID string, status string, resultCount int, errMsg string, finishTime time.Time, calcTimeMs int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updateStatusCalls++
	return c.AsyncSearchStore.UpdateJobStatus(ctx, jobID, status, resultCount, errMsg, finishTime, calcTimeMs)
}

// selfExecutingCaptureStore wraps captureSearchStore and implements the
// spi.SelfExecutingSearchStore marker interface.
type selfExecutingCaptureStore struct {
	*captureSearchStore
}

func (s *selfExecutingCaptureStore) SelfExecuting() {}

// TestSubmitAsync_SelfExecutingStore_SkipsGoroutine verifies that a store
// implementing SelfExecutingSearchStore is not driven by the service's
// background goroutine — SaveResults and UpdateJobStatus must not be called.
func TestSubmitAsync_SelfExecutingStore_SkipsGoroutine(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	baseStore, _ := factory.AsyncSearchStore(context.Background())

	capture := newCaptureSearchStore(baseStore)
	store := &selfExecutingCaptureStore{captureSearchStore: capture}

	svc := search.NewSearchService(factory, uuids, store)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "Order", ModelVersion: "1"}
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.x",
		OperatorType: "EQUALS",
		Value:        "y",
	}

	jobID, err := svc.SubmitAsync(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatalf("SubmitAsync: %v", err)
	}
	if jobID == "" {
		t.Error("expected non-empty jobID")
	}

	// Wait long enough that any (incorrect) goroutine would have finished.
	time.Sleep(100 * time.Millisecond)

	capture.mu.Lock()
	defer capture.mu.Unlock()

	if capture.createJobCalls != 1 {
		t.Errorf("CreateJob: want 1 call, got %d", capture.createJobCalls)
	}
	if capture.saveResultsCalls != 0 {
		t.Errorf("self-executing store should never have SaveResults called by the service; got %d calls", capture.saveResultsCalls)
	}
	if capture.updateStatusCalls != 0 {
		t.Errorf("self-executing store should never have UpdateJobStatus called by the service; got %d calls", capture.updateStatusCalls)
	}
}

// I-3 variant: ensure the fix doesn't break normal successful flow.
func TestAsyncSuccessfulWhenNotCancelled(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	uuids := common.NewTestUUIDGenerator()
	searchStore, _ := factory.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, uuids, searchStore)

	ctx := tenantCtx("tenant-1")
	ref := common.ModelRef{EntityName: "person", ModelVersion: "1"}

	saveEntity(t, ctx, factory, ref, "e1", []byte(`{"name":"Alice"}`))

	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Alice",
	}

	jobID, err := svc.SubmitAsync(ctx, ref, cond, search.SearchOptions{})
	if err != nil {
		t.Fatalf("SubmitAsync: %v", err)
	}

	// Wait for completion.
	deadline := time.Now().Add(5 * time.Second)
	var status search.SearchJobStatus
	for time.Now().Before(deadline) {
		status, err = svc.GetAsyncStatus(ctx, jobID)
		if err != nil {
			t.Fatalf("GetAsyncStatus: %v", err)
		}
		if status.Status == "SUCCESSFUL" || status.Status == "FAILED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Status != "SUCCESSFUL" {
		t.Fatalf("expected SUCCESSFUL, got %s", status.Status)
	}
}

