# Issue #24 — Stats Scalability and Search Precision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve issue #24 — three independent domain-layer correctness/scalability bugs: stats `GetAll`-and-count, XML int64→float64 precision loss, and `matchArray` non-numeric comparison.

**Architecture:** Three sequential PRs in `cyoda-go`, plus two coordinated upstream PRs in `cyoda-go-spi` (add `CountByState` to `EntityStore` interface) and `cyoda-go-cassandra` (implement the new SPI method). PR-1 spans repos and is the only one with a breaking SPI change; PR-2 (XML `json.Number`) and PR-3 (`matchArray` → `opEquals` + `toFloat64` extension) are pure in-module fixes.

**Tech Stack:** Go 1.26, `log/slog`, `encoding/json`, `github.com/cyoda-platform/cyoda-go-spi`, `github.com/tidwall/gjson`, postgres (via `pgx`), sqlite (`mattn/go-sqlite3`), cassandra (`gocql`), testcontainers-go for E2E.

**Spec:** `docs/superpowers/specs/2026-04-16-issue-24-stats-and-precision-design.md`.

---

## File Structure

### PR-A — `cyoda-go-spi` repo (separate checkout, not in this worktree)

- Modify: `persistence.go` — add `CountByState` to `EntityStore` interface
- Modify: `spitest/entity.go` — add `testEntityCountByState` conformance test (registered in `RunEntityStoreSuite`)
- Bump module to `v0.5.0` (tag the release)

### PR-B — `cyoda-go-cassandra` repo (`../cyoda-go-cassandra`, not in this worktree)

- Modify: `entity_store.go` — implement `CountByState` mirroring `Count`'s shard-scan
- Modify: `go.mod` — bump `cyoda-go-spi` to `v0.5.0`
- Add: `entity_store_count_by_state_test.go` — focused unit tests beyond the conformance suite (optional but encouraged)
- Tag a new release version (whatever scheme that repo uses)

### PR-1 — `cyoda-go` (this repo): stats SPI consumer

- Modify: `go.mod` — bump `cyoda-go-spi` to `v0.5.0`, bump cassandra dep to its new release
- Create: `plugins/memory/entity_store_count_by_state_test.go` — focused tests beyond conformance
- Modify: `plugins/memory/entity_store.go` — add `CountByState` method
- Create: `plugins/sqlite/entity_store_count_by_state_test.go` — focused tests beyond conformance
- Modify: `plugins/sqlite/entity_store.go` — add `CountByState` method
- Create: `plugins/postgres/entity_store_count_by_state_test.go` — focused tests beyond conformance
- Modify: `plugins/postgres/entity_store.go` — add `CountByState` method
- Modify: `internal/domain/entity/service.go:316,370` — replace `GetAll`-and-count with `CountByState` call
- Modify: `internal/e2e/entity_lifecycle_test.go` — extend `TestEntityLifecycle_Statistics` to assert state-filter behavior

### PR-2 — `cyoda-go` (this repo): XML json.Number

- Modify: `internal/domain/model/importer/parser.go:101-112` — replace `inferXMLValue`
- Create: `internal/domain/model/importer/parser_xml_value_test.go` — focused unit tests
- Audit and fix: any consumer of `ParseXML` that does typed assertions on `int64`/`float64`

### PR-3 — `cyoda-go` (this repo): matchArray + toFloat64

- Modify: `internal/match/operators.go:243` — extend `toFloat64` for `json.Number`
- Modify: `internal/match/match.go:150-168` — replace `matchArray` element compare with `opEquals`
- Modify: `internal/match/match_test.go` — add tests at end of file (after existing array-condition tests)
- Modify: `internal/match/operators_test.go` (or wherever `toFloat64` is tested; create test if no test exists) — add `json.Number` case

---

## PR-A: SPI Change in `cyoda-go-spi`

> **Working directory:** `~/go-projects/cyoda-light/cyoda-go-spi` (or wherever the SPI repo is cloned). NOT the cyoda-go worktree.

### Task A1: Add CountByState to EntityStore interface

**Files:**
- Modify: `persistence.go` (add method to `EntityStore` interface)

- [ ] **Step 1: Verify current SPI version**

```bash
cd ~/go-projects/cyoda-light/cyoda-go-spi
git status
git log --oneline -5
cat go.mod | head -3
```
Expected: clean working tree on main; `module github.com/cyoda-platform/cyoda-go-spi`.

- [ ] **Step 2: Add CountByState to the EntityStore interface**

Open `persistence.go`. Find the existing `Count` method in the `EntityStore` interface (around line 40). Insert immediately after it:

```go
	// CountByState returns the count of non-deleted entities grouped by state
	// for the given model. If states is non-nil, only the listed states are
	// included in the result. If states is nil, all states are returned.
	// An empty (non-nil) states slice returns an empty map without querying
	// the storage layer.
	//
	// Unknown model: returns an empty map with no error, matching Count's
	// behavior (no model-registry check at this layer).
	//
	// Implementations MUST push the state filter down to the storage layer
	// when feasible. Callers may invoke this from inside a transaction; the
	// returned counts MUST reflect the transactional view (uncommitted writes
	// from the current tx are visible, writes from other in-flight txs are not),
	// matching the semantics of Count.
	CountByState(ctx context.Context, modelRef ModelRef, states []string) (map[string]int64, error)
```

- [ ] **Step 3: Verify build fails (no implementations yet)**

```bash
cd ~/go-projects/cyoda-light/cyoda-go-spi
go build ./...
```
Expected: SUCCESS — interfaces don't fail to build until something tries to implement them. The SPI repo itself only defines the interface; we expect this to compile.

- [ ] **Step 4: Commit interface change**

```bash
git add persistence.go
git commit -m "feat: add CountByState to EntityStore interface"
```

### Task A2: Add testEntityCountByState to spitest

**Files:**
- Modify: `spitest/entity.go` — add `testEntityCountByState` function and register it in the runner

- [ ] **Step 1: Locate the runner and existing testEntityCount**

```bash
grep -n "testEntityCount\b\|runSubtest.*Count" spitest/entity.go | head -10
```
Note the line where `runSubtest(t, h, tracker, "Count", testEntityCount)` is registered, and where `testEntityCount` itself is defined. The new test will be registered next to "Count" and defined near `testEntityCount`.

- [ ] **Step 2: Register the new subtest**

In `spitest/entity.go`, find the line:
```go
	runSubtest(t, h, tracker, "Count", testEntityCount)
```
and add immediately after it:
```go
	runSubtest(t, h, tracker, "CountByState", testEntityCountByState)
```

- [ ] **Step 3: Add the testEntityCountByState function**

In `spitest/entity.go`, after the body of `testEntityCount`, add:

