package search_test

// Driving tests for the stale-job RECLAIM reaper (task 8): ReclaimStaleJobs
// claims stale/released RUNNING async-search jobs and RE-EXECUTES them on this
// node, or FAILS those past the attempt cap (a bound on StaleClaims, executor
// losses). These are the two behaviour drivers the task asks for; the full
// matrix (zero-headroom, ClearResults-error, self-reclaim, Cap(),
// self-executing skip beyond the one below) is a later task.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go-spi/predicate"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
	"github.com/cyoda-platform/cyoda-go/internal/domain/search"
	"github.com/cyoda-platform/cyoda-go/plugins/memory"
)

// newReclaimTestService builds a memory-backed SearchService with a bounded
// pool so the reclaim sweep can actually re-execute a claimed job end to end.
// The pool is drained at cleanup so no worker goroutines outlive the test.
func newReclaimTestService(t *testing.T) (*search.SearchService, *memory.StoreFactory, spi.AsyncSearchStore) {
	t.Helper()
	factory := memory.NewStoreFactory()
	t.Cleanup(func() { factory.Close() })
	store, err := factory.AsyncSearchStore(context.Background())
	if err != nil {
		t.Fatalf("AsyncSearchStore: %v", err)
	}
	pool := search.NewWorkerPool(2, 8)
	t.Cleanup(func() { pool.Drain(context.Background()) })
	svc := search.NewSearchService(factory, common.NewTestUUIDGenerator(), store).
		WithAsyncPool(pool).
		WithHeartbeat(50 * time.Millisecond)
	return svc, factory, store
}

// createStaleReclaimJob persists a RUNNING job whose CreateTime is an hour in
// the past (so it is stale against any staleAfter well under an hour) and no
// heartbeat, with the condition and options envelope SubmitAsync itself
// persists — so decodeStoredJob can reconstruct and re-run it.
func createStaleReclaimJob(t *testing.T, store spi.AsyncSearchStore, tenant spi.TenantID, id string, ref spi.ModelRef, cond predicate.Condition, pit time.Time) {
	t.Helper()
	condJSON, err := json.Marshal(cond)
	if err != nil {
		t.Fatalf("marshal condition: %v", err)
	}
	optsJSON, err := json.Marshal(struct {
		Limit       int             `json:"limit"`
		PointInTime *time.Time      `json:"pointInTime,omitempty"`
		OrderBy     []spi.OrderSpec `json:"orderBy,omitempty"`
	}{Limit: 10, PointInTime: &pit})
	if err != nil {
		t.Fatalf("marshal opts: %v", err)
	}
	job := &spi.SearchJob{
		ID:         id,
		TenantID:   tenant,
		Status:     "RUNNING",
		ModelRef:   ref,
		Condition:  condJSON,
		SearchOpts: optsJSON,
		CreateTime: time.Now().Add(-time.Hour),
	}
	if err := store.CreateJob(tenantCtx(string(tenant)), job); err != nil {
		t.Fatalf("CreateJob(%s): %v", id, err)
	}
}

// (a) a stale job with StaleClaims below the attempt cap is re-enqueued and
// runs to SUCCESSFUL on this node — a crashed node's job is completed by a
// live node, not failed.
func TestReclaimStaleJobs_ReenqueuesStaleJobToSuccessful(t *testing.T) {
	svc, factory, store := newReclaimTestService(t)
	ctx := tenantCtx("tenant-a")
	ref := spi.ModelRef{EntityName: "person", ModelVersion: "1"}

	saveModelWithFields(t, ctx, factory, ref, map[string]schema.DataType{"name": schema.String})
	saveEntity(t, ctx, factory, ref, "e1", []byte(`{"name":"Alice"}`))
	saveEntity(t, ctx, factory, ref, "e2", []byte(`{"name":"Bob"}`))

	cond := &predicate.SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Alice"}
	createStaleReclaimJob(t, store, "tenant-a", "job-stale", ref, cond, time.Now())

	reenq, failed, err := svc.ReclaimStaleJobs(context.Background(), 5*time.Minute, 5)
	if err != nil {
		t.Fatalf("ReclaimStaleJobs: %v", err)
	}
	if reenq != 1 || failed != 0 {
		t.Fatalf("ReclaimStaleJobs = (reenqueued %d, failed %d), want (1, 0)", reenq, failed)
	}

	status := pollUntilTerminal(t, svc, ctx, "job-stale", 5*time.Second)
	if status.Status != "SUCCESSFUL" {
		t.Fatalf("reclaimed job status = %q, want SUCCESSFUL", status.Status)
	}

	ids, total, err := store.GetResultIDs(ctx, "job-stale", 0, 10)
	if err != nil {
		t.Fatalf("GetResultIDs: %v", err)
	}
	if total != 1 || len(ids) != 1 || ids[0] != "e1" {
		t.Fatalf("results = %v (total %d), want [e1]", ids, total)
	}
}

// (b) a job at the attempt cap (the staleness claim brings StaleClaims up to
// maxAttempts) is FAILED with the jobAttemptsExhausted message, not re-run.
func TestReclaimStaleJobs_AttemptCapFailsJob(t *testing.T) {
	svc, _, store := newReclaimTestService(t)
	ref := spi.ModelRef{EntityName: "person", ModelVersion: "1"}
	cond := &predicate.SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Alice"}
	createStaleReclaimJob(t, store, "tenant-a", "job-cap", ref, cond, time.Now())

	// maxAttempts=1: this first staleness claim bumps StaleClaims to 1, which
	// meets the cap, so the job is abandoned (FAILED) rather than re-run.
	reenq, failed, err := svc.ReclaimStaleJobs(context.Background(), 5*time.Minute, 1)
	if err != nil {
		t.Fatalf("ReclaimStaleJobs: %v", err)
	}
	if reenq != 0 || failed != 1 {
		t.Fatalf("ReclaimStaleJobs = (reenqueued %d, failed %d), want (0, 1)", reenq, failed)
	}

	got, err := store.GetJob(tenantCtx("tenant-a"), "job-cap")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != "FAILED" {
		t.Errorf("job status = %q, want FAILED", got.Status)
	}
	if got.Error != search.JobAttemptsExhausted() {
		t.Errorf("job error = %q, want %q", got.Error, search.JobAttemptsExhausted())
	}
}
