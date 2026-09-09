# Async search: orphaned jobs are re-executed, not failed (#509)

**Status:** design; fresh-context review done 2026-09-08 (three blocking
findings B1–B3 and five should-fix items S1–S5 applied, see §2 D3/D5/D7,
§3.2, §4.2–4.4, §5); pending Paul's sign-off.
**Milestone:** v0.8.4 (PATCH; backward compatibility is not a constraint).
**Predecessor:** `2026-08-22-472-search-spi-surface-design.md` §4.3, §4.6, §11.

## 1. Goal

An async search job is re-executable by construction: its condition,
options and pinned `PointInTime` are durably stored before it runs. Today
(#472's interim disposition) a job whose executor node dies is *claimed and
failed*. This change makes node loss invisible to async clients on
engine-executed backends: the job is claimed by a live node, its partial
results cleared, and it is executed again as-at the stored `PointInTime`,
producing the same result set. Graceful shutdown hands jobs off promptly
rather than leaving them to go stale; a starting node sweeps immediately; a
job that keeps killing its executor is failed after a configured number of
attempts.

The commercial backend already has this property through its own shard
recovery; engine-executed backends must not be contractually weaker.

## 2. Settled decisions

| # | Decision | Settled |
|---|---|---|
| D1 | Graceful shutdown **releases** in-flight jobs through a new fenced SPI method rather than leaving them to go stale (`staleAfter` + one sweep, ten minutes at the defaults). A rolling restart is the common operational event and must have the best handoff, not the worst. | Paul, 2026-09-08 (Option B) |
| D2 | The claim sweep runs on the **heartbeat interval** (`CYODA_SEARCH_JOB_HEARTBEAT_INTERVAL`, 15s default), not on the 5-minute snapshot-TTL reap ticker. Without this, D1's benefit is capped at five minutes. No new env var: the heartbeat interval is the cluster's liveness cadence and the claim sweep is its consumer. The TTL reap keeps its own ticker. | this spec |
| D3 | Attempt cap bounds **executor losses**, not claims: the store counts claims taken by staleness (`SearchJob.StaleClaims`) and never counts a claim of a released job. `CYODA_SEARCH_JOB_MAX_ATTEMPTS`, default 3, minimum 1, is the number of executions a job may consume; value 1 reproduces today's claim-then-FAIL exactly. A rolling restart of any length costs no attempt. (Design review, blocking finding B1: with the cap on `Epoch`, a three-node rolling restart would FAIL a healthy long job with a message saying it crashed.) | this spec |
| D4 | Reclaimed jobs are never refused by the per-tenant in-flight cap (admission happened at submit, cluster-wide); they are bounded only by the claimant's pool headroom. | this spec |
| D5 | A reclaimed job that loses the enqueue race (pool full between headroom check and `Submit`) is **released** again, not left to go stale. A release is not a loss, so it costs no attempt. | this spec |
| D6 | Shutdown keeps the existing drain budget (short jobs finish, still heartbeating) and then releases whatever is still registered. The FAILED-at-shutdown path is deleted. | this spec |
| D7 | The executor writes **no failure** for a job cancelled by release, on every exit path (not only the cancellation branch); the cancellation cause distinguishes release from every other abort. A scan that completes anyway still writes SUCCESSFUL: the result set is complete and singly authored. | this spec |
| D8 | The single-node restart case needs no owner identity: released jobs are claimable immediately, and the starting node's first sweep runs at startup, not after the first interval. | this spec |

## 3. SPI change (one method; SPI "wave 12" of the v0.8.4 pseudo-pin sequence)

### 3.1 `AsyncSearchStore.Release`

```go
// Release relinquishes the caller's claim on a RUNNING job without
// finishing it: the job stays RUNNING and becomes eligible for ClaimStale
// immediately, regardless of staleAfter, so a live node can take it over
// without waiting for the heartbeat to age out. Fenced by epoch like every
// executor-side write: ErrStaleClaim if epoch is not the job's current
// Epoch, ErrAlreadyTerminal if the job is terminal, ErrNotFound if it does
// not exist. Idempotent at the same epoch. Release does not bump Epoch —
// a release is not an attempt. The released mark survives a later
// Heartbeat at the same epoch (a stray stamp from a node that is going
// away cannot resurrect the job) and is cleared by the ClaimStale that
// takes the job.
// Tenant-scoped, like Heartbeat.
Release(ctx context.Context, jobID string, epoch int64) error
```

- "Released" is store-internal state whose only contractual observation
  is through `ClaimStale`. It is not a field on `SearchJob`: exposing it
  would invite the engine to reason about it, and it has no reason to.
- `SearchJob` gains `StaleClaims int64`: *"the number of times ClaimStale
  took this job because its heartbeat went stale — an executor lost without
  releasing. A claim of a released job does not count. CreateJob persists 0
  regardless of the input value. The engine's attempt cap bounds this, not
  Epoch."* `ClaimStale` increments it inside the same atomic claim when,
  and only when, the job was not released.
- `ClaimStale` doc gains: *"A released job is eligible regardless of
  staleAfter; claiming it clears the released mark and does not increment
  StaleClaims. A job claimed by staleness has StaleClaims incremented."*
  The existing "stamps strictly later than the stamp it found stale"
  sentence is scoped to jobs claimed by staleness (a released job's stamp
  may be fresh).
- The interface preamble's fenced set ("UpdateJobStatus, SaveResults, and
  Heartbeat") and `SelfExecutingSearchStore`'s no-op set ("Heartbeat,
  ClaimStale, and ClearResults") both gain `Release`. The engine never
  calls it for self-executing stores.
- Nil `HeartbeatTime` keeps meaning "measure from `CreateTime`". Reusing
  nil for "released" was rejected: a just-created job has a nil stamp until
  its first tick and would be stolen from a live owner.

### 3.2 Per-backend representation (plugin-internal)

| Backend | Representation | Migration |
|---|---|---|
| memory | `released bool` on the job entry; `StaleClaims` on the job | none |
| sqlite | `released INTEGER NOT NULL DEFAULT 0`, `stale_claims INTEGER NOT NULL DEFAULT 0` on `search_jobs` | `000007_search_release` |
| postgres | `released BOOLEAN NOT NULL DEFAULT false`, `stale_claims BIGINT NOT NULL DEFAULT 0` on `search_jobs` | `000010_search_release` |

`Release` is the same fenced conditional UPDATE shape as `Heartbeat`
(`WHERE ... AND epoch = ? AND status NOT IN (terminal)`), setting the mark;
zero rows affected is classified by the existing probe. `ClaimStale`'s
candidate predicate becomes `status = 'RUNNING' AND (released OR
COALESCE(heartbeat_time, created_at) < cutoff)`, and the claiming UPDATE
sets `stale_claims = stale_claims + CASE WHEN released THEN 0 ELSE 1 END,
released = false`. Ordering stays oldest-`CreateTime`-first. sqlite's
per-row CAS and postgres's `FOR UPDATE SKIP LOCKED` are unchanged.

**Postgres: a dead client must not pin the job row.** `SaveResults` takes
`FOR UPDATE` on the job row for the duration of each chunk transaction. A
node that dies inside that window leaves a server session idle in
transaction, holding the row lock until the server notices the dead TCP
peer — hours at Linux keepalive defaults — and `SKIP LOCKED` would pass the
job over on every sweep, stalling it for that long. The chunk transaction
therefore begins with `SET LOCAL idle_in_transaction_session_timeout` at a
fixed modest value (30s; a live chunk is never idle between its
sequential statements for more than microseconds, and `COPY` in progress
is not idle). `SET LOCAL` is transaction-scoped, so entity transactions and
every other session are untouched. This is a non-functional property that
cannot be unit-tested without killing a socket; it is reviewed, and the
TDD waiver is recorded in the plan.

### 3.3 spitest conformance (new subtests under `AsyncSearch/`)

| Subtest | Asserts |
|---|---|
| `Release/ImmediatelyClaimable` | Release at epoch 1; `ClaimStale(1h, 1000)` + `findClaimed` (the suite is cross-tenant and earlier subtests leave stale rows) returns the job with `Epoch == 2` and `StaleClaims == 0`; `GetJob` agrees; status still RUNNING. |
| `Release/Semantics` | missing → `ErrNotFound`; wrong epoch → `ErrStaleClaim`; terminal → `ErrAlreadyTerminal`; the failed calls do not mark the job (a subsequent `ClaimStale(1h)` claims nothing). |
| `Release/Idempotent` | two releases at the same epoch both return nil; one claim results. |
| `Release/HeartbeatDoesNotResurrect` | Release, then `Heartbeat` at the same epoch returns nil; `ClaimStale(1h)` still claims it. |
| `Release/ClaimClearsMark` | after the claim (epoch 2) and a `Heartbeat` at epoch 2, `ClaimStale(1h)` claims nothing. |
| `Claim/ReleasedNotTerminal` | a released job that is then cancelled is never claimed. |
| `Claim/StaleClaimCounted` | a job claimed by staleness has `StaleClaims == 1` in the returned record and via `GetJob`; a second staleness claim makes it 2; `CreateJob` with `StaleClaims = 7` persists 0. |
| `Release/ClaimNotCounted` | release → claim → `StaleClaims` still 0; then let it go stale → claim → 1. |

No in-tree plugin may skip these.

### 3.4 Consumers

- The interface addition breaks any out-of-tree `AsyncSearchStore` until it
  adds `Release` and populates `StaleClaims` (a self-executing store may
  leave it 0). Cassandra is self-executing and adds a no-op. Per
  `MAINTAINING.md`, the consumer notice on `cyoda-go-cassandra` is filed
  **before** the SPI PR merges and linked from the PR description.
- Issue #509 and #472 §11 promised "no SPI change". D1 supersedes that;
  the supersession is recorded on the issue and named in the SPI
  `CHANGELOG.md` entry so the tag-2 promise is visibly retired.
- SPI `CHANGELOG.md` `### Breaking` entry; `COMPATIBILITY.md` wave entry in
  cyoda-go; pin bump in root + three plugin `go.mod`s in one commit at the
  end, `make check-spi-pin-sync` green; `go.work` `use` line for the SPI
  stays uncommitted during the window; `make repin-plugins` after plugin
  changes (push → repin → new commit).

## 4. Engine

### 4.1 The executor takes its epoch

`initialEpoch` (the constant 1) is deleted. The registry handle carries the
epoch; `SubmitAsync` registers at 1, the reclaim path at the claimed epoch.
`startHeartbeat`, `SaveResults`, the SUCCESSFUL write, `writeAsyncFailure`
and the panic-recovery write all take it from the handle. This is the "one
place to change instead of four" the #472 comment promised.

### 4.2 Reclaim replaces FailStaleJobs

`search.FailStaleJobs` (free function over a store) is deleted and replaced
by a service method, because re-execution needs the pool, the registry and
the heartbeat machinery:

```go
// ReclaimStaleJobs claims stale or released RUNNING jobs and re-executes
// them on this node, or fails those past the attempt cap. Returns
// (reenqueued, failed). Self-executing stores: no-op.
func (s *SearchService) ReclaimStaleJobs(ctx context.Context, staleAfter time.Duration) (int, int, error)
```

Per tick:

1. `limit = min(StaleClaimBatch, pool.Headroom())`. Headroom is free
   worker slots plus free queue slots. If zero, return without calling the
   store: a node with no capacity must not claim work it cannot start.
   During `Shutdown`/`Close` the sweep goroutine is stopped **and awaited**
   (done channel) before the pool drains, so no sweep can claim into a
   draining pool.
2. `jobs := store.ClaimStale(tenantlessCtx, staleAfter, limit)`.
3. For each job, under `common.SystemUserContext(job.TenantID)`:
   - **Attempt cap:** if `job.StaleClaims >= maxAttempts`,
     `UpdateJobStatus(..., job.Epoch, "FAILED", 0, jobAttemptsExhausted,
     now, 0)` and log at Warn with job ID, epoch and stale-claim count.
     `jobAttemptsExhausted` is a distinct safe message (`"search abandoned:
     executor lost repeatedly"`) so an operator can tell a crash-loop from
     an ordinary failure without reading logs. The initial run plus
     `StaleClaims` re-executions are the executions consumed, so
     `maxAttempts = 3` allows the initial run and two re-executions after
     executor loss; the third loss fails the job. `ErrAlreadyTerminal` on
     this write (a cancel landed between the claim and the write) is
     tolerated at Warn, as the reaper does today.
   - `ClearResults(tenantCtx, job.ID)`. On error: log at Error and
     `Release` the job (uncounted) so a peer, or this node's next sweep,
     retries promptly; a store that cannot clear results cannot run the job
     either. Never enqueue over unknown residue (fail closed). A store that
     is persistently broken loops claim→release every sweep at Error level
     until it recovers; the job stays RUNNING and is never falsely FAILED.
   - Decode `job.Condition` (domain wire JSON) and `job.SearchOpts`
     (`limit`, `pointInTime`, `orderBy`) into the executor's inputs. The
     stored `orderBy` is already resolved; the executor does not re-run
     submit-time validation (config and stored jobs are QA'd inputs, not
     runtime input to defend against). A decode failure is a defect, not a
     runtime condition: log at Error, write FAILED at the claimed epoch with
     the generic fallback message, and continue.
   - Build the job context exactly as `SubmitAsync` does: system user
     context → the backend's `AsyncScanContext` scoper (a re-execution must
     run under the same async scan ceiling as the first execution, or a
     scan that succeeded on the first node fails on the second) →
     `context.WithCancelCause`.
   - Register the job at `job.Epoch` (cancel registry + tenant in-flight
     count, **no cap check**, D4), start the heartbeat, `pool.Submit`.
     **Self-reclaim:** a node paused past `staleAfter` can claim a job it
     still has registered (its own stale executor). The registry replaces
     the old handle after cancelling it with cause *superseded* (its writes
     are fenced regardless), and `deregisterJob` is compare-and-delete on
     handle identity so the old executor's deferred deregistration cannot
     evict the new epoch's handle.
   - On `ErrQueueFull` (D5): deregister, stop the heartbeat, then
     `Release(tenantCtx, job.ID, job.Epoch)` so a peer with capacity, or
     this node's next sweep, takes it without waiting for staleness.

