package sqlite

import (
	"context"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// workflowStore implements spi.WorkflowStore backed by SQLite via KV store delegation.
// Full implementation will be added in a later task.
type workflowStore struct {
	kv *kvStore
}

func (s *workflowStore) Save(_ context.Context, _ spi.ModelRef, _ []spi.WorkflowDefinition) error {
	return fmt.Errorf("sqlite workflow store: not yet implemented")
}

func (s *workflowStore) Get(_ context.Context, _ spi.ModelRef) ([]spi.WorkflowDefinition, error) {
	return nil, fmt.Errorf("sqlite workflow store: not yet implemented")
}

func (s *workflowStore) Delete(_ context.Context, _ spi.ModelRef) error {
	return fmt.Errorf("sqlite workflow store: not yet implemented")
}