```go
func testEntityCountByState(t *testing.T, h Harness) {
	ctx := tenantContext(h.NewTenant())
	mref := spi.ModelRef{EntityName: "m-cbs", ModelVersion: "1"}

	// Empty model: nil filter -> empty map.
	es, _ := h.Factory.EntityStore(ctx)
	got, err := es.CountByState(ctx, mref, nil)
	require.NoError(t, err)
	require.Empty(t, got, "empty model with nil filter should return empty map")

	// Empty model: nil-but-empty-slice filter -> empty map (no storage call expected).
	got, err = es.CountByState(ctx, mref, []string{})
	require.NoError(t, err)
	require.Empty(t, got, "empty filter slice should return empty map")

	// Save 3 entities in "new", 2 in "approved", 1 in "rejected", 1 deleted "approved".
	withTx(t, h, ctx, func(txCtx context.Context) {
		esTx, _ := h.Factory.EntityStore(txCtx)
		for i := 0; i < 3; i++ {
			e := newEntity(t, "m-cbs", newID(), map[string]any{"i": i})
			e.Meta.State = "new"
			_, err := esTx.Save(txCtx, e)
			require.NoError(t, err)
		}
		for i := 0; i < 2; i++ {
			e := newEntity(t, "m-cbs", newID(), map[string]any{"i": i})
			e.Meta.State = "approved"
			_, err := esTx.Save(txCtx, e)
			require.NoError(t, err)
		}
		e := newEntity(t, "m-cbs", newID(), map[string]any{"i": 99})
		e.Meta.State = "rejected"
		_, err := esTx.Save(txCtx, e)
		require.NoError(t, err)

		// Save and delete one "approved" — must NOT appear in counts.
		toDel := newEntity(t, "m-cbs", newID(), map[string]any{"i": 100})
		toDel.Meta.State = "approved"
		_, err = esTx.Save(txCtx, toDel)
		require.NoError(t, err)
		require.NoError(t, esTx.Delete(txCtx, toDel.Meta.ID))
	})

	// nil filter -> all states (3 + 2 + 1 = 6, deleted excluded).
	got, err = es.CountByState(ctx, mref, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"new": 3, "approved": 2, "rejected": 1}, got)

	// Filter to "approved" only.
	got, err = es.CountByState(ctx, mref, []string{"approved"})
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"approved": 2}, got)

	// Filter to multiple states including a missing one — missing omitted.
	got, err = es.CountByState(ctx, mref, []string{"approved", "missing"})
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"approved": 2}, got)

	// Tenant isolation: a different tenant must see zero entities for this model.
	otherCtx := tenantContext(h.NewTenant())
	esOther, _ := h.Factory.EntityStore(otherCtx)
	got, err = esOther.CountByState(otherCtx, mref, nil)
	require.NoError(t, err)
	require.Empty(t, got, "different tenant must not see other tenant's entities")

	// Transactional visibility: uncommitted save in current tx must be visible inside the tx.
	withTx(t, h, ctx, func(txCtx context.Context) {
		esTx, _ := h.Factory.EntityStore(txCtx)
		e := newEntity(t, "m-cbs", newID(), map[string]any{"tx": true})
		e.Meta.State = "in_review"
		_, err := esTx.Save(txCtx, e)
		require.NoError(t, err)

		got, err := esTx.CountByState(txCtx, mref, []string{"in_review"})
		require.NoError(t, err)
		require.Equal(t, map[string]int64{"in_review": 1}, got, "uncommitted tx save must be visible inside tx")
	})
}
```

- [ ] **Step 4: Verify spitest compiles**

```bash
cd ~/go-projects/cyoda-light/cyoda-go-spi
go build ./spitest/...
```
Expected: SUCCESS.

> Note: `go test ./spitest/...` will not run anything because `spitest` only exposes a runner consumed by plugins; it has no own _test.go entry points.

- [ ] **Step 5: Commit conformance test**

```bash
git add spitest/entity.go
git commit -m "test(spitest): add CountByState conformance suite"
```

### Task A3: Tag and publish v0.5.0

- [ ] **Step 1: Tag the release**

```bash
cd ~/go-projects/cyoda-light/cyoda-go-spi
git tag v0.5.0
git push origin main
git push origin v0.5.0
```

- [ ] **Step 2: Verify the tag is published**

```bash
git ls-remote --tags origin | grep v0.5.0
```
Expected: a single line with the SHA and `refs/tags/v0.5.0`.

---

## PR-B: Cassandra Plugin Implementation

> **Working directory:** `~/go-projects/cyoda-light/cyoda-go-cassandra`. NOT the cyoda-go worktree.

### Task B1: Bump SPI dependency

- [ ] **Step 1: Update go.mod**

```bash
cd ~/go-projects/cyoda-light/cyoda-go-cassandra
go get github.com/cyoda-platform/cyoda-go-spi@v0.5.0
go mod tidy
```

- [ ] **Step 2: Verify build fails (CountByState not implemented yet)**

```bash
go build ./...
```
Expected: FAIL with something like `*EntityStore does not implement spi.EntityStore (missing method CountByState)`.

- [ ] **Step 3: Commit dep bump**

```bash
git add go.mod go.sum
git commit -m "chore: bump cyoda-go-spi to v0.5.0"
```
> The build is currently red. Next task makes it green. This is the RED of TDD at the integration level — the SPI conformance suite is the failing test driving the implementation.

### Task B2: Implement CountByState

**Files:**
- Modify: `entity_store.go` — add `CountByState` method on `*EntityStore`

- [ ] **Step 1: Locate the existing Count method**

```bash
grep -n "func.*EntityStore.*Count(\|func.*EntityStore.*GetVersionHistory" entity_store.go
```
Note the `Count` method's body — the new method will mirror its shard-scan pattern but additionally read the `state` column.

- [ ] **Step 2: Confirm the entity_by_model query reads state**

```bash
grep -n "cqlEntityByModelSelectByModel" cql.go entity_store.go | head -10
```
Find the SQL constant. If it does NOT include `state`, you'll need to either:
- (preferred) add a new constant `cqlEntityByModelSelectByModelWithState` that selects `(eid, deleted, state)` from the same partition key, or
- modify the existing constant if no other call site needs the narrower result.

Check call sites of the existing constant first:
```bash
grep -rn "cqlEntityByModelSelectByModel" .
```
If only `Count` uses it, it's safe to extend the column list. Otherwise add a new constant.

- [ ] **Step 3: Write the CountByState method**

In `entity_store.go`, immediately after the `Count` method:

```go
// CountByState returns counts of non-deleted entities grouped by state for the
// given model. NOTE: Mirrors Count's shard-scan; reads the state column for
// every row in addition to (eid, deleted). O(n). Acceptable for the current
// phase; a state-indexed materialized view is a possible future optimization.
func (s *EntityStore) CountByState(ctx context.Context, modelRef spi.ModelRef, states []string) (map[string]int64, error) {
	if states != nil && len(states) == 0 {
		return map[string]int64{}, nil
	}

	tid := string(s.tenantID)
	modelVer := modelVersionToInt(modelRef.ModelVersion)

	// Build state filter set for fast lookup.
	var filter map[string]struct{}
	if states != nil {
		filter = make(map[string]struct{}, len(states))
		for _, st := range states {
			filter[st] = struct{}{}
		}
	}

	result := make(map[string]int64)
	for shard := 0; shard < s.numShards; shard++ {
		iter := newTimedQuery(s.session, s.cqlDuration, ctx,
			cqlEntityByModelSelectByModelWithState, // or whatever constant from Step 2
			tid, modelRef.EntityName, modelVer, shard,
		).Iter()

		var eid gocql.UUID
		var deleted bool
		var state string
		for iter.Scan(&eid, &deleted, &state) {
			if deleted {
				continue
			}
			if filter != nil {
				if _, ok := filter[state]; !ok {
					continue
				}
			}
			result[state]++
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("failed to query entity_by_model shard %d: %w", shard, err)
		}
	}
	return result, nil
}
```

> Adapt the constant name to whatever you chose in Step 2.

- [ ] **Step 4: Run the SPI conformance suite against the cassandra plugin**

