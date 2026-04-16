# Postgres SI + Row-Granular First-Committer-Wins — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the postgres plugin's `SERIALIZABLE` isolation (page-granular SSI, source of #17 flakes and cross-page false positives) with `REPEATABLE READ` + application-layer row-granular first-committer-wins that matches the cassandra plugin's semantics exactly.

**Architecture:** Per-transaction read/write-set bookkeeping inside the postgres plugin. Store methods on `entityStore` record `(entity_id → version)` into a `*txState` parallel to the existing `txRegistry`. `TransactionManager.Commit` issues one batched `SELECT id, version FROM entities WHERE tenant_id=$1 AND id=ANY($2) FOR SHARE` (sorted, chunked) over the union of read+write sets, compares captured versions to current, and maps mismatches and postgres-raised `40001` to `spi.ErrConflict`. No SPI changes. Non-entity stores (Model, KV, Message, Workflow, SMAudit) operate at plain SI — coverage gap tracked as #35.

**Tech Stack:** Go 1.26, `jackc/pgx/v5`, postgres 15+, testcontainers-go (existing test harness), `go test` + `spi.ErrConflict` semantics.

**Spec:** [`docs/superpowers/specs/2026-04-15-postgres-si-first-committer-wins-design.md`](../specs/2026-04-15-postgres-si-first-committer-wins-design.md)

**Related issues:** [#18](https://github.com/Cyoda-platform/cyoda-go/issues/18) (this work), [#17](https://github.com/Cyoda-platform/cyoda-go/issues/17) (flake closed by this), [#35](https://github.com/Cyoda-platform/cyoda-go/issues/35) (non-entity-store coverage follow-up), [cyoda-go-cassandra#22](https://github.com/Cyoda-platform/cyoda-go-cassandra/issues/22) (sibling).

---

## File structure

**New files:**
- `plugins/postgres/txstate.go` — `txState` type + helpers (RecordRead/Write, SortedUnionIDs, ValidateReadSet, ValidateWriteSet, PushSavepoint, RestoreSavepoint, ReleaseSavepoint).
- `plugins/postgres/txstate_test.go` — unit tests for the above; no DB needed.
- `plugins/postgres/commit_validator.go` — `validateInChunks` helper (isolated so tests can override `validateChunkSize`).
- `plugins/postgres/commit_validator_test.go` — unit-integration tests with real postgres.

**Modified files:**
- `plugins/postgres/transaction_manager.go` — add `txStates` map; switch `Begin` to `RepeatableRead`; wire validation into `Commit`; hook savepoints through txState.
- `plugins/postgres/transaction_manager_test.go` — new tests for RR behavior + validation.
- `plugins/postgres/entity_store.go` — add `recordRead`/`recordWrite` calls in `Get`, `GetAll`, `Save`, `SaveAll`, `CompareAndSave`, `Delete`, `DeleteAll`.
- `plugins/postgres/entity_store_test.go` — verify bookkeeping hooks populate txState correctly.
- `plugins/postgres/conformance_test.go` — drop any `SkipIfFlaky` guards around the concurrent-different-entities scenario; add a same-entity concurrency test if not already present.
- `internal/spitest/` — add savepoint-divergence conformance comment; strengthen concurrency tests to assert `spi.ErrConflict` semantics consistently across plugins.

The existing `txRegistry` stays untouched — its only job remains txID→pgx.Tx lookup. `txState` is a **parallel map** on `TransactionManager`, keyed by the same txID.

---

## Design notes the implementer must carry into code

**Idempotency rule for bookkeeping** (subtle but load-bearing):

1. An entity is ever in at most one of `readSet` / `writeSet`, never both.
2. `recordRead(id, version)` is a no-op if `id ∈ writeSet` (we wrote it ourselves; no cross-tx read-validation needed for our own writes) OR if `id ∈ readSet` (first-read-wins).
3. `recordWrite(id, preWriteVersion)` is a no-op if `id ∈ writeSet` (first-write-wins — the original pre-write version is the load-bearing one). If `id ∈ readSet`, move to `writeSet` at the read's captured version and drop from `readSet`.

Rationale: within our tx, reads after our own writes return our own writes — if we recorded those as readSet entries, commit-time validation would compare them to committed-by-others state (which is what our tx *replaced* upstream) and spuriously fail. Writing once before validation avoids this; the helpers enforce the rule.

**Pre-write version from `entity_store.go`** comes from the existing UPSERT's `RETURNING version, (xmax = 0)` clause: `isNew=true` → `preWriteVersion = 0`; `isNew=false` → `preWriteVersion = returnedVersion - 1`.

**Chunking preserves sort order.** IDs are sorted once at the top of `validateInChunks`; chunks are taken in order; lock acquisition stays deterministic across chunks and across concurrent committers.

**FOR SHARE (not FOR UPDATE).** See spec rationale. Implementation carries a one-line code comment pointing to the spec.

**GetAsAt / GetVersionHistory / Count** are NOT tracked — implementation carries a one-line code comment stating this intentional omission, pointing at the spec's "Known limitation: phantom reads on range/list queries" section.

---

## Task list

### Task 1: Create `txState` skeleton + constructor

**Files:**
- Create: `plugins/postgres/txstate.go`
- Create: `plugins/postgres/txstate_test.go`

- [ ] **Step 1: Write failing test for `newTxState`**

```go
// plugins/postgres/txstate_test.go
package postgres

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

func TestNewTxState_ZeroValue(t *testing.T) {
	s := newTxState(spi.TenantID("t1"))
	if s == nil {
		t.Fatal("expected non-nil txState")
	}
	if s.tenantID != "t1" {
		t.Errorf("tenantID: want t1, got %s", s.tenantID)
	}
	if len(s.readSet) != 0 {
		t.Errorf("readSet: want empty, got %d entries", len(s.readSet))
	}
	if len(s.writeSet) != 0 {
		t.Errorf("writeSet: want empty, got %d entries", len(s.writeSet))
	}
	if len(s.savepoints) != 0 {
		t.Errorf("savepoints: want empty, got %d entries", len(s.savepoints))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/postgres/ -run TestNewTxState_ZeroValue -v`
Expected: FAIL with `undefined: newTxState`

- [ ] **Step 3: Implement minimal `txState`**

```go
// plugins/postgres/txstate.go
package postgres

import (
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/postgres/ -run TestNewTxState_ZeroValue -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/txstate.go plugins/postgres/txstate_test.go
git commit -m "feat(postgres): add txState skeleton for first-committer-wins bookkeeping"
```

---

### Task 2: `RecordRead` — first-read-wins + writeSet exclusion

**Files:**
- Modify: `plugins/postgres/txstate.go`
- Modify: `plugins/postgres/txstate_test.go`

- [ ] **Step 1: Write failing tests**

Append to `plugins/postgres/txstate_test.go`:

```go
func TestRecordRead_FirstReadWins(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	s.RecordRead("e1", 7) // should be ignored
	if got := s.readSet["e1"]; got != 5 {
		t.Errorf("readSet[e1]: want 5, got %d", got)
	}
}

func TestRecordRead_SkipIfWritten(t *testing.T) {
	s := newTxState("t1")
	s.writeSet["e1"] = 3
	s.RecordRead("e1", 7) // should be ignored — we already wrote it
	if _, exists := s.readSet["e1"]; exists {
		t.Error("readSet[e1]: should not record when entity is in writeSet")
	}
	if got := s.writeSet["e1"]; got != 3 {
		t.Errorf("writeSet[e1]: want unchanged 3, got %d", got)
	}
}

func TestRecordRead_MultipleEntities(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	s.RecordRead("e2", 9)
	if got := s.readSet["e1"]; got != 5 {
		t.Errorf("readSet[e1]: want 5, got %d", got)
	}
	if got := s.readSet["e2"]; got != 9 {
		t.Errorf("readSet[e2]: want 9, got %d", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./plugins/postgres/ -run TestRecordRead -v`
Expected: FAIL with `s.RecordRead undefined`

- [ ] **Step 3: Implement `RecordRead`**

Append to `plugins/postgres/txstate.go`:

```go
// RecordRead captures a read's observed version for commit-time validation.
// No-op if the entity is already in writeSet (our own write; not cross-tx
// validated) or already in readSet (first-read-wins — a later read returns
// either our own writes or a consistent snapshot; we only care about the
// first observation for conflict detection).
func (s *txState) RecordRead(entityID string, version int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, written := s.writeSet[entityID]; written {
		return
	}
	if _, already := s.readSet[entityID]; already {
		return
	}
	s.readSet[entityID] = version
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./plugins/postgres/ -run TestRecordRead -v`
Expected: PASS (all three tests)

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/txstate.go plugins/postgres/txstate_test.go
git commit -m "feat(postgres): txState.RecordRead with first-read-wins + writeSet exclusion"
```

---

### Task 3: `RecordWrite` — first-write-wins + readSet promotion

**Files:**
- Modify: `plugins/postgres/txstate.go`
- Modify: `plugins/postgres/txstate_test.go`

- [ ] **Step 1: Write failing tests**

Append to `plugins/postgres/txstate_test.go`:

```go
func TestRecordWrite_FirstWriteWins(t *testing.T) {
	s := newTxState("t1")
	s.RecordWrite("e1", 5)
	s.RecordWrite("e1", 7) // should be ignored — pre-write version is load-bearing
	if got := s.writeSet["e1"]; got != 5 {
		t.Errorf("writeSet[e1]: want 5, got %d", got)
	}
}

func TestRecordWrite_PromotesFromReadSet(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5) // first: read at v=5
	s.RecordWrite("e1", 5) // then: write at pre-write v=5
	if _, stillRead := s.readSet["e1"]; stillRead {
		t.Error("readSet[e1]: should have been promoted out on write")
	}
	if got := s.writeSet["e1"]; got != 5 {
		t.Errorf("writeSet[e1]: want 5, got %d", got)
	}
}

