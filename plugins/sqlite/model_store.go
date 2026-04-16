package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// modelStore implements spi.ModelStore backed by SQLite.
// Full implementation will be added in a later task.
type modelStore struct {
	db       *sql.DB
	tenantID spi.TenantID
}

func (s *modelStore) Save(_ context.Context, _ *spi.ModelDescriptor) error {
	return fmt.Errorf("sqlite model store: not yet implemented")
}

func (s *modelStore) Get(_ context.Context, _ spi.ModelRef) (*spi.ModelDescriptor, error) {
	return nil, fmt.Errorf("sqlite model store: not yet implemented")
}

func (s *modelStore) GetAll(_ context.Context) ([]spi.ModelRef, error) {
	return nil, fmt.Errorf("sqlite model store: not yet implemented")
}

func (s *modelStore) Delete(_ context.Context, _ spi.ModelRef) error {
	return fmt.Errorf("sqlite model store: not yet implemented")
}

func (s *modelStore) Lock(_ context.Context, _ spi.ModelRef) error {
	return fmt.Errorf("sqlite model store: not yet implemented")
}

func (s *modelStore) Unlock(_ context.Context, _ spi.ModelRef) error {
	return fmt.Errorf("sqlite model store: not yet implemented")
}

func (s *modelStore) IsLocked(_ context.Context, _ spi.ModelRef) (bool, error) {
	return false, fmt.Errorf("sqlite model store: not yet implemented")
}

func (s *modelStore) SetChangeLevel(_ context.Context, _ spi.ModelRef, _ spi.ChangeLevel) error {
	return fmt.Errorf("sqlite model store: not yet implemented")
}
