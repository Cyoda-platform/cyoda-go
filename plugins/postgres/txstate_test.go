package postgres

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// TestNewTxState_ZeroValue verifies that a freshly constructed txState has
// the expected tenantID and empty/nil collections.
func TestNewTxState_ZeroValue(t *testing.T) {
	tid := spi.TenantID("tenant-1")
	s := newTxState(tid)

	if s.tenantID != tid {
		t.Errorf("tenantID = %q, want %q", s.tenantID, tid)
	}
	if s.readSet == nil {
		t.Error("readSet is nil, want empty map")
	}
	if len(s.readSet) != 0 {
		t.Errorf("readSet len = %d, want 0", len(s.readSet))
	}
	if s.writeSet == nil {
		t.Error("writeSet is nil, want empty map")
	}
	if len(s.writeSet) != 0 {
		t.Errorf("writeSet len = %d, want 0", len(s.writeSet))
	}
	if s.savepoints != nil {
		t.Errorf("savepoints = %v, want nil", s.savepoints)
	}
}

// TestRecordRead_FirstReadWins verifies that the first read version is
// captured and a subsequent read of the same entity is ignored.
func TestRecordRead_FirstReadWins(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	s.RecordRead("e1", 7) // should be ignored
	if got := s.readSet["e1"]; got != 5 {
		t.Errorf("readSet[e1] = %d, want 5", got)
	}
	if len(s.readSet) != 1 {
		t.Errorf("readSet len = %d, want 1", len(s.readSet))
	}
}

// TestRecordRead_SkipIfWritten verifies that RecordRead is a no-op when the
// entity is already in writeSet.
func TestRecordRead_SkipIfWritten(t *testing.T) {
	s := newTxState("t1")
	s.writeSet["e1"] = 3 // pre-populate directly
	s.RecordRead("e1", 7)
	if _, ok := s.readSet["e1"]; ok {
		t.Error("readSet should not contain e1 after writeSet pre-populate")
	}
	if s.writeSet["e1"] != 3 {
		t.Errorf("writeSet[e1] = %d, want 3", s.writeSet["e1"])
	}
}

// TestRecordRead_MultipleEntities verifies that distinct entities are
// recorded independently.
func TestRecordRead_MultipleEntities(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 1)
	s.RecordRead("e2", 2)
	s.RecordRead("e3", 3)
	if got := s.readSet["e1"]; got != 1 {
		t.Errorf("readSet[e1] = %d, want 1", got)
	}
	if got := s.readSet["e2"]; got != 2 {
		t.Errorf("readSet[e2] = %d, want 2", got)
	}
	if got := s.readSet["e3"]; got != 3 {
		t.Errorf("readSet[e3] = %d, want 3", got)
	}
	if len(s.readSet) != 3 {
		t.Errorf("readSet len = %d, want 3", len(s.readSet))
	}
}

// TestRecordWrite_FirstWriteWins verifies that the first write version is
// kept and subsequent writes of the same entity are ignored.
func TestRecordWrite_FirstWriteWins(t *testing.T) {
	s := newTxState("t1")
	s.RecordWrite("e1", 5)
	s.RecordWrite("e1", 7) // should be ignored
	if got := s.writeSet["e1"]; got != 5 {
		t.Errorf("writeSet[e1] = %d, want 5", got)
	}
	if len(s.writeSet) != 1 {
		t.Errorf("writeSet len = %d, want 1", len(s.writeSet))
	}
}

// TestRecordWrite_PromotesFromReadSet verifies that an entity already in
// readSet is promoted to writeSet using the readSet's captured version
// and removed from readSet.
func TestRecordWrite_PromotesFromReadSet(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	s.RecordWrite("e1", 5)
	if _, ok := s.readSet["e1"]; ok {
		t.Error("e1 should have been removed from readSet after promotion")
	}
	if got := s.writeSet["e1"]; got != 5 {
		t.Errorf("writeSet[e1] = %d, want 5", got)
	}
}

// TestRecordWrite_FreshInsertZero verifies that a fresh insert (version 0)
// is recorded in writeSet with value 0.
func TestRecordWrite_FreshInsertZero(t *testing.T) {
	s := newTxState("t1")
	s.RecordWrite("e1", 0)
	if got, ok := s.writeSet["e1"]; !ok || got != 0 {
		t.Errorf("writeSet[e1] = %d (ok=%v), want 0 and present", got, ok)
	}
}
