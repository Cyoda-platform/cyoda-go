package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// kvStore implements spi.KeyValueStore backed by SQLite.
// Full implementation will be added in a later task.
type kvStore struct {
	db       *sql.DB
	tenantID spi.TenantID
}

func (s *kvStore) Put(_ context.Context, _ string, _ string, _ []byte) error {
	return fmt.Errorf("sqlite kv store: not yet implemented")
}

func (s *kvStore) Get(_ context.Context, _ string, _ string) ([]byte, error) {
	return nil, fmt.Errorf("sqlite kv store: not yet implemented")
}

func (s *kvStore) Delete(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("sqlite kv store: not yet implemented")
}

func (s *kvStore) List(_ context.Context, _ string) (map[string][]byte, error) {
	return nil, fmt.Errorf("sqlite kv store: not yet implemented")
}