The executor path for a reclaimed job is `runAsyncJob` unchanged except for
the epoch parameter: same panic containment, same terminal-write rules.

### 4.3 Sweep cadence and startup (D2, D8)

The reaper goroutine gets two tickers: the snapshot-TTL reap on
`SearchReapInterval` (unchanged, 5m) and the claim sweep on
`SearchJobHeartbeatInterval` (15s). Before the first tick it runs one claim
sweep immediately, so a restarted node picks up its own released jobs and
any already-stale jobs the moment it can execute. Both sweeps keep the
existing panic containment that latches the health flag. The goroutine is
stopped and awaited by both `Shutdown` and `Close` (idempotent): a node
whose store is closed must not keep sweeping against it every 15s.
Lockstep sweeps across nodes need no jitter: claims are disjoint by
construction and an empty sweep is one cheap query.

Detection latency after this change:

| Event | Handoff bound |
|---|---|
| Graceful shutdown / rolling restart | one heartbeat interval (15s) on any live peer, or immediately on the restarted node |
| Crash, pause, partition | `staleAfter` + one heartbeat interval (5m15s) |

### 4.4 Shutdown releases (D6, D7)

`App.Shutdown` order: stop the reaper; `pool.Drain(searchDrainBudget)` with
jobs still heartbeating (short jobs complete and are never handed off);
then `searchService.ReleaseRegisteredJobs(ctx)`:

- Each registered job's context is cancelled **with cause**
  `errJobReleased` (`context.WithCancelCause` replaces `WithCancel` at
  registration). The heartbeat goroutine exits on ctx done.
- Failure writes are suppressed for a released job on **every** exit
  path: `writeAsyncFailure` itself checks `context.Cause(ctx) ==
  errJobReleased` and returns without writing, which covers the
  cancellation branch, the early returns before the scan (model load,
  schema load, entity-store lookup), the save-error path and the panic
  path in one place; and `runAsyncJob` exits at its top when it starts on
  an already-released context (a queued job the release reached before a
  worker did — the common case at shutdown, and the one that would
  otherwise land a FAILED at epoch 1 per queued job before any peer
  sweeps). Every other cause (user cancel, heartbeat fence, backend abort)
  keeps today's behaviour. A scan that completes before the cancellation
  is observed still writes SUCCESSFUL (D7).
- `Release(tenantCtx, jobID, epoch)` is issued right after cancelling,
  without waiting for the executor goroutine to unwind. Safe by fencing: a
  save chunk that commits before a peer's claim is wiped by the peer's
  `ClearResults`; one that reaches the store after the claim is refused
  with `ErrStaleClaim`. Waiting would let a backend that ignores ctx hang
  shutdown. `ErrStaleClaim`/`ErrAlreadyTerminal` from `Release` are logged
  at Warn (someone else already settled it); other errors at Error. In every
  case the job is left RUNNING; peers reclaim by staleness at worst.