The cassandra repo runs the SPI suite via something like `RunEntityStoreSuite(...)` from `cyoda-go-spi/spitest`. Find the test that drives it:
```bash
grep -rn "RunEntityStoreSuite\|runSubtest" --include="*_test.go" | head -10
```

Run it (adjust to actual cassandra test invocation, likely requires a running cassandra container):
```bash
go test -run TestConformance/CountByState -v ./...
```
Expected: PASS for `CountByState` subtest. If it fails, fix the implementation. Do not commit until green.

- [ ] **Step 5: Run full conformance suite as regression check**

```bash
go test -run TestConformance -v ./... 2>&1 | tail -50
```
Expected: all subtests including pre-existing ones still pass.

- [ ] **Step 6: Commit implementation**

```bash
git add entity_store.go cql.go
git commit -m "feat: implement EntityStore.CountByState

Mirrors Count's shard-scan; additionally reads the state column for
each non-deleted entity to build the grouping. Applies optional state
filter in Go. O(n) — same scan-cost class as Count. A state-indexed
materialized view is a possible future optimization."
```

### Task B3: Tag a release

- [ ] **Step 1: Tag and push**

Use whatever version scheme the cassandra repo uses (check `git tag --sort=-v:refname | head -3` for the latest). Bump appropriately and push.

```bash
git push origin main
git push origin <new-tag>
```

---

## PR-1: cyoda-go Stats Consumer + Plugin Implementations

> **Working directory:** the cyoda-go worktree (`~/go-projects/cyoda-light/cyoda-go` or your worktree). All remaining tasks are here.

### Task 1.1: Bump SPI and cassandra dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Bump SPI**

```bash
go get github.com/cyoda-platform/cyoda-go-spi@v0.5.0
```

- [ ] **Step 2: Bump cassandra dep**

```bash
go get github.com/Cyoda-platform/cyoda-go-cassandra@<new-tag-from-PR-B>
go mod tidy
```

- [ ] **Step 3: Verify build is RED**

```bash
go build ./...
```
Expected: FAIL — three errors, one per plugin (`*entityStore does not implement spi.EntityStore (missing method CountByState)`). This is the failing-test signal driving the next three tasks.

- [ ] **Step 4: Commit dep bump (red)**

```bash
git add go.mod go.sum
git commit -m "chore: bump cyoda-go-spi to v0.5.0 and cassandra to <new-tag>"
```

### Task 1.2: Memory plugin — write conformance-driven test

**Files:**
- Create: `plugins/memory/entity_store_count_by_state_test.go`

- [ ] **Step 1: Confirm conformance suite is invoked from memory's tests**

```bash
grep -rn "RunEntityStoreSuite\|runSubtest" plugins/memory/ | head -5
```
The memory plugin's `conformance_test.go` should already drive the SPI suite. If it does, the new `CountByState` subtest auto-runs as part of `go test ./plugins/memory/...`.

- [ ] **Step 2: Run conformance suite to confirm CountByState test FAILS for the right reason**

```bash
go test -run TestConformance/EntityStore/CountByState -v ./plugins/memory/...
```
Expected: FAIL — either with a compile error (build is red from Task 1.1 still) or with a runtime "method not found" panic. We need this to be a real test failure, not a build failure, before the next step. If it's a build failure, that's fine — the build will go green once the implementation lands in the next task; at that point re-run the conformance suite.

- [ ] **Step 3: Add focused unit tests for memory-specific edge cases**

Create `plugins/memory/entity_store_count_by_state_test.go`:

```go
package memory_test

import (
	"context"
	"testing"

	"github.com/cyoda-platform/cyoda-go/plugins/memory"
	spi "github.com/cyoda-platform/cyoda-go-spi"
)

func TestCountByState_EmptyStatesSliceShortCircuits(t *testing.T) {
	// Verifies the SPI contract: an empty (non-nil) states slice returns
	// an empty map without iterating storage. Specifically, this guards
	// against accidental "no filter" semantics when len(states) == 0.
	ctx := context.Background()
	factory, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	defer factory.Close()

	tenantCtx := spi.WithTenantID(ctx, "t1")
	es, err := factory.EntityStore(tenantCtx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}

	mref := spi.ModelRef{EntityName: "m", ModelVersion: "1"}
	got, err := es.CountByState(tenantCtx, mref, []string{})
	if err != nil {
		t.Fatalf("CountByState: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for empty states slice, got %v", got)
	}
}
```

> Note: adapt the package import path and tenant-context helper name to what the memory plugin actually exposes — confirm with `grep -rn "func New\|WithTenantID" plugins/memory/`.

- [ ] **Step 4: Run new test to confirm it FAILS**

```bash
go test -run TestCountByState_EmptyStatesSliceShortCircuits -v ./plugins/memory/...
```
Expected: FAIL (build error or "method not found"). This is the RED in the TDD cycle for Task 1.3.

### Task 1.3: Memory plugin — implement CountByState (GREEN)

**Files:**
- Modify: `plugins/memory/entity_store.go` — add method after existing `Count`

- [ ] **Step 1: Locate the existing Count method**

```bash
grep -n "func (s \*EntityStore) Count(" plugins/memory/entity_store.go
```
Expected: line 545 (or whichever line the existing `Count` is at).

- [ ] **Step 2: Add CountByState after Count**

In `plugins/memory/entity_store.go`, immediately after the closing brace of `Count` (line ~574):

```go
// CountByState returns counts of non-deleted entities grouped by state for the
// given model. See SPI godoc on EntityStore.CountByState for filter semantics.
func (s *EntityStore) CountByState(ctx context.Context, modelRef spi.ModelRef, states []string) (map[string]int64, error) {
	if states != nil && len(states) == 0 {
		return map[string]int64{}, nil
	}

	var filter map[string]struct{}
	if states != nil {
		filter = make(map[string]struct{}, len(states))
		for _, st := range states {
			filter[st] = struct{}{}
		}
	}

	tx := spi.GetTransaction(ctx)
	if tx != nil {
		// In-tx: use GetAll's merged-view logic (matches existing Count's in-tx fallback).
		all, err := s.GetAll(ctx, modelRef)
		if err != nil {
			return nil, err
		}
		result := make(map[string]int64)
		for _, e := range all {
			st := e.Meta.State
			if filter != nil {
				if _, ok := filter[st]; !ok {
					continue
				}
			}
			result[st]++
		}
		return result, nil
	}

	// Non-transaction: iterate latest versions directly.
	s.factory.entityMu.RLock()
	defer s.factory.entityMu.RUnlock()

	result := make(map[string]int64)
	for _, versions := range s.factory.entityData[s.tenant] {
		if len(versions) == 0 {
			continue
		}
		latest := versions[len(versions)-1]
		if latest.deleted {
			continue
		}
		if latest.entity.Meta.ModelRef != modelRef {
			continue
		}
		st := latest.entity.Meta.State
		if filter != nil {
			if _, ok := filter[st]; !ok {
				continue
			}
		}
		result[st]++
	}
	return result, nil
}
```

- [ ] **Step 3: Run unit test to confirm GREEN**

```bash
go test -run TestCountByState_EmptyStatesSliceShortCircuits -v ./plugins/memory/...
```
Expected: PASS.

- [ ] **Step 4: Run conformance suite for memory**

```bash
go test -run TestConformance/EntityStore/CountByState -v ./plugins/memory/...
```
Expected: PASS.