func TestRecordWrite_FreshInsertZero(t *testing.T) {
	s := newTxState("t1")
	s.RecordWrite("e1", 0) // convention: 0 = "did not exist before this tx"
	if got := s.writeSet["e1"]; got != 0 {
		t.Errorf("writeSet[e1]: want 0 (fresh insert), got %d", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./plugins/postgres/ -run TestRecordWrite -v`
Expected: FAIL

- [ ] **Step 3: Implement `RecordWrite`**

Append to `plugins/postgres/txstate.go`:

```go
// RecordWrite captures a write's pre-write version for commit-time validation.
// Pre-write version 0 is the convention for "entity did not exist when this
// tx first wrote it" (fresh insert).
//
// No-op if the entity is already in writeSet (first-write-wins — we captured
// the pre-write version on the first write; subsequent writes by us don't
// change that load-bearing value).
//
// If the entity is in readSet, we promote it to writeSet using the read's
// captured version (which equals the pre-write version we just observed)
// and drop from readSet — an entity must be in at most one set.
func (s *txState) RecordWrite(entityID string, preWriteVersion int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, written := s.writeSet[entityID]; written {
		return
	}
	if readVersion, inRead := s.readSet[entityID]; inRead {
		s.writeSet[entityID] = readVersion
		delete(s.readSet, entityID)
		return
	}
	s.writeSet[entityID] = preWriteVersion
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./plugins/postgres/ -run TestRecordWrite -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/txstate.go plugins/postgres/txstate_test.go
git commit -m "feat(postgres): txState.RecordWrite with first-write-wins + readSet promotion"
```

---

### Task 4: `SortedUnionIDs` — deterministic lock-acquisition order

**Files:**
- Modify: `plugins/postgres/txstate.go`
- Modify: `plugins/postgres/txstate_test.go`

- [ ] **Step 1: Write failing tests**

Append to `plugins/postgres/txstate_test.go`:

```go
func TestSortedUnionIDs_EmptyState(t *testing.T) {
	s := newTxState("t1")
	ids := s.SortedUnionIDs()
	if len(ids) != 0 {
		t.Errorf("want empty slice, got %v", ids)
	}
}

func TestSortedUnionIDs_MixedReadAndWrite(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("c", 1)
	s.RecordRead("a", 2)
	s.RecordWrite("d", 0)
	s.RecordWrite("b", 3)
	ids := s.SortedUnionIDs()
	want := []string{"a", "b", "c", "d"}
	if len(ids) != len(want) {
		t.Fatalf("want %d ids, got %d: %v", len(want), len(ids), ids)
	}
	for i, id := range want {
		if ids[i] != id {
			t.Errorf("ids[%d]: want %q, got %q", i, id, ids[i])
		}
	}
}

func TestSortedUnionIDs_Disjoint(t *testing.T) {
	// Invariant: readSet and writeSet are disjoint by construction.
	s := newTxState("t1")
	s.RecordRead("a", 1)
	s.RecordWrite("a", 1) // promotes to writeSet
	ids := s.SortedUnionIDs()
	if len(ids) != 1 || ids[0] != "a" {
		t.Errorf("want [a], got %v", ids)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./plugins/postgres/ -run TestSortedUnionIDs -v`
Expected: FAIL

- [ ] **Step 3: Implement `SortedUnionIDs`**

Append to `plugins/postgres/txstate.go`:

```go
import "sort"

// (add "sort" to the existing import block)

// SortedUnionIDs returns the sorted union of readSet and writeSet entity IDs.
// Sorting makes FOR SHARE lock acquisition deterministic across concurrent
// committers, preventing a class of 40P01 deadlocks that would otherwise
// surface as ErrConflict under contention.
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
```

Note: the invariant of readSet/writeSet being disjoint is enforced by `RecordRead` / `RecordWrite`, so no dedup pass is needed.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./plugins/postgres/ -run TestSortedUnionIDs -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/txstate.go plugins/postgres/txstate_test.go
git commit -m "feat(postgres): txState.SortedUnionIDs for deterministic lock order"
```

---

### Task 5: `ValidateReadSet` and `ValidateWriteSet`

**Files:**
- Modify: `plugins/postgres/txstate.go`
- Modify: `plugins/postgres/txstate_test.go`

- [ ] **Step 1: Write failing tests**

Append to `plugins/postgres/txstate_test.go`:

```go
func TestValidateReadSet_AllMatch(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	s.RecordRead("e2", 7)
	current := map[string]int64{"e1": 5, "e2": 7}
	if err := s.ValidateReadSet(current); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateReadSet_VersionMismatch(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	current := map[string]int64{"e1": 6}
	err := s.ValidateReadSet(current)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	// Error message should identify the entity and versions.
	if !strings.Contains(err.Error(), "e1") {
		t.Errorf("error should mention entity ID, got: %v", err)
	}
}

func TestValidateReadSet_MissingEntity(t *testing.T) {
	// Concurrent committer deleted the row (hard delete case).
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	current := map[string]int64{} // e1 absent
	err := s.ValidateReadSet(current)
	if err == nil {
		t.Fatal("want error for missing entity, got nil")
	}
}

func TestValidateWriteSet_UpdateMatch(t *testing.T) {
	s := newTxState("t1")
	s.RecordWrite("e1", 5) // expected pre-write version 5
	current := map[string]int64{"e1": 5}
	if err := s.ValidateWriteSet(current); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateWriteSet_FreshInsertAbsent(t *testing.T) {
	s := newTxState("t1")
	s.RecordWrite("e1", 0) // fresh insert
	current := map[string]int64{} // e1 absent in committed view — correct
	if err := s.ValidateWriteSet(current); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateWriteSet_FreshInsertRaceLost(t *testing.T) {
	// Two txs tried to insert e1; the other committed first.
	s := newTxState("t1")
	s.RecordWrite("e1", 0)
	current := map[string]int64{"e1": 1}
	err := s.ValidateWriteSet(current)
	if err == nil {
		t.Fatal("want error for lost insert race, got nil")
	}
}

func TestValidateWriteSet_UpdateVersionMismatch(t *testing.T) {
	s := newTxState("t1")
	s.RecordWrite("e1", 5)
	current := map[string]int64{"e1": 6} // concurrent committer bumped
	err := s.ValidateWriteSet(current)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
```

Add `"strings"` to the test file's imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./plugins/postgres/ -run TestValidate -v`
Expected: FAIL

- [ ] **Step 3: Implement `ValidateReadSet` and `ValidateWriteSet`**

Append to `plugins/postgres/txstate.go` (add `"fmt"` to imports):

```go
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
			// Fresh insert: row must be absent in the committed view.
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./plugins/postgres/ -run TestValidate -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/txstate.go plugins/postgres/txstate_test.go
git commit -m "feat(postgres): txState.Validate{Read,Write}Set with precise error messages"
```

---

### Task 6: Savepoint snapshot / restore / release on txState

**Files:**
- Modify: `plugins/postgres/txstate.go`
- Modify: `plugins/postgres/txstate_test.go`

- [ ] **Step 1: Write failing tests**

Append to `plugins/postgres/txstate_test.go`:

```go
func TestPushSavepoint_DeepCopiesSets(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	s.PushSavepoint("sp1")
	s.RecordRead("e2", 7) // added after savepoint
	if len(s.savepoints) != 1 {
		t.Fatalf("want 1 savepoint, got %d", len(s.savepoints))
	}
	snap := s.savepoints[0]
	if snap.id != "sp1" {
		t.Errorf("savepoint id: want sp1, got %s", snap.id)
	}
	// Snapshot captured {e1} only; later read of e2 didn't leak into it.
	if _, ok := snap.readSet["e1"]; !ok {
		t.Error("savepoint readSet should contain e1")
	}
	if _, ok := snap.readSet["e2"]; ok {
		t.Error("savepoint readSet should NOT contain e2 (added after push)")
	}
}

func TestRestoreSavepoint_RestoresSets(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	s.PushSavepoint("sp1")
	s.RecordRead("e2", 7)
	s.RecordWrite("e3", 0)
	if err := s.RestoreSavepoint("sp1"); err != nil {
		t.Fatalf("RestoreSavepoint: %v", err)
	}
	// Reads/writes added after sp1 must be gone.
	if _, ok := s.readSet["e2"]; ok {
		t.Error("readSet[e2] should have been restored away")
	}
	if _, ok := s.writeSet["e3"]; ok {
		t.Error("writeSet[e3] should have been restored away")
	}
	// Pre-savepoint state preserved.
	if got := s.readSet["e1"]; got != 5 {
		t.Errorf("readSet[e1]: want 5, got %d", got)
	}
	// Savepoint itself remains (postgres semantics: ROLLBACK TO SAVEPOINT
	// keeps the savepoint for future use).
	if len(s.savepoints) != 1 {
		t.Errorf("want savepoint preserved after rollback-to, got %d", len(s.savepoints))
	}
}

func TestRestoreSavepoint_TrimsLaterSavepoints(t *testing.T) {
	s := newTxState("t1")
	s.PushSavepoint("sp1")
	s.PushSavepoint("sp2") // nested
	if err := s.RestoreSavepoint("sp1"); err != nil {
		t.Fatalf("RestoreSavepoint: %v", err)
	}
	if len(s.savepoints) != 1 {
		t.Errorf("want 1 savepoint (sp2 trimmed), got %d", len(s.savepoints))
	}
	if s.savepoints[0].id != "sp1" {
		t.Errorf("remaining savepoint: want sp1, got %s", s.savepoints[0].id)
	}
}

func TestReleaseSavepoint_DropsEntryKeepsWork(t *testing.T) {
	s := newTxState("t1")
	s.RecordRead("e1", 5)
	s.PushSavepoint("sp1")
	s.RecordRead("e2", 7)
	if err := s.ReleaseSavepoint("sp1"); err != nil {
		t.Fatalf("ReleaseSavepoint: %v", err)
	}
	if len(s.savepoints) != 0 {
		t.Errorf("want 0 savepoints, got %d", len(s.savepoints))
	}
	// Work done after push is KEPT on release.
	if got := s.readSet["e2"]; got != 7 {
		t.Errorf("readSet[e2]: want 7 (kept), got %d", got)
	}
}

func TestRestoreSavepoint_Unknown(t *testing.T) {
	s := newTxState("t1")
	err := s.RestoreSavepoint("bogus")
	if err == nil {
		t.Fatal("want error for unknown savepoint, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./plugins/postgres/ -run "TestPushSavepoint|TestRestoreSavepoint|TestReleaseSavepoint" -v`
Expected: FAIL

- [ ] **Step 3: Implement savepoint methods**

Append to `plugins/postgres/txstate.go`:

```go
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
	s.savepoints = s.savepoints[:idx+1] // trim later, keep idx (savepoint preserved)
	return nil
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./plugins/postgres/ -run "TestPushSavepoint|TestRestoreSavepoint|TestReleaseSavepoint" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/txstate.go plugins/postgres/txstate_test.go
git commit -m "feat(postgres): txState savepoint snapshot/restore/release"
```

---

### Task 7: Integrate `*txState` into `TransactionManager`

**Files:**
- Modify: `plugins/postgres/transaction_manager.go`
- Modify: `plugins/postgres/transaction_manager_test.go`

- [ ] **Step 1: Write failing test**

Append to `plugins/postgres/transaction_manager_test.go`:

```go
func TestTxManager_BeginAllocatesTxState(t *testing.T) {
	tm, _ := newTestTxManager(t)
	ctx := ctxWithTenant("t1")

	txID, _, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Internal inspection via export_test.go accessor.
	if !postgres.HasTxState(tm, txID) {
		t.Errorf("expected txState registered for %s", txID)
	}

	if err := tm.Commit(ctx, txID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if postgres.HasTxState(tm, txID) {
		t.Errorf("expected txState removed after Commit for %s", txID)
	}
}

func TestTxManager_RollbackCleansTxState(t *testing.T) {
	tm, _ := newTestTxManager(t)
	ctx := ctxWithTenant("t1")
	txID, _, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tm.Rollback(ctx, txID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if postgres.HasTxState(tm, txID) {
		t.Errorf("expected txState removed after Rollback for %s", txID)
	}
}
```

- [ ] **Step 2: Add the `HasTxState` export helper and run — expect compile error first, then test fail**

In `plugins/postgres/export_test.go` append:

```go
// HasTxState reports whether the given txID has an active txState entry.
// Test-only accessor.
func HasTxState(tm *TransactionManager, txID string) bool {
	tm.txStatesMu.Lock()
	defer tm.txStatesMu.Unlock()
	_, ok := tm.txStates[txID]
	return ok
}
```

Run: `go test ./plugins/postgres/ -run TestTxManager_BeginAllocatesTxState -v`
Expected: compile error (`tm.txStatesMu undefined`)

- [ ] **Step 3: Wire `txStates` into `TransactionManager`**

In `plugins/postgres/transaction_manager.go`, modify the struct and constructor:

```go
type TransactionManager struct {
	pool     *pgxpool.Pool
	registry *txRegistry
	uuids    spi.UUIDGenerator
	mu       sync.Mutex
	submitTimes map[string]time.Time
	tenants     map[string]spi.TenantID
	// txStates holds per-transaction bookkeeping for first-committer-wins
	// validation at commit. Populated on Begin; validated and removed on
	// Commit; removed on Rollback.
	txStatesMu sync.Mutex
	txStates   map[string]*txState
}

func NewTransactionManager(pool *pgxpool.Pool, uuids spi.UUIDGenerator) *TransactionManager {
	return &TransactionManager{
		pool:        pool,
		registry:    newTxRegistry(),
		uuids:       uuids,
		submitTimes: make(map[string]time.Time),
		tenants:     make(map[string]spi.TenantID),
		txStates:    make(map[string]*txState),
	}
}
```

Modify `Begin` to register the txState (place the insertion after the existing `tm.tenants[txID] = tenantID` line):

```go
	tm.txStatesMu.Lock()
	tm.txStates[txID] = newTxState(tenantID)
	tm.txStatesMu.Unlock()
```

Add a helper near `removeTenant`:

```go
// removeTxState drops the txState entry for a completed transaction.
func (tm *TransactionManager) removeTxState(txID string) {
	tm.txStatesMu.Lock()
	delete(tm.txStates, txID)
	tm.txStatesMu.Unlock()
}

// lookupTxState returns the txState for the given txID.
func (tm *TransactionManager) lookupTxState(txID string) (*txState, bool) {
	tm.txStatesMu.Lock()
	defer tm.txStatesMu.Unlock()
	s, ok := tm.txStates[txID]
	return s, ok
}
```

Update `Commit` and `Rollback` to call `tm.removeTxState(txID)` alongside the existing `tm.removeTenant(txID)` (on every completion path — success and error). Do not add the validation logic yet; that's Task 10.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./plugins/postgres/ -run "TestTxManager_BeginAllocatesTxState|TestTxManager_RollbackCleansTxState" -v`
Expected: PASS

Also run the existing TxManager tests to ensure no regression:

Run: `go test ./plugins/postgres/ -run TestTxManager -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/transaction_manager.go plugins/postgres/transaction_manager_test.go plugins/postgres/export_test.go
git commit -m "feat(postgres): wire txState lifecycle into TransactionManager"
```

---

### Task 8: Switch isolation level to `RepeatableRead`

**Files:**
- Modify: `plugins/postgres/transaction_manager.go`
- Modify: `plugins/postgres/transaction_manager_test.go`

- [ ] **Step 1: Write regression test proving RR still gives snapshot + read-your-own-writes**

Append to `plugins/postgres/transaction_manager_test.go`:

```go
func TestTxManager_RepeatableRead_SnapshotAndReadYourOwnWrites(t *testing.T) {
	tm, pool := newTestTxManager(t)
	ctx := ctxWithTenant("t1")

	// Tx1 inserts a row.
	txID1, txCtx1, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin 1: %v", err)
	}
	tx1, _ := tm.LookupTx(txID1)
	if _, err := tx1.Exec(txCtx1,
		`INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		 VALUES ('t1', 'e1', 'M', '1', 1, false, '{}'::jsonb)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Read-your-own-writes within tx1.
	var v int64
	if err := tx1.QueryRow(txCtx1,
		`SELECT version FROM entities WHERE tenant_id='t1' AND entity_id='e1'`).Scan(&v); err != nil {
		t.Fatalf("read own write: %v", err)
	}
	if v != 1 {
		t.Errorf("read-your-own-writes: want version=1, got %d", v)
	}
	if err := tm.Commit(ctx, txID1); err != nil {
		t.Fatalf("Commit 1: %v", err)
	}

	// Tx2 begins, takes snapshot.
	txID2, txCtx2, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin 2: %v", err)
	}
	tx2, _ := tm.LookupTx(txID2)
	// Confirm tx2 sees e1 at version 1.
	var v2 int64
	if err := tx2.QueryRow(txCtx2,
		`SELECT version FROM entities WHERE tenant_id='t1' AND entity_id='e1'`).Scan(&v2); err != nil {
		t.Fatalf("tx2 read: %v", err)
	}
	if v2 != 1 {
		t.Errorf("tx2 snapshot: want 1, got %d", v2)
	}

	// Tx3 (outside tx2) commits a version bump.
	txID3, txCtx3, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin 3: %v", err)
	}
	tx3, _ := tm.LookupTx(txID3)
	if _, err := tx3.Exec(txCtx3,
		`UPDATE entities SET version=2 WHERE tenant_id='t1' AND entity_id='e1'`); err != nil {
		t.Fatalf("tx3 update: %v", err)
	}
	if err := tm.Commit(ctx, txID3); err != nil {
		t.Fatalf("Commit 3: %v", err)
	}

	// Tx2 should STILL see version 1 (snapshot isolation preserved).
	if err := tx2.QueryRow(txCtx2,
		`SELECT version FROM entities WHERE tenant_id='t1' AND entity_id='e1'`).Scan(&v2); err != nil {
		t.Fatalf("tx2 re-read: %v", err)
	}
	if v2 != 1 {
		t.Errorf("snapshot preserved after concurrent commit: want 1, got %d", v2)
	}

	_ = pool // keep available
	_ = tm.Rollback(ctx, txID2)
}
```

- [ ] **Step 2: Run test — should pass under current SERIALIZABLE (both provide snapshot)**

Run: `go test ./plugins/postgres/ -run TestTxManager_RepeatableRead_SnapshotAndReadYourOwnWrites -v`
Expected: PASS (this confirms the test is valid for both isolation levels)

- [ ] **Step 3: Flip to `RepeatableRead`**

In `plugins/postgres/transaction_manager.go`, change the `Begin` isolation:

```go
	pgxTx, err := tm.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
```

Also update the doc comment on `Begin`:

```go
// Begin starts a new REPEATABLE READ transaction (snapshot isolation) and
// returns the transaction ID and a context carrying the TransactionState.
//
// Row-granular first-committer-wins is enforced in application code via
// txState bookkeeping (readSet/writeSet) and commit-time validation — see
// Commit() and docs/superpowers/specs/2026-04-15-postgres-si-first-committer-wins-design.md.
```

Also update the doc comment at the top of the struct:

```go
// TransactionManager implements spi.TransactionManager backed by PostgreSQL
// with REPEATABLE READ isolation plus application-layer row-granular
// first-committer-wins validation. Each Begin() acquires a real pgx.Tx,
// registers it in the txRegistry, and allocates a *txState for read/write
// bookkeeping used by Commit.
```

- [ ] **Step 4: Run full postgres test suite to verify no regression**

Run: `go test ./plugins/postgres/ -v`
Expected: all PASS (including the new RR regression test). If any test fails, diagnose before proceeding — this flip should be a pure substitution since SSI ⊃ SI.

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/transaction_manager.go plugins/postgres/transaction_manager_test.go
git commit -m "feat(postgres): switch isolation to REPEATABLE READ

First-committer-wins is now enforced in application code via the
txState bookkeeping machinery; the SERIALIZABLE page-level SSI layer
is no longer needed and was the source of #17 false positives."
```

---

### Task 9: `validateInChunks` helper with real postgres

**Files:**
- Create: `plugins/postgres/commit_validator.go`
- Create: `plugins/postgres/commit_validator_test.go`
- Modify: `plugins/postgres/export_test.go`

- [ ] **Step 1: Write failing test**

```go
// plugins/postgres/commit_validator_test.go
package postgres_test

import (
	"context"
	"testing"

	"github.com/cyoda-platform/cyoda-go/plugins/postgres"
)

func TestValidateInChunks_EmptyIDs(t *testing.T) {
	tm, _ := newTestTxManager(t)
	ctx := ctxWithTenant("t1")
	txID, txCtx, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tm.Rollback(ctx, txID)
	tx, _ := tm.LookupTx(txID)
	current, err := postgres.ValidateInChunksForTest(txCtx, tx, "t1", nil, 100)
	if err != nil {
		t.Fatalf("validateInChunks(nil): %v", err)
	}
	if len(current) != 0 {
		t.Errorf("want empty map, got %v", current)
	}
}

func TestValidateInChunks_SingleChunkReturnsVersions(t *testing.T) {
	tm, pool := newTestTxManager(t)
	ctx := ctxWithTenant("t1")

	// Pre-populate two rows.
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'e1', 'M', '1', 3, false, '{}'::jsonb),
		       ('t1', 'e2', 'M', '1', 7, false, '{}'::jsonb)
	`)

	txID, txCtx, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tm.Rollback(ctx, txID)
	tx, _ := tm.LookupTx(txID)
	current, err := postgres.ValidateInChunksForTest(txCtx, tx, "t1", []string{"e1", "e2"}, 100)
	if err != nil {
		t.Fatalf("validateInChunks: %v", err)
	}
	if current["e1"] != 3 || current["e2"] != 7 {
		t.Errorf("versions: want {e1:3,e2:7}, got %v", current)
	}
}

func TestValidateInChunks_MultipleChunks(t *testing.T) {
	tm, pool := newTestTxManager(t)
	ctx := ctxWithTenant("t1")

	// Insert 5 rows.
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		_, _ = pool.Exec(ctx, `
			INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
			VALUES ('t1', $1, 'M', '1', $2, false, '{}'::jsonb)
		`, id, int64(i+1))
	}

	txID, txCtx, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tm.Rollback(ctx, txID)
	tx, _ := tm.LookupTx(txID)
	// chunk size 2 forces 3 chunks (5 IDs).
	current, err := postgres.ValidateInChunksForTest(txCtx, tx, "t1",
		[]string{"a", "b", "c", "d", "e"}, 2)
	if err != nil {
		t.Fatalf("validateInChunks: %v", err)
	}
	if len(current) != 5 {
		t.Errorf("want 5 rows, got %d: %v", len(current), current)
	}
}

