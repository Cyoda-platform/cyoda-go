package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// entityStore implements spi.EntityStore backed by SQLite.
// Full implementation will be added in a later task.
type entityStore struct {
	db       *sql.DB
	tenantID spi.TenantID
	tm       *transactionManager
	clock    Clock
	cfg      config
}

func (s *entityStore) Save(_ context.Context, _ *spi.Entity) (int64, error) {
	return 0, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) CompareAndSave(_ context.Context, _ *spi.Entity, _ string) (int64, error) {
	return 0, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) SaveAll(_ context.Context, _ iter.Seq[*spi.Entity]) ([]int64, error) {
	return nil, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) Get(_ context.Context, _ string) (*spi.Entity, error) {
	return nil, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) GetAsAt(_ context.Context, _ string, _ time.Time) (*spi.Entity, error) {
	return nil, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) GetAll(_ context.Context, _ spi.ModelRef) ([]*spi.Entity, error) {
	return nil, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) GetAllAsAt(_ context.Context, _ spi.ModelRef, _ time.Time) ([]*spi.Entity, error) {
	return nil, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) DeleteAll(_ context.Context, _ spi.ModelRef) error {
	return fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) Exists(_ context.Context, _ string) (bool, error) {
	return false, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) Count(_ context.Context, _ spi.ModelRef) (int64, error) {
	return 0, fmt.Errorf("sqlite entity store: not yet implemented")
}

func (s *entityStore) GetVersionHistory(_ context.Context, _ string) ([]spi.EntityVersion, error) {
	return nil, fmt.Errorf("sqlite entity store: not yet implemented")
}
