# Cyoda-Go Transactional Consistency

**Version:** 1.0
**Date:** 2026-04-18
**Status:** Canonical reference for cyoda-go's isolation and consistency semantics.

This document is the source of truth for what cyoda-go guarantees, what it
doesn't, and what operational rules apply to workflow authors. ARCHITECTURE.md
§3 (Transaction Model) and PRD.md §4 (Transaction Model) point here for depth.

---

## 1. The contract at a glance

Every cyoda-go storage plugin delivers the same semantic guarantee:

> **Snapshot Isolation with First-Committer-Wins on entity-level conflicts.**

- Reads within a transaction see a consistent snapshot taken at `Begin`.
- Writes within a transaction are visible to its own subsequent reads
  (read-your-own-writes).
- At commit time, if any entity in the transaction's read-set has been
  modified by a concurrent transaction that committed first, the
  transaction aborts with `spi.ErrConflict`. The first transaction to
  commit wins; losers can retry.
- Write-write conflicts on the same entity are caught by the same
  mechanism (and, on the postgres plugin, additionally by row-level tuple
  locks that raise `40001` at DML time before the commit phase even runs).

The guarantee is **entity-granular**: it is expressed in terms of entity
identities, not row identities, predicate ranges, or table partitions.
All four plugins (memory, sqlite, postgres, and the commercial cassandra
plugin) implement this same contract against very different underlying
engines.

## 2. What this contract catches

All three anomalies classically prevented by Snapshot Isolation:

- **Dirty read.** A transaction never observes another transaction's
  uncommitted writes. Cyoda transactions take a consistent snapshot at
  `Begin` and see only that snapshot plus their own writes.
- **Non-repeatable read.** Re-reading an entity within the same transaction
  always returns the same value. A concurrent committer's update is
  invisible to us until we commit (at which point we either succeed, if our
  read-set is still valid, or abort on conflict).
- **Lost update.** Two transactions reading the same entity, both updating
  it, both committing: exactly one commits, the other aborts. The "last
  committer wins silently" anomaly of READ COMMITTED cannot occur.

Plus the conflict class SI+FCW adds on top of plain SI:

- **Write-write conflicts on the same entity.** Even if both transactions
  read the same initial version and their writes do not depend on each
  other's, only the first to commit succeeds.
- **Write-after-read conflicts on the same entity.** If transaction T1
  reads entity `E` and T2 concurrently writes `E`, T1 cannot commit after
  T2 has committed — T1's read-set validation fails.

## 3. What this contract does NOT catch

**Phantom anomalies from predicate reads.** This is the only anomaly class
that cyoda's SI+FCW does not prevent, and it deserves a precise statement
because workflow authors must understand it.

Consider:

```text
T1: search("status = ACTIVE")           returns [A, B, C]
T1: insert entity X (also ACTIVE)
T1: commit

T2: search("status = ACTIVE")           returns [A, B, C]    (T2 started
                                                              before T1 commit)
T2: insert entity Y (also ACTIVE)
T2: commit
```

Both transactions see `[A, B, C]`, both add one matching entity to the
ACTIVE set, both commit successfully. Neither's read-set included the
other's new entity (how could it? the entity didn't exist at snapshot
time, and a predicate range is not something the read-set can capture
without full predicate locking). No conflict is detected.

In isolation-level terms: cyoda provides **Snapshot Isolation with
First-Committer-Wins on entity-level conflicts** — not full Serializability.
This matches the semantic Oracle's `SERIALIZABLE`, SQL Server's `SNAPSHOT`,
and MySQL InnoDB's `REPEATABLE READ` with commit-time validation deliver.
It is weaker than PostgreSQL's native `SERIALIZABLE` (Cahill SSI with
rw-antidependency tracking) on precisely this one anomaly class.

The cyoda postgres plugin deliberately runs at `REPEATABLE READ` and
layers first-committer-wins on top, rather than using PostgreSQL's native
`SERIALIZABLE`. The design rationale is captured in
`docs/superpowers/specs/2026-04-15-postgres-si-first-committer-wins-design.md`
— PostgreSQL's SSI uses b-tree *page* granularity for its dependency
tracking, producing false-positive `40001` aborts on concurrent writes to
disjoint rows that happen to share a page. The FCW implementation gives
entity-row granularity across all plugins and eliminates those false
positives, at the cost of not catching predicate-phantom anomalies. This
is an accepted trade-off captured in cyoda's published semantic contract.

