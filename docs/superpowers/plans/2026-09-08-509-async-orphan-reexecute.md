# Async Orphan Re-execution (#509) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An async-search job whose executor node dies is claimed by a live node, its partial results cleared, and re-executed as-at its stored PointInTime — so node loss becomes invisible to async clients on engine-executed backends, bounded by an attempt cap.

**Architecture:** One new fenced SPI method (`Release`) plus one new job field (`StaleClaims`) let a departing node hand a job off promptly instead of leaving it to age out. The engine's stale-job reaper stops failing claimed jobs and instead re-enqueues them into its own worker pool at the claimed epoch; the attempt cap bounds *executor losses* (staleness claims), not graceful handoffs. The claim sweep moves onto the heartbeat-interval ticker with a startup sweep; shutdown releases in-flight jobs rather than failing them.

**Tech Stack:** Go 1.26+, `log/slog`, `github.com/cyoda-platform/cyoda-go-spi`, in-tree plugins (memory/sqlite/postgres), testcontainers-go (postgres:17-alpine), spitest conformance harness.

**Spec:** `docs/superpowers/specs/2026-09-08-509-async-orphan-reexecute-design.md` (read it alongside this plan; the plan argues from it).

## Global Constraints

- **Go 1.26+**, `log/slog` only (never `log.Printf`/`fmt.Printf`), errors wrapped `fmt.Errorf("...: %w", err)`, `uuid.UUID` not `string`, `CYODA_`-prefixed env vars.
- **Correctness over availability, fail closed.** A job whose required computation cannot run is never committed with a guessed/omitted value. `ClearResults` failure → `Release` (retry), never enqueue over unknown residue.
- **Multi-node is the primary target.** Cross-node correctness (a peer completing an orphaned job with a singly-authored result set) is designed in, not descoped.
- **No test hooks in production code.** Non-functional properties (the postgres idle-in-transaction timeout) are reviewed with a recorded TDD waiver, never asserted via injected counters.
- **No issue IDs in shipped artefacts** (errors, logs, responses, comments, OpenAPI/help). PR bodies, commits, specs, CHANGELOG history only.
- **Coordinated SPI release window** (see `cyoda-go-spi/MAINTAINING.md`): SPI change lands on SPI `main` first and is pushed (not tagged); cyoda-go develops against it via an **uncommitted** `go.work use` line; the pin bump (root + 3 plugin `go.mod`s in one commit) is the final step; `make repin-plugins` after plugin changes; `make check-spi-pin-sync` green. Never `git add -A` while the local SPI `use` line is present.
- **Attempt-cap default = 3**, min 1; value 1 reproduces claim-then-FAIL.
- **The FAILED status after the cap is contractual; the message text is not.** No new wire error code is added.
- **Verification:** `make test` while iterating; `make test-full` (Docker required; `make preflight` first) at the end; `go vet ./...`; `make race` once before PR.

## File Structure

**cyoda-go-spi (sibling checkout `/Users/paul/go-projects/cyoda-light/cyoda-go-spi`):**
- Modify `search_store.go` — add `Release` to `AsyncSearchStore`; add `StaleClaims int64` to `SearchJob`; amend `ClaimStale` doc, interface preamble, `SelfExecutingSearchStore` doc.
- Modify `spitest/asyncsearch.go` — register + implement `Release/*`, `Claim/StaleClaimCounted`, `Release/ClaimNotCounted`, `Claim/ReleasedNotTerminal`.
- Modify `CHANGELOG.md` — `### Breaking` entry.

**Plugins (cyoda-go worktree):**
- `plugins/memory/search_store.go` — `released` on `searchJobEntry`; `Release`; `ClaimStale` predicate + counter; `CreateJob` zeroes `StaleClaims`.
- `plugins/sqlite/migrations/000007_search_release.{up,down}.sql` (new); `plugins/sqlite/search_store.go` — columns, scan, `Release`, `ClaimStale`.
- `plugins/postgres/migrations/000010_search_release.{up,down}.sql` (new); `plugins/postgres/search_store.go` — columns, scan, `Release`, `ClaimStale`, `SaveResults` chunk-tx idle timeout.

**Engine (cyoda-go):**
- `internal/domain/search/service.go` — delete `initialEpoch`; thread epoch through `runAsyncJob`/`startHeartbeat`/`writeAsyncFailure`; `asyncJobHandle` gains `epoch`; `WithCancelCause` + `errJobReleased`; `ReleaseRegisteredJobs`; suppress failure writes on release.
- `internal/domain/search/reaper.go` — delete `FailStaleJobs`; add `(*SearchService).ReclaimStaleJobs`.
- `internal/domain/search/pool.go` — store worker count; add `Cap()`.
- `app/app.go` — split tickers (claim sweep on heartbeat interval + startup sweep; TTL reap own ticker); stop+await sweep on `Shutdown` and `Close`; shutdown calls `ReleaseRegisteredJobs`; delete `AbortRegisteredJobs` wiring; `searchReaperTick` splits.
- `app/config.go` — `SearchJobMaxAttempts` field, default, `ValidateSearchJobMaxAttempts`.
- `cmd/cyoda/help/config_registry.go`, `app/config_registry_binding_test.go` — new var.

**Tests (cyoda-go):**
- `internal/domain/search/reaper_test.go` — rewrite to `ReclaimStaleJobs`; new cases.
- `internal/domain/search/reaper_self_executing_test.go` — adapt to `ReclaimStaleJobs`.
- `internal/domain/search/pool_test.go` — `Cap()` test.
- `app/config_test.go`, `app/config_validate_test.go`, `app/search_reaper_test.go` — new var + split tick.
- `internal/e2e/async_stream_test.go`, `internal/e2e/async_cancel_multinode_test.go` — rewrite reaper/shutdown; new crash/restart/cap e2e.
- `e2e/parity/postgres/multinode_fixture.go`, `e2e/parity/fixtureutil/fixtureutil.go`, new `e2e/parity/postgres/async_node_crash_test.go` — KillNode + SIGKILL cluster test.

**Docs:**
- `cmd/cyoda/help/content/config.md`, `cmd/cyoda/help/content/search.md`, `docs/ARCHITECTURE.md`, `README.md`, `CHANGELOG.md`, new `docs/cloud-parity/async-job-node-failure-resilience.md`, `COMPATIBILITY.md`, `cyoda-go-cassandra` consumer notice.

---

## Prelude: local dev wiring (do once, before Task 1)

- [ ] **Step 1: Point go.work at the local SPI (uncommitted)**

Run (absolute path; `go.work` is tracked — this edit stays uncommitted):

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.claude/worktrees/feat-509-async-reexecute
go work edit -use /Users/paul/go-projects/cyoda-light/cyoda-go-spi
git diff --stat go.work   # shows go.work modified — DO NOT commit this line
```

- [ ] **Step 2: Confirm the SPI checkout is on main and clean**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi && git status --short && git branch --show-current
```
Expected: clean, `main`. If not, stop and surface it.

---

## Task 1: SPI — `Release` method and `StaleClaims` field

**Files:**
- Modify: `cyoda-go-spi/search_store.go`
- Modify: `cyoda-go-spi/CHANGELOG.md`

**Interfaces:**
- Produces: `spi.AsyncSearchStore.Release(ctx context.Context, jobID string, epoch int64) error`; `spi.SearchJob.StaleClaims int64`.

- [ ] **Step 1: Add `StaleClaims` to `SearchJob`** (in `cyoda-go-spi/search_store.go`, immediately after the `Epoch int64` field)

```go
	// StaleClaims counts how many times ClaimStale took this job because
	// its heartbeat went stale — an executor lost without releasing. A
	// claim of a released job (see Release) does NOT count. CreateJob
	// persists 0 regardless of the value on the input job; ClaimStale
	// increments it, atomically with the claim, only for a staleness claim.
	// The engine's attempt cap bounds this counter, not Epoch, so a graceful
	// handoff (Release then claim) never advances a job toward being failed.
	StaleClaims int64
```

- [ ] **Step 2: Add `Release` to the `AsyncSearchStore` interface** (after the `ClearResults` method)

```go
	// Release relinquishes the caller's claim on a RUNNING job without
	// finishing it: the job stays RUNNING and becomes eligible for
	// ClaimStale immediately, regardless of staleAfter, so a live node can
	// take it over without waiting for the heartbeat to age out. Fenced by
	// epoch like every executor-side write: ErrStaleClaim if epoch is not
	// the job's current Epoch, ErrAlreadyTerminal if the job is terminal,
	// ErrNotFound if it does not exist. Idempotent at the same epoch.
	// Release does NOT bump Epoch and does NOT increment StaleClaims — a
	// release is a graceful handoff, not an attempt. The released mark
	// survives a later Heartbeat at the same epoch (a stray stamp from a
	// node that is going away cannot resurrect the job) and is cleared by
	// the ClaimStale that takes the job. Tenant-scoped, like Heartbeat.
	Release(ctx context.Context, jobID string, epoch int64) error
```

- [ ] **Step 3: Amend the interface preamble and `ClaimStale`/`SelfExecutingSearchStore` docs**

In the interface preamble comment, add `Release` to the epoch-fenced set:
- Find `UpdateJobStatus, SaveResults, and Heartbeat each take the` → change to `UpdateJobStatus, SaveResults, Heartbeat, and Release each take the`.

In `ClaimStale`'s doc comment, append after the existing "obtain disjoint sets of jobs" sentence:

```
	// A released job (see Release) is eligible regardless of staleAfter;
	// claiming it clears the released mark and does NOT increment
	// StaleClaims. A job claimed because its heartbeat went stale has
	// StaleClaims incremented, atomically with the claim.
```

In `SelfExecutingSearchStore`'s doc, add `Release` to the no-op list: find `no-op Heartbeat, ClaimStale, and ClearResults` → `no-op Heartbeat, ClaimStale, ClearResults, and Release`.

- [ ] **Step 4: Add the CHANGELOG `### Breaking` entry** (in `cyoda-go-spi/CHANGELOG.md`, top of the `[Unreleased] ### Breaking` list)

```markdown
- **`AsyncSearchStore` gains `Release`; `SearchJob` gains `StaleClaims`.**
  `Release(ctx, jobID, epoch) error` relinquishes a claim without finishing
  the job: it stays RUNNING and is immediately claimable by ClaimStale,
  regardless of staleAfter, so a node departing gracefully hands its
  in-flight jobs off within one claim sweep instead of leaving them to age
  out. Fenced like the other executor-side writes (ErrStaleClaim /
  ErrAlreadyTerminal / ErrNotFound), idempotent at the same epoch, and it
  neither bumps Epoch nor counts as an attempt. `SearchJob.StaleClaims int64`
  is the new attempt counter: ClaimStale increments it only for a staleness
  claim (never for a released job), CreateJob persists 0. This retires the
  "no SPI change" note on the deferred re-execution follow-up: the engine's
  attempt cap now bounds StaleClaims, so a rolling restart of any length is
  free.

  Migration: implement `Release` on every `AsyncSearchStore` (a
  `SelfExecutingSearchStore` MAY no-op it); populate `StaleClaims` from the
  claim path (a self-executing store may leave it 0).
```

- [ ] **Step 5: Build the SPI**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi && go build ./... && go vet ./...
```
Expected: PASS (spitest has no test files of its own; interface compiles).

- [ ] **Step 6: Commit (SPI, not pushed yet)**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi
git add search_store.go CHANGELOG.md
git commit -m "feat(search)!: AsyncSearchStore.Release and SearchJob.StaleClaims for graceful async-job handoff"
```

---

## Task 2: SPI — spitest conformance for the new surface

**Files:**
- Modify: `cyoda-go-spi/spitest/asyncsearch.go`

**Interfaces:**
- Consumes: `Harness` (`Factory`, `NewTenant`, `Now`, `AdvanceClock`), helpers `newSearchJob`, `backdatedJob`, `findClaimed`, `tenantContext`, `newID`, `claimJobAge`, `claimStaleAfter`.
- Produces: subtests registered under `AsyncSearch/` — `Release/ImmediatelyClaimable`, `Release/Semantics`, `Release/Idempotent`, `Release/HeartbeatDoesNotResurrect`, `Release/ClaimClearsMark`, `Claim/StaleClaimCounted`, `Release/ClaimNotCounted`, `Claim/ReleasedNotTerminal`.