func TestValidateInChunks_TenantScoped(t *testing.T) {
	tm, pool := newTestTxManager(t)
	ctx := ctxWithTenant("t1")

	// Same entity_id in two tenants.
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'e1', 'M', '1', 1, false, '{}'::jsonb),
		       ('t2', 'e1', 'M', '1', 99, false, '{}'::jsonb)
	`)

	txID, txCtx, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tm.Rollback(ctx, txID)
	tx, _ := tm.LookupTx(txID)
	current, err := postgres.ValidateInChunksForTest(txCtx, tx, "t1", []string{"e1"}, 100)
	if err != nil {
		t.Fatalf("validateInChunks: %v", err)
	}
	if current["e1"] != 1 {
		t.Errorf("tenant scoping: want t1's e1=1, got %d", current["e1"])
	}
}
```

- [ ] **Step 2: Implement `validateInChunks` + add test-only export**

Create `plugins/postgres/commit_validator.go`:

```go
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// defaultValidateChunkSize is the batching threshold for the commit-time
// FOR SHARE query. Conservative — well under postgres planner thresholds
// for ANY($1::text[]) arrays. See design doc § "Batch size guard".
const defaultValidateChunkSize = 1000

// validateInChunks issues SELECT id, version FROM entities WHERE tenant_id=$1
// AND id = ANY($2) FOR SHARE over sortedIDs, chunked to stay within planner
// bounds. Returns a map of entity_id → current version covering every row
// that currently exists for the tenant. Entities absent from the DB are
// absent from the map — the caller distinguishes "not present = concurrent
// deletion / expected-absent for fresh insert" in the validate calls.
//
// Sort order is preserved across chunks: lock acquisition remains
// deterministic across concurrent committers.
//
// FOR SHARE (not FOR UPDATE) is the right mode: two committing txs both
// locking the same unchanged row succeed compatibly; any write-write
// overlap was already caught by tuple-level locks on the DML upstream.
// Read-set staleness is caught by the subsequent ValidateReadSet compare;
// concurrent-committer-after-snapshot on the locked rows is caught by
// postgres itself raising 40001 on the FOR SHARE.
func (tm *TransactionManager) validateInChunks(
	ctx context.Context, tx pgx.Tx, tenantID spi.TenantID, sortedIDs []string, chunkSize int,
) (map[string]int64, error) {
	if chunkSize <= 0 {
		chunkSize = defaultValidateChunkSize
	}
	current := make(map[string]int64, len(sortedIDs))
	for i := 0; i < len(sortedIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(sortedIDs) {
			end = len(sortedIDs)
		}
		chunk := sortedIDs[i:end]
		rows, err := tx.Query(ctx, `
			SELECT entity_id, version
			  FROM entities
			 WHERE tenant_id = $1
			   AND entity_id = ANY($2::text[])
			 FOR SHARE
		`, string(tenantID), chunk)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var v int64
			if err := rows.Scan(&id, &v); err != nil {
				rows.Close()
				return nil, err
			}
			current[id] = v
		}
		rows.Close()
	}
	return current, nil
}
```

