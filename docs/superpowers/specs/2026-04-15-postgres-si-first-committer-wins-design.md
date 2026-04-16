# Postgres plugin: SI + row-granular first-committer-wins

**Status:** Design, ready for implementation planning
**Issues:** [#18](https://github.com/Cyoda-platform/cyoda-go/issues/18) (structural fix), [#17](https://github.com/Cyoda-platform/cyoda-go/issues/17) (flake this obsoletes), [#35](https://github.com/Cyoda-platform/cyoda-go/issues/35) (non-entity-store coverage follow-up)
**Related:** [cyoda-go-cassandra#22](https://github.com/Cyoda-platform/cyoda-go-cassandra/issues/22) (sibling coverage issue on cassandra)

## Motivation

Postgres `SERIALIZABLE` (SSI) tracks read/write dependencies at b-tree **page** granularity, producing false-positive `40001` aborts when concurrent transactions write to disjoint rows that happen to share pages. This manifests as the flake in #17 and, more broadly, as unpredictable tail latency under concurrent entity writes within the same tenant.

Cyoda's published semantic is **Snapshot Isolation with first-committer-wins at entity-row granularity** — the same guarantee the cassandra plugin implements via per-transaction read-set tracking and commit-time version validation. The postgres plugin currently over-delivers (SSI strength) at a page-granular implementation that produces false positives. This design replaces the page-granular SSI implementation with a row-granular first-committer-wins implementation, matching the cassandra plugin exactly.

## Goals

1. **Behavioral parity with cyoda-go-cassandra** on entity operations: same commit semantics, same conflict detection boundaries, same `spi.ErrConflict` surface.
2. **Eliminate SSI page-level false positives.** The #17 flake and its class go away.
3. **No SPI changes.** `TransactionManager` and all `Store` interfaces stay identical. The fix is entirely inside `plugins/postgres`.
4. **Preserve snapshot isolation + read-your-own-writes** semantics for all tx participants.
5. **Preserve existing multi-node routing** via `Join`.

## Non-goals

1. **Non-entity-store coverage.** `ModelStore`, `KVStore`, `MessageStore`, `WorkflowStore`, `StateMachineAuditStore` remain at plain SI without read-set tracking. Identical coverage gap to cassandra; tracked as #35 for later resolution in lockstep with cassandra's #22.
2. **Advisory locks** on model lock/unlock/change-level or other invariant paths. Deferred to #35.
3. **Retry wrappers** for remaining 40001s. Deferred; can be layered later as defense-in-depth.
4. **Distributed read-set bookkeeping** across multiple postgres-connected processes without proxying. Current multi-node topology anchors a tx to its origin node; that stays.

## Design overview

The postgres plugin implements first-committer-wins by:

1. Running every tx at `REPEATABLE READ` (snapshot isolation) instead of `SERIALIZABLE` (SSI).
2. Tracking `(entity_id → expected_version)` for every entity read within the tx, in a plugin-local per-tx state map.
3. Tracking `(entity_id → pre-write version)` for every entity write within the tx.
4. At `Commit`, issuing one batched `SELECT ... FOR SHARE` over the union of read-set and write-set rows. Postgres's own RR-level concurrent-update detection raises `40001` if any of those rows was modified by a concurrent committer since our snapshot. The returned versions are compared to the expected versions; any mismatch → `spi.ErrConflict`. On success, row-level locks held until `pgxTx.Commit` protect the validate-then-commit window.

The entire mechanism lives inside the postgres plugin. No SPI changes.

## Why this preserves the published semantic

Cassandra's mechanism (for reference): every entity read captures `expected_version`; commit phase validates each via a coordinated version-check fan-out against the owning shard plus a `shard_commit_log` SSI check ("anyone committed a write to this entity's row after my snapshot HLC?"). Both the version check and the SSI check are **per-entity (row-level)**.

Postgres's native RR gives us both checks for free — but via **two distinct mechanisms** working in concert. Making the distinction explicit is important for reviewer intuition:

- **Write-write conflicts (writeSet vs concurrent writer)** are caught by postgres's **inherent tuple-level locks** from the DML statements themselves. When our tx's `INSERT` / `UPDATE` / `DELETE` runs against the entity row, postgres takes a row-exclusive lock and, under RR, raises `40001` immediately if another concurrent tx has already committed a write to that row. This protection is already in place before the commit phase runs.
- **Read-set staleness (readSet vs concurrent committer we never wrote against)** is what our commit-time validation catches. A tx that reads X at v=5, decides something based on it, and writes Y (not X) has no native DB mechanism surfacing the fact that X moved to v=6 under a concurrent committer. That's the case our `SELECT ... FOR SHARE` + version compare covers.

Put together:

- `SELECT id, version FROM entities WHERE id = ANY($1) AND tenant_id = $2 FOR SHARE` reads the **latest committed** row version, not the snapshot version. Comparing to `expected_version` catches the read-set staleness case.
- Under RR, if that row was modified by a concurrent committed tx after our snapshot, postgres raises `40001` automatically on the `FOR SHARE` itself (`could not serialize access due to concurrent update`). That's a second, redundant guard that mirrors the SSI-style "committed after snapshot" check at row granularity.
- `FOR SHARE` (not `FOR UPDATE`) is the right lock mode: we need to detect concurrent writers, not block concurrent readers. Two concurrent committing txs both issuing `FOR SHARE` on the same unchanged row succeed compatibly — exactly what we want; the first-committer-wins enforcement is still intact because any write-write overlap was already caught by the DML's tuple locks upstream.
- `FOR SHARE` holds the row locks until our tx commits or rolls back, protecting the validate → commit window from a fourth-party committing mid-window.

The coverage contract is explicit: this applies to entity reads/writes only. Non-entity stores (Model, KV, Message, Workflow, SMAudit) are untracked and operate at plain SI — identical to cassandra's data-store paths. #35 tracks the symmetric follow-up.

### Preconditions on the `entities` schema

1. **Unique constraint on `(tenant_id, id)`.** Required for insert-conflict correctness: two concurrent txs inserting the same entity ID must collide at DML time, not leak through the commit-time validator. The commit-time `SELECT ... FOR SHARE` would not detect a concurrent uncommitted insert on its own — the unique constraint is what surfaces it (as a duplicate-key error from the `INSERT`).
2. **Index usable by `WHERE tenant_id = $1 AND id = ANY($2)`.** The unique constraint above covers this if it is a btree index leading with `(tenant_id, id)`. Any commit with a non-trivial read-set runs this query; a missing or mis-ordered index would turn it into a seq scan per commit. Validate in the implementation PR.

## Data structures

New type inside `plugins/postgres`:

```go
// txState holds per-transaction bookkeeping for first-committer-wins
// validation. One instance per active tx, indexed by txID.
type txState struct {
    mu       sync.Mutex
    tenantID spi.TenantID
    readSet  map[string]int64  // entity_id → expected version at read time
    writeSet map[string]int64  // entity_id → pre-write version
    // savepoints stack: each entry snapshots readSet/writeSet at the
    // moment Savepoint() was called, for restore on RollbackToSavepoint().
    savepoints []savepointEntry
}

type savepointEntry struct {
    id       string
    readSet  map[string]int64
    writeSet map[string]int64
}
```

Stored in a parallel map on the `TransactionManager`:

```go
txStates    map[string]*txState
txStatesMu  sync.Mutex
```

The existing `txRegistry` (which maps `txID → pgx.Tx`) stays focused on pgx connection lookup; read/write-set state is separate.

## Store-layer changes

Every method on `EntityStore` that touches an entity row inside a tx context records into `txState`:

| Method | Action on txState |
|---|---|
| `Get(txCtx, id)` | `readSet[id] = entity.Meta.Version` (if not already present) |
| `GetAll(txCtx, ref)` | For each returned entity: `readSet[id] = version` |
| `Save(txCtx, entity)` | `writeSet[id] = previousVersion` (or `0` for INSERT) |
| `SaveAll(txCtx, iter)` | Per-entity: `writeSet[id] = previousVersion` |
| `CompareAndSave(txCtx, entity, ifMatch)` | `writeSet[id] = parsedIfMatch` |
| `Delete(txCtx, id)` | `writeSet[id] = previousVersion` (soft delete bumps version) |
| `GetAsAt(txCtx, id, t)` | **Not tracked** — point-in-time reads target an immutable historical version, not the live row; they don't participate in first-committer-wins. If application logic reads a historical version and then decides a live-row write, the decision is intentionally not validated at commit (the "live-row only" semantic). The implementation should carry a code comment stating this explicitly so a future contributor doesn't "fix" the omission by adding tracking. |
| `GetVersionHistory(txCtx, id)` | **Not tracked** — history reads are observational |
| `Count(txCtx, ref)` | **Not tracked** — coarse aggregate, no per-row identity to validate |

Non-tx reads (no `txCtx`) bypass bookkeeping entirely. A public HTTP GET on `/entities/{id}` reads the current snapshot without recording anything.

Helper: `plugins/postgres/txstate.go` exposes

```go
func recordRead(ctx context.Context, tm *TransactionManager, entityID string, version int64)
func recordWrite(ctx context.Context, tm *TransactionManager, entityID string, prevVersion int64)
```

called from the store methods. Both are no-ops when the context carries no tx.

## Transaction manager changes

### Begin
```go
pgxTx, err := tm.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}) // was: Serializable
```

Plus: allocate and register a fresh `*txState{tenantID: tenantID}` in `tm.txStates[txID]`. The tenantID is already resolved for RLS; cache it on the txState for use in the commit-time validation query.

### Savepoint / RollbackToSavepoint / ReleaseSavepoint

- `Savepoint` pushes a deep copy of the current `readSet` / `writeSet` onto `savepoints`, then executes the `SAVEPOINT` SQL as today.
- `RollbackToSavepoint` restores `readSet` / `writeSet` from the named savepoint entry and trims later entries, then executes `ROLLBACK TO SAVEPOINT` as today.
- `ReleaseSavepoint` drops the savepoint entry without touching the sets (work is kept), then executes `RELEASE SAVEPOINT` as today.

This mirrors how a RR-level save-restore model treats the read-set. It diverges from cassandra's "clear read-set on savepoint" semantic — this divergence is intentional because postgres tracks row visibility natively and the read-set in this plugin is purely commit-validation bookkeeping, not a linearization fence. Tests will cover the savepoint semantics explicitly.

### Commit

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

    // Collect all distinct entity IDs from read+write set, sorted.
    // Sorting makes FOR SHARE lock acquisition order deterministic across
    // concurrent committers, eliminating a class of deadlock (40P01) that
    // would otherwise surface as ErrConflict under contention. postgres
    // still may deadlock-abort in pathological cases — classifyError maps
    // 40P01 to ErrConflict — but sorted acquisition keeps that to the
    // genuinely unavoidable cases.
    ids := state.sortedUnionIDs()

    if len(ids) > 0 {
        // Single batched FOR SHARE over the union set, scoped to tenant
        // for defence-in-depth alongside RLS. Chunked to keep the ANY($2)
        // array within planner-friendly bounds (see validateInChunks).
        // FOR SHARE (not FOR UPDATE) is the right mode: we want to detect
        // concurrent committed writers, not block concurrent readers.
        // Two committing txs both issuing FOR SHARE on the same unchanged
        // row succeed compatibly; any write-write overlap between them
        // was already caught by tuple-level locks on the DML upstream.
        current, err := tm.validateInChunks(ctx, pgxTx, state.tenantID, ids)
        if err != nil {
            // 40001 here is exactly the signal we want — postgres caught
            // a concurrent committer since our snapshot. Map to ErrConflict.
            tm.cleanupTx(txID)
            _ = pgxTx.Rollback(context.Background())
            return classifyError(fmt.Errorf("Commit: validate: %w", err))
        }

        // Read-set check: every captured entity must still exist at the
        // captured version. Missing row = deleted by a concurrent
        // committer; version mismatch = updated by a concurrent committer.
        if err := state.validateReadSet(current); err != nil {
            tm.cleanupTx(txID)
            _ = pgxTx.Rollback(context.Background())
            return fmt.Errorf("%w: %w", spi.ErrConflict, err)
        }

        // Write-set check: every write target must still be at the
        // pre-write version we expected. (Inserts: 0 expected / absent.)
        if err := state.validateWriteSet(current); err != nil {
            tm.cleanupTx(txID)
            _ = pgxTx.Rollback(context.Background())
            return fmt.Errorf("%w: %w", spi.ErrConflict, err)
        }
    }

    // Existing: capture submit time, commit pgxTx, record submit time.
    // Map 40001 from the Commit itself (pure write-write conflict on
    // the same row) to ErrConflict as today.
    // ... (existing commit logic unchanged) ...

    tm.cleanupTx(txID)
    return nil
}
```

`validateReadSet` / `validateWriteSet` are straightforward map compares. Semantics:

- Read-set: captured version must equal current; missing current (deleted by concurrent committer) → conflict.
- Write-set: expected pre-write version must equal current; version `0` (new insert) requires the row to not yet exist in `current`.

`classifyError` already maps pgcode `40001` and `40P01` to `spi.ErrConflict`. We rely on it for errors raised from the validation query itself (postgres raising concurrent-update mid-SELECT-FOR-SHARE).

### Batch size guard

A single tx's read+write set is typically tens of entities, but pathological workloads (long workflows with re-entrant processors, bulk imports) may produce hundreds or thousands. Postgres handles `ANY($1::uuid[])` efficiently up to a few thousand elements, but planning cost grows and very large arrays degrade predictably. The validator chunks accordingly:

```go
const validateChunkSize = 1000  // conservative — well under planner thresholds.

func (tm *TransactionManager) validateInChunks(
    ctx context.Context, tx pgx.Tx, tenantID spi.TenantID, sortedIDs []string,
) (map[string]int64, error) {
    current := make(map[string]int64, len(sortedIDs))
    for i := 0; i < len(sortedIDs); i += validateChunkSize {
        end := i + validateChunkSize
        if end > len(sortedIDs) {
            end = len(sortedIDs)
        }
        chunk := sortedIDs[i:end]
        rows, err := tx.Query(ctx, `
            SELECT id, version
              FROM entities
             WHERE tenant_id = $1
               AND id = ANY($2)
             FOR SHARE
        `, tenantID, chunk)
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

Because `sortedIDs` is sorted and chunks are taken in order, lock acquisition order remains deterministic across chunks within one tx and across concurrent txs — the deadlock prevention property of sorted acquisition is preserved through chunking.

### Rollback

Same as today, plus `tm.cleanupTx(txID)` to drop the txState entry.

## Store-layer error handling

No new error types. Any query that raises `40001` (`serialization_failure`) or `40P01` (`deadlock_detected`) during normal tx operations — which should be rare under RR but possible — continues to map to `spi.ErrConflict` via the existing `classifyError`.

## Multi-node behavior

A tx is anchored to the cyoda-go node that called `Begin`. That node owns:

- The `pgx.Tx` connection (existing behavior).
- The `*txState` with read/write sets (new).

Other cyoda-go nodes that receive a CRUD request referencing this txID proxy it back to the origin node (existing routing via `Join`). All read-set/write-set bookkeeping happens on the origin. Single source of truth; no cross-node state synchronization needed.

Failure mode: if the origin node dies mid-tx, the pgx connection drops, the tx aborts server-side, and `txState` is lost with the process — same behavior as today. No regression.

## Test strategy

**New unit tests** in `plugins/postgres`:

1. `TestTxState_RecordRead` — read-set populated on `EntityStore.Get` inside a tx.
2. `TestTxState_RecordWrite` — write-set populated on `Save` / `CompareAndSave` / `Delete`.
3. `TestTxState_NonTxReadsNotTracked` — reads without `txCtx` do not populate any state.
4. `TestTxState_SavepointSnapshot` — `Savepoint` snapshots sets; `RollbackToSavepoint` restores; `ReleaseSavepoint` preserves.
5. `TestCommit_ReadSetConflict` — tx A reads entity X; concurrent tx B modifies X and commits; A's commit returns `ErrConflict`.
6. `TestCommit_WriteSetConflict` — two txs race an update on the same entity; first wins, second sees `ErrConflict`.
7. `TestCommit_DisjointRowsNoConflict` — the #17 scenario: 8 concurrent txs writing distinct UUIDs, all commit. (Regression guard.)
8. `TestCommit_InsertNoReadSetInterference` — pure INSERT of a new entity with no prior reads does not hit validation overhead.
9. `TestCommit_TenantScoped` — validation query is tenant-scoped; entities in other tenants cannot collide.
10. `TestCommit_BatchedQuery` — a tx touching N entities (N ≤ `validateChunkSize`) triggers exactly one SELECT at commit.
11. `TestCommit_DeletedEntityConflict` — tx A reads entity X; concurrent tx B (soft-)deletes X; A's commit detects the version bump and returns `ErrConflict`.
12. `TestCommit_LargeReadSet` — tx touching > `validateChunkSize` entities validates via multiple chunked SELECTs in sorted order; commit succeeds when no conflicts, and performance stays within a documented bound.
13. `TestCommit_SortedLockAcquisition` — two concurrent committers with overlapping read-sets in different insertion orders both issue their validation queries in the same sorted order; no deadlock-induced `40P01`.
14. `TestCommit_SavepointRollbackDropsReadSet` — tx reads X, takes savepoint, reads Y, rolls back to savepoint; Y is dropped from readSet; concurrent tx modifies Y; commit succeeds because Y is no longer validated.

**Updated conformance tests** in `internal/spitest`:

- `TestConformance/Entity/Concurrent/DifferentEntities` (the #17 flake case) now passes reliably on postgres. Issue #17 closes.
- Add a `TestConformance/Entity/Concurrent/SameEntity` scenario: two txs race an update on the same entity, expect exactly one success and one `ErrConflict` on both memory and postgres plugins. Cassandra behavior unchanged (it already passes).
- **Savepoint semantic note.** The postgres plugin preserves readSet across savepoint via snapshot/restore; the cassandra plugin clears readSet on savepoint. The two are *not* behaviorally identical on the edge case of "read inside savepoint → rollback → commit main tx": on postgres the read is validated, on cassandra it is not. The conformance harness documents this divergence explicitly (a `// DIVERGENCE:` comment on the relevant assertion) so a future contributor doesn't file it as a bug. If we later choose to converge the two plugins, that's a separate, scoped change.

**Parity verification:**

- Run full spitest conformance suite across memory / postgres / cassandra. All three plugins must produce identical conflict semantics on the concurrency scenarios.

## Migration / rollout

1. Land behind no feature flag — this is a bug fix (page-granular SSI was wrong-shaped, not a design we want to keep opt-in).
2. Pre-merge: run spitest suite 20× in a loop to confirm no SI-specific anomalies surface on production-shape workflows.
3. Pre-merge: run the full parity suite against all three plugins.
4. Close #17 (flake obsolete). Reference #35 in the PR description for follow-up visibility.

## Out of scope / future work

- **#35** — extend first-committer-wins to non-entity stores in postgres. To be addressed in coordination with cassandra's #22.
- **Advisory locks** on model lock/unlock/change-level and async-search status transitions. May land as part of #35's resolution.
- **Retry wrapper** (originally cyoda-go#3). Not load-bearing after this change; may still be useful as defense-in-depth for the legitimate write-write 40001s that surface cleanly under the new design.
- **Distributed read-set** across non-proxying multi-node deployments. Not on the current roadmap.
