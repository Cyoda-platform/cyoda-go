package common

import (
	"encoding/binary"
	"sync/atomic"

	"github.com/google/uuid"
)

// UUIDGenerator generates time-based UUIDs.
type UUIDGenerator interface {
	NewTimeUUID() uuid.UUID
}

// DefaultUUIDGenerator generates real UUID v1 values.
type DefaultUUIDGenerator struct{}

func NewDefaultUUIDGenerator() *DefaultUUIDGenerator {
	return &DefaultUUIDGenerator{}
}

func (g *DefaultUUIDGenerator) NewTimeUUID() uuid.UUID {
	// uuid.NewUUID() only fails if the system clock is unavailable, which is
	// not a recoverable error in this context.
	id, _ := uuid.NewUUID() // v1
	return id
}

// TestUUIDGenerator generates deterministic sequential UUIDs for testing.
// The counter is atomic so the generator is safe for concurrent use.
type TestUUIDGenerator struct {
	counter atomic.Uint64
}

func NewTestUUIDGenerator() *TestUUIDGenerator {
	return &TestUUIDGenerator{}
}

func (g *TestUUIDGenerator) NewTimeUUID() uuid.UUID {
	n := g.counter.Add(1)
	var id uuid.UUID
	binary.BigEndian.PutUint64(id[0:8], n)
	// Set version to 1.
	id[6] = (id[6] & 0x0f) | 0x10
	// Set variant to RFC 4122.
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