- [ ] **Step 5: Run full memory test suite (regression)**

```bash
go test ./plugins/memory/... -v 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 6: Commit memory implementation**

```bash
git add plugins/memory/entity_store.go plugins/memory/entity_store_count_by_state_test.go
git commit -m "feat(memory): implement EntityStore.CountByState"
```

### Task 1.4: SQLite plugin — write focused test (RED)

**Files:**
- Create: `plugins/sqlite/entity_store_count_by_state_test.go`

- [ ] **Step 1: Add focused test for empty-states short-circuit + JSON-extract correctness**

Create `plugins/sqlite/entity_store_count_by_state_test.go`:

```go
package sqlite_test

import (
	"context"
	"testing"

	"github.com/cyoda-platform/cyoda-go/plugins/sqlite"
	spi "github.com/cyoda-platform/cyoda-go-spi"
)

func TestCountByState_SQLite_EmptyStatesShortCircuits(t *testing.T) {
	ctx := context.Background()
	factory, err := sqlite.New(sqlite.Config{DSN: ":memory:"})
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	defer factory.Close()

	tenantCtx := spi.WithTenantID(ctx, "t1")
	es, err := factory.EntityStore(tenantCtx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}

	mref := spi.ModelRef{EntityName: "m", ModelVersion: "1"}
	got, err := es.CountByState(tenantCtx, mref, []string{})
	if err != nil {
		t.Fatalf("CountByState: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for empty states slice, got %v", got)
	}
}
```

> Adapt `sqlite.New(...)` and config field names by checking `grep -n "func New" plugins/sqlite/plugin.go`.

- [ ] **Step 2: Run to confirm FAIL**

```bash
go test -run TestCountByState_SQLite_EmptyStatesShortCircuits -v ./plugins/sqlite/...
```
Expected: FAIL (build error — `CountByState` not yet implemented).

### Task 1.5: SQLite plugin — implement CountByState (GREEN)

**Files:**
- Modify: `plugins/sqlite/entity_store.go` — add method after existing `Count` (line ~798)

- [ ] **Step 1: Confirm meta JSON contains state under `$.state`**

```bash
grep -n "marshalEntityMeta\|State string" plugins/sqlite/entity_store.go | head -5
```
Confirm `entityMetaDB.State` is tagged `json:"state,omitempty"`. The query path for state is therefore `json_extract(meta, '$.state')`.

- [ ] **Step 2: Add CountByState after Count**

In `plugins/sqlite/entity_store.go`, immediately after the closing brace of `Count` (after line ~798):

```go
// CountByState returns counts of non-deleted entities grouped by state for the
// given model. See SPI godoc on EntityStore.CountByState for filter semantics.
//
// The state value is stored inside the meta BLOB as JSON; we extract it via
// json_extract(meta, '$.state'). An indexed expression on this extraction is
// a future optimization (out of scope for this issue).
func (s *entityStore) CountByState(ctx context.Context, modelRef spi.ModelRef, states []string) (map[string]int64, error) {
	if states != nil && len(states) == 0 {
		return map[string]int64{}, nil
	}

	tx := spi.GetTransaction(ctx)
	if tx != nil {
		// In-tx: use GetAll's merged-view logic (matches existing Count's in-tx fallback).
		all, err := s.GetAll(ctx, modelRef)
		if err != nil {
			return nil, err
		}
		var filter map[string]struct{}
		if states != nil {
			filter = make(map[string]struct{}, len(states))
			for _, st := range states {
				filter[st] = struct{}{}
			}
		}
		result := make(map[string]int64)
		for _, e := range all {
			st := e.Meta.State
			if filter != nil {
				if _, ok := filter[st]; !ok {
					continue
				}
			}
			result[st]++
		}
		return result, nil
	}

	// Non-transaction: aggregate at the database.
	args := []any{string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion}
	q := `SELECT COALESCE(json_extract(meta, '$.state'), '') AS state, COUNT(*)
	      FROM entities
	      WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND NOT deleted`

	if states != nil {
		// Build IN (?, ?, ...) placeholder list.
		placeholders := make([]byte, 0, 2*len(states))
		for i := range states {
			if i > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
		}
		q += ` AND json_extract(meta, '$.state') IN (` + string(placeholders) + `)`
		for _, st := range states {
			args = append(args, st)
		}
	}
	q += ` GROUP BY state`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("count by state: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, fmt.Errorf("scan count by state: %w", err)
		}
		result[st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate count by state: %w", err)
	}
	return result, nil
}
```

- [ ] **Step 3: Run unit test (GREEN)**

```bash
go test -run TestCountByState_SQLite_EmptyStatesShortCircuits -v ./plugins/sqlite/...
```
Expected: PASS.

- [ ] **Step 4: Run conformance suite for sqlite**

```bash
go test -run TestConformance/EntityStore/CountByState -v ./plugins/sqlite/...
```
Expected: PASS.

- [ ] **Step 5: Run full sqlite test suite (regression)**

```bash
go test ./plugins/sqlite/... -v 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add plugins/sqlite/entity_store.go plugins/sqlite/entity_store_count_by_state_test.go
git commit -m "feat(sqlite): implement EntityStore.CountByState"
```

### Task 1.6: Postgres plugin — write focused test (RED)

**Files:**
- Create: `plugins/postgres/entity_store_count_by_state_test.go`

- [ ] **Step 1: Add focused test mirroring sqlite's**

Create `plugins/postgres/entity_store_count_by_state_test.go`:

```go
package postgres_test

import (
	"context"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

func TestCountByState_Postgres_EmptyStatesShortCircuits(t *testing.T) {
	if testing.Short() {
		t.Skip("postgres tests require -short=false and a running postgres")
	}
	ctx, factory := setupPostgresTest(t) // existing helper
	tenantCtx := spi.WithTenantID(ctx, "t-cbs-empty")
	es, err := factory.EntityStore(tenantCtx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}

	mref := spi.ModelRef{EntityName: "m", ModelVersion: "1"}
	got, err := es.CountByState(tenantCtx, mref, []string{})
	if err != nil {
		t.Fatalf("CountByState: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for empty states slice, got %v", got)
	}
}
```

> Adapt the helper name to the actual postgres test setup (check `grep -n "func setup\|func newTest" plugins/postgres/postgres_test.go`).

- [ ] **Step 2: Run to confirm FAIL**

```bash
go test -run TestCountByState_Postgres_EmptyStatesShortCircuits -v ./plugins/postgres/...
```
Expected: FAIL (build error — `CountByState` not yet implemented).

### Task 1.7: Postgres plugin — implement CountByState (GREEN)

**Files:**
- Modify: `plugins/postgres/entity_store.go` — add method after existing `Count` (line ~386)

- [ ] **Step 1: Confirm doc JSON shape**

```bash
grep -n "_meta\|State\b" plugins/postgres/entity_doc.go | head -5
```
Confirm `_meta.state` is the path. Postgres extraction syntax: `doc -> '_meta' ->> 'state'` (the `->>` returns text).

- [ ] **Step 2: Add CountByState after Count**

In `plugins/postgres/entity_store.go`, immediately after the closing brace of `Count` (after line ~386):

```go
// CountByState returns counts of non-deleted entities grouped by state for the
// given model. See SPI godoc on EntityStore.CountByState for filter semantics.
//
// State is stored inside the doc JSONB at $._meta.state. An indexed expression
// (e.g. CREATE INDEX ON entities ((doc->'_meta'->>'state')) WHERE NOT deleted)
// is a future optimization (out of scope for this issue).
//
// Deliberately not tracked in readSet: aggregate with no per-row identity.
// See Count's note on phantom reads.
func (s *entityStore) CountByState(ctx context.Context, modelRef spi.ModelRef, states []string) (map[string]int64, error) {
	if states != nil && len(states) == 0 {
		return map[string]int64{}, nil
	}

	args := []any{string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion}
	q := `SELECT COALESCE(doc -> '_meta' ->> 'state', '') AS state, COUNT(*)
	      FROM entities
	      WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND NOT deleted`

	if states != nil {
		args = append(args, states)
		q += ` AND doc -> '_meta' ->> 'state' = ANY($4)`
	}
	q += ` GROUP BY state`

	rows, err := s.q.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to count entities by state: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, fmt.Errorf("scan count by state: %w", err)
		}
		result[st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate count by state: %w", err)
	}
	return result, nil
}
```

- [ ] **Step 3: Run unit test (GREEN)**

```bash
go test -run TestCountByState_Postgres_EmptyStatesShortCircuits -v ./plugins/postgres/...
```
Expected: PASS.

- [ ] **Step 4: Run conformance suite for postgres**

```bash
go test -run TestConformance/EntityStore/CountByState -v ./plugins/postgres/...
```
Expected: PASS.

- [ ] **Step 5: Run full postgres test suite**

```bash
go test ./plugins/postgres/... -v 2>&1 | tail -30
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add plugins/postgres/entity_store.go plugins/postgres/entity_store_count_by_state_test.go
git commit -m "feat(postgres): implement EntityStore.CountByState