These are the executable contract for Tasks 3–5. They cannot run in the SPI repo (no Harness); they first execute RED against each plugin's `TestConformance` after the local `go.work` picks up the SPI change.

- [ ] **Step 1: Register the new subtests** (in `runAsyncSearchSuite`, after the `Heartbeat/Semantics` line)

```go
	runSubtest(t, h, tracker, "Release/ImmediatelyClaimable", testASReleaseImmediatelyClaimable)
	runSubtest(t, h, tracker, "Release/Semantics", testASReleaseSemantics)
	runSubtest(t, h, tracker, "Release/Idempotent", testASReleaseIdempotent)
	runSubtest(t, h, tracker, "Release/HeartbeatDoesNotResurrect", testASReleaseHeartbeatNoResurrect)
	runSubtest(t, h, tracker, "Release/ClaimClearsMark", testASReleaseClaimClearsMark)
	runSubtest(t, h, tracker, "Release/ClaimNotCounted", testASReleaseClaimNotCounted)
	runSubtest(t, h, tracker, "Claim/StaleClaimCounted", testASClaimStaleClaimCounted)
	runSubtest(t, h, tracker, "Claim/ReleasedNotTerminal", testASClaimReleasedNotTerminal)
```

- [ ] **Step 2: Implement the Release subtests** (append to `asyncsearch.go`)

```go
func testASReleaseImmediatelyClaimable(t *testing.T, h Harness) {
	tid := h.NewTenant()
	ctx := tenantContext(tid)
	as, _ := h.Factory.AsyncSearchStore(ctx)
	id := newID()
	require.NoError(t, as.CreateJob(ctx, newSearchJob(h, tid, id)))
	require.NoError(t, as.Release(ctx, id, 1))

	// staleAfter of an hour: only the released mark, not age, can make this
	// claimable (its CreateTime is "now" from h.Now). limit 1000 + findClaimed
	// because the suite is cross-tenant and earlier subtests leave stale rows.
	claimed, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	job := findClaimed(claimed, id)
	require.NotNil(t, job, "a released job must be claimable regardless of staleAfter")
	require.Equal(t, int64(2), job.Epoch, "claiming a released job still bumps Epoch")
	require.Equal(t, int64(0), job.StaleClaims, "claiming a RELEASED job must not count as a stale claim")
	require.Equal(t, "RUNNING", job.Status)

	got, err := as.GetJob(ctx, id)
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Epoch)
	require.Equal(t, int64(0), got.StaleClaims)
}

func testASReleaseSemantics(t *testing.T, h Harness) {
	tid := h.NewTenant()
	ctx := tenantContext(tid)
	as, _ := h.Factory.AsyncSearchStore(ctx)

	require.ErrorIs(t, as.Release(ctx, newID(), 1), spi.ErrNotFound, "release of a missing job")

	id := newID()
	require.NoError(t, as.CreateJob(ctx, newSearchJob(h, tid, id)))
	require.ErrorIs(t, as.Release(ctx, id, 2), spi.ErrStaleClaim, "release with the wrong epoch")
	// A failed release must not mark the job: an hour-later stale sweep with a
	// long staleAfter still claims nothing off THIS fresh job.
	claimed, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	require.Nil(t, findClaimed(claimed, id), "a rejected release must not leave the job marked released")

	require.NoError(t, as.UpdateJobStatus(ctx, id, 1, "SUCCESSFUL", 0, "", h.Now(), 0))
	require.ErrorIs(t, as.Release(ctx, id, 1), spi.ErrAlreadyTerminal, "release of a terminal job")
}

func testASReleaseIdempotent(t *testing.T, h Harness) {
	tid := h.NewTenant()
	ctx := tenantContext(tid)
	as, _ := h.Factory.AsyncSearchStore(ctx)
	id := newID()
	require.NoError(t, as.CreateJob(ctx, newSearchJob(h, tid, id)))
	require.NoError(t, as.Release(ctx, id, 1))
	require.NoError(t, as.Release(ctx, id, 1), "release is idempotent at the same epoch")

	claimed, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	require.NotNil(t, findClaimed(claimed, id), "two releases still yield exactly one claimable job")
	// The job was claimed once (epoch 2); a second claim finds nothing new.
	again, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	require.Nil(t, findClaimed(again, id), "a claimed job is no longer released")
}

func testASReleaseHeartbeatNoResurrect(t *testing.T, h Harness) {
	tid := h.NewTenant()
	ctx := tenantContext(tid)
	as, _ := h.Factory.AsyncSearchStore(ctx)
	id := newID()
	require.NoError(t, as.CreateJob(ctx, newSearchJob(h, tid, id)))
	require.NoError(t, as.Release(ctx, id, 1))
	// A stray heartbeat from the departing executor at the same epoch must
	// not clear the released mark.
	require.NoError(t, as.Heartbeat(ctx, id, 1))
	claimed, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	require.NotNil(t, findClaimed(claimed, id), "a heartbeat must not resurrect a released job")
}

func testASReleaseClaimClearsMark(t *testing.T, h Harness) {
	tid := h.NewTenant()
	ctx := tenantContext(tid)
	as, _ := h.Factory.AsyncSearchStore(ctx)
	id := newID()
	require.NoError(t, as.CreateJob(ctx, newSearchJob(h, tid, id)))
	require.NoError(t, as.Release(ctx, id, 1))
	claimed, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	require.NotNil(t, findClaimed(claimed, id))
	// The new claimant heartbeats at epoch 2; the released mark is gone, so a
	// fresh long-staleAfter sweep no longer takes it.
	require.NoError(t, as.Heartbeat(ctx, id, 2))
	again, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	require.Nil(t, findClaimed(again, id), "claiming must clear the released mark")
}

func testASReleaseClaimNotCounted(t *testing.T, h Harness) {
	tid := h.NewTenant()
	ctx := tenantContext(tid)
	as, _ := h.Factory.AsyncSearchStore(ctx)
	id := newID()
	require.NoError(t, as.CreateJob(ctx, newSearchJob(h, tid, id)))
	require.NoError(t, as.Release(ctx, id, 1))
	claimed, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	job := findClaimed(claimed, id)
	require.NotNil(t, job)
	require.Equal(t, int64(0), job.StaleClaims, "a release-then-claim must not count")

	// Now let it go stale as-of the store clock and claim by staleness.
	h.AdvanceClock(2 * claimStaleAfter)
	staled, err := as.ClaimStale(ctx, claimStaleAfter, 1000)
	require.NoError(t, err)
	after := findClaimed(staled, id)
	require.NotNil(t, after, "the job (epoch 2, heartbeated by the prior claim then aged) must go stale")
	require.Equal(t, int64(1), after.StaleClaims, "a staleness claim increments StaleClaims")
}

func testASClaimStaleClaimCounted(t *testing.T, h Harness) {
	tid := h.NewTenant()
	ctx := tenantContext(tid)
	as, _ := h.Factory.AsyncSearchStore(ctx)
	id := newID()
	require.NoError(t, as.CreateJob(ctx, backdatedJob(h, tid, id, claimJobAge)))

	claimed, err := as.ClaimStale(ctx, claimStaleAfter, 1000)
	require.NoError(t, err)
	job := findClaimed(claimed, id)
	require.NotNil(t, job)
	require.Equal(t, int64(1), job.StaleClaims, "first staleness claim sets StaleClaims to 1")
	got, err := as.GetJob(ctx, id)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.StaleClaims, "the increment is persisted")

	// Age it again (the claim refreshed the heartbeat) and re-claim.
	h.AdvanceClock(2 * claimStaleAfter)
	again, err := as.ClaimStale(ctx, claimStaleAfter, 1000)
	require.NoError(t, err)
	job2 := findClaimed(again, id)
	require.NotNil(t, job2)
	require.Equal(t, int64(2), job2.StaleClaims, "a second staleness claim makes it 2")

	// CreateJob ignores an input StaleClaims.
	id2 := newID()
	seed := newSearchJob(h, tid, id2)
	seed.StaleClaims = 7
	require.NoError(t, as.CreateJob(ctx, seed))
	got2, err := as.GetJob(ctx, id2)
	require.NoError(t, err)
	require.Equal(t, int64(0), got2.StaleClaims, "CreateJob persists StaleClaims as 0")
}

func testASClaimReleasedNotTerminal(t *testing.T, h Harness) {
	tid := h.NewTenant()
	ctx := tenantContext(tid)
	as, _ := h.Factory.AsyncSearchStore(ctx)
	id := newID()
	require.NoError(t, as.CreateJob(ctx, newSearchJob(h, tid, id)))
	require.NoError(t, as.Release(ctx, id, 1))
	// Cancel wins over the released mark: a terminal job is never claimed.
	require.NoError(t, as.Cancel(ctx, id, h.Now()))
	claimed, err := as.ClaimStale(ctx, time.Hour, 1000)
	require.NoError(t, err)
	require.Nil(t, findClaimed(claimed, id), "a released-then-cancelled job must never be claimed")
}
```

- [ ] **Step 3: Build the SPI** (still no test files; conformance runs at the consumer)

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi && go build ./... && go vet ./...
```
Expected: PASS.

- [ ] **Step 4: Commit (SPI)**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi
git add spitest/asyncsearch.go
git commit -m "test(spitest): Release and StaleClaims conformance for the async claim surface"
```

---

## Task 3: memory plugin — `Release`, `StaleClaims`, released mark

**Files:**
- Modify: `plugins/memory/search_store.go`
- Test: `plugins/memory/conformance_test.go` (already runs `spitest.StoreFactoryConformance`; no edit needed — the new subtests run automatically)

**Interfaces:**
- Consumes: `spi.AsyncSearchStore.Release`, `spi.SearchJob.StaleClaims` (Task 1); the new spitest subtests (Task 2).
- Produces: memory implementation of `Release`; `ClaimStale` populates `StaleClaims`; `CreateJob` zeroes it.

