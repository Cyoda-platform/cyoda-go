package spi

import "context"

type AuditService interface {
	Record(ctx context.Context, tenantID string, event any) error
	GetEntityAudit(ctx context.Context, tenantID string, entityID string) ([]byte, error)
}
