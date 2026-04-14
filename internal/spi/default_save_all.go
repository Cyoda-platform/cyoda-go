package spi

import (
	"context"
	"iter"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// DefaultSaveAll is the sequential fallback for EntityStore.SaveAll.
// It calls store.Save for each entity in order and stops on the first error.
// Backends that don't need concurrent saves delegate their SaveAll to this.
func DefaultSaveAll(store EntityStore, ctx context.Context, entities iter.Seq[*common.Entity]) ([]int64, error) {
	var versions []int64
	for entity := range entities {
		v, err := store.Save(ctx, entity)
		if err != nil {
			return versions, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}