Aggregates at the database via doc->'_meta'->>'state' extraction and
GROUP BY. Supports optional state filter via state = ANY(\$4)."
```

### Task 1.8: Switch handler to use CountByState — write failing test

**Files:**
- Modify: `internal/domain/entity/service_test.go` (or create if absent — check existing tests first)

- [ ] **Step 1: Check existing handler tests for stats**

```bash
grep -rn "GetStatisticsByState\b\|TestGetStatistics" internal/domain/entity/ | head -10
```

- [ ] **Step 2: Add a focused test asserting the handler calls CountByState (no GetAll)**

The simplest integration-style test against the memory plugin proves end-to-end behavior. Append to `internal/domain/entity/service_test.go` (create if it doesn't exist with package `entity` — match whatever package the file uses):

```go
func TestGetStatisticsByState_UsesCountByState(t *testing.T) {
	// Arrange: factory + handler backed by memory plugin.
	ctx := context.Background()
	factory, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	defer factory.Close()

	tenantCtx := spi.WithTenantID(ctx, "t-stats")
	h := NewHandler(factory) // adapt to actual constructor signature

	// Save a model and three entities in two states.
	mref := spi.ModelRef{EntityName: "stats-model", ModelVersion: "1"}
	mstore, _ := factory.ModelStore(tenantCtx)
	require.NoError(t, mstore.Save(tenantCtx, &spi.ModelDescriptor{ModelRef: mref}))

	es, _ := factory.EntityStore(tenantCtx)
	for i, st := range []string{"new", "new", "approved"} {
		e := &spi.Entity{
			Meta: spi.EntityMeta{ID: fmt.Sprintf("e%d", i), ModelRef: mref, State: st},
			Data: []byte(`{}`),
		}
		_, err := es.Save(tenantCtx, e)
		require.NoError(t, err)
	}

	// Act: nil filter -> all states.
	stats, err := h.GetStatisticsByState(tenantCtx, nil)
	require.NoError(t, err)

	// Assert: 2 in "new", 1 in "approved".
	got := map[string]int64{}
	for _, s := range stats {
		got[s.State] = s.Count
	}
	require.Equal(t, map[string]int64{"new": 2, "approved": 1}, got)

	// Filter to "approved".
	filter := []string{"approved"}
	stats, err = h.GetStatisticsByState(tenantCtx, &filter)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "approved", stats[0].State)
	require.Equal(t, int64(1), stats[0].Count)

	// Empty (non-nil) slice: per SPI contract, empty map -> no stats rows.
	emptyFilter := []string{}
	stats, err = h.GetStatisticsByState(tenantCtx, &emptyFilter)
	require.NoError(t, err)
	require.Empty(t, stats)
}
```

> Adapt the `NewHandler` call and `ModelDescriptor` field names to match the actual signatures — check `grep -n "func NewHandler\|type ModelDescriptor" internal/domain/entity/ /Users/paul/go/pkg/mod/github.com/cyoda-platform/cyoda-go-spi@v0.5.0/types.go`.

- [ ] **Step 3: Run to confirm FAIL**

```bash
go test -run TestGetStatisticsByState_UsesCountByState -v ./internal/domain/entity/
```
Expected: PASS for the "all states" case (because the existing `GetAll` loop also produces correct results), but FAIL for the empty-slice case if the current handler treats `&[]string{}` as "no filter" — which it does (the current code only checks `if states != nil`, then iterates the empty slice for membership, finds nothing, skips everything; result: empty stats — actually this might pass too).

If the test passes against the current implementation, that's fine — the test now serves as a regression guard. The implementation change in Task 1.9 must keep all three assertions green.

### Task 1.9: Switch handler to use CountByState (GREEN)

**Files:**
- Modify: `internal/domain/entity/service.go:316-367` (`GetStatisticsByState`) and `:370-414` (`GetStatisticsByStateForModel`)

- [ ] **Step 1: Replace GetStatisticsByState body**

In `internal/domain/entity/service.go`, replace the body of `GetStatisticsByState` (lines 316-367) with:

```go
// GetStatisticsByState retrieves entity count statistics by state for all models.
//
// Known limitation (follow-up): this still iterates every model definition and
// issues one CountByState call per model. For tenants with many models, the
// per-model fan-out is the next pressure point now that the per-entity loading
// bottleneck is gone. Possible directions for a follow-up: a batched
// CountByStateAll SPI method, or bounded parallelism over models.
func (h *Handler) GetStatisticsByState(ctx context.Context, states *[]string) ([]EntityStatByState, error) {
	modelStore, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	refs, err := modelStore.GetAll(ctx)
	if err != nil {
		return nil, common.Internal("failed to list models", err)
	}

	// Dereference the optional filter. Distinguish nil-pointer (no filter)
	// from pointer-to-empty-slice (per SPI: empty map, no storage call).
	var filterStates []string
	if states != nil {
		filterStates = *states
	}

	result := make([]EntityStatByState, 0)
	for _, ref := range refs {
		counts, err := entityStore.CountByState(ctx, ref, filterStates)
		if err != nil {
			return nil, common.Internal("failed to count entities by state", err)
		}
		for state, count := range counts {
			result = append(result, EntityStatByState{
				ModelName:    ref.EntityName,
				ModelVersion: ref.ModelVersion,
				State:        state,
				Count:        count,
			})
		}
	}
	return result, nil
}
```

- [ ] **Step 2: Replace GetStatisticsByStateForModel body**

In `internal/domain/entity/service.go`, replace the body of `GetStatisticsByStateForModel` (lines 370-414) with:

```go
// GetStatisticsByStateForModel retrieves entity count statistics by state for a specific model.
func (h *Handler) GetStatisticsByStateForModel(ctx context.Context, entityName string, modelVersion string, states *[]string) ([]EntityStatByState, error) {
	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	ref := spi.ModelRef{
		EntityName:   entityName,
		ModelVersion: modelVersion,
	}

	var filterStates []string
	if states != nil {
		filterStates = *states
	}

	counts, err := entityStore.CountByState(ctx, ref, filterStates)
	if err != nil {
		return nil, common.Internal("failed to count entities by state", err)
	}

	result := make([]EntityStatByState, 0, len(counts))
	for state, count := range counts {
		result = append(result, EntityStatByState{
			ModelName:    entityName,
			ModelVersion: modelVersion,
			State:        state,
			Count:        count,
		})
	}
	return result, nil
}
```

- [ ] **Step 3: Run handler test (GREEN)**

```bash
go test -run TestGetStatisticsByState_UsesCountByState -v ./internal/domain/entity/
```
Expected: PASS.

- [ ] **Step 4: Run full domain test suite (regression)**

```bash
go test ./internal/domain/... -v 2>&1 | tail -30
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/entity/service.go internal/domain/entity/service_test.go
git commit -m "refactor(entity): use CountByState for state statistics

