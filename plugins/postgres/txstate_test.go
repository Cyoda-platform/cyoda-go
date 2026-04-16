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