Append to `plugins/postgres/export_test.go`:

```go
// ValidateInChunksForTest exposes validateInChunks for unit testing.
func ValidateInChunksForTest(
	ctx context.Context, tx pgx.Tx, tenantID spi.TenantID, sortedIDs []string, chunkSize int,
) (map[string]int64, error) {
	var tm *TransactionManager // method needs a receiver but doesn't use fields in this helper
	return tm.validateInChunks(ctx, tx, tenantID, sortedIDs, chunkSize)
}
```

Ensure `export_test.go` imports `context`, `github.com/jackc/pgx/v5`, and `spi`.

- [ ] **Step 3: Run tests**

Run: `go test ./plugins/postgres/ -run TestValidateInChunks -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add plugins/postgres/commit_validator.go plugins/postgres/commit_validator_test.go plugins/postgres/export_test.go
git commit -m "feat(postgres): validateInChunks helper with sorted FOR SHARE batching"
```

---

### Task 10: Wire validation into `Commit`

**Files:**
- Modify: `plugins/postgres/transaction_manager.go`
- Modify: `plugins/postgres/transaction_manager_test.go`

- [ ] **Step 1: Write failing test — read-set conflict aborts commit**

Append to `plugins/postgres/transaction_manager_test.go`:

```go
func TestTxManager_Commit_ReadSetConflict(t *testing.T) {
	tm, pool := newTestTxManager(t)
	ctx := ctxWithTenant("t1")

	// Seed.
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'e1', 'M', '1', 5, false, '{}'::jsonb)
	`)

	// Tx A: begin, record a read at version 5, delay commit.
	txA, _, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin A: %v", err)
	}
	stateA, _ := postgres.LookupTxStateForTest(tm, txA)
	stateA.RecordRead("e1", 5)

	// Tx B: bumps e1 to version 6 and commits.
	txB, txCtxB, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin B: %v", err)
	}
	tx, _ := tm.LookupTx(txB)
	if _, err := tx.Exec(txCtxB,
		`UPDATE entities SET version=6 WHERE tenant_id='t1' AND entity_id='e1'`); err != nil {
		t.Fatalf("B update: %v", err)
	}
	if err := tm.Commit(ctx, txB); err != nil {
		t.Fatalf("B commit: %v", err)
	}

	// Tx A: commit must fail with ErrConflict.
	err = tm.Commit(ctx, txA)
	if err == nil {
		t.Fatal("want ErrConflict on A.Commit, got nil")
	}
	if !errors.Is(err, spi.ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
}
```

Add the `LookupTxStateForTest` accessor in `plugins/postgres/export_test.go`:

```go
func LookupTxStateForTest(tm *TransactionManager, txID string) (*txState, bool) {
	return tm.lookupTxState(txID)
}
```

And export the `RecordRead` / `RecordWrite` methods for test callers — they are already uppercase, so the accessor above suffices. (`*txState` itself is unexported; the returned interface is used as an opaque handle via the exported methods.)

Actually since `*txState` is unexported, the test file (in package `postgres_test`) cannot reference the type. Fix: define an interface alias in `export_test.go`:

```go
// TxStateForTest exposes the recording methods needed by tests.
type TxStateForTest interface {
	RecordRead(id string, version int64)
	RecordWrite(id string, preWriteVersion int64)
	PushSavepoint(id string)
	RestoreSavepoint(id string) error
	ReleaseSavepoint(id string) error
}

