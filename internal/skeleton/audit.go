package skeleton

import "context"

type AuditService struct{}

func NewAuditService() *AuditService { return &AuditService{} }

func (s *AuditService) Record(ctx context.Context, tenantID string, event any) error {
	return nil
}

func (s *AuditService) GetEntityAudit(ctx context.Context, tenantID string, entityID string) ([]byte, error) {
	return []byte("[]"), nil
}