- `AbortRegisteredJobs` and the FAILED-at-shutdown write are deleted.

A panic inside the executor is a job fault, not a node loss: it still
writes FAILED at the job's epoch immediately, as today.

### 4.5 Configuration (Gate 4)

| Var | Default | Validation | Meaning |
|---|---|---|---|
| `CYODA_SEARCH_JOB_MAX_ATTEMPTS` | `3` | `>= 1` | Executions a job may consume before it is failed: the initial run plus one per executor lost without release. Graceful handoffs do not count. `1` disables re-execution (claim-then-FAIL). |

`SearchReapInterval` doc changes: it no longer drives the stale sweep.
`SearchJobHeartbeatInterval` doc gains: it also sets the claim-sweep
cadence. Help topic `config.md` rows, `search.md` narrative, `README.md`,
`docs/ARCHITECTURE.md` §4.6 (orphan handling, shutdown, env table),
`DefaultConfig()`, the `Config` field docs, config registry, and
`config_registry_binding_test.go` change together. Also: the
`ARCHITECTURE.md` pending-gaps table row for this feature (it is closed),
its "one ticker drives both sweeps" sentence, and the coverage-matrix
header of `internal/e2e/async_stream_test.go` (rows naming
`FailStaleJobs`/`AbortRegisteredJobs`). Operational note in help
`search.md`: on postgres a node that dies mid-save is reaped by the
server's transaction-local idle timeout (§3.2), so a crash mid-save hands
off within `staleAfter` + one interval like any other crash.

