package common

import (
	"encoding/binary"
	"sync/atomic"

	"github.com/google/uuid"
)

// DefaultUUIDGenerator generates real UUID v1 values.
// Implements spi.UUIDGenerator (returns [16]byte; callers that want
// the uuid.UUID type perform a zero-cost type conversion).
type DefaultUUIDGenerator struct{}

func NewDefaultUUIDGenerator() *DefaultUUIDGenerator {
	return &DefaultUUIDGenerator{}
}

func (g *DefaultUUIDGenerator) NewTimeUUID() [16]byte {
	// uuid.NewUUID() only fails if the system clock is unavailable, which is
	// not a recoverable error in this context.
	id, _ := uuid.NewUUID() // v1
	return [16]byte(id)
}

// TestUUIDGenerator generates deterministic sequential UUIDs for testing.
// The counter is atomic so the generator is safe for concurrent use.
type TestUUIDGenerator struct {
	counter atomic.Uint64
}

func NewTestUUIDGenerator() *TestUUIDGenerator {
	return &TestUUIDGenerator{}
}

func (g *TestUUIDGenerator) NewTimeUUID() [16]byte {
	n := g.counter.Add(1)
	var id [16]byte
	binary.BigEndian.PutUint64(id[0:8], n)
	// Set version to 1.
	id[6] = (id[6] & 0x0f) | 0x10
	// Set variant to RFC 4122.
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