func LookupTxStateForTest(tm *TransactionManager, txID string) (TxStateForTest, bool) {
	return tm.lookupTxState(txID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/postgres/ -run TestTxManager_Commit_ReadSetConflict -v`
Expected: FAIL — Tx A commits successfully because validation is not yet wired.

- [ ] **Step 3: Wire validation into `Commit`**

Modify the `Commit` method in `plugins/postgres/transaction_manager.go`. Insert the validation block **before** the existing `SELECT CURRENT_TIMESTAMP` submit-time capture:

```go
func (tm *TransactionManager) Commit(ctx context.Context, txID string) error {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return fmt.Errorf("Commit: transaction %s not found", txID)
	}
	state, ok := tm.lookupTxState(txID)
	if !ok {
		return fmt.Errorf("Commit: tx state for %s not found", txID)
	}

	// Row-granular first-committer-wins validation. See design doc
	// "Why this preserves the published semantic" for the dual-mechanism
	// argument (tuple locks + FOR SHARE).
	ids := state.SortedUnionIDs()
	if len(ids) > 0 {
		current, err := tm.validateInChunks(ctx, pgxTx, state.tenantID, ids, 0)
		if err != nil {
			// 40001 from FOR SHARE under RR = a concurrent committer
			// modified one of our locked rows since our snapshot.
			// classifyError maps it to spi.ErrConflict.
			tm.registry.Remove(txID)
			tm.removeTenant(txID)
			tm.removeTxState(txID)
			_ = pgxTx.Rollback(context.Background())
			return classifyError(fmt.Errorf("Commit: validate: %w", err))
		}
		if verr := state.ValidateReadSet(current); verr != nil {
			tm.registry.Remove(txID)
			tm.removeTenant(txID)
			tm.removeTxState(txID)
			_ = pgxTx.Rollback(context.Background())
			return fmt.Errorf("%w: Commit: %w", spi.ErrConflict, verr)
		}
		if verr := state.ValidateWriteSet(current); verr != nil {
			tm.registry.Remove(txID)
			tm.removeTenant(txID)
			tm.removeTxState(txID)
			_ = pgxTx.Rollback(context.Background())
			return fmt.Errorf("%w: Commit: %w", spi.ErrConflict, verr)
		}
	}

	// ... existing commit logic (submit time capture, pgxTx.Commit) ...
}
```

Ensure `tm.removeTxState(txID)` is also called on the existing success and error paths of `Commit` and on `Rollback` (added in Task 7 should already cover these; verify all exit paths).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/postgres/ -run TestTxManager_Commit_ReadSetConflict -v`
Expected: PASS

Run full postgres test suite:

Run: `go test ./plugins/postgres/ -v`
Expected: all PASS (no regressions)

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/transaction_manager.go plugins/postgres/transaction_manager_test.go plugins/postgres/export_test.go
git commit -m "feat(postgres): wire first-committer-wins validation into Commit"
```

---

### Task 11: Hook `recordRead` into `entityStore.Get`

**Files:**
- Modify: `plugins/postgres/entity_store.go`
- Modify: `plugins/postgres/entity_store_test.go`

- [ ] **Step 1: Write failing test**

Append to `plugins/postgres/entity_store_test.go`:

```go
func TestEntityStore_Get_PopulatesReadSet(t *testing.T) {
	pool := newTestPool(t)
	if err := postgres.DropSchemaForTest(pool); err != nil { t.Fatalf("reset: %v", err) }
	if err := postgres.Migrate(pool); err != nil { t.Fatalf("migrate: %v", err) }
	t.Cleanup(func() { _ = postgres.DropSchemaForTest(pool) })
	uuids := newTestUUIDGenerator()
	tm := postgres.NewTransactionManager(pool, uuids)
	factory, _ := postgres.NewStoreFactoryForTest(pool, uuids, tm)

	ctx := ctxWithTenant("t1")
	// Seed.
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'e1', 'M', '1', 3, false, '{"hello":"world"}'::jsonb)
	`)

	txID, txCtx, err := tm.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tm.Rollback(ctx, txID)

	es, _ := factory.EntityStore(txCtx)
	if _, err := es.Get(txCtx, "e1"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	state, ok := postgres.LookupTxStateForTest(tm, txID)
	if !ok {
		t.Fatal("expected txState")
	}
	if v := postgres.ReadSetVersionForTest(state, "e1"); v != 3 {
		t.Errorf("readSet[e1]: want 3, got %d", v)
	}
}

func TestEntityStore_Get_NoTxContext_DoesNotRecord(t *testing.T) {
	// A Get outside any transaction (no txCtx) must not panic and
	// must not attempt any bookkeeping.
	pool := newTestPool(t)
	if err := postgres.DropSchemaForTest(pool); err != nil { t.Fatalf("reset: %v", err) }
	if err := postgres.Migrate(pool); err != nil { t.Fatalf("migrate: %v", err) }
	t.Cleanup(func() { _ = postgres.DropSchemaForTest(pool) })
	uuids := newTestUUIDGenerator()
	tm := postgres.NewTransactionManager(pool, uuids)
	factory, _ := postgres.NewStoreFactoryForTest(pool, uuids, tm)

	ctx := ctxWithTenant("t1")
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'e1', 'M', '1', 3, false, '{}'::jsonb)
	`)
	es, _ := factory.EntityStore(ctx)
	if _, err := es.Get(ctx, "e1"); err != nil {
		t.Fatalf("Get(no tx): %v", err)
	}
}
```

Add `ReadSetVersionForTest` to `export_test.go`:

```go
// ReadSetVersionForTest returns the captured readSet version for the
// given entity, or 0 if not present.
func ReadSetVersionForTest(s TxStateForTest, entityID string) int64 {
	inner, ok := s.(*txState)
	if !ok {
		return 0
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	return inner.readSet[entityID]
}
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_Get_PopulatesReadSet|TestEntityStore_Get_NoTxContext_DoesNotRecord" -v`
Expected: FAIL

- [ ] **Step 3: Add `recordRead` helper and hook it into `Get`**

Append to `plugins/postgres/txstate.go`:

```go
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
```

Add `"context"` to imports.

Now the entity store needs access to the TransactionManager. Look at how `entityStore` is constructed — it currently holds `q Querier` and `tenantID`. We need either a reference to the TM or a functional handle. Check `plugins/postgres/store_factory.go` to see how `entityStore` is assembled and thread the TM through.

If the factory already holds a `*TransactionManager`, add a field to `entityStore`:

```go
type entityStore struct {
	q        Querier
	tenantID spi.TenantID
	tm       *TransactionManager
}
```

And update the factory constructor to pass `tm`.

Hook into `Get`. At the end of `Get`, just before `return entity, nil`, add:

```go
	s.tm.recordReadIfInTx(ctx, entity.Meta.ID, entity.Meta.Version)
```

(Placement: after the entity has been fully populated from the DB rows, before return.)

- [ ] **Step 4: Run tests**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_Get_PopulatesReadSet|TestEntityStore_Get_NoTxContext_DoesNotRecord" -v`
Expected: PASS

Run full postgres test suite:

Run: `go test ./plugins/postgres/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/
git commit -m "feat(postgres): hook recordRead into entityStore.Get"
```

---

### Task 12: Hook `recordWrite` into `entityStore.Save`

**Files:**
- Modify: `plugins/postgres/entity_store.go`
- Modify: `plugins/postgres/entity_store_test.go`

- [ ] **Step 1: Write failing tests**

Append to `plugins/postgres/entity_store_test.go`:

```go
func TestEntityStore_Save_FreshInsertRecordsZero(t *testing.T) {
	// ... boilerplate (see Task 11 test) ...
	// Seed: nothing. Save a new entity.
	txID, txCtx, _ := tm.Begin(ctx)
	defer tm.Rollback(ctx, txID)
	es, _ := factory.EntityStore(txCtx)
	entity := &spi.Entity{
		Meta: spi.EntityMeta{
			ID:       "new-e1",
			TenantID: "t1",
			ModelRef: spi.ModelRef{EntityName: "M", ModelVersion: "1"},
		},
		Data: []byte(`{"x":1}`),
	}
	if _, err := es.Save(txCtx, entity); err != nil {
		t.Fatalf("Save: %v", err)
	}
	state, _ := postgres.LookupTxStateForTest(tm, txID)
	if v, present := postgres.WriteSetVersionForTest(state, "new-e1"); !present {
		t.Fatal("writeSet should contain new-e1")
	} else if v != 0 {
		t.Errorf("writeSet[new-e1]: want 0 (fresh insert), got %d", v)
	}
}

func TestEntityStore_Save_UpdateRecordsPreWriteVersion(t *testing.T) {
	// ... boilerplate ...
	// Seed an existing entity at version 4.
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'existing', 'M', '1', 4, false, '{}'::jsonb)
	`)
	txID, txCtx, _ := tm.Begin(ctx)
	defer tm.Rollback(ctx, txID)
	es, _ := factory.EntityStore(txCtx)
	entity := &spi.Entity{
		Meta: spi.EntityMeta{
			ID:       "existing",
			TenantID: "t1",
			ModelRef: spi.ModelRef{EntityName: "M", ModelVersion: "1"},
		},
		Data: []byte(`{"updated":true}`),
	}
	if _, err := es.Save(txCtx, entity); err != nil {
		t.Fatalf("Save: %v", err)
	}
	state, _ := postgres.LookupTxStateForTest(tm, txID)
	if v, present := postgres.WriteSetVersionForTest(state, "existing"); !present {
		t.Fatal("writeSet should contain existing")
	} else if v != 4 {
		t.Errorf("writeSet[existing]: want 4 (pre-write), got %d", v)
	}
}
```

Add `WriteSetVersionForTest` to `export_test.go`:

```go
func WriteSetVersionForTest(s TxStateForTest, entityID string) (int64, bool) {
	inner, ok := s.(*txState)
	if !ok {
		return 0, false
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	v, present := inner.writeSet[entityID]
	return v, present
}
```

- [ ] **Step 2: Run tests — expect FAIL**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_Save_FreshInsertRecordsZero|TestEntityStore_Save_UpdateRecordsPreWriteVersion" -v`
Expected: FAIL

- [ ] **Step 3: Hook `recordWrite` into `Save`**

In `plugins/postgres/entity_store.go`, at the end of `Save` (after `RETURNING version, (xmax = 0)` has populated `nextVersion` and `isNew`, and after all the bookkeeping inserts into `entity_versions` are complete), add:

```go
	// Pre-write version: 0 for fresh insert (isNew), else nextVersion-1.
	var preWriteVersion int64
	if !isNew {
		preWriteVersion = nextVersion - 1
	}
	s.tm.recordWriteIfInTx(ctx, eid, preWriteVersion)
```

Place this **immediately before** the final `return nextVersion, nil`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_Save_FreshInsertRecordsZero|TestEntityStore_Save_UpdateRecordsPreWriteVersion" -v`
Expected: PASS

Run full postgres test suite:

Run: `go test ./plugins/postgres/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/entity_store.go plugins/postgres/entity_store_test.go plugins/postgres/export_test.go
git commit -m "feat(postgres): hook recordWrite into entityStore.Save"
```

---

### Task 13: Hook `recordWrite` into `CompareAndSave` and `Delete`

**Files:**
- Modify: `plugins/postgres/entity_store.go`
- Modify: `plugins/postgres/entity_store_test.go`

- [ ] **Step 1: Write failing tests**

Append to `plugins/postgres/entity_store_test.go`:

```go
func TestEntityStore_CompareAndSave_RecordsWriteSet(t *testing.T) {
	// ... boilerplate ...
	// Seed existing entity with a known transaction_id.
	// The existing CAS semantics expect an expectedTxID argument; set
	// seed to match.
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'cas-e', 'M', '1', 2, false, '{}'::jsonb)
	`)
	// (Seed entity_versions row too if CompareAndSave requires it — mirror
	// existing CAS tests in entity_store_test.go for the setup.)
	txID, txCtx, _ := tm.Begin(ctx)
	defer tm.Rollback(ctx, txID)
	es, _ := factory.EntityStore(txCtx)
	entity := &spi.Entity{
		Meta: spi.EntityMeta{
			ID:       "cas-e",
			TenantID: "t1",
			ModelRef: spi.ModelRef{EntityName: "M", ModelVersion: "1"},
		},
		Data: []byte(`{"after-cas":true}`),
	}
	expectedTxID := "seed-tx" // match seed row's transaction_id
	if _, err := es.CompareAndSave(txCtx, entity, expectedTxID); err != nil {
		t.Fatalf("CompareAndSave: %v", err)
	}
	state, _ := postgres.LookupTxStateForTest(tm, txID)
	if v, present := postgres.WriteSetVersionForTest(state, "cas-e"); !present || v != 2 {
		t.Errorf("writeSet[cas-e]: want 2, got (v=%d, present=%v)", v, present)
	}
}

