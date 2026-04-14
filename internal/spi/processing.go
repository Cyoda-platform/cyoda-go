package spi

import (
	"context"
	"encoding/json"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// ExternalProcessingService dispatches processor execution and criteria evaluation
// to external calculation nodes.
type ExternalProcessingService interface {
	DispatchProcessor(ctx context.Context, entity *common.Entity, processor common.ProcessorDefinition, workflowName string, transitionName string, txID string) (*common.Entity, error)
	DispatchCriteria(ctx context.Context, entity *common.Entity, criterion json.RawMessage, target string, workflowName string, transitionName string, processorName string, txID string) (bool, error)
}