Exit check (greppable, run before PR): `FailStaleJobs`,
`AbortRegisteredJobs`, `initialEpoch`, `claim-then-FAIL` appear nowhere
outside `docs/plans/`, `docs/superpowers/`, and `CHANGELOG.md` history.

### 4.6 Wire surface: no change

No endpoint, status code, or error code is added or changed. For the record
(gate: table per touched endpoint):

| Endpoint | Status | Behaviour after this change |
|---|---|---|
| `POST /search/async/{entity}/{version}` | 200 / 400 / 404 / 503 `SEARCH_QUEUE_FULL` | unchanged; reclaimed jobs do not consume per-tenant admission (D4) but do occupy pool capacity, so a node busy re-executing peers' jobs sheds new submits sooner. |
| `GET /search/async/{jobID}/status` | 200 / 404 | `RUNNING` now spans a handoff; `FAILED` with error `search abandoned: executor lost repeatedly` after the attempt cap (the status is contractual; the message text is not). |
| `GET /search/async/{jobID}` | 200 / 400 / 404 | unchanged; results are the singly-authored set of the claim epoch that finished. |
| `PUT /search/async/{jobID}/cancel` | 200 / 404 | unchanged; `Cancel` is not fenced, so a client can cancel a job mid-handoff and no claimant will run it (`ClaimStale` never claims terminal jobs; a running claimant's next heartbeat sees CANCELLED). |

gRPC `EntitySnapshotSearchRequest` is the same submit path; no envelope
change.

## 5. Test strategy and coverage matrix

Concurrency and node-loss scenarios live in isolated single-backend e2e
(postgres), never the shared parity suite (`.claude/rules/test-coverage.md`).

Crash simulation in `internal/e2e` uses the existing two-App-one-Postgres
pattern (`async_cancel_multinode_test.go`) plus the blocking-`Iterate`
backend gate (`async_stream_test.go`). A node "crashes" by `Close()` without
`Shutdown()`: its store pool closes (pgxpool marks itself closed before
blocking, so every later acquire fails), its heartbeat fails, its executor
cannot write, and it goes silent exactly as a dead process does. Crash
tests build their own Apps (the harness registers `Shutdown` as a cleanup,
which is the opposite of a crash). A second gate on `SaveResults` (same
wrapping-plugin technique, honouring ctx so `Close()` does not block on a
held scan connection) gives the mid-save crash flow; the "partial rows are
cleared" assertion is made deterministically by synthesising result rows
by SQL under a stale RUNNING row (the existing `insertOrphanRunningJob`
technique) and asserting the reclaim clears them and the final set equals
a direct search — a gated crash commits a partial chunk only past 1000
matches on postgres. A "deposed executor" is produced with per-node
config: node A heartbeats slowly (8s, its own stale bound at the 4× floor),
node B has a 2s stale bound, so B legitimately takes A's gated job while A
is alive; releasing the gate lets A's write hit the fence. The test asserts
the outcome (SUCCESSFUL, single author, no write over SUCCESSFUL), not
which of A's calls was fenced — that depends on tick timing.

**TestMain's global App must not compete.** It shares the package Postgres
and, under D2, would sweep every 15s and claim released or stale jobs from
any test's Apps, making attribution (which node completed the job) random.
It is configured with a one-hour heartbeat interval and a four-hour stale
bound by plain config in `TestMain`, so its only sweep is the startup one.
Tests asserting attribution own every App in play.

One real-crash test uses the subprocess cluster fixture with a new
per-node `KillNode(i)` (SIGKILL via the existing `killProcessGroupNoWait`,
reaped through the node's exit channel) and an env-override variant of
`MustSetupMultiNode` for the fast cadence. It runs **three** nodes so two
survivors race on `SKIP LOCKED`, submits a batch of jobs to node 0, kills
it, and asserts every job ends SUCCESSFUL with the correct count via a
survivor, and that each job ID appears in exactly one completion log line
across all three nodes' logs (no double execution). Which jobs were in
flight at the kill is not asserted; correctness of every outcome is. It is
a standalone test in `e2e/parity/postgres`, not registered in the
multinode scenario registry, and it costs one extra cluster boot in the
uncached parity package on every `make test`.

| # | Scenario | Unit (fake store/pool) | Running-backend e2e (postgres, `internal/e2e`) | Subprocess cluster | spitest (memory/sqlite/postgres) | Parity | gRPC |
|---|---|---|---|---|---|---|---|
| 1 | Claimed job cleared, re-enqueued at claimed epoch, completes; count equals uninterrupted run | ✓ | ✓ crash mid-scan | ✓ SIGKILL batch | — | waived¹ | waived² |
| 2 | Crash mid-save: partial rows cleared, no duplicates, single author | ✓ | ✓ | (covered by 1) | `SaveResults/ChunkSeqContinuity` (exists) | waived¹ | waived² |
| 3 | Deposed executor: next write fenced, aborts, no terminal write over SUCCESSFUL | ✓ (exists: `HeartbeatFencingAborts`) | ✓ config-skew | — | `Epoch/FencedWrites` (exists) | waived¹ | waived² |
| 4 | Attempt cap: stale claims at cap → FAILED with distinct message; a release-then-claim does not count | ✓ | ✓ synthesised stale row at `stale_claims` = cap − 1 | — | `Claim/StaleClaimCounted`, `Release/ClaimNotCounted` | waived¹ | waived² |
| 5 | Shutdown releases (running and queued); peer claims within one heartbeat interval with `staleAfter` = 1h; no FAILED written by the departing node | ✓ (`Release` called, no failure write on any path, queued job exits at top) | ✓ | — | `Release/*` | waived¹ | waived² |
| 6 | Single-node restart: new App reclaims at startup sweep (release + immediate sweep) | ✓ (first sweep before first tick) | ✓ | — | — | waived¹ | waived² |
| 7 | Reclaim with zero headroom claims nothing; enqueue race → `Release`; `ClearResults` error → `Release` | ✓ | — | — | — | — | — |
| 7a | Self-reclaim: registry handle replaced, old executor's deregister does not evict the new handle | ✓ | — | — | — | — | — |
| 7b | Sweep goroutine stopped and awaited on `Shutdown` and `Close`; no sweep after `Close` | ✓ | — | — | — | — | — |
| 8 | Self-executing store: never claimed, never released | ✓ (exists, adapted) | — | — | — | — | — |
| 9 | Cancel during handoff: claimant never runs it / running claimant stops | ✓ | ✓ (extend cross-node cancel test) | — | `Claim/ReleasedNotTerminal` | waived¹ | waived² |
| 10 | Release contract per backend | — | — | — | `Release/*` (six subtests) | n/a | n/a |
| 11 | Config validation and env binding for the new var | ✓ | — | — | — | — | — |
| 12 | Panic in executor still FAILs immediately (not reclaimed) | ✓ (exists) | ✓ (exists) | — | — | — | — |
| 13 | Postgres save-chunk transaction sets the transaction-local idle timeout | reviewed, TDD waiver (non-functional; needs a killed socket) | — | (SIGKILL test exercises the path incidentally) | — | — | — |

¹ Waived: a parity scenario runs over HTTP against a server binary it cannot
kill, pause, or restart, so no orphan can be produced there. Backend
consistency of the store surface is guaranteed by spitest on every backend,
and the engine orchestration is backend-agnostic code exercised on postgres.
² Waived: no gRPC envelope or code changes; the submit path is shared.

Existing tests that encode claim-then-FAIL (`TestFailStaleJobs_*`,
`TestE2E_AsyncSearch_StaleJobReaper_FailsOrphan`,
`TestE2E_AsyncSearch_ShutdownDrain_FailsInFlightJob`) are rewritten to the
new disposition, not kept alongside it.

## 6. Documentation and parity

- `docs/cloud-parity/async-job-node-failure-resilience.md`: the contract
  Cloud mirrors — a RUNNING job survives loss of its executor node and
  completes with a singly-authored result set; graceful node departure
  hands off promptly; a job is FAILED only after a bounded number of
  executions; clients observe nothing but a longer RUNNING span. The
  FAILED status after the cap is contractual; the message text is not.
- `CHANGELOG.md` (cyoda-go): `### Changed` for the disposition, `### Added`
  for the env var; SPI `CHANGELOG.md` `### Breaking` for `Release`.
- `COMPATIBILITY.md` wave entry (pin, `Release`, consumer obligation).
- Help: `config.md` (new row, two amended rows), `search.md` (orphan
  handling narrative), `docs/ARCHITECTURE.md` §4.6.

## 7. Out of scope

- Owner/node identity on the job record. Not needed: release + immediate
  sweep covers the restart case, and staleness covers everything else.
- A hung-but-heartbeating executor is never reclaimed: liveness is not
  progress. The same residual the #472 spec flags for the commercial
  backend; a progress-based bound is a separate design.
- Reserving pool capacity before claiming (would remove the D5 race
  entirely). The race is narrow, costs one attempt, and the released job is
  picked up promptly; revisit if the cap is ever observed firing for
  capacity reasons.
- Cross-node forwarding of async status reads. Reads are served from the
  shared store on every node already.
- The commercial backend's liveness-driven recovery gap (their side, per
  #472 §4.3).
