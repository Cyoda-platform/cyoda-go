package postgres

import (
	"context"
	"fmt"
	"sort"
	"sync"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// txState holds per-transaction bookkeeping for first-committer-wins
// validation. One instance per active tx, indexed by txID on the
// TransactionManager.
//
// Invariants:
//   - An entity ID appears in at most one of readSet/writeSet at any time.
//   - readSet[id] = the version as first observed by a Get within this tx.
//   - writeSet[id] = the pre-write version for an entity we wrote; 0 for
//     a fresh insert.
//
// See docs/superpowers/specs/2026-04-15-postgres-si-first-committer-wins-design.md
// for the full semantic model.
type txState struct {
	mu         sync.Mutex
	tenantID   spi.TenantID
	readSet    map[string]int64
	writeSet   map[string]int64
	savepoints []savepointEntry
}

type savepointEntry struct {
	id       string
	readSet  map[string]int64
	writeSet map[string]int64
}

func newTxState(tenantID spi.TenantID) *txState {
	return &txState{
		tenantID: tenantID,
		readSet:  make(map[string]int64),
		writeSet: make(map[string]int64),
	}
}

// RecordRead records a read of the given entity at the given version.
//
// Invariants enforced:
//   - No-op if id ∈ writeSet: we wrote it; our own writes don't need
//     cross-tx read validation.
//   - No-op if id ∈ readSet: first-read-wins — we capture the version we
//     made decisions on, not a later re-read.
func (s *txState) RecordRead(id string, version int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Invariant: writeSet takes precedence — skip if we already wrote this entity.
	if _, inWrite := s.writeSet[id]; inWrite {
		return
	}
	// Invariant: first-read-wins — skip if already in readSet.
	if _, inRead := s.readSet[id]; inRead {
		return
	}
	s.readSet[id] = version
}

// RecordWrite records a write (save/delete) of the given entity with the
// given pre-write version. Pass 0 for a fresh insert.
//
// Invariants enforced:
//   - First-write-wins: if id ∈ writeSet, keep the original pre-write version.
//   - Promotion: if id ∈ readSet, move to writeSet using the readSet's captured
//     version (they agree by construction) and remove from readSet.
func (s *txState) RecordWrite(id string, preWriteVersion int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Invariant: first-write-wins — keep original pre-write version.
	if _, inWrite := s.writeSet[id]; inWrite {
		return
	}
	// Invariant: readSet promotion — if we read it, promote to writeSet using
	// the read's captured version (not the caller's preWriteVersion, which must
	// agree but the readSet version is the authoritative captured value).
	if readVersion, inRead := s.readSet[id]; inRead {
		s.writeSet[id] = readVersion
		delete(s.readSet, id)
		return
	}
	s.writeSet[id] = preWriteVersion
}

// SortedUnionIDs returns a sorted slice of all entity IDs appearing in
// either readSet or writeSet. The sorted order provides a deterministic
// lock-acquisition sequence for the commit-time validation phase.
//
// The readSet and writeSet are guaranteed disjoint by the RecordRead/RecordWrite
// invariants, so no deduplication is needed.
func (s *txState) SortedUnionIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.readSet)+len(s.writeSet))
	for id := range s.readSet {
		ids = append(ids, id)
	}
	for id := range s.writeSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SortedReadIDs returns a sorted slice of entity IDs in readSet only.
// Used by Commit to restrict the FOR SHARE validation query to entities we
// read but did not write in this transaction. Write-write conflicts are
// detected by PostgreSQL's tuple-level locks (SQLSTATE 40001), so writeSet
// entities do not need to be included in the validation query.
func (s *txState) SortedReadIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.readSet))
	for id := range s.readSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ValidateReadSet checks that every entity in readSet still exists in