- [ ] **Step 1: Run the conformance suite RED**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.claude/worktrees/feat-509-async-reexecute
go test ./plugins/memory/ -run TestConformance -count=1
```
Expected: FAIL — `*AsyncSearchStore` does not implement `spi.AsyncSearchStore` (missing `Release`), and (once it compiles) the `Release/*` and `StaleClaims` subtests fail. (This is the first place the Task 2 contract executes.)

- [ ] **Step 2: Add `released` to the entry struct** (in `plugins/memory/search_store.go`)

```go
type searchJobEntry struct {
	job       spi.SearchJob
	entityIDs []string
	released  bool // set by Release; cleared by the ClaimStale that takes the job
}
```

- [ ] **Step 3: Zero `StaleClaims` in `CreateJob`** (after the existing `copied.Epoch = 1`)

```go
	copied.Epoch = 1
	copied.StaleClaims = 0
```

- [ ] **Step 4: Implement `Release`** (after `ClearResults`)

```go
// Release marks a RUNNING job released without finishing it, so the next
// ClaimStale takes it regardless of staleAfter. Fenced exactly like the
// write methods (missing/terminal/epoch), idempotent at the same epoch, and
// it neither bumps Epoch nor touches StaleClaims. A stray Heartbeat at the
// same epoch does not clear the mark — only a claim does.
func (s *AsyncSearchStore) Release(ctx context.Context, jobID string, epoch int64) error {
	tid, err := s.resolveTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.guardWrite(tid, jobID, epoch)
	if err != nil {
		return err
	}
	entry.released = true
	return nil
}
```

- [ ] **Step 5: Update `ClaimStale`** — candidate predicate and per-claim bookkeeping.

Change the candidate test so a released job qualifies regardless of staleness:

```go
			if entry.job.Status != "RUNNING" {
				continue
			}
			if !entry.released {
				baseline := entry.job.CreateTime
				if entry.job.HeartbeatTime != nil {
					baseline = *entry.job.HeartbeatTime
				}
				if !baseline.Before(cutoff) {
					continue
				}
			}
			candidates = append(candidates, entry)
```

Change the claim loop so a staleness claim counts and the released mark clears:

```go
	for _, entry := range candidates {
		entry.job.Epoch++
		if !entry.released {
			entry.job.StaleClaims++
		}
		entry.released = false
		hb := now
		entry.job.HeartbeatTime = &hb

		copied := copySearchJob(entry.job)
		claimed = append(claimed, &copied)
	}
```

- [ ] **Step 6: Run the conformance suite GREEN**

```bash
go test ./plugins/memory/ -run TestConformance -count=1
```
Expected: PASS (all `AsyncSearch/*` subtests, including the eight new ones).

- [ ] **Step 7: Commit**

```bash
git add plugins/memory/search_store.go
git commit -m "feat(memory): implement AsyncSearchStore.Release and StaleClaims"
```

---

## Task 4: sqlite plugin — migration, `Release`, `StaleClaims`

**Files:**
- Create: `plugins/sqlite/migrations/000007_search_release.up.sql`, `plugins/sqlite/migrations/000007_search_release.down.sql`
- Modify: `plugins/sqlite/search_store.go`

**Interfaces:**
- Consumes: Task 1, Task 2.
- Produces: sqlite `Release`; `released`/`stale_claims` columns; `ClaimStale` predicate + counter; `searchJobColumns`/`scanSearchJob` carry `stale_claims`.

- [ ] **Step 1: Run the conformance suite RED**

```bash
go test ./plugins/sqlite/ -run TestConformance -count=1
```
Expected: FAIL (missing `Release`, `StaleClaims` subtests).

- [ ] **Step 2: Write the migration** — `000007_search_release.up.sql`:

```sql
-- Graceful async-job handoff (spi.AsyncSearchStore.Release) and the
-- stale-claim attempt counter (SearchJob.StaleClaims). released is 0 until
-- Release marks it and is reset by the claim that takes the job; stale_claims
-- counts only staleness claims (never a claim of a released job) and bounds
-- the engine's attempt cap.
ALTER TABLE search_jobs ADD COLUMN released INTEGER NOT NULL DEFAULT 0;
ALTER TABLE search_jobs ADD COLUMN stale_claims INTEGER NOT NULL DEFAULT 0;
```

`000007_search_release.down.sql`:

```sql
ALTER TABLE search_jobs DROP COLUMN stale_claims;
ALTER TABLE search_jobs DROP COLUMN released;
```

- [ ] **Step 3: Add `stale_claims` to the column projection and scan.**

In `searchJobColumns` add `, stale_claims` at the end of the list. In `scanSearchJob`, add `staleClaims` to the `int64` var group and to the `row.Scan(...)` arg list (last), then `job.StaleClaims = staleClaims`. (`released` is store-internal and is NOT scanned into `SearchJob`.)

- [ ] **Step 4: Implement `Release`** (after `ClearResults`, reusing `fencedUpdate`)

```go
// Release marks a RUNNING job released so the next ClaimStale takes it
// regardless of staleAfter. Same fenced conditional UPDATE shape as
// Heartbeat; idempotent at the same epoch; does not bump epoch or
// stale_claims.
func (s *asyncSearchStore) Release(ctx context.Context, jobID string, epoch int64) error {
	tid, err := s.tenant(ctx)
	if err != nil {
		return err
	}
	return fencedUpdate(ctx, s.db, tid, jobID, epoch, "released = 1")
}
```

- [ ] **Step 5: Update `ClaimStale`** — the staleness scan and the CAS.

Change the candidate `SELECT` predicate to include released rows:

```go
		`SELECT tenant_id, job_id, epoch FROM search_jobs
		 WHERE status = 'RUNNING'
		   AND (released = 1 OR COALESCE(heartbeat_time, create_time) < ?)
		 ORDER BY create_time
		 LIMIT ?`,
```

The candidate scan also needs to know whether each row was released, so the counter is set correctly under the CAS. Add `released` to the `SELECT` list and the `staleClaimCandidate` struct:

```go
type staleClaimCandidate struct {
	tenantID string
	jobID    string
	epoch    int64
	released bool
}
```
Scan it (`&c.tenantID, &c.jobID, &c.epoch, &c.released`) and add `, released` to the SELECT. Change the CAS UPDATE to advance the counter only for a non-released claim and clear the mark:

```go
		res, err := s.db.ExecContext(ctx,
			`UPDATE search_jobs
			 SET epoch = epoch + 1,
			     heartbeat_time = ?,
			     stale_claims = stale_claims + ?,
			     released = 0
			 WHERE tenant_id = ? AND job_id = ? AND epoch = ? AND status = 'RUNNING'`,
			now, boolToInt(!c.released), c.tenantID, c.jobID, c.epoch)
```

Add a small local helper if one does not already exist:

```go
func boolToInt(b bool) int { if b { return 1 }; return 0 }
```

- [ ] **Step 6: Run the conformance suite GREEN**

```bash
go test ./plugins/sqlite/ -run TestConformance -count=1
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add plugins/sqlite/search_store.go plugins/sqlite/migrations/000007_search_release.up.sql plugins/sqlite/migrations/000007_search_release.down.sql
git commit -m "feat(sqlite): implement AsyncSearchStore.Release and StaleClaims"
```

---

## Task 5: postgres plugin — migration, `Release`, `StaleClaims`, save-chunk idle timeout

**Files:**
- Create: `plugins/postgres/migrations/000010_search_release.up.sql`, `plugins/postgres/migrations/000010_search_release.down.sql`
- Modify: `plugins/postgres/search_store.go`

**Interfaces:**
- Consumes: Task 1, Task 2.
- Produces: postgres `Release`; `released`/`stale_claims` columns; `ClaimStale` predicate + counter; `searchJobColumns`/`qualifiedSearchJobColumns`/`scanSearchJobRow` carry `stale_claims`; `SaveResults` chunk-tx `SET LOCAL idle_in_transaction_session_timeout`.

- [ ] **Step 1: Run the conformance suite RED** (Docker required)

```bash
make preflight && go test ./plugins/postgres/ -run TestConformance -count=1
```
Expected: FAIL (missing `Release`, `StaleClaims` subtests). If preflight complains the probe image is missing while `docker images` lists `postgres:17-alpine`, re-tag it: `docker tag <id> postgres:17-alpine` (never pull).

- [ ] **Step 2: Write the migration** — `000010_search_release.up.sql`:

```sql
-- Graceful async-job handoff (spi.AsyncSearchStore.Release) and the
-- stale-claim attempt counter (SearchJob.StaleClaims). released is false
-- until Release marks it and is reset by the claim that takes the job;
-- stale_claims counts only staleness claims (never a claim of a released
-- job) and bounds the engine's attempt cap.
ALTER TABLE search_jobs ADD COLUMN released BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE search_jobs ADD COLUMN stale_claims BIGINT NOT NULL DEFAULT 0;
```

`000010_search_release.down.sql`:

```sql
ALTER TABLE search_jobs DROP COLUMN stale_claims;
ALTER TABLE search_jobs DROP COLUMN released;
```

- [ ] **Step 3: Add `stale_claims` to both column constants and the scan.**

Append `, stale_claims` to `searchJobColumns`; append `, j.stale_claims` to `qualifiedSearchJobColumns`. In `scanSearchJobRow`, add `&job.StaleClaims` as the final `scan(...)` arg. (`released` is store-internal; not scanned.)

- [ ] **Step 4: Implement `Release`** (after `ClearResults`; single fenced conditional UPDATE like `Heartbeat`)

```go
// Release marks a RUNNING job released so the next ClaimStale takes it
// regardless of staleAfter. Fenced conditional UPDATE like Heartbeat;
// idempotent at the same epoch; does not bump epoch or stale_claims.
func (s *asyncSearchStore) Release(ctx context.Context, jobID string, epoch int64) error {
	tid, err := s.tenant(ctx)
	if err != nil {
		return err
	}
	tag, err := s.q.Exec(ctx,
		`UPDATE search_jobs SET released = true
		 WHERE id = $1 AND tenant_id = $2 AND epoch = $3
		   AND status NOT IN ('SUCCESSFUL', 'FAILED', 'CANCELLED')`,
		jobID, string(tid), epoch)
	if err != nil {
		return fmt.Errorf("failed to release search job %s: %w", jobID, err)
	}
	if tag.RowsAffected() == 0 {
		return s.probeFenced(ctx, s.q, jobID, tid, epoch, false)
	}
	return nil
}
```

- [ ] **Step 5: Update `ClaimStale`** — CTE predicate and the bumping UPDATE.

Change the CTE `WHERE` to include released rows, and the UPDATE to count only staleness claims and clear the mark:

```go
		`WITH claimed AS (
		   SELECT tenant_id, id, released FROM search_jobs
		   WHERE status = 'RUNNING'
		     AND (released OR COALESCE(heartbeat_time, created_at) < now() - ($1 * interval '1 microsecond'))
		   ORDER BY created_at
		   LIMIT $2
		   FOR UPDATE SKIP LOCKED
		 )
		 UPDATE search_jobs j
		    SET epoch = j.epoch + 1,
		        heartbeat_time = now(),
		        stale_claims = j.stale_claims + (CASE WHEN c.released THEN 0 ELSE 1 END),
		        released = false
		 FROM claimed c
		 WHERE j.tenant_id = c.tenant_id AND j.id = c.id
		 RETURNING `+qualifiedSearchJobColumns,
```

(The `RETURNING` reads `j.stale_claims` — already the post-increment value.)

- [ ] **Step 6: Add the save-chunk idle timeout** (in `SaveResults`'s `flush`, immediately after `BeginTx` succeeds and before `probeFenced`)

```go
		// A node that dies mid-chunk leaves this transaction holding FOR
		// UPDATE on the job row; without a server-side bound the lock would
		// survive until TCP keepalive notices the dead client (hours at Linux
		// defaults) and ClaimStale's SKIP LOCKED would pass the job over every
		// sweep. SET LOCAL is transaction-scoped, so no other session or
		// entity transaction is affected; a live chunk is never idle between
		// its own sequential statements for more than microseconds, and a
		// COPY in progress is not idle.
		if _, err := tx.Exec(ctx, "SET LOCAL idle_in_transaction_session_timeout = '30s'"); err != nil {
			return fmt.Errorf("failed to set chunk idle timeout for job %s: %w", jobID, classifyError(err))
		}
```

This is a non-functional property (only a killed socket exercises it); the crash-mid-save e2e (Task 12) covers the observable handoff, not the timeout value. **Record the TDD waiver in the commit body.**

- [ ] **Step 7: Run the conformance suite GREEN**

```bash
go test ./plugins/postgres/ -run TestConformance -count=1
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add plugins/postgres/search_store.go plugins/postgres/migrations/000010_search_release.up.sql plugins/postgres/migrations/000010_search_release.down.sql
git commit -m "feat(postgres): implement AsyncSearchStore.Release, StaleClaims, and a save-chunk idle timeout

TDD waiver: idle_in_transaction_session_timeout is a non-functional property
that only a killed TCP socket exercises; the observable handoff it enables is
covered by the crash-mid-save e2e, and the timeout itself is reviewed, not
unit-tested (no test hook in production code)."
```

---

## Task 6: engine — thread the claim epoch through the executor

**Files:**
- Modify: `internal/domain/search/service.go`
- Test: `internal/domain/search/executor_test.go` (existing tests must stay green; adjust call sites if any test invokes `runAsyncJob`/`startHeartbeat`/`writeAsyncFailure` directly — grep first)

**Interfaces:**
- Produces: `runAsyncJob(jobCtx, cancel, jobID, epoch int64, modelRef, cond, opts, resolvedOrderBy)`; `startHeartbeat(jobCtx, cancel, jobID, epoch int64)`; `writeAsyncFailure(ctx, jobID, epoch int64, msg, finishTime, calcTimeMs)`; `asyncJobHandle{cancel, uc, epoch int64}`.
- Consumes (later tasks): the epoch parameter, so `ReclaimStaleJobs` (Task 8) can run the executor at the claimed epoch.

- [ ] **Step 1: Find every reference to `initialEpoch` and the three methods**

```bash
grep -rn "initialEpoch\|runAsyncJob\|startHeartbeat\|writeAsyncFailure" internal/domain/search/
```
Note each call site; all must be updated in this task.

- [ ] **Step 2: Write/adjust a failing test** — a unit test proving a reclaimed-style executor writes at a non-1 epoch. Add to `executor_test.go`:

```go
// (h) the executor honours the epoch it is given: a job whose store epoch is
// already 2 (as after a ClaimStale takeover) has its terminal write accepted,
// where an epoch-1 write would be fenced with ErrStaleClaim.
func TestExecutor_RunsAtGivenEpoch(t *testing.T) {
	base := memory.NewStoreFactory()
	defer base.Close()
	ctx := tenantCtx("tenant-1")
	ref := spi.ModelRef{EntityName: "epochitem", ModelVersion: "1"}
	saveMinimalModel(t, ctx, base, ref)
	saveEntity(t, ctx, base, ref, "e001", []byte(`{}`))

	searchStore, _ := base.AsyncSearchStore(context.Background())
	uuids := common.NewTestUUIDGenerator()
	pool := newTinyPool(t)
	svc := search.NewSearchService(base, uuids, searchStore).WithAsyncPool(pool)

	// Seed a job and bump it to epoch 2 the way a takeover would.
	cond := &predicate.LifecycleCondition{Field: "state", OperatorType: "EQUALS", Value: "NEW"}
	jobID, err := svc.SubmitAsync(ctx, ref, cond, search.SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("SubmitAsync: %v", err)
	}
	// Wait for it to finish under epoch 1 so the store is quiescent.
	_ = pollUntilTerminal(t, svc, ctx, jobID, 5*time.Second)
	// (Assertion detail lives in Task 8's ReclaimStaleJobs tests; here we only
	// need the signatures to compile with an epoch parameter.)
}
```
This is a compile-driver for the signature change; the substantive re-execution assertions are in Task 8/11. Keep it minimal.

- [ ] **Step 3: Delete the `initialEpoch` const and add the epoch parameter**

Remove the `const initialEpoch = 1` block. Change signatures:
- `func (s *SearchService) runAsyncJob(jobCtx context.Context, cancel context.CancelFunc, jobID string, epoch int64, modelRef spi.ModelRef, cond predicate.Condition, opts SearchOptions, resolvedOrderBy []spi.OrderSpec)`
- `func (s *SearchService) startHeartbeat(jobCtx context.Context, cancel context.CancelFunc, jobID string, epoch int64)`
- `func (s *SearchService) writeAsyncFailure(ctx context.Context, jobID string, epoch int64, msg string, finishTime time.Time, calcTimeMs int64)`

Inside each, replace every `initialEpoch` literal with the `epoch` parameter (`startHeartbeat`'s `Heartbeat` call; `runAsyncJob`'s `SaveResults`, success `UpdateJobStatus`, panic-path and switch-path `writeAsyncFailure` calls; `writeAsyncFailure`'s `UpdateJobStatus`).

- [ ] **Step 4: Add `epoch` to the registry handle**

```go
type asyncJobHandle struct {
	cancel context.CancelFunc
	uc     *spi.UserContext
	epoch  int64
}
```
Update `registerJob` to take and store `epoch` (signature `registerJob(jobID string, cancel context.CancelFunc, uc *spi.UserContext, epoch int64) bool`), setting `s.registry[jobID] = &asyncJobHandle{cancel: cancel, uc: uc, epoch: epoch}`.

- [ ] **Step 5: Update `SubmitAsync`'s call sites** to pass epoch `1`

`s.registerJob(jobID, cancel, uc, 1)`, `s.startHeartbeat(jobCtx, cancel, jobID, 1)`, and inside the `pool.Submit` closure `s.runAsyncJob(jobCtx, cancel, jobID, 1, modelRef, cond, opts, orderBy)`.

- [ ] **Step 6: Update `AbortRegisteredJobs`** temporarily to compile — it calls `writeAsyncFailure(writeCtx, jobID, entry.epoch, jobFailureFallback, time.Now(), 0)`. (This method is deleted in Task 10; this keeps the tree building between tasks.)

- [ ] **Step 7: Build and run the search unit tests**

```bash
go test ./internal/domain/search/... -count=1
```
Expected: PASS (existing behaviour unchanged — epoch is 1 everywhere so far).

- [ ] **Step 8: Commit**

```bash
git add internal/domain/search/service.go internal/domain/search/executor_test.go
git commit -m "refactor(search): thread the claim epoch through the async executor"
```

---

## Task 7: engine — release cause and suppression of failure writes on release

**Files:**
- Modify: `internal/domain/search/service.go`
- Test: `internal/domain/search/executor_test.go`

**Interfaces:**
- Produces: `errJobReleased` (package sentinel); `registerJob` uses `context.WithCancelCause`; `writeAsyncFailure` and `runAsyncJob` short-circuit when `context.Cause(ctx) == errJobReleased`; `ReleaseRegisteredJobs(ctx) int` (consumed by Task 10).

- [ ] **Step 1: Write failing tests** (append to `executor_test.go`)

```go
// (i) a released job writes no terminal status on any executor exit path.
func TestExecutor_ReleasedJobWritesNothing(t *testing.T) {
	base := memory.NewStoreFactory()
	defer base.Close()
	ctx := tenantCtx("tenant-1")
	ref := spi.ModelRef{EntityName: "relitem", ModelVersion: "1"}
	saveMinimalModel(t, ctx, base, ref)
	for i := 0; i < 20; i++ {
		saveEntity(t, ctx, base, ref, fmt.Sprintf("e%03d", i), []byte(`{}`))
	}
	ies := wrapIterate(t, base, ctx, func(it spi.Iterator) spi.Iterator {
		return &delayIterator{Iterator: it, delay: 25 * time.Millisecond}
	})
	factory := &iterableFactory{StoreFactory: base, entityStore: ies}
	searchStore, _ := base.AsyncSearchStore(context.Background())
	svc := search.NewSearchService(factory, common.NewTestUUIDGenerator(), searchStore).
		WithAsyncPool(newTinyPool(t)).WithHeartbeat(15 * time.Millisecond)

	cond := &predicate.LifecycleCondition{Field: "state", OperatorType: "EQUALS", Value: "NEW"}
	jobID, err := svc.SubmitAsync(ctx, ref, cond, search.SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("SubmitAsync: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // scan underway
	if n := svc.ReleaseRegisteredJobs(ctx); n == 0 {
		t.Fatal("ReleaseRegisteredJobs found nothing to release")
	}
	// The job must remain RUNNING (released), never FAILED, and Release must
	// have been recorded so a peer claim would take it immediately.
	time.Sleep(300 * time.Millisecond)
	job, err := searchStore.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != "RUNNING" {
		t.Fatalf("status = %q, want RUNNING (a released job must not be failed by its departing executor)", job.Status)
	}
}
```
Run: `go test ./internal/domain/search/ -run TestExecutor_ReleasedJobWritesNothing -count=1` — Expected: FAIL (`ReleaseRegisteredJobs` undefined).

- [ ] **Step 2: Add the sentinel** (near the other package errors)

```go
// errJobReleased is the cancellation cause set when a job is released
// (graceful shutdown handoff). The executor writes no terminal status for a
// job cancelled with this cause: a peer, or this node's next sweep, reclaims
// it. Every other cancellation cause keeps recording a terminal status.
var errJobReleased = errors.New("async search job released for reclaim")
```

- [ ] **Step 3: Switch registration to `WithCancelCause`**

In `SubmitAsync`, replace `jobCtx, cancel := context.WithCancel(bgCtx)` with `jobCtx, cancel := context.WithCancelCause(bgCtx)`. `cancel` is now `context.CancelCauseFunc`; the plain `cancel()` calls (queue-full cleanup, per-tenant-cap cleanup) become `cancel(nil)`. The registry handle's `cancel` field type must accept a cause; store a normalized `context.CancelFunc` wrapper OR change the field to `context.CancelCauseFunc`. Change `asyncJobHandle.cancel` to `context.CancelCauseFunc` and update `registerJob`, `CancelRunning` (`entry.cancel(nil)` — user cancel keeps default behaviour, its own status poll records CANCELLED), and `runAsyncJob`'s `defer cancel()` → the executor is passed a `context.CancelFunc`; keep `runAsyncJob`'s parameter as `context.CancelFunc` and pass `func() { cancel(nil) }` when submitting, OR change `runAsyncJob` to take `context.CancelCauseFunc` and `defer cancel(nil)`. Choose the latter (fewer wrappers): `runAsyncJob(jobCtx, cancel context.CancelCauseFunc, ...)` with `defer cancel(nil)`.

- [ ] **Step 4: Suppress failure writes on the release cause**

At the top of `writeAsyncFailure`, before the `UpdateJobStatus`:

```go
func (s *SearchService) writeAsyncFailure(ctx context.Context, jobID string, epoch int64, msg string, finishTime time.Time, calcTimeMs int64) {
	if errors.Is(context.Cause(ctx), errJobReleased) {
		// Released for reclaim: a peer (or this node's next sweep) re-runs it.
		// Recording FAILED here would defeat the handoff.
		return
	}
	ctx = context.WithoutCancel(ctx)
	...
```

At the very top of `runAsyncJob` (before the panic-recovery defer touches anything), exit immediately if the job was released before a worker picked it up (the common shutdown case for a queued job):

```go
	if errors.Is(context.Cause(jobCtx), errJobReleased) {
		s.deregisterJob(jobID)
		return
	}
	defer cancel(nil)
	defer s.deregisterJob(jobID)
	...
```
(Keep the existing `defer cancel(...)`/`defer s.deregisterJob` after this guard; the early return handles the never-started case without double-deregistering.)

Note: `context.Cause` returns the cancellation cause even after `WithoutCancel` strips *cancellation propagation*, because the check reads `jobCtx` directly (not the WithoutCancel derivative). The success path already runs on `context.WithoutCancel(jobCtx)` for the store write but the *cause* is read from `jobCtx`.

- [ ] **Step 5: Add `ReleaseRegisteredJobs`** (mirrors `AbortRegisteredJobs`, but releases instead of failing)

```go
// ReleaseRegisteredJobs cancels every in-flight (queued or executing) job on
// this node with errJobReleased and issues a fenced store Release for each,
// so a peer (or this node's next startup sweep) reclaims and re-runs it
// promptly instead of waiting for the heartbeat to age out. Returns the count
// released. Called by App.Shutdown after the drain budget: jobs that finished
// within the budget are already gone from the registry.
//
// The store Release is issued right after cancelling, without waiting for the
// executor goroutine to unwind: fencing makes that safe (a save chunk that
// commits before a peer's claim is wiped by the peer's ClearResults; one that
// reaches the store after the claim is refused with ErrStaleClaim). Waiting
// would let a backend that ignores ctx stall shutdown.
func (s *SearchService) ReleaseRegisteredJobs(ctx context.Context) int {
	entries := func() map[string]*asyncJobHandle {
		s.registryMu.Lock()
		defer s.registryMu.Unlock()
		snap := make(map[string]*asyncJobHandle, len(s.registry))
		for id, e := range s.registry {
			snap[id] = e
		}
		return snap
	}()
	for jobID, entry := range entries {
		entry.cancel(errJobReleased)
		relCtx := ctx
		if entry.uc != nil {
			relCtx = spi.WithUserContext(context.WithoutCancel(ctx), entry.uc)
		}
		if err := s.searchStore.Release(relCtx, jobID, entry.epoch); err != nil {
			if errors.Is(err, spi.ErrAlreadyTerminal) || errors.Is(err, spi.ErrStaleClaim) {
				slog.Warn("async search job release lost the race; already settled or reclaimed", "pkg", "search", "jobID", jobID, "err", err)
				continue
			}
			slog.Error("failed to release async search job at shutdown", "pkg", "search", "jobID", jobID, "err", err)
		}
	}
	return len(entries)
}
```

- [ ] **Step 6: Run the test GREEN**

```bash
go test ./internal/domain/search/ -run 'TestExecutor_ReleasedJobWritesNothing|TestExecutor_' -count=1
```
Expected: PASS (including the pre-existing executor tests — user cancel still records CANCELLED, heartbeat-fence still aborts).

- [ ] **Step 7: Commit**

```bash
git add internal/domain/search/service.go internal/domain/search/executor_test.go
git commit -m "feat(search): release cause suppresses the departing node's terminal write; ReleaseRegisteredJobs"
```

---

## Task 8: engine — `ReclaimStaleJobs` replaces `FailStaleJobs`

**Files:**
- Modify: `internal/domain/search/reaper.go`
- Modify: `internal/domain/search/pool.go` (add `Cap()`)
- Modify: `internal/domain/search/service.go` (self-reclaim handle replacement; `deregisterJob` compare-and-delete)
- Test: `internal/domain/search/reaper_test.go` (Task 11 rewrites it; here add the driving cases)

**Interfaces:**
- Consumes: Task 6 (`runAsyncJob(...,epoch,...)`, `registerJob(...,epoch)`, `startHeartbeat(...,epoch)`), Task 5/4/3 (`Release`, `ClaimStale` populating `StaleClaims`), the backend `asyncScanScoper` (already in `service.go`).
- Produces: `func (s *SearchService) ReclaimStaleJobs(ctx context.Context, staleAfter time.Duration, maxAttempts int) (reenqueued, failed int, err error)`; `WorkerPool.Cap() int`; `errJobSuperseded` sentinel; deletes `FailStaleJobs`.

- [ ] **Step 1: Add `Cap()` to the pool** (store the worker count)

In `WorkerPool`, add a field `workers int`; set it in `NewWorkerPool` (`p := &WorkerPool{jobs: ..., workers: workers}`). Add:

```go
// Cap is the pool's total in-flight capacity: running workers plus queue
// slots. The reclaim sweep uses it (minus the current registry size) to bound
// how many stale jobs it claims, so a node never claims work it has no
// capacity to start.
func (p *WorkerPool) Cap() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.workers + cap(p.jobs)
}
```

- [ ] **Step 2: Add a headroom accessor and self-reclaim support to the service**

Add a sentinel and a registry-size accessor to `service.go`:

```go
// errJobSuperseded cancels a stale in-process executor when this same node
// reclaims a job it still had registered (its own paused executor). The old
// executor's writes are fenced by epoch regardless; the cause is distinct
// from errJobReleased so its failure write is still suppressed but not
// mistaken for a graceful handoff.
var errJobSuperseded = errors.New("async search job superseded by self-reclaim")
```

Extend the `writeAsyncFailure` guard to also suppress on supersede:

```go
	if c := context.Cause(ctx); errors.Is(c, errJobReleased) || errors.Is(c, errJobSuperseded) {
		return
	}
```
And the top-of-`runAsyncJob` early-exit similarly (`errors.Is(c, errJobReleased) || errors.Is(c, errJobSuperseded)`).

Add a registry-size helper and a compare-and-delete deregister:

```go
func (s *SearchService) registrySize() int {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	return len(s.registry)
}
```

Change `deregisterJob` to compare-and-delete on handle identity so a superseded old executor's deferred deregistration cannot evict the new epoch's handle:

```go
func (s *SearchService) deregisterJobHandle(jobID string, h *asyncJobHandle) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	cur, ok := s.registry[jobID]
	if !ok || cur != h {
		return // a newer handle (self-reclaim) owns this id now
	}
	delete(s.registry, jobID)
	tenant := tenantOf(cur.uc)
	if n := s.tenantInFlight[tenant]; n <= 1 {
		delete(s.tenantInFlight, tenant)
	} else {
		s.tenantInFlight[tenant] = n - 1
	}
}
```
`runAsyncJob` must capture its own handle to deregister by identity. Change `registerJob` to return the handle it created (`(*asyncJobHandle, bool)`), have `SubmitAsync`/reclaim pass that handle into the `runAsyncJob` closure, and `runAsyncJob`'s `defer s.deregisterJob(jobID)` becomes `defer s.deregisterJobHandle(jobID, handle)`. Add a reclaim-only `registerReclaim` that replaces an existing handle (cancelling the old one with `errJobSuperseded`) and never applies the per-tenant cap:

```go
// registerReclaim registers a reclaimed job's handle at its claimed epoch. It
// bypasses the per-tenant in-flight cap (admission happened at submit,
// cluster-wide) and, if this node still has a stale handle for the job (its
// own paused executor), cancels that handle with errJobSuperseded and replaces
// it. Returns the new handle.
func (s *SearchService) registerReclaim(jobID string, cancel context.CancelCauseFunc, uc *spi.UserContext, epoch int64) *asyncJobHandle {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if s.registry == nil {
		s.registry = make(map[string]*asyncJobHandle)
	}
	if s.tenantInFlight == nil {
		s.tenantInFlight = make(map[spi.TenantID]int)
	}
	h := &asyncJobHandle{cancel: cancel, uc: uc, epoch: epoch}
	if old, ok := s.registry[jobID]; ok {
		old.cancel(errJobSuperseded) // fence-safe; its writes are stale-epoch
	} else {
		s.tenantInFlight[tenantOf(uc)]++
	}
	s.registry[jobID] = h
	return h
}
```
(When replacing an existing handle the tenant count is unchanged — one in, one out.)

- [ ] **Step 3: Replace `FailStaleJobs` with `ReclaimStaleJobs`** (rewrite `reaper.go`'s function; keep `StaleClaimBatch`)

```go
// ReclaimStaleJobs claims stale or released RUNNING async-search jobs and
// re-executes them on this node, or fails those past the attempt cap. It
// replaces the interim claim-then-FAIL disposition: a crashed node's job is
// now completed by a live node, not failed. Returns (reenqueued, failed).
//
// Self-executing stores own their own recovery — skipped, exactly as
// SubmitAsync skips its own execution goroutine for them.
//
// The claim is bounded by this node's free capacity (pool.Cap() minus the
// current registry size), so a saturated node claims nothing rather than
// taking work it cannot start. maxAttempts bounds StaleClaims (executor
// losses), not Epoch: a graceful handoff (Release then claim) never advances
// a job toward being failed.
func (s *SearchService) ReclaimStaleJobs(ctx context.Context, staleAfter time.Duration, maxAttempts int) (int, int, error) {
	if _, ok := s.searchStore.(spi.SelfExecutingSearchStore); ok {
		return 0, 0, nil
	}

	headroom := s.asyncPool().Cap() - s.registrySize()
	if headroom <= 0 {
		return 0, 0, nil
	}
	limit := StaleClaimBatch
	if headroom < limit {
		limit = headroom
	}

	jobs, err := s.searchStore.ClaimStale(ctx, staleAfter, limit)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to claim stale search jobs: %w", err)
	}

	reenqueued, failed := 0, 0
	for _, job := range jobs {
		tenantCtx := common.SystemUserContext(job.TenantID)

		// Attempt cap: bound on executor losses, not on graceful handoffs.
		if int64(maxAttempts) <= job.StaleClaims {
			if werr := s.searchStore.UpdateJobStatus(tenantCtx, job.ID, job.Epoch, "FAILED", 0, jobAttemptsExhausted, time.Now(), 0); werr != nil {
				if errors.Is(werr, spi.ErrAlreadyTerminal) || errors.Is(werr, spi.ErrStaleClaim) {
					slog.Warn("attempt-cap fail lost the race; job already settled", "pkg", "search", "jobID", job.ID, "err", werr)
					continue
				}
				slog.Error("failed to fail crash-looping search job", "pkg", "search", "jobID", job.ID, "err", werr)
				continue
			}
			slog.Warn("async search job abandoned after repeated executor loss", "pkg", "search", "jobID", job.ID, "epoch", job.Epoch, "staleClaims", job.StaleClaims)
			failed++
			continue
		}

		// Clear the prior epoch's partial rows before re-running. On failure,
		// release (uncounted) so a peer or the next sweep retries — never
		// enqueue over unknown residue (fail closed).
		if cerr := s.searchStore.ClearResults(tenantCtx, job.ID); cerr != nil {
			slog.Error("failed to clear results before reclaim; releasing", "pkg", "search", "jobID", job.ID, "err", cerr)
			if rerr := s.searchStore.Release(tenantCtx, job.ID, job.Epoch); rerr != nil {
				slog.Error("failed to release after ClearResults error", "pkg", "search", "jobID", job.ID, "err", rerr)
			}
			continue
		}

		if s.reenqueueClaimed(job) {
			reenqueued++
		}
	}
	return reenqueued, failed, nil
}
```

- [ ] **Step 4: Add `reenqueueClaimed`** (decode the stored job, build the ctx as submit does, register at the claimed epoch, heartbeat, submit; on queue-full, release)

```go
// reenqueueClaimed re-runs a claimed job on this node at its claimed epoch.
// Returns true if the job entered the pool. On any failure it either fails the
// job (a genuine defect — an undecodable stored job) or releases it (transient
// — queue full), never leaves it silently RUNNING with no executor.
func (s *SearchService) reenqueueClaimed(job *spi.SearchJob) bool {
	uc := common.SystemUserContextValue(job.TenantID) // *spi.UserContext for the job's tenant
	baseCtx := spi.WithUserContext(context.Background(), uc)
	if scoper, ok := s.searchStore.(asyncScanScoper); ok {
		baseCtx = scoper.AsyncScanContext(baseCtx)
	}
	tenantCtx := spi.WithUserContext(context.Background(), uc)

	cond, opts, orderBy, decErr := decodeStoredJob(job)
	if decErr != nil {
		// A stored job that cannot be decoded is a defect, not a runtime
		// condition: fail it at the claimed epoch rather than loop on it.
		slog.Error("failed to decode claimed search job; failing", "pkg", "search", "jobID", job.ID, "err", decErr)
		if werr := s.searchStore.UpdateJobStatus(tenantCtx, job.ID, job.Epoch, "FAILED", 0, jobFailureFallback, time.Now(), 0); werr != nil {
			slog.Error("failed to fail undecodable claimed job", "pkg", "search", "jobID", job.ID, "err", werr)
		}
		return false
	}

	jobCtx, cancel := context.WithCancelCause(baseCtx)
	handle := s.registerReclaim(job.ID, cancel, uc, job.Epoch)
	s.startHeartbeat(jobCtx, cancel, job.ID, job.Epoch)

	submitErr := s.asyncPool().Submit(func() {
		s.runAsyncJobWithHandle(jobCtx, cancel, handle, job.ID, job.Epoch, job.ModelRef, cond, opts, orderBy)
	})
	if submitErr != nil {
		cancel(nil)
		s.deregisterJobHandle(job.ID, handle)
		// Release (uncounted) so a peer with capacity, or this node's next
		// sweep, takes it without waiting for staleness.
		if rerr := s.searchStore.Release(tenantCtx, job.ID, job.Epoch); rerr != nil {
			slog.Warn("failed to release reclaimed job after queue-full", "pkg", "search", "jobID", job.ID, "err", rerr)
		}
		return false
	}
	return true
}
```

Notes for the implementer:
- `runAsyncJobWithHandle` is `runAsyncJob` with the captured `*asyncJobHandle` for identity-based deregistration (Task 6/Step-2 of this task). Refactor `runAsyncJob` to take the handle, and have `SubmitAsync` pass its handle too.
- `decodeStoredJob(job *spi.SearchJob) (predicate.Condition, SearchOptions, []spi.OrderSpec, error)` unmarshals `job.Condition` via `predicate.ParseCondition` and `job.SearchOpts` via the same struct `SubmitAsync` marshals (`{limit, pointInTime, orderBy}`); the stored `orderBy` is already resolved, so it becomes both `opts`' order source and the `resolvedOrderBy` arg. Add it to `reaper.go`.
- `common.SystemUserContextValue` — if `common` only exposes `SystemUserContext(tenant) context.Context`, add a sibling `SystemUserContextValue(tenant) *spi.UserContext` (or extract the `*spi.UserContext` the existing helper builds) so the same system principal is reused. Check `internal/common/tenant.go` and reuse, do not invent a second construction.
- Add the message constant near `jobFailureFallback`: `const jobAttemptsExhausted = "search abandoned: executor lost repeatedly"`.

- [ ] **Step 5: Delete `FailStaleJobs`** and update its callers (the app tick — Task 10 rewrites `app.go`; for now leave a compile error to be resolved in Task 10, or temporarily point the tick at `ReclaimStaleJobs` with a placeholder maxAttempts — do the real wiring in Task 10). Prefer: implement Task 10 immediately after so the tree builds.

- [ ] **Step 6: Add the driving unit tests** (in `reaper_test.go`; full rewrite in Task 11)

Add at minimum a compile-and-behaviour test that a stale job with `StaleClaims` below the cap is re-enqueued and completes, and one at the cap is failed with `jobAttemptsExhausted`. (Full matrix in Task 11.)

- [ ] **Step 7: Build**

```bash
go build ./... && go test ./internal/domain/search/... -count=1
```
Expected: PASS once Task 10 wiring lands; if building standalone, expect the `app.go` caller to fail — proceed to Task 10.

- [ ] **Step 8: Commit** (may be combined with Task 10 if the tree does not build independently)

```bash
git add internal/domain/search/reaper.go internal/domain/search/pool.go internal/domain/search/service.go internal/domain/search/reaper_test.go internal/domain/search/pool_test.go
git commit -m "feat(search): reclaim and re-execute stale async jobs; attempt cap on StaleClaims"
```

---

## Task 9: config — `CYODA_SEARCH_JOB_MAX_ATTEMPTS`

**Files:**
- Modify: `app/config.go`
- Modify: `cmd/cyoda/help/config_registry.go`
- Test: `app/config_test.go`, `app/config_validate_test.go`, `app/config_registry_binding_test.go`

**Interfaces:**
- Produces: `Config.SearchJobMaxAttempts int`; `DefaultConfig()` binds `CYODA_SEARCH_JOB_MAX_ATTEMPTS` default `3`; `ValidateSearchJobMaxAttempts(int) error`.
- Consumes (Task 10): `cfg.SearchJobMaxAttempts` passed to `ReclaimStaleJobs`.

- [ ] **Step 1: Write failing tests** (in `app/config_test.go` and `app/config_validate_test.go`)

`config_test.go` — default and override:

```go
func TestConfig_SearchJobMaxAttempts_Default(t *testing.T) {
	os.Unsetenv("CYODA_SEARCH_JOB_MAX_ATTEMPTS")
	if got := DefaultConfig().SearchJobMaxAttempts; got != 3 {
		t.Fatalf("default = %d, want 3", got)
	}
}
func TestConfig_SearchJobMaxAttempts_Override(t *testing.T) {
	t.Setenv("CYODA_SEARCH_JOB_MAX_ATTEMPTS", "5")
	if got := DefaultConfig().SearchJobMaxAttempts; got != 5 {
		t.Fatalf("override = %d, want 5", got)
	}
}
```

`config_validate_test.go` — reject `< 1`:

```go
func TestValidateSearchJobMaxAttempts(t *testing.T) {
	if err := ValidateSearchJobMaxAttempts(1); err != nil {
		t.Fatalf("1 must be valid: %v", err)
	}
	if err := ValidateSearchJobMaxAttempts(0); err == nil {
		t.Fatal("0 must be rejected")
	}
}
```
Run both — Expected: FAIL (field/validator undefined).

- [ ] **Step 2: Add the field** (in `Config`, after `SearchJobStaleAfter`)

```go
	// SearchJobMaxAttempts bounds how many executions an async-search job may
	// consume before it is failed: the initial run plus one per executor lost
	// without a graceful release (SearchJob.StaleClaims). A graceful handoff
	// (Release then reclaim) does not count. CYODA_SEARCH_JOB_MAX_ATTEMPTS,
	// default 3, minimum 1; 1 disables re-execution (claim-then-FAIL).
	SearchJobMaxAttempts int
```

- [ ] **Step 3: Bind it in `DefaultConfig()`** (after `SearchJobStaleAfter`)

```go
		SearchJobMaxAttempts: envInt("CYODA_SEARCH_JOB_MAX_ATTEMPTS", 3),
```

- [ ] **Step 4: Add the validator and call it in `Validate()`**

```go
// ValidateSearchJobMaxAttempts rejects a cap below 1. Config is a QA'd
// artefact: an invalid value is a hard startup error, not a clamp. A cap of 1
// disables re-execution (a job is failed on its first executor loss);
// anything below 1 would fail a job before it ever ran.
func ValidateSearchJobMaxAttempts(n int) error {
	if n < 1 {
		return fmt.Errorf("CYODA_SEARCH_JOB_MAX_ATTEMPTS must be >= 1, got %d", n)
	}
	return nil
}
```
In `Config.Validate()`, add `if err := ValidateSearchJobMaxAttempts(c.SearchJobMaxAttempts); err != nil { return err }`. Add the matching per-setting call in `cmd/cyoda/main.go`'s startup validation block (grep for `ValidateSearchJobStaleAfter` there and add alongside).

- [ ] **Step 5: Register the env var** (in `cmd/cyoda/help/config_registry.go`, after the stale-after entry)

```go
	{Name: "CYODA_SEARCH_JOB_MAX_ATTEMPTS", Topic: "search", Type: "int", Default: "3", Description: "Executions an async-search job may consume before it is failed: the initial run plus one per executor lost without a graceful release. Graceful handoffs do not count. Must be >= 1 (1 disables re-execution)."},
```
And add the binding-test row in `app/config_registry_binding_test.go`:

```go
		"CYODA_SEARCH_JOB_MAX_ATTEMPTS": renderInt(c.SearchJobMaxAttempts),
```
(Use the int renderer the file already uses for other int vars; grep for `renderInt`/how `CYODA_SEARCH_ASYNC_WORKERS` is rendered and match it.)

- [ ] **Step 6: Run the config tests GREEN**

```bash
go test ./app/ -run 'Config|Validate' -count=1
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add app/config.go cmd/cyoda/help/config_registry.go app/config_test.go app/config_validate_test.go app/config_registry_binding_test.go cmd/cyoda/main.go
git commit -m "feat(config): CYODA_SEARCH_JOB_MAX_ATTEMPTS bounds async re-execution attempts"
```

---

## Task 10: app wiring — split tickers, startup sweep, shutdown releases

**Files:**
- Modify: `app/app.go`
- Test: `app/search_reaper_test.go`

**Interfaces:**
- Consumes: `ReclaimStaleJobs` (Task 8), `ReleaseRegisteredJobs` (Task 7), `cfg.SearchJobMaxAttempts` (Task 9).
- Produces: `reclaimStaleTick` and a snapshot-TTL tick as separate functions; two ticker goroutines with a startup sweep; `stopSearchReaper` closed+awaited on both `Shutdown` and `Close`; `Shutdown` calls `ReleaseRegisteredJobs`; `AbortRegisteredJobs` deleted.

- [ ] **Step 1: Write/adjust failing tests** (in `app/search_reaper_test.go`)

Rewrite `TestSearchReaperTick_*` to target the new split. The panic-containment test now targets `reclaimStaleTick` (which calls `ReclaimStaleJobs` on a store whose `ClaimStale` returns a nil element) — keep the `nilJobClaimStore` but have it drive the reclaim path. Add a test that a healthy tick leaves health alone. (These compile-fail until Step 2–4.)

- [ ] **Step 2: Split `searchReaperTick`** into two functions in `app.go`

```go
// reapExpiredSnapshotsTick deletes terminal jobs past the snapshot TTL. Runs
// on SearchReapInterval. Panic-latches health like the other engine-work sites.
func reapExpiredSnapshotsTick(ctx context.Context, store spi.AsyncSearchStore, snapshotTTL time.Duration, healthFlag *atomic.Bool) {
	defer latchOnPanic(healthFlag, "search snapshot reaper")
	reaped, err := store.ReapExpired(ctx, snapshotTTL)
	if err != nil {
		slog.Error("search snapshot reaper error", "pkg", "search", "err", err)
	} else if reaped > 0 {
		slog.Info("reaped expired search snapshots", "pkg", "search", "count", reaped)
	}
}

// reclaimStaleTick claims stale/released RUNNING jobs and re-executes them on
// this node (or fails those past the attempt cap). Runs on the heartbeat
// interval — a finer cadence than the snapshot reap — plus once at startup.
func reclaimStaleTick(ctx context.Context, svc *search.SearchService, staleAfter time.Duration, maxAttempts int, healthFlag *atomic.Bool) {
	defer latchOnPanic(healthFlag, "search stale-job reaper")
	reenqueued, failed, err := svc.ReclaimStaleJobs(ctx, staleAfter, maxAttempts)
	if err != nil {
		slog.Error("stale search job reclaim error", "pkg", "search", "err", err)
		return
	}
	if reenqueued > 0 {
		slog.Info("re-enqueued stale async search jobs", "pkg", "search", "count", reenqueued)
	}
	if failed > 0 {
		slog.Warn("failed async search jobs past the attempt cap", "pkg", "search", "count", failed)
	}
}
```
Add the shared `latchOnPanic(flag *atomic.Bool, site string)` helper (extract the existing recover/latch closure so both ticks share it), or inline the existing recover block in each — match the file's current style.

- [ ] **Step 3: Rewire the goroutines** (replace the single-ticker block after `a.stopSearchReaper = make(chan struct{})`)

```go
	a.stopSearchReaper = make(chan struct{})
	a.searchReaperDone = make(chan struct{})
	go func() {
		defer close(a.searchReaperDone)
		// Startup sweep: a restarted node reclaims its own released jobs and
		// any already-stale jobs the moment it can execute, not after the
		// first interval.
		reclaimStaleTick(context.Background(), a.searchService, cfg.SearchJobStaleAfter, cfg.SearchJobMaxAttempts, a.healthFlag)

		snapTicker := time.NewTicker(cfg.SearchReapInterval)
		defer snapTicker.Stop()
		claimTicker := time.NewTicker(cfg.SearchJobHeartbeatInterval)
		defer claimTicker.Stop()
		for {
			select {
			case <-snapTicker.C:
				reapExpiredSnapshotsTick(context.Background(), searchStore, cfg.SearchSnapshotTTL, a.healthFlag)
			case <-claimTicker.C:
				reclaimStaleTick(context.Background(), a.searchService, cfg.SearchJobStaleAfter, cfg.SearchJobMaxAttempts, a.healthFlag)
			case <-a.stopSearchReaper:
				return
			}
		}
	}()
```
Add the `searchReaperDone chan struct{}` field to `App` (near `stopSearchReaper`).

- [ ] **Step 4: Make the stop idempotent and awaited on both `Shutdown` and `Close`**

Extract a helper on `App`:

```go
// stopSearchReaperLoop signals the reaper goroutine and waits for it to exit.
// Idempotent: safe to call from both Shutdown and Close (sync.Once guards the
// close; the done-channel wait is a no-op once the goroutine has already
// returned).
func (a *App) stopSearchReaperLoop() {
	if a.stopSearchReaper == nil {
		return
	}
	a.stopSearchReaperOnce.Do(func() { close(a.stopSearchReaper) })
	<-a.searchReaperDone
}
```
Add `stopSearchReaperOnce sync.Once`. Call `a.stopSearchReaperLoop()` at the top of both `Shutdown` and `Close` (Close needs it so a node whose store is closing does not keep sweeping on the 15s ticker).

- [ ] **Step 5: Shutdown releases instead of failing** (replace the `AbortRegisteredJobs` block in `Shutdown`)

```go
	a.stopSearchReaperLoop()
	if a.searchPool != nil {
		drainCtx, cancel := context.WithTimeout(context.Background(), searchDrainBudget)
		a.searchPool.Drain(drainCtx)
		cancel()
	}
	if a.searchService != nil {
		// Jobs still registered after the drain budget are released for
		// reclaim (a peer, or this node on restart, re-runs them), not failed.
		if n := a.searchService.ReleaseRegisteredJobs(context.Background()); n > 0 {
			slog.Info("released in-flight async search jobs for reclaim at shutdown", "pkg", "search", "count", n)
		}
	}
```
Delete `AbortRegisteredJobs` from `service.go` (Task 7 left it; remove it now that nothing calls it). Grep to confirm no other caller: `grep -rn AbortRegisteredJobs`.

- [ ] **Step 6: Build and run app + search tests**

```bash
go build ./... && go vet ./... && go test ./app/ ./internal/domain/search/... -count=1
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add app/app.go internal/domain/search/service.go app/search_reaper_test.go
git commit -m "feat(app): reclaim sweep on the heartbeat interval with a startup sweep; shutdown releases for reclaim"
```

---

## Task 11: engine unit tests — the reclaim matrix

**Files:**
- Modify: `internal/domain/search/reaper_test.go` (rewrite from claim-then-FAIL to reclaim)
- Modify: `internal/domain/search/reaper_self_executing_test.go` (adapt to `ReclaimStaleJobs`)
- Modify: `internal/domain/search/pool_test.go` (add `Cap()` test)

**Interfaces:**
- Consumes: `ReclaimStaleJobs`, `registerReclaim`, `WorkerPool.Cap()`, `errJobReleased`/`errJobSuperseded`.

- [ ] **Step 1: Rewrite the existing `TestFailStaleJobs_*` tests** as `TestReclaimStaleJobs_*`, driven through a `SearchService` (not the free function). The reclaim path needs a live model + entities in the store so a re-executed job can actually complete; build a `SearchService` over a memory factory with a registered model, mirroring `executor_test.go`'s setup. Cover:
  - `ReenqueuesAndCompletesStaleJob` — a stale RUNNING job (StaleClaims 0) with a live model is claimed, cleared, re-run, and reaches SUCCESSFUL with the correct count.
  - `FailsAtAttemptCap` — a job seeded at `StaleClaims == maxAttempts` (bump it via repeated `ClaimStale` on a `TestClock`, or seed directly) is failed with `jobAttemptsExhausted`, never re-enqueued.
  - `ReleaseThenClaimDoesNotCount` — a released job claimed and re-run has `StaleClaims == 0` afterward (assert via `GetJob`).
  - `ZeroHeadroomClaimsNothing` — with the pool saturated (registry size == Cap), `ReclaimStaleJobs` returns `(0,0,nil)` and the store's `ClaimStale` is never called (wrap the store to fail the test if called).
  - `ClearResultsErrorReleases` — a store whose `ClearResults` errors leaves the job RUNNING and released (assert a subsequent `ClaimStale` with a long staleAfter re-takes it), never FAILED, never re-enqueued.
  - `SelfReclaimReplacesHandle` — register a job, then reclaim the same id; assert the old handle was cancelled with `errJobSuperseded` and the new epoch's handle survives a `deregisterJobHandle(old)` call.

- [ ] **Step 2: Adapt `reaper_self_executing_test.go`** — the guard now lives in `ReclaimStaleJobs`; the `selfExecutingAsyncStore` should fail the test if `ClaimStale`/`UpdateJobStatus`/`Release`/`ClearResults` is called, and `TestReclaimStaleJobs_SelfExecutingStore_NeverClaims` asserts `ReclaimStaleJobs` returns `(0,0,nil)`.

- [ ] **Step 3: Add the `Cap()` test** (`pool_test.go`)

```go
func TestWorkerPool_Cap(t *testing.T) {
	p := search.NewWorkerPool(3, 7)
	t.Cleanup(func() { p.Drain(context.Background()) })
	if got := p.Cap(); got != 10 {
		t.Fatalf("Cap() = %d, want 10 (workers 3 + queue 7)", got)
	}
}
```

- [ ] **Step 4: Run the search package**

```bash
go test ./internal/domain/search/... -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/search/reaper_test.go internal/domain/search/reaper_self_executing_test.go internal/domain/search/pool_test.go
git commit -m "test(search): reclaim matrix — reenqueue, attempt cap, release-not-counted, zero-headroom, clear-error, self-reclaim"
```

---

## Task 12: isolated e2e — crash, restart, cap, shutdown-release

**Files:**
- Modify: `internal/e2e/async_stream_test.go` (rewrite the reaper + shutdown tests; add crash/cap/restart)
- Modify: `internal/e2e/async_cancel_multinode_test.go` (add the deposed-executor scenario)
- Modify: `internal/e2e/e2e_test.go` (quiesce the global App's reclaim cadence)

**Interfaces:**
- Consumes: the whole engine + plugin change; existing helpers (`newBlockingIterateBackend`, `iterateGate`, `newCallbackHarnessConfigured`, `insertOrphanRunningJob`, `backdateJobCreatedAt`, `persistedJobErrorFor`, `waitForAsyncTerminal`, `asyncJobStatus`, `reaperFastCadence`).

- [ ] **Step 1: Quiesce TestMain's global App** (in `e2e_test.go`, in the `cfg` built for `testApp`)

```go
	// The package-global testApp shares this Postgres with every per-test
	// harness. With the reclaim sweep on the heartbeat interval it would
	// otherwise claim released/stale jobs from other tests' Apps and make
	// "which node completed the job" nondeterministic. Quiesce it: a 1h
	// heartbeat interval and a 4h stale bound (staleAfter >= 4x interval) mean
	// its only reclaim sweep is the startup one. Plain config, no test hook.
	cfg.SearchJobHeartbeatInterval = time.Hour
	cfg.SearchJobStaleAfter = 4 * time.Hour
```
Confirm `Config.Validate` (called in `app.New`) accepts 4h >= 4×1h.

- [ ] **Step 2: Rewrite `TestE2E_AsyncSearch_StaleJobReaper_FailsOrphan`** → `TestE2E_AsyncSearch_OrphanReExecuted`. The orphan row (via `insertOrphanRunningJob` + `backdateJobCreatedAt`) now needs real entities behind its model so the re-execution produces results; seed a few. Assert the job reaches **SUCCESSFUL** (not FAILED) with a result count equal to a direct search over the same condition. `TestE2E_AsyncSearch_StaleJobReaper_SparesLiveExecutor` stays (a live blocked executor is still never claimed).

- [ ] **Step 3: Add `TestE2E_AsyncSearch_CrashMidScan_PeerCompletes`** — two Apps (A, B) over one Postgres, the blocking-`Iterate` backend on A, fast reclaim cadence on B (and on A). Submit on A, wait for A's worker to enter `Iterate`, then `a.Close()` **without** `Shutdown()` (build A as a standalone app like `ShutdownDrain` does, registering only `Close` in cleanup — but call it explicitly here to simulate the crash). Assert B completes the job SUCCESSFUL with the correct count, via B's HTTP status endpoint.

- [ ] **Step 4: Add `TestE2E_AsyncSearch_CrashMidSave_PartialCleared`** — synthesise a stale RUNNING row (epoch 1) and directly insert a partial page of `search_job_results` for it by SQL (the "committed partial chunk" shape without needing >1000 live matches), seed the model's real entities, run one App with fast reclaim cadence, and assert: the job reaches SUCCESSFUL, the final result set equals a direct search (no duplicates from the stale rows), i.e. the reclaim's `ClearResults` wiped the partial page.

- [ ] **Step 5: Add `TestE2E_AsyncSearch_AttemptCap_Fails`** — seed a stale RUNNING row with `stale_claims` set to `MaxAttempts - 1` by SQL (so the next staleness claim reaches the cap), configure the App with `SearchJobMaxAttempts` small (e.g. via a cfg mutator setting it to 1 so the first claim caps), assert the job reaches FAILED with the `jobAttemptsExhausted` message via `persistedJobErrorFor`.

- [ ] **Step 6: Add `TestE2E_AsyncSearch_SingleNodeRestart_Reclaims`** — one App submits a job over the blocking backend; before the scan can finish, `Shutdown()` (which releases it, leaving it RUNNING+released); then build a **second** App on the same Postgres with a non-blocking backend and fast cadence and assert its startup sweep reclaims and completes the job SUCCESSFUL. (Proves release + startup sweep, D8.)

- [ ] **Step 7: Add `TestE2E_AsyncSearch_ShutdownReleases_NoFailedWrite`** (replaces `TestE2E_AsyncSearch_ShutdownDrain_FailsInFlightJob`) — submit over the blocking backend, `Shutdown()`, assert the job is **RUNNING** (released) not FAILED, and that a peer App with a live backend and a long `staleAfter` (1h) still reclaims it within one heartbeat interval (proving Release, not staleness, drove the handoff).

- [ ] **Step 8: Add the deposed-executor scenario** (in `async_cancel_multinode_test.go`) — `TestE2E_AsyncSearch_DeposedExecutorFenced`: node A over the blocking backend with a slow heartbeat (8s) and its stale bound at the 4× floor; node B with a 2s stale bound. Submit on A, wait for A's `Iterate` to block, let B's reclaim take it (A is alive but not heartbeating fast enough) and complete it; then release A's gate and assert A's resumed write is fenced (the job stays SUCCESSFUL as B wrote it, single author) — assert the outcome, not which of A's calls was fenced.

- [ ] **Step 9: Run the e2e package** (Docker)

```bash
make preflight && go test ./internal/e2e/... -count=1
```
Expected: PASS. (Do not add `-v`; do not treat this as whole-suite verification.)

- [ ] **Step 10: Commit**

```bash
git add internal/e2e/async_stream_test.go internal/e2e/async_cancel_multinode_test.go internal/e2e/e2e_test.go
git commit -m "test(e2e): async orphan re-execution — crash mid-scan/mid-save, restart, attempt cap, shutdown-release, deposed executor"
```

---

## Task 13: subprocess-cluster SIGKILL test

**Files:**
- Modify: `e2e/parity/fixtureutil/fixtureutil.go` (per-node kill + env-override launch)
- Modify: `e2e/parity/postgres/multinode_fixture.go` (expose kill + a cadence-override setup)
- Create: `e2e/parity/postgres/async_node_crash_test.go`

**Interfaces:**
- Consumes: `LaunchCyodaClusterAndCompute`, `ClusterLaunchResult`, `killProcessGroupNoWait`, `NodeLogs`, parity client (`SubmitAsyncSearch`, `GetAsyncSearchStatus`, `GetAsyncSearchResults`), `driver.NewRemote`.
- Produces: a `KillNode(i int)` capability on the postgres multinode fixture; an env-override `MustSetupMultiNodeWithEnv`; a standalone `TestAsyncNodeCrash_PeerCompletes` (NOT registered in the multinode scenario registry).

- [ ] **Step 1: Expose a per-node kill from the fixture.** The `killNodes` closure and the per-node `clusterNode` are private to `LaunchCyodaClusterAndComputeWithBinaries`. Add a `KillNode func(i int)` to `ClusterLaunchResult`, closing over the launched `nodes` slice: it calls `killProcessGroupNoWait(nodes[i].cmd)` then `<-nodes[i].exitedCh`. Populate it on the successful-launch path alongside `CyodaCmds`. This is the minimal, honest exposure (no double-Wait).

- [ ] **Step 2: Add an env-override multinode setup.** `MustSetupMultiNode` hard-codes its `extraEnv`; add `MustSetupMultiNodeWithEnv(t, n, extraEnv []string)` (the current one delegates to it with the default env), threading `extraEnv` into `LaunchCyodaClusterAndCompute`. Expose `KillNode(i int)` on `pgMultiNode` (store `result.KillNode`). Extend the `MultiNodeFixture` interface? No — keep `KillNode` off the shared interface (a type assertion in the crash test, like `NodeLogs`/`ComputeUser`), since crash is postgres-first and the shared scenario registry must not gain a kill.

- [ ] **Step 3: Write the crash test** (`async_node_crash_test.go`, standalone `func TestAsyncNodeCrash_PeerCompletes(t *testing.T)`, NOT via `multinode.Register`)

Steps in the test:
- `MustSetupMultiNodeWithEnv(t, 3, []string{ ...postgres env..., "CYODA_SEARCH_JOB_HEARTBEAT_INTERVAL=1s", "CYODA_SEARCH_JOB_STALE_AFTER=4s", "CYODA_SEARCH_REAP_INTERVAL=1s"})` (3 nodes so two survivors race on `SKIP LOCKED`).
- Via `driver.NewRemote(urls[0], tenant.Token)`: create+lock a model, create N entities.
- Submit a batch of async searches to node 0 via a parity client pointed at `urls[0]` (`SubmitAsyncSearch`), collecting job IDs.
- `fix.(interface{ KillNode(int) }).KillNode(0)`.
- For each job, poll a **survivor** node (`urls[1]`) via `GetAsyncSearchStatus` until SUCCESSFUL (budget generous: `staleAfter` + a few reap intervals + scan time), then `GetAsyncSearchResults` and assert the count equals the seeded matching count.
- Assert no double execution: for each job ID, grep all three `NodeLogs` for the job's completion log line and assert it appears at most once across surviving nodes. (Add a distinctive log line to the success path if none exists — check `runAsyncJob`'s SUCCESSFUL branch; it currently logs only on error. If needed, add an `slog.Debug` on success keyed by jobID, or assert via the DB that `epoch` advanced by exactly the number of reclaims, which is cleaner: assert final `epoch <= 2` for jobs claimed once. Prefer the DB-epoch assertion to avoid adding a production log line purely for a test.)

Prefer the DB-epoch assertion (no production log added): after all jobs complete, read each job's `epoch` from Postgres and assert it is small (a job executed once and reclaimed once ends at epoch 2; a double-execution would show a higher epoch or a torn count). The authoritative "single author" guarantee is the result-count equality per job plus epoch fencing.

- [ ] **Step 4: Run it** (Docker; heavy — one cluster boot)

```bash
make preflight && go test ./e2e/parity/postgres/ -run TestAsyncNodeCrash_PeerCompletes -count=1
```
Expected: PASS. Note this adds one cluster boot to the uncached parity package on every `make test`; that cost is accepted in the spec.

- [ ] **Step 5: Commit**

```bash
git add e2e/parity/fixtureutil/fixtureutil.go e2e/parity/postgres/multinode_fixture.go e2e/parity/postgres/async_node_crash_test.go
git commit -m "test(e2e): SIGKILL a cluster node mid-async-job; a survivor completes it, singly authored"
```

---

## Task 14: documentation

**Files:**
- Modify: `cmd/cyoda/help/content/config.md`, `cmd/cyoda/help/content/search.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `README.md`
- Modify: `CHANGELOG.md`
- Create: `docs/cloud-parity/async-job-node-failure-resilience.md`

- [ ] **Step 1: `config.md`** — add the `CYODA_SEARCH_JOB_MAX_ATTEMPTS` row under "Search internals". Amend the `CYODA_SEARCH_REAP_INTERVAL` note: it no longer drives the stale sweep (it drives only the snapshot-TTL reap now). Amend the `CYODA_SEARCH_JOB_STALE_AFTER` note: the stale/reclaim sweep runs on `CYODA_SEARCH_JOB_HEARTBEAT_INTERVAL`'s ticker plus a startup sweep, so a crash hands off within `staleAfter` + one heartbeat interval, and a graceful shutdown/restart hands off within one heartbeat interval (or immediately on the restarted node). Add the operational note: on postgres a node that dies mid-save is reaped by the server's transaction-local idle timeout, so mid-save handoff matches any other crash.

- [ ] **Step 2: `search.md`** — rewrite the orphan-handling paragraph. Replace "This milestone fails the job outright rather than re-executing it elsewhere" with the re-execution semantics: an orphaned job is claimed by a live node, its partial results cleared, and re-run as-at its stored PointInTime; it is FAILED only after `CYODA_SEARCH_JOB_MAX_ATTEMPTS` executor losses (the status is contractual, the message is not); graceful shutdown/restart releases jobs for prompt reclaim. Correct the reap-cadence sentence (heartbeat interval, not `SEARCH_REAP_INTERVAL`).

- [ ] **Step 3: `docs/ARCHITECTURE.md`** — three edits:
  - §3.4 "Panic containment": the reaper now has two sweeps on two tickers (snapshot-TTL on `SearchReapInterval`, reclaim on `SearchJobHeartbeatInterval`), both panic-latching. Update "the snapshot-TTL and stale-job sweeps share one ticker" to reflect the split.
  - The "Orphan handling" subsection (~§4.6): change "This milestone's disposition is claim-then-FAIL, not re-execution" to the re-execution semantics; document `StaleClaims`, the attempt cap, `Release`, the two tickers + startup sweep, and shutdown-releases-not-fails. Update the "Cancellation and shutdown" paragraph (shutdown releases for reclaim, does not fail). Update the schema paragraph (`released`, `stale_claims` columns).
  - The env table: add `CYODA_SEARCH_JOB_MAX_ATTEMPTS`; correct the `CYODA_SEARCH_REAP_INTERVAL` description (snapshot reap only).
  - §12 pending-gaps table: **remove** the "Async job re-execution after an orphan claim" row (the gap is closed).

- [ ] **Step 4: `README.md`** — in "Async search backpressure", change "A background reaper claims and fails any job whose heartbeat has gone silent" to "claims and re-executes ... on a live node; a job is failed only after `CYODA_SEARCH_JOB_MAX_ATTEMPTS` executor losses". Mention graceful shutdown hands off promptly.

- [ ] **Step 5: `CHANGELOG.md`** (cyoda-go `[Unreleased]`) — a `### Changed` entry for the disposition (claim-then-re-execute, shutdown releases, reclaim on the heartbeat interval + startup sweep) and an `### Added` entry for `CYODA_SEARCH_JOB_MAX_ATTEMPTS`. No issue number in the text.

- [ ] **Step 6: Create `docs/cloud-parity/async-job-node-failure-resilience.md`** — the contract Cloud mirrors: a RUNNING async job survives loss of its executor node and completes with a singly-authored result set; a node departing gracefully hands its jobs off promptly (not after a stale timeout); a job is FAILED only after a bounded number of executions (status contractual, message text not); clients observe nothing but a longer RUNNING span. Note the commercial backend gets this via its own shard recovery and MAY no-op `Release`/`ClaimStale`/`ClearResults`; the residual (a hung-but-heartbeating executor is never reclaimed — liveness is not progress) is shared with cyoda-go and out of scope here.

- [ ] **Step 7: Run the help/doc gates**

```bash
go test ./cmd/cyoda/help/... -count=1   # TestErrCode_Parity (no new code, must still pass), config registry parity
go test ./app/ -run Config -count=1
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/cyoda/help/content/config.md cmd/cyoda/help/content/search.md docs/ARCHITECTURE.md README.md CHANGELOG.md docs/cloud-parity/async-job-node-failure-resilience.md
git commit -m "docs: async orphan re-execution — help, architecture, README, changelog, cloud-parity contract"
```

---

## Task 15: consumer notice, exit check, and the SPI pin bump (release window)

**Files:**
- `cyoda-go-cassandra` (sibling repo `../cyoda-go-cassandra`) — consumer notice / no-op `Release`
- Modify: `COMPATIBILITY.md`
- Modify: `go.mod`, `plugins/{memory,sqlite,postgres}/go.mod`, `go.sum` (pin bump)
- Modify: `go.work` (drop the local SPI use line)

- [ ] **Step 1: Greppable exit check** — confirm the retired vocabulary is gone from shipped source:

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.claude/worktrees/feat-509-async-reexecute
grep -rn "FailStaleJobs\|AbortRegisteredJobs\|initialEpoch\|claim-then-FAIL" \
  --include=*.go --include=*.md . \
  | grep -v "docs/plans/\|docs/superpowers/\|CHANGELOG.md"
```
Expected: no hits. Any hit outside those paths is a leftover — fix it before proceeding.

- [ ] **Step 2: File the cassandra consumer notice BEFORE the SPI tag/merge.** Per `MAINTAINING.md`, `KNOWN_CONSUMERS.md` lists `cyoda-platform/cyoda-go-cassandra`. Open a courtesy PR (or issue) on `../cyoda-go-cassandra` describing the breaking SPI change (`Release` added, `StaleClaims` added) and that a self-executing store may no-op `Release`. The courtesy PR carries only its title (per repo convention). Add the no-op `Release` to the cassandra `AsyncSearchStore` there so it compiles against the new SPI. Link it from the cyoda-go PR description.

- [ ] **Step 3: Push the SPI commits** (Tasks 1–2), then get the pseudo-version.

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi && git push origin main
# wait ~30s for the module proxy, then from the cyoda-go worktree:
```

- [ ] **Step 4: Drop the local SPI `use` line and bump the pin** (all four go.mod files to the pushed SPI pseudo-version, one commit)

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.claude/worktrees/feat-509-async-reexecute
go work edit -dropuse /Users/paul/go-projects/cyoda-light/cyoda-go-spi
SPI_VER="$(cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi && GOWORK=off GOFLAGS=-mod=mod go list -m -f '{{.Version}}' github.com/cyoda-platform/cyoda-go-spi@$(git rev-parse HEAD))"
echo "SPI pseudo-version: $SPI_VER"
for m in go.mod plugins/memory/go.mod plugins/sqlite/go.mod plugins/postgres/go.mod; do
  (cd "$(dirname "$m")" && go mod edit -require="github.com/cyoda-platform/cyoda-go-spi@${SPI_VER}")
done
go mod download github.com/cyoda-platform/cyoda-go-spi
make check-spi-pin-sync
```
Expected: `check-spi-pin-sync` passes (all four pin the same version).

- [ ] **Step 5: Repin the plugins** (the GOWORK=off jobs resolve the root go.mod's plugin pins as a consumer). Push the plugin+engine commits first if not already pushed, then:

```bash
make repin-plugins   # pins root go.mod to a pseudo-version of pushed HEAD
```
Commit the repin result as its own commit (never amend the commit repin pointed at). See `feedback_spi_release_mechanics` / MAINTAINING.md §3.

- [ ] **Step 6: Update `COMPATIBILITY.md`** — add a "twelfth wave" note to the `v0.8.4 (planned)` row: the pin advances to the new SPI pseudo-version across all four go.mod files; `AsyncSearchStore` gains `Release` and `SearchJob` gains `StaleClaims`; the consumer obligation on the commercial backend (implement or no-op `Release`, populate `StaleClaims`); note the tag-2 "no SPI change" promise for #509's follow-up is superseded.

- [ ] **Step 7: Full verification** (Docker; the Gate-5 command)

```bash
make preflight && make test-full && go vet ./...
```
Expected: green — root + all three plugin submodules + `internal/e2e`. Fix anything red; do not narrow the run.

- [ ] **Step 8: Race check once** (before PR)

```bash
make race
```
Expected: green (excludes `internal/e2e` by design).

- [ ] **Step 9: Commit the pin/compat/repin work**

```bash
git add go.mod go.sum plugins/memory/go.mod plugins/sqlite/go.mod plugins/postgres/go.mod plugins/*/go.sum go.work COMPATIBILITY.md
git commit -m "chore: bump cyoda-go-spi pin for AsyncSearchStore.Release; COMPATIBILITY wave note"
```
(`go.work` change here is the `dropuse` — its committed form is `.` + 3 plugins, so this restores the tracked shape.)

---

## Self-Review

**Spec coverage** (§ of the spec → task):
- §2 D1 (Release for handoff) → Tasks 1,3,4,5,7. D2 (sweep on heartbeat interval) → Task 10. D3 (attempt cap on StaleClaims) → Tasks 1,3,4,5,8,9. D4 (no cap on reclaim) → Task 8 `registerReclaim`. D5 (queue-full → release) → Task 8. D6/D7 (shutdown releases; suppress writes every path) → Tasks 7,10. D8 (startup sweep) → Task 10.
- §3.1 Release + StaleClaims → Task 1; spitest → Task 2. §3.2 per-backend + idle timeout → Tasks 3,4,5. §3.3 spitest subtests → Task 2. §3.4 consumers → Task 15.
- §4.1 epoch threading → Task 6. §4.2 ReclaimStaleJobs → Task 8. §4.3 cadence+startup → Task 10. §4.4 shutdown → Tasks 7,10. §4.5 config → Task 9. §4.6 wire unchanged → no code; verified by e2e in Task 12.
- §5 coverage matrix → Tasks 11 (unit), 12 (e2e), 13 (cluster). spitest cells → Task 2. Parity/gRPC cells waived per spec (documented in the matrix).
- §6 docs → Task 14. §7 out-of-scope → nothing built.

**Placeholder scan:** no "TBD"/"handle errors appropriately"; every code step carries real code. The two "prefer X" notes (headroom via `pool.Cap()`; DB-epoch assertion over a production log line) are explicit design choices with the rationale stated, not deferrals.

**Type consistency:** `Release(ctx, jobID string, epoch int64) error` and `StaleClaims int64` consistent across Tasks 1–5. `ReclaimStaleJobs(ctx, staleAfter, maxAttempts) (int,int,error)` consistent Tasks 8/10/11. `WorkerPool.Cap() int` Tasks 8/11. `errJobReleased`/`errJobSuperseded` Tasks 7/8/11. `asyncJobHandle{cancel context.CancelCauseFunc, uc, epoch}` Tasks 6/7/8. `deregisterJobHandle(jobID, *asyncJobHandle)` Tasks 6-note/8. `jobAttemptsExhausted` const Tasks 8/12.

**Coverage-matrix carry-forward (Gate):** every documented status stays covered — the wire surface is unchanged (§4.6), so no new endpoint×code cell exists; the FAILED-after-cap status is asserted in Task 12 Step 5; backend-agnostic store behaviour is spitest (Task 2) on all three backends; concurrency/crash tests are isolated single-backend e2e (Tasks 12–13), never the shared parity suite. No new error code → no `errors/<CODE>.md` task (confirmed: the cap message is a persisted job-error string, not a wire `errorCode`).