## 4. Why the transactional umbrella doesn't fix this by itself

Every transaction in cyoda is started by an entity create/update at the
API surface — `BeginTx` is called in exactly five places, all inside
`internal/domain/entity/service.go` (create, delete, delete-all, batch
create, XML import). All cross-entity CRUD performed by workflow
processors during transition execution happens inside that enclosing
transaction.

This "transactional umbrella" bounds *when* a transaction exists and
*what work falls under a single commit point*. But it does not prevent
phantom-driven write-skew, because two concurrent transactions anchored
on *different* entities can still both perform the same predicate search
and both insert matching entities:

```text
T1 (anchored on order #42):
  update order #42
  search("status = ACTIVE AND type = premium")    returns 3 matches
  insert subordinate entity based on that count
  commit

T2 (anchored on order #43):
  update order #43
  search("status = ACTIVE AND type = premium")    returns 3 matches
  insert subordinate entity based on that count
  commit
```

Different anchors, disjoint write-sets on the subordinates, both count
results are "3" at their respective snapshot times, both commit
successfully. Five matching entities now exist although both transactions
thought there would be four.

The umbrella limits the scope of a transaction; it doesn't eliminate the
phantom class.

## 5. Operational rule: don't count inside a transaction

The single canonical rule that workflow authors must observe:

> **Workflow criterion and processor implementations must not perform a
> search within a transactional workflow step and branch on the result
> count or set completeness.**

Code patterns like `search(predicate).count() < threshold` or
`if !search(predicate).any() { ... }` inside a transition criterion or a
processor are susceptible to phantom anomalies. Entity-level reads and
writes (operating on a known entity ID, not a predicate range) are safe.

If your business logic requires a count-based invariant, there are three
robust alternatives:

**(a) Promote the count to a materialised counter entity.** Instead of
searching, read and update a dedicated counter entity by its known ID.
Counter reads land in the read-set; counter writes land in the write-set;
FCW handles contention naturally. Two concurrent transactions racing on
the same counter will serialise via first-committer-wins.

**(b) Encode the invariant as a state-machine precondition.** If the
invariant is "at most N entities of type X can be in state Y at once",
express that as a transition criterion on the individual entity's
state-machine rather than as a global count check. The criterion
evaluates entity-local reads (safe), and the transition commit's FCW
validation handles concurrent contenders.

**(c) Add a post-commit reconciliation step.** If the invariant can be
violated temporarily and a reaper/reconciler pass can detect and correct
it, model that explicitly. This is often the right shape for invariants
that are emergent rather than point-in-time — e.g., "no more than N
active sessions per user" where the correction is to close the oldest.

Workflow authors who follow this rule get effectively-serialisable
behaviour for the transaction classes they write. The isolation level is
still SI+FCW underneath; the rule keeps their workloads inside the
anomaly-free region of it.

## 6. Per-plugin implementation

All plugins honour the same contract. Implementation strategy varies.

| Plugin | Engine-level mechanism | Application-layer validation | Effective guarantee | Conflict granularity |
|---|---|---|---|---|
| **memory** | n/a — all in-process Go | Committed-log + per-transaction read/write-set tracking; first-committer-wins at commit | **SI+FCW** | per-entity |
| **sqlite** | Database-level write lock (single writer at the engine level) | Same SSI-engine code ported from memory; SQLite is the durability layer only | **SI+FCW** | per-entity |
| **postgres** | `REPEATABLE READ` (snapshot) + row-level tuple locks on entity tables | Entity-keyed read-set validation at commit; `40001` (`could not serialize access`) and `40P01` (`deadlock_detected`) mapped to `spi.ErrConflict` with bounded retry | **SI+FCW** | per-entity (via tuple locks on 1-to-1 `entities` table) |
| **cassandra** (commercial) | *(proprietary)* | *(plugin-internal)* | **SI+FCW** | per-entity |

Entity tables on the postgres plugin are keyed by `(tenant_id, entity_id)`
for `entities` and by `(tenant_id, entity_id, version)` for
`entity_versions`. Because `entities` is 1-to-1 with logical entities,
PostgreSQL's tuple-level row lock on an `entities` row is semantically
equivalent to an entity-level lock.

