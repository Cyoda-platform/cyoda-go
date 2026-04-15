package memory

import (
	"github.com/google/uuid"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// defaultUUIDGenerator produces real UUID v1 values. Used by NewFactory
// to initialize the plugin's TransactionManager.
type defaultUUIDGenerator struct{}

func (g *defaultUUIDGenerator) NewTimeUUID() [16]byte {
	id, _ := uuid.NewUUID() // v1; only fails if the system clock is unavailable
	return [16]byte(id)
}

var _ spi.UUIDGenerator = (*defaultUUIDGenerator)(nil)
