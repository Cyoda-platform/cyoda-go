package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/search"
	"github.com/cyoda-platform/cyoda-go/plugins/memory"
)

// nilJobClaimStore is a real AsyncSearchStore with one deliberate defect:
// ClaimStale hands back a slice containing a nil element. ReclaimStaleJobs
// dereferences each returned *spi.SearchJob (job.StaleClaims, job.TenantID),
// so a nil there panics — and, in an unrecovered goroutine, takes the whole
// process down. No current plugin returns nil, so this is hardening: the
// reclaim tick must survive a misbehaving store the way every other
// engine-work goroutine does.
type nilJobClaimStore struct {
	spi.AsyncSearchStore
}

func (s *nilJobClaimStore) ClaimStale(context.Context, time.Duration, int) ([]*spi.SearchJob, error) {
	return []*spi.SearchJob{nil}, nil
}

// newReclaimService builds a SearchService over store with a bounded pool, so
// reclaimStaleTick has the headroom (pool.Cap() - registrySize()) it needs to
// reach ClaimStale rather than returning early.
func newReclaimService(t *testing.T, factory *memory.StoreFactory, store spi.AsyncSearchStore) *search.SearchService {
	t.Helper()
	pool := search.NewWorkerPool(2, 8)
	t.Cleanup(func() { pool.Drain(context.Background()) })
	return search.NewSearchService(factory, common.NewTestUUIDGenerator(), store).WithAsyncPool(pool)
}

// TestReclaimStaleTick_RecoversPanicAndLatchesHealth pins that one reclaim
// tick recovers a panic raised anywhere beneath it (here a nil job from a
// misbehaving ClaimStale), latches the node-health flag the same way the
// async-search executor's own recovery does, and returns normally so the
// ticker loop keeps running.
func TestReclaimStaleTick_RecoversPanicAndLatchesHealth(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	realAsync, err := factory.AsyncSearchStore(context.Background())
	if err != nil {
		t.Fatalf("AsyncSearchStore: %v", err)
	}
	svc := newReclaimService(t, factory, &nilJobClaimStore{AsyncSearchStore: realAsync})

	health := &atomic.Bool{}
	health.Store(true)

	// Must not panic out of the call.
	reclaimStaleTick(context.Background(), svc, 5*time.Minute, 3, health)

	if health.Load() {
		t.Error("healthFlag = true after a recovered panic in the reclaim tick; a node that has panicked has state nothing has verified")
	}
}

// TestReclaimStaleTick_HealthyStoreLeavesHealthAlone is the control: a tick
// against a well-behaved store (no stale jobs) must not latch the node
// unhealthy.
func TestReclaimStaleTick_HealthyStoreLeavesHealthAlone(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	realAsync, err := factory.AsyncSearchStore(context.Background())
	if err != nil {
		t.Fatalf("AsyncSearchStore: %v", err)
	}
	svc := newReclaimService(t, factory, realAsync)

	health := &atomic.Bool{}
	health.Store(true)

	reclaimStaleTick(context.Background(), svc, 5*time.Minute, 3, health)

	if !health.Load() {
		t.Error("healthFlag = false after an uneventful reclaim tick")
	}
}

// TestReapExpiredSnapshotsTick_HealthyStoreLeavesHealthAlone pins the split's
// other half: the snapshot-TTL sweep against a well-behaved store leaves
// health alone.
func TestReapExpiredSnapshotsTick_HealthyStoreLeavesHealthAlone(t *testing.T) {
	factory := memory.NewStoreFactory()
	defer factory.Close()
	realAsync, err := factory.AsyncSearchStore(context.Background())
	if err != nil {
		t.Fatalf("AsyncSearchStore: %v", err)
	}

	health := &atomic.Bool{}
	health.Store(true)

	reapExpiredSnapshotsTick(context.Background(), realAsync, time.Hour, health)

	if !health.Load() {
		t.Error("healthFlag = false after an uneventful snapshot reaper tick")
	}
}
