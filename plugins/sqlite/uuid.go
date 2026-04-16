package sqlite

import (
	"github.com/google/uuid"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// defaultUUIDGenerator produces real UUID v4 values. Used by NewFactory
// to initialize the plugin's TransactionManager.
type defaultUUIDGenerator struct{}

func (g *defaultUUIDGenerator) NewTimeUUID() [16]byte {
	// uuid.New() generates a v4 UUID and panics on failure, which is
	// appropriate — if the system can't generate random bytes, the
	// process is fundamentally broken.
	return [16]byte(uuid.New())
}

var _ spi.UUIDGenerator = (*defaultUUIDGenerator)(nil)
