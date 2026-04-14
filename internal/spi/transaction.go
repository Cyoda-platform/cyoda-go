package spi

import (
	"context"
	"time"
)

type TransactionManager interface {
	Begin(ctx context.Context) (txID string, txCtx context.Context, err error)
	Commit(ctx context.Context, txID string) error
	Rollback(ctx context.Context, txID string) error
	Join(ctx context.Context, txID string) (txCtx context.Context, err error)
	GetSubmitTime(ctx context.Context, txID string) (time.Time, error)

	// Savepoint creates a named savepoint within the given transaction.
	Savepoint(ctx context.Context, txID string) (savepointID string, err error)

	// RollbackToSavepoint rolls back all work done since the savepoint was created.
	RollbackToSavepoint(ctx context.Context, txID string, savepointID string) error

	// ReleaseSavepoint releases a savepoint, merging its work into the parent transaction.
	ReleaseSavepoint(ctx context.Context, txID string, savepointID string) error
}