// the DB at the captured version. Returns an error describing the first
// mismatch; nil if all match.
func (s *txState) ValidateReadSet(current map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, expected := range s.readSet {
		got, ok := current[id]
		if !ok {
			return fmt.Errorf("read-set validation: entity %s deleted by concurrent committer (expected version %d)", id, expected)
		}
		if got != expected {
			return fmt.Errorf("read-set validation: entity %s version changed: expected %d, current %d", id, expected, got)
		}
	}
	return nil
}

// ValidateWriteSet checks that every entity in writeSet is still at its
// captured pre-write version (for updates/deletes) or absent from the DB
// (for fresh inserts, pre-write version 0).
func (s *txState) ValidateWriteSet(current map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, expected := range s.writeSet {
		got, ok := current[id]
		if expected == 0 {
			if ok {
				return fmt.Errorf("write-set validation: entity %s lost insert race — concurrent committer created it at version %d", id, got)
			}
			continue
		}
		if !ok {
			return fmt.Errorf("write-set validation: entity %s deleted by concurrent committer (expected pre-write version %d)", id, expected)
		}
		if got != expected {
			return fmt.Errorf("write-set validation: entity %s pre-write version changed: expected %d, current %d", id, expected, got)
		}
	}
	return nil
}

// PushSavepoint stores a deep copy of the current readSet/writeSet under
// the given savepoint ID. Subsequent RestoreSavepoint(id) restores both
// sets to this snapshot and trims later savepoints (postgres nested
// savepoint semantics).
func (s *txState) PushSavepoint(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := savepointEntry{
		id:       id,
		readSet:  make(map[string]int64, len(s.readSet)),
		writeSet: make(map[string]int64, len(s.writeSet)),
	}
	for k, v := range s.readSet {
		snap.readSet[k] = v
	}
	for k, v := range s.writeSet {
		snap.writeSet[k] = v
	}
	s.savepoints = append(s.savepoints, snap)
}

// RestoreSavepoint restores readSet/writeSet to the snapshot captured at
// PushSavepoint(id) and trims any savepoints pushed after id. The named
// savepoint itself remains (mirroring postgres ROLLBACK TO SAVEPOINT).
func (s *txState) RestoreSavepoint(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, sp := range s.savepoints {
		if sp.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("unknown savepoint %q", id)
	}
	snap := s.savepoints[idx]
	s.readSet = make(map[string]int64, len(snap.readSet))
	s.writeSet = make(map[string]int64, len(snap.writeSet))
	for k, v := range snap.readSet {
		s.readSet[k] = v
	}
	for k, v := range snap.writeSet {
		s.writeSet[k] = v
	}
	s.savepoints = s.savepoints[:idx+1]
	return nil
}

// recordReadIfInTx records a read into the tx's state, if the context
// carries a transaction. No-op for non-tx reads.
func (tm *TransactionManager) recordReadIfInTx(ctx context.Context, entityID string, version int64) {
	txState := spi.GetTransaction(ctx)
	if txState == nil {
		return
	}
	s, ok := tm.lookupTxState(txState.ID)
	if !ok {
		return
	}
	s.RecordRead(entityID, version)
}

// recordWriteIfInTx records a write into the tx's state, if the context
// carries a transaction. No-op for non-tx writes.
func (tm *TransactionManager) recordWriteIfInTx(ctx context.Context, entityID string, preWriteVersion int64) {
	txState := spi.GetTransaction(ctx)
	if txState == nil {
		return
	}
	s, ok := tm.lookupTxState(txState.ID)
	if !ok {
		return
	}
	s.RecordWrite(entityID, preWriteVersion)
}

// ReleaseSavepoint drops the savepoint entry without touching the current
// readSet/writeSet — work done after the push is kept. Mirrors postgres
// RELEASE SAVEPOINT semantics.
func (s *txState) ReleaseSavepoint(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, sp := range s.savepoints {
		if sp.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("unknown savepoint %q", id)
	}
	s.savepoints = append(s.savepoints[:idx], s.savepoints[idx+1:]...)
	return nil
}
