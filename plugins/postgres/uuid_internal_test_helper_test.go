package postgres

import (
	"encoding/binary"
	"sync/atomic"
)

// testUUIDGenerator (internal test variant) for *_test.go files in package postgres.
type testUUIDGenerator struct{ counter atomic.Uint64 }

func newTestUUIDGenerator() *testUUIDGenerator { return &testUUIDGenerator{} }

func (g *testUUIDGenerator) NewTimeUUID() [16]byte {
	n := g.counter.Add(1)
	var id [16]byte
	binary.BigEndian.PutUint64(id[0:8], n)
	id[6] = (id[6] & 0x0f) | 0x10
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