Replaces the GetAll-then-count-in-Go loop with a single CountByState
SPI call per model. Memory bottleneck eliminated. Per-model fan-out
remains and is documented as a known limitation for follow-up."
```

### Task 1.10: Extend E2E test with state filter assertion

**Files:**
- Modify: `internal/e2e/entity_lifecycle_test.go` — extend `TestEntityLifecycle_Statistics` (around line 349)

- [ ] **Step 1: Verify the existing test still passes after handler change**

```bash
go test -run TestEntityLifecycle_Statistics -v ./internal/e2e/
```
Expected: PASS (Docker must be running).

- [ ] **Step 2: Extend the test to assert state filtering**

In `internal/e2e/entity_lifecycle_test.go`, find the closing brace of `TestEntityLifecycle_Statistics` (around line 402, after `t.Error("state stats: expected CREATED state in response")`). Insert before the closing `}`:

```go
	// State filter: request only CREATED — APPROVED must not appear.
	filteredPath := fmt.Sprintf("/api/entity/stats/states/%s/%d?states=CREATED", model, 1)
	filteredResp := doAuth(t, http.MethodGet, filteredPath, "")
	filteredBody := readBody(t, filteredResp)
	if filteredResp.StatusCode != http.StatusOK {
		t.Fatalf("filtered state stats: expected 200, got %d: %s", filteredResp.StatusCode, filteredBody)
	}
	if !strings.Contains(filteredBody, "CREATED") {
		t.Error("filtered state stats: expected CREATED in response")
	}
	if strings.Contains(filteredBody, "APPROVED") {
		t.Error("filtered state stats: APPROVED must NOT appear when filter is CREATED only")
	}
```

> Verify the query-parameter name (`states`) matches the OpenAPI spec — `grep -n "states" api/spec.yaml` or check `GetEntityStatisticsByStateForModelParams` in `api/generated.go`.

- [ ] **Step 3: Run extended E2E**

```bash
go test -run TestEntityLifecycle_Statistics -v ./internal/e2e/
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/e2e/entity_lifecycle_test.go
git commit -m "test(e2e): assert state filter in stats-by-state endpoint"
```

### Task 1.11: Full PR-1 verification

- [ ] **Step 1: Vet**

```bash
go vet ./...
```
Expected: no output.

- [ ] **Step 2: Race-free unit + integration**

```bash
go test -race -short ./...
```
Expected: all PASS.

- [ ] **Step 3: Full suite (including E2E)**

```bash
go test ./... 2>&1 | tail -40
```
Expected: all PASS. Docker must be running for E2E.

- [ ] **Step 4: Push the branch and open PR-1**

```bash
git push -u origin <branch-name>
gh pr create --title "feat: CountByState SPI + plugin impls + handler switch (issue #24 part 1/3)" --body "$(cat <<'EOF'
## Summary

Resolves the stats-scalability portion of #24:

- Bumps `cyoda-go-spi` to `v0.5.0` (adds `EntityStore.CountByState`)
- Bumps cassandra plugin to the matching release
- Implements `CountByState` in memory, sqlite, postgres
- Switches `Handler.GetStatisticsByState` and `GetStatisticsByStateForModel` to use the new method, eliminating the `GetAll`-and-count-in-Go loop

Two follow-up PRs (XML `json.Number` precision, `matchArray` numeric comparison) are tracked in #24 and will land sequentially.

## Test plan

- [ ] `go test ./... -race` green
- [ ] `go vet ./...` clean
- [ ] SPI conformance suite passes for memory, sqlite, postgres
- [ ] E2E `TestEntityLifecycle_Statistics` passes with new state-filter assertion

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## PR-2: XML json.Number precision