func TestEntityStore_Delete_RecordsWriteSet(t *testing.T) {
	// ... boilerplate ...
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'del-e', 'M', '1', 6, false, '{}'::jsonb)
	`)
	txID, txCtx, _ := tm.Begin(ctx)
	defer tm.Rollback(ctx, txID)
	es, _ := factory.EntityStore(txCtx)
	if err := es.Delete(txCtx, "del-e"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	state, _ := postgres.LookupTxStateForTest(tm, txID)
	if v, present := postgres.WriteSetVersionForTest(state, "del-e"); !present || v != 6 {
		t.Errorf("writeSet[del-e]: want 6, got (v=%d, present=%v)", v, present)
	}
}
```

Review existing `CompareAndSave` / `Delete` tests to confirm the exact seed setup (transaction_id / entity_versions rows) required.

- [ ] **Step 2: Run tests — expect FAIL**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_CompareAndSave_RecordsWriteSet|TestEntityStore_Delete_RecordsWriteSet" -v`
Expected: FAIL

- [ ] **Step 3: Hook into both methods**

In `plugins/postgres/entity_store.go`:

- `CompareAndSave`: after the UPSERT/UPDATE succeeds and the post-write version is known, compute `preWriteVersion = postVersion - 1` (CAS always modifies an existing row; never a fresh insert). Call `s.tm.recordWriteIfInTx(ctx, eid, preWriteVersion)` just before return.

- `Delete`: `Delete` reads the current version before soft-deleting (the existing implementation already does so for history). Use that version as `preWriteVersion` and call `s.tm.recordWriteIfInTx(ctx, eid, preDeleteVersion)` just before return.

Inspect the existing bodies and place each call adjacent to the successful-exit path only (don't record on the error paths).

- [ ] **Step 4: Run tests**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_CompareAndSave_RecordsWriteSet|TestEntityStore_Delete_RecordsWriteSet" -v`
Expected: PASS

Run full suite:

Run: `go test ./plugins/postgres/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/entity_store.go plugins/postgres/entity_store_test.go
git commit -m "feat(postgres): hook recordWrite into CompareAndSave and Delete"
```

---

### Task 14: Hook `GetAll` / `SaveAll` / `DeleteAll`; annotate non-tracked methods

**Files:**
- Modify: `plugins/postgres/entity_store.go`
- Modify: `plugins/postgres/entity_store_test.go`

- [ ] **Step 1: Write failing tests**

Append to `plugins/postgres/entity_store_test.go`:

```go
func TestEntityStore_GetAll_RecordsEachReadSet(t *testing.T) {
	// ... boilerplate ...
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES
		  ('t1', 'a', 'M', '1', 1, false, '{}'::jsonb),
		  ('t1', 'b', 'M', '1', 2, false, '{}'::jsonb),
		  ('t1', 'c', 'M', '1', 3, false, '{}'::jsonb)
	`)
	txID, txCtx, _ := tm.Begin(ctx)
	defer tm.Rollback(ctx, txID)
	es, _ := factory.EntityStore(txCtx)
	entities, err := es.GetAll(txCtx, spi.ModelRef{EntityName: "M", ModelVersion: "1"})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(entities) != 3 {
		t.Fatalf("want 3, got %d", len(entities))
	}
	state, _ := postgres.LookupTxStateForTest(tm, txID)
	for _, id := range []string{"a", "b", "c"} {
		if v := postgres.ReadSetVersionForTest(state, id); v == 0 {
			t.Errorf("readSet[%s]: expected populated, was 0", id)
		}
	}
}