The application-layer read-set in `plugins/postgres/txstate.go` is keyed
by entity ID (`readSet map[string]int64`, value = version observed).
Validation at commit compares each entity's expected version to the
latest committed version via a batched `SELECT ... FOR SHARE` over the
read-set rows. Mismatches raise `spi.ErrConflict`; PostgreSQL's own
`40001` on the `FOR SHARE` is a second, redundant guard for the same
condition. Write-write conflicts are caught earlier, at the DML statement
that tries to `UPDATE`/`INSERT` the entity row, by PostgreSQL's implicit
tuple-exclusive lock.

## 7. Worked scenarios

### 7.1 Concurrent updates to the same entity — FCW serialises them

```text
Initial: entity E version 1.

T1: read E @ v1, update E (write-set captures pre-write version 1)
T2: read E @ v1, update E (write-set captures pre-write version 1)

T1 commits first.
- Memory/sqlite: writes E@v2 to committedLog, prunes older versions.
- Postgres: DML UPDATE takes tuple-exclusive lock; commits E@v2.

T2 tries to commit.
- Memory/sqlite: read-set validation finds E moved from v1 to v2 →
  ErrConflict.
- Postgres: either (a) T2's UPDATE raised 40001 at DML time because T1's
  commit had already landed, or (b) commit-phase `FOR SHARE` raises 40001
  on the stale snapshot. Either way, ErrConflict.

Result: T1 succeeds, T2 aborts. T2 retries with a fresh snapshot.
```

Safe. All plugins handle this identically from the caller's perspective.

### 7.2 Read one entity, write another — FCW catches cross-entity conflict

```text
Initial: entity A v1, entity B v1.

T1: read A @ v1 (captures A:v1 in read-set),
    update B based on A's data (captures B's pre-write version in write-set)
T2: update A (captures A's pre-write version in write-set)

T2 commits first → A@v2.

T1 tries to commit.
- Read-set validation: A was read at v1, latest committed is v2 → ErrConflict.

Result: T1 aborts. T1 retries, observes A@v2, recomputes B's update
based on the new A data.
```

Safe. The cross-entity dependency `B depends on A` is captured in T1's
read-set explicitly (T1 read A), so FCW validates it at commit.

### 7.3 Predicate-based count — NOT safe (the phantom case)

```text
Initial: 3 entities matching "status = ACTIVE".

T1: search("status = ACTIVE") → [A, B, C]; "3 < 5, ok to add another"
    insert X with status = ACTIVE
T2: search("status = ACTIVE") → [A, B, C]; "3 < 5, ok to add another"
    insert Y with status = ACTIVE

Neither transaction's read-set contains the other's new entity (X and Y
didn't exist at snapshot time). The commit validation cannot detect
anything wrong.

T1 commits. T2 commits. Now there are 5 ACTIVE entities, though each
transaction believed there would be 4.
```

**This is the anomaly class the operational rule in §5 exists to prevent.**
The workaround is any of §5 (a), (b), or (c) — most commonly, promote
the count to a counter entity the workflow can read/write by ID.

### 7.4 Workflow-encoded invariant — safe by design

State-machine transitions naturally funnel the "should I do X?" decision
through entity-local criteria that evaluate against the anchor entity's
own state. A transition's criterion is evaluated within the transaction
that performs the transition, and the criterion's reads enter the
transaction's read-set.

Consider an Order entity whose "SHIP" transition has the criterion
`order.approvals_received >= 2`. If two concurrent transactions both try
to fire SHIP on the same order:

- Both read `order.approvals_received` = 2 and the criterion passes.
- Both capture `order@current-version` in their read-set.
- Both attempt to write the new state.
- First committer wins via FCW on the `order` entity. Second aborts with
  `ErrConflict` and retries; on retry, the order may already be in state
  SHIPPED and the transition no longer applicable.

Because the invariant is expressed as a criterion on the entity being
transitioned, FCW on that entity is the guard. No phantom can slip past.

## 8. Multi-node routing preserves the contract

cyoda-go runs as a cluster (3–10 stateless nodes behind a load balancer
for the postgres plugin; other plugins have their own topologies). The
per-node `TransactionManager` holds per-transaction state (read-set,
write-set, postgres `pgx.Tx` handle) in the process memory of the node
that called `Begin`. Subsequent requests inside the same transaction
must route back to that node — enforced by the cluster-mode dispatch
layer (signed transaction tokens + HMAC-authenticated forwarding).

