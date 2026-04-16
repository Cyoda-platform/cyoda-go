package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// asyncSearchStore implements spi.AsyncSearchStore backed by SQLite.
// Full implementation will be added in a later task.
type asyncSearchStore struct {
	db    *sql.DB
	clock Clock
}

func (s *asyncSearchStore) CreateJob(_ context.Context, _ *spi.SearchJob) error {
	return fmt.Errorf("sqlite search store: not yet implemented")
}

func (s *asyncSearchStore) GetJob(_ context.Context, _ string) (*spi.SearchJob, error) {
	return nil, fmt.Errorf("sqlite search store: not yet implemented")
}

func (s *asyncSearchStore) UpdateJobStatus(_ context.Context, _ string, _ string, _ int, _ string, _ time.Time, _ int64) error {
	return fmt.Errorf("sqlite search store: not yet implemented")
}

func (s *asyncSearchStore) SaveResults(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("sqlite search store: not yet implemented")
}

func (s *asyncSearchStore) GetResultIDs(_ context.Context, _ string, _, _ int) ([]string, int, error) {
	return nil, 0, fmt.Errorf("sqlite search store: not yet implemented")
}

func (s *asyncSearchStore) DeleteJob(_ context.Context, _ string) error {
	return fmt.Errorf("sqlite search store: not yet implemented")
}

func (s *asyncSearchStore) ReapExpired(_ context.Context, _ time.Duration) (int, error) {
	return 0, fmt.Errorf("sqlite search store: not yet implemented")
}

func (s *asyncSearchStore) Cancel(_ context.Context, _ string) error {
	return fmt.Errorf("sqlite search store: not yet implemented")
}
