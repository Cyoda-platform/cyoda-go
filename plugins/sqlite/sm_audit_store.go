package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// smAuditStore implements spi.StateMachineAuditStore backed by SQLite.
// Full implementation will be added in a later task.
type smAuditStore struct {
	db       *sql.DB
	tenantID spi.TenantID
}

func (s *smAuditStore) Record(_ context.Context, _ string, _ spi.StateMachineEvent) error {
	return fmt.Errorf("sqlite sm audit store: not yet implemented")
}

func (s *smAuditStore) GetEvents(_ context.Context, _ string) ([]spi.StateMachineEvent, error) {
	return nil, fmt.Errorf("sqlite sm audit store: not yet implemented")
}

func (s *smAuditStore) GetEventsByTransaction(_ context.Context, _ string, _ string) ([]spi.StateMachineEvent, error) {
	return nil, fmt.Errorf("sqlite sm audit store: not yet implemented")
}
