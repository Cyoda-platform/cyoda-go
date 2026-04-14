package spi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// SearchJob represents the persistent state of an async search operation.
type SearchJob struct {
	ID          string
	TenantID    common.TenantID
	Status      string // RUNNING, SUCCESSFUL, FAILED, CANCELLED
	ModelRef    common.ModelRef
	Condition   json.RawMessage
	PointInTime time.Time
	SearchOpts  json.RawMessage
	ResultCount int
	Error       string
	CreateTime  time.Time
	FinishTime  *time.Time
	CalcTimeMs  int64
}

// SelfExecutingSearchStore is implemented by AsyncSearchStore variants whose
// CreateJob method also kicks off per-shard execution and result persistence.
// The domain SearchService detects this via a type assertion after CreateJob
// and skips its own background-execution goroutine for these stores.
//
// Memory and Postgres do NOT implement this — their CreateJob only persists
// the job row, and the SearchService spawns a background goroutine to perform
// the actual search. A backend with native distributed execution can opt in
// by implementing this interface; its CreateJob is expected to dispatch work
// and persist results itself.
type SelfExecutingSearchStore interface {
	AsyncSearchStore
	SelfExecuting()
}

// AsyncSearchStore provides persistence for async search jobs and their results.
type AsyncSearchStore interface {
	CreateJob(ctx context.Context, job *SearchJob) error
	GetJob(ctx context.Context, jobID string) (*SearchJob, error)
	UpdateJobStatus(ctx context.Context, jobID string, status string, resultCount int, errMsg string, finishTime time.Time, calcTimeMs int64) error
	SaveResults(ctx context.Context, jobID string, entityIDs []string) error
	GetResultIDs(ctx context.Context, jobID string, offset, limit int) (entityIDs []string, total int, err error)
	DeleteJob(ctx context.Context, jobID string) error
	ReapExpired(ctx context.Context, ttl time.Duration) (int, error)
	// Cancel marks the job as CANCELLED. Idempotent: cancelling a job
	// already in a terminal state returns nil. Cancelling a non-existent
	// job returns common.ErrNotFound.
	Cancel(ctx context.Context, jobID string) error
}