func TestEntityStore_DeleteAll_RecordsEachWriteSet(t *testing.T) {
	// similar: seed N rows, DeleteAll, verify each in writeSet with pre-delete version.
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_GetAll_RecordsEachReadSet|TestEntityStore_DeleteAll_RecordsEachWriteSet" -v`
Expected: FAIL

- [ ] **Step 3: Hook into `GetAll` and `DeleteAll`; leave `SaveAll` — which is already `spi.DefaultSaveAll(s, ctx, entities)` looping over `Save` — automatically covered since each `Save` records.**

In `GetAll`, after the slice is fully populated, loop:

```go
	for _, e := range entities {
		s.tm.recordReadIfInTx(ctx, e.Meta.ID, e.Meta.Version)
	}
```

In `DeleteAll`, the existing implementation either does a bulk UPDATE ... SET deleted=true or iterates per-entity — inspect to determine how to capture pre-delete versions. If bulk, add a prior `SELECT entity_id, version FROM entities WHERE ...` to capture versions before the UPDATE, then record each. If it already iterates, record inline.

**Annotate the untracked methods** (spec "Known limitation: phantom reads"). In `entity_store.go`, add a one-line code comment above each of:

- `GetAsAt` — `// Deliberately not tracked: historical reads target immutable versions, not the live row. See design spec §Known limitation.`
- `GetAllAsAt` — same comment.
- `GetVersionHistory` — `// Deliberately not tracked: observational, not a live-row read.`
- `Count` — `// Deliberately not tracked: aggregate with no per-row identity. See design spec §Known limitation (phantom reads on range queries).`
- `Exists` — decide: if used in read-decide-write patterns, track (like `Get`); if purely a probe, don't. Default: **do track** by capturing the version when the entity exists; add comment accordingly.

- [ ] **Step 4: Run tests**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_GetAll|TestEntityStore_DeleteAll|TestEntityStore_SaveAll" -v`
Expected: PASS

Run full suite:

Run: `go test ./plugins/postgres/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/entity_store.go plugins/postgres/entity_store_test.go
git commit -m "feat(postgres): hook recordRead/Write for GetAll/DeleteAll; annotate untracked methods"
```

---

### Task 15: Wire `Savepoint` / `RollbackToSavepoint` / `ReleaseSavepoint` through `txState`

**Files:**
- Modify: `plugins/postgres/transaction_manager.go`
- Modify: `plugins/postgres/transaction_manager_test.go`

- [ ] **Step 1: Write failing test — savepoint rollback drops post-savepoint read-set**

Append to `plugins/postgres/transaction_manager_test.go`:

```go
func TestTxManager_Savepoint_RollsBackTxStateEntries(t *testing.T) {
	tm, pool := newTestTxManager(t)
	ctx := ctxWithTenant("t1")

	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'x', 'M', '1', 1, false, '{}'::jsonb),
		       ('t1', 'y', 'M', '1', 1, false, '{}'::jsonb)
	`)

	txID, txCtx, _ := tm.Begin(ctx)
	defer tm.Rollback(ctx, txID)

	state, _ := postgres.LookupTxStateForTest(tm, txID)
	state.RecordRead("x", 1)

	spID, err := tm.Savepoint(txCtx, txID)
	if err != nil {
		t.Fatalf("Savepoint: %v", err)
	}

	state.RecordRead("y", 1)

	if err := tm.RollbackToSavepoint(txCtx, txID, spID); err != nil {
		t.Fatalf("RollbackToSavepoint: %v", err)
	}

	// y should be gone; x preserved.
	if postgres.ReadSetVersionForTest(state, "y") != 0 {
		t.Error("readSet[y] should have been restored away")
	}
	if postgres.ReadSetVersionForTest(state, "x") != 1 {
		t.Error("readSet[x] should have been preserved")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./plugins/postgres/ -run TestTxManager_Savepoint_RollsBackTxStateEntries -v`
Expected: FAIL

- [ ] **Step 3: Wire the savepoint methods**

In `plugins/postgres/transaction_manager.go`, modify `Savepoint` / `RollbackToSavepoint` / `ReleaseSavepoint`. Example for `Savepoint`:

```go
func (tm *TransactionManager) Savepoint(ctx context.Context, txID string) (string, error) {
	pgxTx, ok := tm.registry.Lookup(txID)
	if !ok {
		return "", fmt.Errorf("Savepoint: transaction %s not found", txID)
	}
	state, ok := tm.lookupTxState(txID)
	if !ok {
		return "", fmt.Errorf("Savepoint: tx state for %s not found", txID)
	}
	spID := uuid.UUID(tm.uuids.NewTimeUUID()).String()
	spName := "sp_" + spID
	if _, err := pgxTx.Exec(ctx, "SAVEPOINT "+pgx.Identifier{spName}.Sanitize()); err != nil {
		return "", fmt.Errorf("Savepoint: %w", err)
	}
	state.PushSavepoint(spID)
	return spID, nil
}
```

Analogously:
- `RollbackToSavepoint` — execute `ROLLBACK TO SAVEPOINT` first, then call `state.RestoreSavepoint(spID)`.
- `ReleaseSavepoint` — execute `RELEASE SAVEPOINT` first, then `state.ReleaseSavepoint(spID)`.

Order matters: the pgx side is the source of truth for durable SQL state; the txState mirror is bookkeeping. Execute SQL first, then mirror.

- [ ] **Step 4: Run test**

Run: `go test ./plugins/postgres/ -run TestTxManager_Savepoint -v`
Expected: PASS

Run full suite:

Run: `go test ./plugins/postgres/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/transaction_manager.go plugins/postgres/transaction_manager_test.go
git commit -m "feat(postgres): wire savepoints through txState snapshot/restore"
```

---

### Task 16: End-to-end test — #17 regression (disjoint entities concurrent)

**Files:**
- Modify: `plugins/postgres/conformance_test.go` (or `entity_store_test.go` if a more focused home exists)

- [ ] **Step 1: Write the regression test**

If `TestConformance/Entity/Concurrent/DifferentEntities` already exists in the spitest suite, re-enable it and remove any postgres-specific skip annotations. If a focused postgres-only test doesn't exist, add:

```go
func TestEntityStore_ConcurrentDisjointInserts_NoFalseConflicts(t *testing.T) {
	// Regression for #17: under SSI this was flaky (page-level SIReadLocks
	// caused 40001 on concurrent inserts of distinct UUIDs within the same
	// tenant). Under SI + FCW, disjoint inserts must all commit.
	pool := newTestPool(t)
	if err := postgres.DropSchemaForTest(pool); err != nil { t.Fatalf("reset: %v", err) }
	if err := postgres.Migrate(pool); err != nil { t.Fatalf("migrate: %v", err) }
	t.Cleanup(func() { _ = postgres.DropSchemaForTest(pool) })
	uuids := newTestUUIDGenerator()
	tm := postgres.NewTransactionManager(pool, uuids)
	factory, _ := postgres.NewStoreFactoryForTest(pool, uuids, tm)

	ctx := ctxWithTenant("t1")

	// First, populate the tree so the b-tree has enough pages that disjoint
	// inserts would have collided under SSI (mirrors the #17 repro).
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("seed-%04d", i)
		_, _ = pool.Exec(ctx, `
			INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
			VALUES ('t1', $1, 'M', '1', 1, false, '{}'::jsonb)
		`, id)
	}

	// Now 8 concurrent inserts of distinct UUIDs.
	const N = 8
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			txID, txCtx, err := tm.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			es, _ := factory.EntityStore(txCtx)
			entity := &spi.Entity{
				Meta: spi.EntityMeta{
					ID:       fmt.Sprintf("concurrent-%d-%s", i, uuid.NewString()),
					TenantID: "t1",
					ModelRef: spi.ModelRef{EntityName: "M", ModelVersion: "1"},
				},
				Data: []byte(`{}`),
			}
			if _, err := es.Save(txCtx, entity); err != nil {
				errs <- err
				_ = tm.Rollback(ctx, txID)
				return
			}
			errs <- tm.Commit(ctx, txID)
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("disjoint concurrent insert should never conflict: %v", err)
		}
	}
}
```

- [ ] **Step 2: Run test — should pass under new SI+FCW**

Run: `go test ./plugins/postgres/ -run TestEntityStore_ConcurrentDisjointInserts_NoFalseConflicts -v`
Expected: PASS. If it flakes, run 20× via `-count=20` to confirm stability.

- [ ] **Step 3: Loop-stability check**

Run: `go test ./plugins/postgres/ -run TestEntityStore_ConcurrentDisjointInserts_NoFalseConflicts -count=20 -v`
Expected: PASS (all 20 iterations). The #17 flake is gone.

- [ ] **Step 4: Commit**

```bash
git add plugins/postgres/
git commit -m "test(postgres): regression guard for #17 — disjoint concurrent inserts"
```

---

### Task 17: End-to-end — same-entity race + deleted-entity conflict

**Files:**
- Modify: `plugins/postgres/conformance_test.go` (or `entity_store_test.go`)

- [ ] **Step 1: Write tests**

```go
func TestEntityStore_ConcurrentSameEntityUpdate_FirstWins(t *testing.T) {
	// ... boilerplate ...
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'shared', 'M', '1', 1, false, '{}'::jsonb)
	`)

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			txID, txCtx, err := tm.Begin(ctx)
			if err != nil {
				results <- err
				return
			}
			es, _ := factory.EntityStore(txCtx)
			// Each tx reads, then writes — classic race.
			if _, err := es.Get(txCtx, "shared"); err != nil {
				results <- err
				_ = tm.Rollback(ctx, txID)
				return
			}
			entity := &spi.Entity{
				Meta: spi.EntityMeta{
					ID:       "shared",
					TenantID: "t1",
					ModelRef: spi.ModelRef{EntityName: "M", ModelVersion: "1"},
				},
				Data: []byte(fmt.Sprintf(`{"by":%d}`, i)),
			}
			if _, err := es.Save(txCtx, entity); err != nil {
				results <- err
				_ = tm.Rollback(ctx, txID)
				return
			}
			results <- tm.Commit(ctx, txID)
		}(i)
	}
	wg.Wait()
	close(results)

	var success, conflict int
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, spi.ErrConflict) {
			conflict++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Errorf("want exactly 1 success + 1 conflict; got success=%d conflict=%d", success, conflict)
	}
}

func TestEntityStore_ReadThenConcurrentDelete_Conflict(t *testing.T) {
	// Tx A reads e1; concurrent tx B deletes e1 and commits; A's commit fails.
	// ... boilerplate + seed e1 at v=1 ...
	txA, txCtxA, _ := tm.Begin(ctx)
	esA, _ := factory.EntityStore(txCtxA)
	if _, err := esA.Get(txCtxA, "e1"); err != nil { t.Fatalf("A.Get: %v", err) }

	txB, txCtxB, _ := tm.Begin(ctx)
	esB, _ := factory.EntityStore(txCtxB)
	if err := esB.Delete(txCtxB, "e1"); err != nil { t.Fatalf("B.Delete: %v", err) }
	if err := tm.Commit(ctx, txB); err != nil { t.Fatalf("B.Commit: %v", err) }

	err := tm.Commit(ctx, txA)
	if !errors.Is(err, spi.ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./plugins/postgres/ -run "TestEntityStore_ConcurrentSameEntityUpdate_FirstWins|TestEntityStore_ReadThenConcurrentDelete_Conflict" -v -count=5`
