package search

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/match"

	"github.com/cyoda-platform/cyoda-go-spi/predicate"
)

// SearchOptions controls search behavior.
type SearchOptions struct {
	PointInTime     *time.Time
	Limit           int
	Offset          int
	PerShardTimeout *time.Duration // nil means use node default; ignored by memory/postgres
	AllowUnbounded  bool           // opt into "no per-shard timeout"; ignored by memory/postgres
}

// ResultOptions controls pagination when retrieving async search results.
type ResultOptions struct {
	Limit  int
	Offset int
}

// SearchJobStatus reports the current state of an async search job.
type SearchJobStatus struct {
	JobID      string
	Status     string // "RUNNING", "SUCCESSFUL", "FAILED", "CANCELLED"
	Total      int
	CreateTime time.Time
	FinishTime *time.Time
	CalcTimeMs int64
}

// SnapshotStatus is a transport-friendly summary of an async search job's state.
type SnapshotStatus struct {
	SnapshotID    string
	Status        string // RUNNING, SUCCESSFUL, FAILED, CANCELLED, NOT_FOUND
	EntitiesCount int
}

// SearchService provides synchronous and asynchronous entity search over
// the in-memory entity store, evaluating predicate conditions.
type SearchService struct {
	factory     spi.StoreFactory
	uuids       spi.UUIDGenerator
	searchStore spi.AsyncSearchStore
}

// NewSearchService creates a SearchService backed by the given store factory.
func NewSearchService(factory spi.StoreFactory, uuids spi.UUIDGenerator, searchStore spi.AsyncSearchStore) *SearchService {
	return &SearchService{
		factory:     factory,
		uuids:       uuids,
		searchStore: searchStore,
	}
}

// Search performs a synchronous entity search, returning matching entities.
func (s *SearchService) Search(ctx context.Context, modelRef spi.ModelRef, cond predicate.Condition, opts SearchOptions) ([]*spi.Entity, error) {
	store, err := s.factory.EntityStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get entity store: %w", err)
	}

	var entities []*spi.Entity
	if opts.PointInTime != nil {
		entities, err = store.GetAllAsAt(ctx, modelRef, *opts.PointInTime)
	} else {
		entities, err = store.GetAll(ctx, modelRef)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve entities: %w", err)
	}

	var matches []*spi.Entity
	for _, e := range entities {
		ok, matchErr := match.Match(cond, e.Data, e.Meta)
		if matchErr != nil {
			return nil, fmt.Errorf("predicate match failed: %w", matchErr)
		}
		if ok {
			matches = append(matches, e)
		}
	}

	// Apply offset.
	if opts.Offset > 0 {
		if opts.Offset >= len(matches) {
			return nil, nil
		}
		matches = matches[opts.Offset:]
	}

	// Apply limit (default 1000).
	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit < len(matches) {
		matches = matches[:limit]
	}

	return matches, nil
}

