# Async search job survives its executor node's loss — Cloud twin-alignment spec

This document is the contract Cyoda Cloud implements to stay aligned with
cyoda-go's async-search node-failure resilience. cyoda-go is the
authoritative implementation.

No wire, endpoint, or error-code changes accompany this contract. A client
observes only a longer `RUNNING` span than before — and, past the attempt
cap, the same `FAILED` status it could already receive.

## The contract

**A `RUNNING` async-search job survives the loss of its executor node and
completes with a singly-authored result set.** cyoda-go heartbeats a
running job on `CYODA_SEARCH_JOB_HEARTBEAT_INTERVAL`; when the owning node
stops heartbeating, another node claims the job (bumping an `Epoch` fence
so a deposed executor that later recovers cannot write into a result set
another node has since taken over), clears the dead executor's partial
results, and re-runs the job on a live node as-at its originally stored
`pointInTime`. The job completes `SUCCESSFUL`, authored entirely by the
node that finished it — never a merge of two executors' partial output.

**A node departing gracefully hands its jobs off promptly, not after a
stale timeout.** A planned shutdown or rolling restart releases every
in-flight job immediately rather than leaving it to age past
`CYODA_SEARCH_JOB_STALE_AFTER`. A released job is picked up by a peer, or
by the same node on its own restart, well inside the crash-detection
window — cyoda-go's reclaim sweep runs on `CYODA_SEARCH_JOB_HEARTBEAT_INTERVAL`'s
ticker plus once at process startup, so a routine deploy or rolling
restart does not degrade to crash-level detection latency, and a release
never counts against the attempt cap below.

**A job is `FAILED` only after a bounded number of executions.** Losing an
executor and being re-run is not free forever: cyoda-go counts genuine
executor losses (never graceful releases) and fails the job once that
count reaches `CYODA_SEARCH_JOB_MAX_ATTEMPTS` (default 3), so a job whose
executor keeps crashing does not retry indefinitely. **The `FAILED` status
at the cap is contractual; the message text is not** — cyoda-go's persisted
message (`search abandoned: executor lost repeatedly`) is diagnostic, and
Cloud is free to use its own wording as long as the status and error
classification match.

**Clients observe nothing but a longer `RUNNING` span.** No new status
value, no new field on the job resource, no new error code. A poll of
`GET /api/search/async/{jobId}` during a reclaim sees the same `RUNNING`
it would have seen anyway; the only externally visible difference from
before this contract existed is that a node loss which used to end the job
`FAILED` now, in the common case, ends it `SUCCESSFUL` instead.

## What Cloud must mirror

1. A `RUNNING` job's fate must not depend on which physical executor
   happens to die — the cluster (or Cloud's equivalent unit of compute)
   completes it, not just the machine that started it.
2. A graceful departure (scale-down, deploy, drain) must hand work off on a
   short, bounded cadence — not the same cadence used to detect an
   unannounced crash. Treating a planned exit as an unannounced crash is a
   correctness gap here, not a simplification.
3. The number of executions a job may consume before being given up on
   must be bounded and enforced — an unbounded retry loop against a
   crash-looping executor is not an acceptable substitute for a cap.
4. The `FAILED` status Cloud returns at the cap must be the real, final
   `FAILED` a client already knows how to handle — not a new status, code,
   or shape.

## Unreachable on a self-executing backend — and that is the parity statement

A backend implementing `spi.SelfExecutingSearchStore` (the commercial,
Cassandra-backed backend) owns its own consumer/executor pipeline and its
own shard-recovery mechanism for surviving the loss of the node that was
running a job. cyoda-go's engine-side reclaim path — `Release`,
`ClaimStale`, `ClearResults`, `Heartbeat`-based staleness detection — exists
to give the same guarantee to backends that do not have their own recovery
underneath them (memory, sqlite, postgres). A self-executing backend MAY
no-op `Release`, `ClaimStale`, and `ClearResults` entirely: cyoda-go's
`SubmitAsync` and reclaim sweep already skip a self-executing store by type
assertion, exactly as it skips the engine's own execution goroutine for
one, so nothing in the engine depends on those calls doing anything for
such a backend.

That is a deliberate, documented consequence of owning your own execution
and recovery pipeline, not a missing implementation: what Cloud owes is the
*equivalent* guarantee — a job's shard is recovered and finishes
`SUCCESSFUL` on its own infrastructure's terms — not cyoda-go's specific
heartbeat/claim/epoch mechanism.

## Residual — shared, out of scope here

A hung-but-heartbeating executor is never reclaimed: heartbeating proves
liveness, not progress, and neither cyoda-go's engine-side reclaim nor a
commercial backend's shard recovery can distinguish a stuck scan that is
still ticking its heartbeat from one that is genuinely working. This is a
shared limitation of the whole approach, not a Cloud-parity gap to close
here, and is out of scope for this document.

## Backend support

Engine-executed backends (memory, sqlite, postgres) all implement the
reclaim path identically — it is engine behaviour
(`internal/domain/search.SearchService.ReclaimStaleJobs`), not plugin
behaviour, so there is no per-backend variation to reconcile among them.
Coverage is a mix of isolated single-backend e2e (crash/restart/cap/
shutdown-release/deposed-executor scenarios, and a SIGKILL-a-cluster-node
scenario asserting the survivor completes the job singly-authored) —
deliberately not the shared cross-backend parity suite, since node-failure
and shutdown timing are concurrency scenarios and those stay out of it.
