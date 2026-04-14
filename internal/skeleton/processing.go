package skeleton

import (
	"context"
	"encoding/json"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

type ExternalProcessingService struct{}

func NewExternalProcessingService() *ExternalProcessingService {
	return &ExternalProcessingService{}
}

func (s *ExternalProcessingService) DispatchProcessor(_ context.Context, entity *common.Entity, _ common.ProcessorDefinition, _ string, _ string, _ string) (*common.Entity, error) {
	return entity, nil
}

func (s *ExternalProcessingService) DispatchCriteria(_ context.Context, _ *common.Entity, _ json.RawMessage, _ string, _ string, _ string, _ string, _ string) (bool, error) {
	return true, nil
}