// SubmitAsync starts an asynchronous search job and returns the job ID.
func (s *SearchService) SubmitAsync(ctx context.Context, modelRef spi.ModelRef, cond predicate.Condition, opts SearchOptions) (string, error) {
	uc := spi.GetUserContext(ctx)
	if uc == nil {
		return "", fmt.Errorf("no user context — cannot determine tenant")
	}

	if opts.PointInTime == nil {
		now := time.Now()
		opts.PointInTime = &now
	}

	jobID := uuid.UUID(s.uuids.NewTimeUUID()).String()
	now := time.Now()

	condJSON, err := json.Marshal(cond)
	if err != nil {
		return "", fmt.Errorf("failed to marshal search condition: %w", err)
	}

	optsJSON, err := json.Marshal(struct {
		Limit       int        `json:"limit"`
		Offset      int        `json:"offset"`
		PointInTime *time.Time `json:"pointInTime,omitempty"`
	}{
		Limit:       opts.Limit,
		Offset:      opts.Offset,
		PointInTime: opts.PointInTime,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal search options: %w", err)
	}

	job := &spi.SearchJob{
		ID:          jobID,
		TenantID:    uc.Tenant.ID,
		Status:      "RUNNING",
		ModelRef:    modelRef,
		Condition:   condJSON,
		SearchOpts:  optsJSON,
		PointInTime: *opts.PointInTime,
		CreateTime:  now,
	}

	if err := s.searchStore.CreateJob(ctx, job); err != nil {
		return "", fmt.Errorf("failed to create search job: %w", err)
	}

	// Self-executing stores handle per-shard execution and result persistence
	// themselves via their own consumer/executor pipeline. Skip the in-process
	// goroutine for them — calling SaveResults or UpdateJobStatus on a
	// self-executing store is an error.
	if _, ok := s.searchStore.(spi.SelfExecutingSearchStore); ok {
		return jobID, nil
	}

	// Create a background context with the same UserContext so the search
	// can proceed after the HTTP request completes.
	bgCtx := spi.WithUserContext(context.Background(), uc)

	go func() {
		start := time.Now()
		results, searchErr := s.Search(bgCtx, modelRef, cond, opts)
		elapsed := time.Since(start)
		finishTime := time.Now()
		calcTimeMs := elapsed.Milliseconds()

		// Check if cancelled before saving results.
		currentJob, getErr := s.searchStore.GetJob(bgCtx, jobID)
		if getErr != nil {
			slog.Error("failed to get search job for status check", "pkg", "search", "jobID", jobID, "err", getErr)
			return
		}
		if currentJob.Status == "CANCELLED" {
			return
		}

		if searchErr != nil {
			if err := s.searchStore.UpdateJobStatus(bgCtx, jobID, "FAILED", 0, searchErr.Error(), finishTime, calcTimeMs); err != nil {
				slog.Error("failed to update search job status", "pkg", "search", "jobID", jobID, "err", err)
			}
			return
		}

		var ids []string
		for _, e := range results {
			ids = append(ids, e.Meta.ID)
		}

		if err := s.searchStore.SaveResults(bgCtx, jobID, ids); err != nil {
			slog.Error("failed to save search results", "pkg", "search", "jobID", jobID, "err", err)
			_ = s.searchStore.UpdateJobStatus(bgCtx, jobID, "FAILED", 0, err.Error(), finishTime, calcTimeMs)
			return
		}

		// Re-check status after SaveResults to guard against cancel race:
		// CancelAsync may have set CANCELLED between the first check and here.
		currentJob, getErr = s.searchStore.GetJob(bgCtx, jobID)
		if getErr != nil {
			slog.Error("failed to re-check search job status", "pkg", "search", "jobID", jobID, "err", getErr)
			return
		}
		if currentJob.Status != "RUNNING" {
			slog.Debug("search job status changed during execution, skipping update", "pkg", "search", "jobID", jobID, "status", currentJob.Status)
			return
		}

		if err := s.searchStore.UpdateJobStatus(bgCtx, jobID, "SUCCESSFUL", len(ids), "", finishTime, calcTimeMs); err != nil {
			slog.Error("failed to update search job status", "pkg", "search", "jobID", jobID, "err", err)
		}
	}()

	return jobID, nil
}

// GetAsyncStatus returns the current status of an async search job.
func (s *SearchService) GetAsyncStatus(ctx context.Context, jobID string) (SearchJobStatus, error) {
	job, err := s.searchStore.GetJob(ctx, jobID)
	if err != nil {
		return SearchJobStatus{}, fmt.Errorf("job %s not found", jobID)
	}

	return SearchJobStatus{
		JobID:      job.ID,
		Status:     job.Status,
		Total:      job.ResultCount,
		CreateTime: job.CreateTime,
		FinishTime: job.FinishTime,
		CalcTimeMs: job.CalcTimeMs,
	}, nil
}

// AsyncResultsPage holds a page of async search results along with the total count.
type AsyncResultsPage struct {
	Results []*spi.Entity
	Total   int
}

// GetAsyncResults returns the results of a completed async search job.
func (s *SearchService) GetAsyncResults(ctx context.Context, jobID string, opts ResultOptions) (AsyncResultsPage, error) {
	job, err := s.searchStore.GetJob(ctx, jobID)
	if err != nil {
		return AsyncResultsPage{}, fmt.Errorf("job %s not found", jobID)
	}

	if job.Status != "SUCCESSFUL" {
		return AsyncResultsPage{}, fmt.Errorf("job %s is not complete (status: %s)", jobID, job.Status)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}

	ids, total, err := s.searchStore.GetResultIDs(ctx, jobID, opts.Offset, limit)
	if err != nil {
		return AsyncResultsPage{}, fmt.Errorf("failed to get result IDs: %w", err)
	}

	entityStore, err := s.factory.EntityStore(ctx)
	if err != nil {
		return AsyncResultsPage{}, fmt.Errorf("failed to get entity store: %w", err)
	}

	var results []*spi.Entity
	for _, id := range ids {
		e, err := entityStore.GetAsAt(ctx, id, job.PointInTime)
		if err != nil {
			slog.Warn("failed to fetch entity for async result", "pkg", "search", "entityId", id, "err", err)
			continue
		}
		results = append(results, e)
	}

	return AsyncResultsPage{Results: results, Total: total}, nil
}

// CancelResult holds the outcome of a cancel attempt.
type CancelResult struct {
	Cancelled     bool
	CurrentStatus string
}

// CancelAsync attempts to cancel a running async search job.
// Returns a CancelResult indicating whether the job was cancelled and its current status.
func (s *SearchService) CancelAsync(ctx context.Context, jobID string) (CancelResult, error) {
	job, err := s.searchStore.GetJob(ctx, jobID)
	if err != nil {
		return CancelResult{}, fmt.Errorf("job %s not found", jobID)
	}

	if job.Status != "RUNNING" {
		return CancelResult{Cancelled: false, CurrentStatus: job.Status}, nil
	}

	finishTime := time.Now()
	if err := s.searchStore.UpdateJobStatus(ctx, jobID, "CANCELLED", 0, "", finishTime, 0); err != nil {
		return CancelResult{}, fmt.Errorf("failed to cancel job: %w", err)
	}

	return CancelResult{Cancelled: true, CurrentStatus: "CANCELLED"}, nil
}

// ---------------------------------------------------------------------------
// Transport-independent service methods (for gRPC / non-HTTP callers)
// ---------------------------------------------------------------------------

// SubmitAsyncSearch starts an asynchronous search job and returns the job ID.
// This is an alias for SubmitAsync, provided for transport-independent callers.
func (s *SearchService) SubmitAsyncSearch(ctx context.Context, modelRef spi.ModelRef, cond predicate.Condition, opts SearchOptions) (string, error) {
	return s.SubmitAsync(ctx, modelRef, cond, opts)
}

// DirectSearch performs a synchronous entity search, returning matching entities.
// This is an alias for Search, provided for transport-independent callers.
func (s *SearchService) DirectSearch(ctx context.Context, modelRef spi.ModelRef, cond predicate.Condition, opts SearchOptions) ([]*spi.Entity, error) {
	return s.Search(ctx, modelRef, cond, opts)
}

// GetAsyncSearchStatus returns a transport-friendly SnapshotStatus for the given job.
func (s *SearchService) GetAsyncSearchStatus(ctx context.Context, snapshotID string) (*SnapshotStatus, error) {
	status, err := s.GetAsyncStatus(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	return &SnapshotStatus{
		SnapshotID:    status.JobID,
		Status:        status.Status,
		EntitiesCount: status.Total,
	}, nil
}

// GetAsyncSearchResults returns a page of results for a completed async search job.
func (s *SearchService) GetAsyncSearchResults(ctx context.Context, snapshotID string, page, size int) ([]*spi.Entity, error) {
	if size <= 0 {
		size = 1000
	}
	opts := ResultOptions{
		Offset: page * size,
		Limit:  size,
	}
	resultPage, err := s.GetAsyncResults(ctx, snapshotID, opts)
	if err != nil {
		return nil, err
	}
	return resultPage.Results, nil
}

// CancelAsyncSearch attempts to cancel a running async search job.
func (s *SearchService) CancelAsyncSearch(ctx context.Context, snapshotID string) error {
	_, err := s.CancelAsync(ctx, snapshotID)
	return err
}
