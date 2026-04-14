package spi

import (
	"context"
	"io"
	"iter"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

type StoreFactory interface {
	EntityStore(ctx context.Context) (EntityStore, error)
	ModelStore(ctx context.Context) (ModelStore, error)
	KeyValueStore(ctx context.Context) (KeyValueStore, error)
	MessageStore(ctx context.Context) (MessageStore, error)
	WorkflowStore(ctx context.Context) (WorkflowStore, error)
	StateMachineAuditStore(ctx context.Context) (StateMachineAuditStore, error)
	AsyncSearchStore(ctx context.Context) (AsyncSearchStore, error)
	Close() error
}

type EntityStore interface {
	Save(ctx context.Context, entity *common.Entity) (int64, error)
	// CompareAndSave saves the entity only if the current latest transaction ID matches expectedTxID.
	// Returns common.ErrConflict if the transaction ID has changed.
	CompareAndSave(ctx context.Context, entity *common.Entity, expectedTxID string) (int64, error)
	// SaveAll saves multiple entities, returning versions in iteration order.
	// Backends may execute saves concurrently. On error, returns the first
	// error encountered; partially-saved entities within an uncommitted
	// transaction are invisible to readers.
	SaveAll(ctx context.Context, entities iter.Seq[*common.Entity]) ([]int64, error)
	Get(ctx context.Context, entityID string) (*common.Entity, error)
	GetAsAt(ctx context.Context, entityID string, asAt time.Time) (*common.Entity, error)
	GetAll(ctx context.Context, modelRef common.ModelRef) ([]*common.Entity, error)
	GetAllAsAt(ctx context.Context, modelRef common.ModelRef, asAt time.Time) ([]*common.Entity, error)
	Delete(ctx context.Context, entityID string) error
	DeleteAll(ctx context.Context, modelRef common.ModelRef) error
	Exists(ctx context.Context, entityID string) (bool, error)
	Count(ctx context.Context, modelRef common.ModelRef) (int64, error)
	GetVersionHistory(ctx context.Context, entityID string) ([]common.EntityVersion, error)
}

type ModelStore interface {
	Save(ctx context.Context, desc *common.ModelDescriptor) error
	Get(ctx context.Context, modelRef common.ModelRef) (*common.ModelDescriptor, error)
	GetAll(ctx context.Context) ([]common.ModelRef, error)
	Delete(ctx context.Context, modelRef common.ModelRef) error
	Lock(ctx context.Context, modelRef common.ModelRef) error
	Unlock(ctx context.Context, modelRef common.ModelRef) error
	IsLocked(ctx context.Context, modelRef common.ModelRef) (bool, error)
	SetChangeLevel(ctx context.Context, modelRef common.ModelRef, level common.ChangeLevel) error
}

type KeyValueStore interface {
	Put(ctx context.Context, namespace string, key string, value []byte) error
	Get(ctx context.Context, namespace string, key string) ([]byte, error)
	Delete(ctx context.Context, namespace string, key string) error
	List(ctx context.Context, namespace string) (map[string][]byte, error)
}

type MessageStore interface {
	Save(ctx context.Context, id string, header common.MessageHeader, metaData common.MessageMetaData, payload io.Reader) error
	Get(ctx context.Context, id string) (common.MessageHeader, common.MessageMetaData, io.ReadCloser, error)
	Delete(ctx context.Context, id string) error
	DeleteBatch(ctx context.Context, ids []string) error
}

type WorkflowStore interface {
	Save(ctx context.Context, modelRef common.ModelRef, workflows []common.WorkflowDefinition) error
	Get(ctx context.Context, modelRef common.ModelRef) ([]common.WorkflowDefinition, error)
	Delete(ctx context.Context, modelRef common.ModelRef) error
}

type StateMachineAuditStore interface {
	Record(ctx context.Context, entityID string, event common.StateMachineEvent) error
	GetEvents(ctx context.Context, entityID string) ([]common.StateMachineEvent, error)
	GetEventsByTransaction(ctx context.Context, entityID string, transactionID string) ([]common.StateMachineEvent, error)
}
