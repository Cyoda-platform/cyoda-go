package memory_test

import (
	"encoding/binary"
	"sync/atomic"
)

// testUUIDGenerator produces deterministic sequential UUIDs for tests.
type testUUIDGenerator struct{ counter atomic.Uint64 }

func newTestUUIDGenerator() *testUUIDGenerator { return &testUUIDGenerator{} }

func (g *testUUIDGenerator) NewTimeUUID() [16]byte {
	n := g.counter.Add(1)
	var id [16]byte
	binary.BigEndian.PutUint64(id[0:8], n)
	id[6] = (id[6] & 0x0f) | 0x10 // v1 marker
	id[8] = (id[8] & 0x3f) | 0x80 // RFC 4122 variant
	return id
}
