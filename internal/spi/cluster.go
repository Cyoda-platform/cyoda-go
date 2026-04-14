package spi

import "context"

type ClusterService interface {
	ListCalculationMembers(ctx context.Context) ([]byte, error)
	GetCalculationMember(ctx context.Context, memberID string) ([]byte, error)
	GetSummary(ctx context.Context) ([]byte, error)
}