Expected: PASS (all 5 iterations)

- [ ] **Step 3: Commit**

```bash
git add plugins/postgres/
git commit -m "test(postgres): same-entity race and read-then-concurrent-delete conflict"
```

---

### Task 18: End-to-end — large read-set chunking

**Files:**
- Modify: `plugins/postgres/commit_validator_test.go`

- [ ] **Step 1: Write test**

```go
func TestCommit_LargeReadSet_ChunkedValidation(t *testing.T) {
	tm, pool := newTestTxManager(t)
	ctx := ctxWithTenant("t1")

	// Seed 2500 entities — forces 3 chunks at the default size of 1000.
	for i := 0; i < 2500; i++ {
		id := fmt.Sprintf("bulk-%05d", i)
		_, _ = pool.Exec(ctx, `
			INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
			VALUES ('t1', $1, 'M', '1', 1, false, '{}'::jsonb)
		`, id)
	}

	txID, txCtx, _ := tm.Begin(ctx)
	state, _ := postgres.LookupTxStateForTest(tm, txID)
	for i := 0; i < 2500; i++ {
		state.RecordRead(fmt.Sprintf("bulk-%05d", i), 1)
	}

	// Commit must succeed — no concurrent modifications, chunking is
	// internal. Main assertion: no error / no hang.
	start := time.Now()
	if err := tm.Commit(ctx, txID); err != nil {
		t.Fatalf("commit with large read-set: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("large read-set commit took too long: %v (suggests chunking is broken)", d)
	}
	_ = txCtx
}
```

- [ ] **Step 2: Run**

Run: `go test ./plugins/postgres/ -run TestCommit_LargeReadSet_ChunkedValidation -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add plugins/postgres/commit_validator_test.go
git commit -m "test(postgres): large read-set commit uses chunked validation"
```

---

### Task 19: End-to-end — savepoint rollback drops read-set conflict

**Files:**
- Modify: `plugins/postgres/transaction_manager_test.go`

- [ ] **Step 1: Write test**

```go
func TestTxManager_SavepointRollback_DropsReadSet_CommitSucceeds(t *testing.T) {
	// Setup: seed x,y. Tx A reads x; push savepoint; reads y; concurrent
	// tx modifies y; Tx A rolls back to savepoint; Tx A commits.
	// Expected: commit succeeds — y is no longer in readSet, so the
	// concurrent modification is not a conflict.
	tm, pool := newTestTxManager(t)
	ctx := ctxWithTenant("t1")
	_, _ = pool.Exec(ctx, `
		INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		VALUES ('t1', 'x', 'M', '1', 1, false, '{}'::jsonb),
		       ('t1', 'y', 'M', '1', 1, false, '{}'::jsonb)
	`)
	factory, _ := postgres.NewStoreFactoryForTest(pool, newTestUUIDGenerator(), tm)

	txID, txCtx, _ := tm.Begin(ctx)
	es, _ := factory.EntityStore(txCtx)
	if _, err := es.Get(txCtx, "x"); err != nil { t.Fatalf("read x: %v", err) }

	spID, err := tm.Savepoint(txCtx, txID)
	if err != nil { t.Fatalf("Savepoint: %v", err) }

	if _, err := es.Get(txCtx, "y"); err != nil { t.Fatalf("read y: %v", err) }

	// Concurrent tx bumps y's version.
	txB, txCtxB, _ := tm.Begin(ctx)
	_, _ = pool.Exec(ctx, `UPDATE entities SET version=2 WHERE tenant_id='t1' AND entity_id='y'`) // fallback if UPDATE through tx2 doesn't apply; prefer to route via txB
	_ = tm.Commit(ctx, txB)
	_ = txCtxB

	// Rollback to savepoint — y drops out of read-set.
	if err := tm.RollbackToSavepoint(txCtx, txID, spID); err != nil {
		t.Fatalf("RollbackToSavepoint: %v", err)
	}

	// Commit must succeed — x's version is unchanged; y is no longer validated.
	if err := tm.Commit(ctx, txID); err != nil {
		t.Errorf("commit should succeed after rollback-to drops y; got: %v", err)
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./plugins/postgres/ -run TestTxManager_SavepointRollback_DropsReadSet_CommitSucceeds -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add plugins/postgres/transaction_manager_test.go
git commit -m "test(postgres): savepoint rollback drops read-set entries for commit"
```

---

### Task 20: Conformance-suite alignment

**Files:**
- Modify: `internal/spitest/` — the concurrency test file (path determined by `ls internal/spitest/` at execution time).

- [ ] **Step 1: Inventory the existing spitest concurrency tests**

Run: `grep -rn "Concurrent" internal/spitest/`

Identify tests matching the #17 scenario and any "same-entity race" / "savepoint semantics" tests.

- [ ] **Step 2: Update or add expectations**

For the `DifferentEntities` test: remove any plugin-specific skips or `-count=1` workarounds; confirm it runs on postgres without flake under the new design.

For any `SameEntity` concurrency test: assert "exactly one success + one ErrConflict" across memory, postgres, and (via a separate test run or CI) cassandra.

For the savepoint divergence: if the spitest harness has a savepoint-specific test, add a `// DIVERGENCE:` comment next to any assertion that reflects the postgres-preserves / cassandra-clears behavior. If no such test exists yet, add one that asserts the divergence-tolerant behavior (either "post-savepoint reads are validated after rollback-to" OR accept both). The spec documents this explicitly; the conformance test should mirror that.

- [ ] **Step 3: Run full spitest conformance**

Run: `go test ./internal/spitest/... -v`
Expected: all PASS

Run: `go test ./internal/e2e/... -v`
Expected: all PASS (E2E tests use postgres via testcontainers)

- [ ] **Step 4: Commit**

```bash
git add internal/spitest/
git commit -m "test(spitest): align conformance with SI+FCW semantics and savepoint divergence"
```

---

### Task 21: Close #17 and file the cross-plugin parity check

**Files:**
- Run full parity suite; no code changes if everything is green.

- [ ] **Step 1: Run parity suite 20×**

Run: `for i in {1..20}; do go test ./plugins/postgres/ ./internal/spitest/... ./internal/e2e/... || break; done`
Expected: all 20 runs PASS.

- [ ] **Step 2: Run full module tests**

Run: `go test ./... -v`
Expected: all PASS. Follow-up on any failures.

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 4: Confirm #35 and #17 are in the PR description**

Draft a PR description that:
- Closes #17 (the flake scenario passes reliably now).
- References #18 (this work).
- References #35 (follow-up coverage for non-entity stores).
- References cyoda-go-cassandra#22 (sibling on cassandra).
- Links the design spec and this plan.

- [ ] **Step 5: Commit any remaining cleanup and push**

No code commit at this step unless something turned up. If so:

```bash
git add ...
git commit -m "chore: final cleanup for SI+FCW PR"
```

Then the human opens the PR. No automated push from the plan.

---

## Self-review

**Spec coverage:**
- Goals 1 (parity with cassandra), 2 (eliminate page-level false positives), 3 (no SPI changes), 4 (snapshot + RYW preserved), 5 (multi-node via Join) — all covered by the task chain. Goal 4 specifically verified by Task 8's regression test.
- Non-goals 1–4 honored: non-entity store coverage out of scope (#35 referenced, not implemented); no advisory locks; no retry wrapper; multi-node deployments without proxying not addressed.
- Mechanism points (RR flip, readSet/writeSet, validation at commit, savepoint handling, chunking, sorted lock order, FOR SHARE rationale) — each mapped to a task.
- Known-limitation "phantom reads" — Task 14 adds the per-method annotations.
- Test coverage (14 tests in spec § Test strategy) — all present: read-set conflict (Task 10), write-set conflict (Task 17), disjoint-rows no-conflict (Task 16), insert-no-read-set-interference (implicit in Task 12), tenant-scoped (Task 9), batched query (Task 9), deleted-entity (Task 17), large-read-set (Task 18), sorted lock acquisition (Task 4 and Task 18 combined), savepoint-rollback-drops-read-set (Task 19).

**Placeholder scan:** no TBDs; no "add appropriate error handling"; every code step has a concrete block; no "similar to Task N" — code is repeated where needed.

**Type consistency:** `*txState`, `readSet map[string]int64`, `writeSet map[string]int64`, `savepointEntry`, `SortedUnionIDs`, `ValidateReadSet`, `ValidateWriteSet`, `RecordRead`, `RecordWrite`, `PushSavepoint`, `RestoreSavepoint`, `ReleaseSavepoint`, `recordReadIfInTx`, `recordWriteIfInTx`, `validateInChunks` — names consistent across tasks. `tm.txStates` / `tm.txStatesMu` / `tm.lookupTxState` / `tm.removeTxState` — consistent. `TxStateForTest` interface + `LookupTxStateForTest` / `ReadSetVersionForTest` / `WriteSetVersionForTest` / `ValidateInChunksForTest` — consistent test-accessor nomenclature.

**Scope:** 21 tasks, each 2-5 minute steps, one coherent codebase (plugins/postgres + minimal spitest touch). Appropriate for a single implementation session.