> **Working directory:** the cyoda-go worktree. PR-2 can begin once PR-1 is merged (or in a separate branch off PR-1's base).

### Task 2.1: Write failing tests for inferXMLValue

**Files:**
- Create: `internal/domain/model/importer/parser_xml_value_test.go`

- [ ] **Step 1: Write the test file**

Create `internal/domain/model/importer/parser_xml_value_test.go`:

```go
package importer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInferXMLValue_Numeric(t *testing.T) {
	cases := []struct {
		input string
		want  json.Number
	}{
		{"0", "0"},
		{"-0", "-0"},
		{"42", "42"},
		{"9007199254740993", "9007199254740993"}, // > 2^53, must NOT round
		{"-123", "-123"},
		{"123.456", "123.456"},
		{"-0.5", "-0.5"},
		{"1e10", "1e10"},
		{"1.5e-5", "1.5e-5"},
		{"1E2", "1E2"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := inferXMLValue(tc.input)
			n, ok := got.(json.Number)
			if !ok {
				t.Fatalf("inferXMLValue(%q) = %T (%v), want json.Number", tc.input, got, got)
			}
			if string(n) != string(tc.want) {
				t.Errorf("inferXMLValue(%q) = %q, want %q", tc.input, n, tc.want)
			}
		})
	}
}

func TestInferXMLValue_Bool(t *testing.T) {
	if got := inferXMLValue("true"); got != true {
		t.Errorf("true: got %v (%T)", got, got)
	}
	if got := inferXMLValue("false"); got != false {
		t.Errorf("false: got %v (%T)", got, got)
	}
}

func TestInferXMLValue_RejectedNumerics(t *testing.T) {
	// JSON-grammar edge cases that MUST NOT be accepted as json.Number.
	rejected := []string{
		"007", "00", "01.5", // leading zeros
		"-",                  // lone minus
		"+1",                 // leading plus
		"1.",                 // trailing dot
		".5",                 // no integer part
		"1e", "1e+",          // incomplete exponent
		"NaN", "Inf", "-Inf", // float literals not in JSON grammar
		"0x1a",                // hex
		"",                    // empty
		"hello",               // non-numeric
	}
	for _, s := range rejected {
		t.Run(s, func(t *testing.T) {
			got := inferXMLValue(s)
			if _, isNum := got.(json.Number); isNum {
				t.Errorf("inferXMLValue(%q) returned json.Number; expected string or bool", s)
			}
		})
	}
}

func TestInferXMLValue_String(t *testing.T) {
	if got := inferXMLValue("hello"); got != "hello" {
		t.Errorf("hello: got %v (%T)", got, got)
	}
	if got := inferXMLValue(""); got != "" {
		t.Errorf("empty: got %v (%T)", got, got)
	}
}

func TestParseXML_JSON_Symmetry_LargeInt(t *testing.T) {
	// XML and JSON parsers must produce structurally identical trees for
	// numeric leaves.
	xmlPayload := `<root><big>9007199254740993</big></root>`
	jsonPayload := `{"big": 9007199254740993}`

	xmlAny, err := ParseXML(strings.NewReader(xmlPayload))
	if err != nil {
		t.Fatalf("ParseXML: %v", err)
	}
	jsonAny, err := ParseJSON(strings.NewReader(jsonPayload))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}

	xmlMap := xmlAny.(map[string]any)
	jsonMap := jsonAny.(map[string]any)

	xmlVal, _ := xmlMap["big"].(json.Number)
	jsonVal, _ := jsonMap["big"].(json.Number)
	if string(xmlVal) != string(jsonVal) {
		t.Errorf("XML %q != JSON %q for large int", xmlVal, jsonVal)
	}
	if string(xmlVal) != "9007199254740993" {
		t.Errorf("XML rendered %q, expected %q", xmlVal, "9007199254740993")
	}
}
```

- [ ] **Step 2: Run to confirm failures**

```bash
go test -run TestInferXMLValue ./internal/domain/model/importer/ -v
```
Expected: many FAIL — current implementation returns `int64`/`float64`, not `json.Number`. Confirm at least one failure says something like `got int64, want json.Number`.

### Task 2.2: Implement json.Number coercion

**Files:**
- Modify: `internal/domain/model/importer/parser.go:101-112`

- [ ] **Step 1: Replace inferXMLValue**

In `internal/domain/model/importer/parser.go`, replace lines 101-112:

```go
func inferXMLValue(s string) any {
	// Defer numeric coercion: keep numbers as json.Number so callers can
	// choose Int64() vs Float64() vs string preservation. Mirrors
	// ParseJSON's UseNumber() — XML and JSON imports produce structurally
	// identical trees for numeric leaves.
	if isJSONNumber(s) {
		return json.Number(s)
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return s
}

// isJSONNumber reports whether s is a valid JSON number per RFC 8259 §6.
// Delegates to encoding/json so the validation rules stay aligned with the
// authority that downstream code uses to round-trip the value.
func isJSONNumber(s string) bool {
	if s == "" {
		return false
	}
	var n json.Number
	return json.Unmarshal([]byte(s), &n) == nil
}
```

- [ ] **Step 2: Remove unused import if needed**

`strconv` is still used for `ParseBool`. No import change needed. Run:
```bash
go build ./internal/domain/model/importer/
```
Expected: SUCCESS.

- [ ] **Step 3: Run new tests (GREEN)**

```bash
go test -run TestInferXMLValue -run TestParseXML_JSON_Symmetry_LargeInt ./internal/domain/model/importer/ -v
```
Expected: all PASS.

### Task 2.3: Audit downstream consumers

- [ ] **Step 1: Find consumers of ParseXML and inferXMLValue**

```bash
grep -rn "ParseXML\b\|importer\.ParseXML\b\|inferXMLValue\b" --include="*.go" .
```

- [ ] **Step 2: Grep for typed assertions on int64/float64 from importer output**

For each consumer found in Step 1, check whether it does a type switch like `case int64:` or assertion `.(int64)` / `.(float64)` on the parsed tree:

```bash
# Example pattern — adapt to actual consumer file paths
grep -nE "\.\((int64|float64)\)" <consumer-file>
```

- [ ] **Step 3: Run the full importer-consumer test suite**

```bash
go test ./internal/domain/model/... -v 2>&1 | tail -30
```
Expected: all PASS. Any failure is a real consumer that needs a fix.

- [ ] **Step 4: Fix any consumer failure inline (per Gate 6)**

If a consumer breaks, change its type switch/assertion to handle `json.Number`. Typical pattern:

```go
// BEFORE:
case int64:
    return v
case float64:
    return v
// AFTER:
case json.Number:
    if i, err := v.Int64(); err == nil {
        return i
    }
    f, _ := v.Float64()
    return f
```

Re-run after each fix.

- [ ] **Step 5: Run race + full test suite**

```bash
go test -race ./...
```
Expected: all PASS.

### Task 2.4: Commit and PR-2

- [ ] **Step 1: Commit**

```bash
git add internal/domain/model/importer/parser.go internal/domain/model/importer/parser_xml_value_test.go
# Plus any consumer fixes from Task 2.3 Step 4.
git commit -m "fix(importer): preserve numeric precision in XML via json.Number

XML inferXMLValue previously cast int64 to float64, losing precision
beyond 2^53. Now produces json.Number for all valid JSON numbers,
matching ParseJSON's UseNumber() behavior. Validates strictly against
the JSON grammar via json.Unmarshal — leading zeros, lone minus,
trailing dot, NaN/Inf, hex literals all rejected.

Closes the XML-precision portion of #24."
```

- [ ] **Step 2: Push and open PR-2**

```bash
git push
gh pr create --title "fix(importer): XML json.Number precision (issue #24 part 2/3)" --body "$(cat <<'EOF'
## Summary

Resolves the XML-precision portion of #24. `inferXMLValue` now produces `json.Number` for all valid JSON numbers, mirroring `ParseJSON`. Strict JSON-grammar validation via `json.Unmarshal` rejects `NaN`/`Inf`/hex/leading-zeros/lone-minus/trailing-dot.

## Test plan

- [ ] New unit tests cover large ints (>2^53), JSON-grammar edge cases, and XML/JSON symmetry
- [ ] `go test -race ./...` green
- [ ] Downstream consumer audit complete; any fixes included

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## PR-3: matchArray + toFloat64

> **Working directory:** the cyoda-go worktree. PR-3 can begin once PR-2 is merged.

### Task 3.1: Write failing test for toFloat64(json.Number)

**Files:**
- Modify or create: `internal/match/operators_test.go`

- [ ] **Step 1: Check whether operators_test.go exists**

```bash
ls internal/match/operators_test.go 2>/dev/null && echo "exists" || echo "create"
```

- [ ] **Step 2: Add toFloat64 json.Number test**

Append to `internal/match/operators_test.go` (create if absent with `package match`):

```go
package match

import (
	"encoding/json"
	"testing"
)

func TestToFloat64_JSONNumber(t *testing.T) {
	cases := []struct {
		in   json.Number
		want float64
	}{
		{"0", 0},
		{"42", 42},
		{"-1.5", -1.5},
		{"1e10", 1e10},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			got, err := toFloat64(tc.in)
			if err != nil {
				t.Fatalf("toFloat64(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("toFloat64(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run to confirm FAIL**

```bash
go test -run TestToFloat64_JSONNumber -v ./internal/match/
```
Expected: FAIL with `cannot convert json.Number to float64`.

### Task 3.2: Extend toFloat64 (GREEN)

**Files:**
- Modify: `internal/match/operators.go:243-258`

- [ ] **Step 1: Add the encoding/json import if needed**

Check `internal/match/operators.go` imports — likely missing `encoding/json`. Add to the import block.

- [ ] **Step 2: Add json.Number case**

In `internal/match/operators.go`, find the `toFloat64` switch (line ~244). Add a case before `case string:`:

```go
	case json.Number:
		return n.Float64()
```

The full switch should now look like:
```go
func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case json.Number:
		return n.Float64()
	case string:
		return strconv.ParseFloat(n, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}
```

- [ ] **Step 3: Run to confirm GREEN**

```bash
go test -run TestToFloat64_JSONNumber -v ./internal/match/
```
Expected: PASS.

### Task 3.3: Write failing tests for matchArray numeric handling

**Files:**
- Modify: `internal/match/match_test.go` — append after line ~666 (existing `TestMatchArrayConditionMismatch`)

- [ ] **Step 1: Confirm sampleData has a numeric array, or add one**

```bash
grep -n "sampleData\b\|scores" internal/match/match_test.go | head -10
```

If `sampleData` doesn't have `"scores": [1, 2, 3]`, the new tests will use a local literal. We use a local literal regardless — keeps tests self-contained.

- [ ] **Step 2: Append numeric-array tests**

Append to `internal/match/match_test.go`:

```go
// --- Issue #24: matchArray numeric-aware comparison ---

func TestMatchArrayCondition_NumericInt(t *testing.T) {
	data := []byte(`{"scores":[1,2,3]}`)
	cond := &predicate.ArrayCondition{
		JsonPath: "$.scores",
		Values:   []any{1, 2, 3}, // Go int
	}
	got, err := Match(cond, data, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected match for int values against numeric JSON array")
	}
}

func TestMatchArrayCondition_NumericInt64(t *testing.T) {
	data := []byte(`{"scores":[1,2,3]}`)
	cond := &predicate.ArrayCondition{
		JsonPath: "$.scores",
		Values:   []any{int64(1), int64(2), int64(3)},
	}
	got, err := Match(cond, data, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected match for int64 values against numeric JSON array")
	}
}

func TestMatchArrayCondition_NumericFloat64(t *testing.T) {
	data := []byte(`{"scores":[1,2,3]}`)
	cond := &predicate.ArrayCondition{
		JsonPath: "$.scores",
		Values:   []any{1.0, 2.0, 3.0},
	}
	got, err := Match(cond, data, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected match for float64 values against numeric JSON array")
	}
}

func TestMatchArrayCondition_JSONNumber(t *testing.T) {
	// Predicates built from XML imports (after PR-2) deliver json.Number.
	data := []byte(`{"scores":[1.5]}`)
	cond := &predicate.ArrayCondition{
		JsonPath: "$.scores",
		Values:   []any{json.Number("1.5")},
	}
	got, err := Match(cond, data, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected match for json.Number expected against numeric JSON array")
	}
}

func TestMatchArrayCondition_TypeMismatch(t *testing.T) {
	// String entity field, numeric expected — must NOT match.
	data := []byte(`{"tags":["go"]}`)
	cond := &predicate.ArrayCondition{
		JsonPath: "$.tags",
		Values:   []any{42},
	}
	got, err := Match(cond, data, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected no match: numeric expected against string JSON array element")
	}
}
```

- [ ] **Step 3: Add `encoding/json` import to the test file**

```bash
grep -n "^import\|encoding/json" internal/match/match_test.go | head -5
```
If `encoding/json` is not yet imported, add it to the import block.

- [ ] **Step 4: Run to confirm FAIL**

```bash
go test -run TestMatchArrayCondition_Numeric -run TestMatchArrayCondition_JSONNumber -run TestMatchArrayCondition_TypeMismatch -v ./internal/match/
```
Expected: most numeric tests FAIL — `int64`/`float64`/`json.Number` predicates don't match because of the `Sprintf("%v")` divergence. The string-vs-number `TypeMismatch` test may already pass.

### Task 3.4: Replace matchArray comparison with opEquals (GREEN)

**Files:**
- Modify: `internal/match/match.go:158-164`

- [ ] **Step 1: Replace the comparison**

In `internal/match/match.go`, replace lines 158-164:

```go
		elemPath := fmt.Sprintf("%s.%d", basePath, i)
		result := gjson.GetBytes(data, elemPath)

		// Delegate to opEquals: it handles numeric-aware comparison
		// (gjson.Number actual + numeric expected) consistently with
		// scalar EQUALS, so per-element semantics don't diverge.
		if !result.Exists() || !opEquals(result, expected) {
			return false, nil
		}
```

- [ ] **Step 2: Run new tests (GREEN)**

```bash
go test -run TestMatchArrayCondition -v ./internal/match/
```
Expected: all PASS, including the existing `TestMatchArrayCondition` and `TestMatchArrayConditionMismatch`.

- [ ] **Step 3: Run full match suite (regression)**

```bash
go test ./internal/match/... -v 2>&1 | tail -30
```
Expected: all PASS.

### Task 3.5: Full regression and commit

- [ ] **Step 1: Vet and race**

```bash
go vet ./...
go test -race -short ./...
```
Expected: clean + all PASS.

- [ ] **Step 2: Full suite**

```bash
go test ./... 2>&1 | tail -30
```
Expected: all PASS (Docker required for E2E).

- [ ] **Step 3: Commit**

```bash
git add internal/match/match.go internal/match/match_test.go internal/match/operators.go internal/match/operators_test.go
git commit -m "fix(match): numeric-aware array element comparison

matchArray previously compared via fmt.Sprintf(\"%v\"), which diverges
from scalar EQUALS for int64/float64/json.Number expected values. Now
delegates to opEquals so per-element equality matches scalar EQUALS
exactly. toFloat64 extended to handle json.Number — required after
PR-2 made XML imports produce json.Number values.

Closes the matchArray portion of #24."
```

- [ ] **Step 4: Push and open PR-3**

```bash
git push
gh pr create --title "fix(match): matchArray numeric comparison via opEquals (issue #24 part 3/3)" --body "$(cat <<'EOF'
## Summary

Closes #24. matchArray now delegates element comparison to opEquals so it picks up the existing numeric-aware path (gjson.Number actual + numeric expected). toFloat64 extended to handle json.Number — required after PR-2 made XML imports produce json.Number values, and benefits all numeric operators uniformly.

## Test plan

- [ ] New tests cover int / int64 / float64 / json.Number predicates against numeric JSON arrays
- [ ] Existing array-condition tests still pass (regression guard)
- [ ] `go test -race ./...` green

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-Review Checklist (executed)

**Spec coverage:** Each spec section has corresponding tasks:
- §2 (CountByState SPI + plugins) → Tasks A1–A3, B1–B3, 1.1–1.11
- §3 (XML json.Number) → Tasks 2.1–2.4
- §4 (matchArray + toFloat64) → Tasks 3.1–3.5

**Placeholder scan:** No "TBD" or "implement later". Every step has explicit code, command, or expected output. The "adapt to actual signature" notes are specific verifications with the exact `grep` to run, not vague TODOs.

**Type consistency:**
- `EntityStore` (memory) is `*EntityStore`; sqlite/postgres use `*entityStore` — matched in their respective tasks.
- `spi.ModelRef`, `spi.Entity`, `spi.EntityMeta`, `spi.GetTransaction` — verified against `cyoda-go-spi@v0.4.0` types and used consistently.
- Handler `*[]string` parameter dereferenced as `var filterStates []string; if states != nil { filterStates = *states }` in both Task 1.9 implementations.
- `EntityStat`/`EntityStatByState` field names (`ModelName`, `ModelVersion`, `State`, `Count`) — match the existing types in `service.go`.
- `predicate.ArrayCondition{JsonPath, Values []any}` — matches SPI definition at `predicate/condition.go:36`.

No issues found.
