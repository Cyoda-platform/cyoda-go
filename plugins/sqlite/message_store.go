package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// messageStore implements spi.MessageStore backed by SQLite.
// Full implementation will be added in a later task.
type messageStore struct {
	db       *sql.DB
	tenantID spi.TenantID
}

func (s *messageStore) Save(_ context.Context, _ string, _ spi.MessageHeader, _ spi.MessageMetaData, _ io.Reader) error {
	return fmt.Errorf("sqlite message store: not yet implemented")
}

func (s *messageStore) Get(_ context.Context, _ string) (spi.MessageHeader, spi.MessageMetaData, io.ReadCloser, error) {
	return spi.MessageHeader{}, spi.MessageMetaData{}, nil, fmt.Errorf("sqlite message store: not yet implemented")
}

func (s *messageStore) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("sqlite message store: not yet implemented")
}

func (s *messageStore) DeleteBatch(_ context.Context, _ []string) error {
	return fmt.Errorf("sqlite message store: not yet implemented")
}
