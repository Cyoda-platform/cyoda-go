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