The isolation contract is therefore preserved end-to-end: an API caller
interacting with transaction `T` always lands on the same node for the
duration of `T`; the `Commit` that finally applies FCW validation
executes on the node that owns `T`. Cluster membership changes (node
failure, scale-out) may cause in-flight transactions on the affected
node to abort, but they cannot cause a committed transaction to be
observed out of order or a concurrent transaction to slip past FCW
validation.

The memory and sqlite plugins are single-process: multi-node deployment
is not supported (memory has no shared store; sqlite holds an exclusive
file lock for the process lifetime). The postgres plugin is designed for
multi-node; the commercial cassandra plugin is designed for
multi-cluster.

## 9. Isolation-level taxonomy for orientation

For readers familiar with the standard isolation-level vocabulary:

| Anomaly | READ COMMITTED | REPEATABLE READ (SI) | cyoda SI+FCW | Cahill SSI / PostgreSQL `SERIALIZABLE` |
|---|---|---|---|---|
| Dirty read | prevented | prevented | prevented | prevented |
| Non-repeatable read | not prevented | prevented | prevented | prevented |
| Phantom read | not prevented | partially prevented | partially prevented | prevented |
| Lost update | not prevented | depends on engine | prevented | prevented |
| Write-skew | not prevented | not prevented | not prevented for predicate reads; prevented for entity-level reads | prevented |

**cyoda-go SI+FCW sits between plain SI and full Serializability.** It
catches all the anomalies SI catches plus lost-update plus entity-level
write-skew. The one remaining class is predicate-based write-skew, which
the operational rule in §5 keeps workloads out of.

PostgreSQL's native `SERIALIZABLE` catches predicate-based write-skew
too, but at the cost of b-tree-page-granular dependency tracking that
produces false-positive `40001` aborts on concurrent writes to disjoint
rows that happen to share a page. Cyoda explicitly chose to not use that
mode — see the postgres-si-first-committer-wins design spec for the
trade-off analysis.

## 10. Practical guidance for workflow authors

A short checklist:

1. **Express invariants as transition criteria on the entity being
   transitioned.** Those criteria's reads are captured in the read-set;
   FCW guards them.
2. **If you need a count, keep the counter as an entity.** A "counters"
   entity with well-known IDs you read/write by name is indistinguishable
   from any other entity to the isolation layer — fully FCW-protected.
3. **Never branch on `search(predicate).count()` inside a transactional
   workflow step.** If you catch yourself writing this, step back and
   apply (a) or (b) from §5.
4. **Expect `spi.ErrConflict`; don't wrap it as an internal error.**
   Conflicts are a normal signal that a concurrent transaction beat yours
   to the commit. The retry loop lives at the API boundary (the HTTP
   handler and gRPC service translate `ErrConflict` to `409 Conflict`
   with `retryable: true`); internal code should let it bubble.
5. **Keep transactions short.** Long-running transactions hold a larger
   read-set, widening the window in which a concurrent committer can
   invalidate it. If a workflow step needs to do slow work (e.g. an
   external HTTP call), prefer `ASYNC_NEW_TX` so the slow part runs in a
   separate transaction with its own short lifespan.
6. **Don't assume Serializable-class isolation.** If your use case truly
   needs phantom protection (e.g. compliance-driven "no more than N
   entities in state X per tenant, ever"), either materialise the count
   or add a reconciliation step.

## 11. References

- `docs/ARCHITECTURE.md` §3 — Transaction Model overview.
- `docs/PRD.md` §4 — Transaction Model at the product level.
- `docs/plugins/IN_MEMORY.md`, `SQLITE.md`, `POSTGRES.md` — per-plugin
  implementation specifics.
- `docs/superpowers/specs/2026-04-15-postgres-si-first-committer-wins-design.md`
  — rationale for postgres plugin running at `REPEATABLE READ` instead
  of native `SERIALIZABLE`.
- `docs/superpowers/specs/2026-04-15-sqlite-storage-plugin-design.md` —
  rationale for porting the memory plugin's SSI engine into sqlite.

The commercial Cassandra plugin's own design document (shipped with the
proprietary binary) captures the same semantic contract with the same
§5 operational rule, verbatim.
