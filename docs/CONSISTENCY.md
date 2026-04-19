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
This matches the semantic that Oracle's `SERIALIZABLE` mode (snapshot
isolation with commit-time read-set validation) delivers. It is weaker
than PostgreSQL's native `SERIALIZABLE` (Cahill SSI with
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
| **sqlite** | Database-level write lock (single writer at the engine level) | Same SI+FCW engine code ported from memory; SQLite is the durability layer only | **SI+FCW** | per-entity |
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
  rationale for porting the memory plugin's SI+FCW engine into sqlite.

The commercial Cassandra plugin's own design document (shipped with the
proprietary binary) captures the same semantic contract with the same
§5 operational rule, verbatim.

---

## Appendix A: Worked example — the Doctor On-Call Roster

The Doctor On-Call Roster is the classic write-skew teaching example. It
is instructive for cyoda because it shows, concretely, how the
state-machine discipline turns a predicate anti-dependency (which SI+FCW
does not catch) into an entity-level read/write conflict (which it does).

### A.1 The invariant and the naive failure

A hospital has an on-call roster. The invariant for this appendix:

> **At most `N` doctors may be ON_DUTY at any one time.**

(The symmetric "at least one must be ON_DUTY" works the same way;
A.6 addresses it briefly.)

Naive model: a `Doctor` entity with two states, `OFF_DUTY` and `ON_DUTY`.
The promotion transition `OFF_DUTY → ON_DUTY` carries a criterion that
counts current ON_DUTY doctors and rejects if the cap would be exceeded.

```text
transition PROMOTE:
  from:      OFF_DUTY
  to:        ON_DUTY
  criterion: Count(Doctor where state = ON_DUTY) < N
```

Two concurrent transactions, each promoting a different doctor, both
start before either commits:

```text
Initial: Alice, Bob ON_DUTY (count = 2). Cap N = 3. Carol, Dave OFF_DUTY.

T1: promote Carol to ON_DUTY
    Count(ON_DUTY) = 2 at snapshot → 2 < 3 → criterion passes
    write Carol @ state = ON_DUTY

T2: promote Dave to ON_DUTY
    Count(ON_DUTY) = 2 at snapshot → 2 < 3 → criterion passes
    write Dave @ state = ON_DUTY

T1 commits. T2 commits. Final: 4 ON_DUTY. Invariant violated.
```

This is the phantom case from §7.3, expressed at the workflow level.

### A.2 Why SI+FCW does not catch it out of the box

Under cyoda's contract (§1), FCW triggers when a transaction's read-set
intersects a concurrent committer's write-set. In the naive design:

- T1's read-set contains `{Alice, Bob}` (the ON_DUTY doctors Count
  would surface) — but the postgres plugin's `Count` does not populate
  the read-set at all: it is an aggregate with no per-row identity, and
  `plugins/postgres/entity_store.go:376` documents that exclusion
  explicitly.
- T1's write-set contains `{Carol}`.
- T2's read-set (if populated) would be the same `{Alice, Bob}`; its
  write-set is `{Dave}`.

Read-sets and write-sets are entity-identity-disjoint across T1 and T2.
Nothing for FCW to latch onto. Both commit. The predicate "how many
ON_DUTY doctors exist" is not an entity — it is a count over a range —
and the snapshot neither of T1 nor of T2 saw the other's addition
because the addition didn't exist at their respective `Begin`.

### A.3 The workflow fence: `PENDING_ON_DUTY`

Introduce a third state, `PENDING_ON_DUTY`, and make the workflow
enforce two invariants:

1. **All admissions go through `PENDING_ON_DUTY` first.** A `Doctor`
   entity can only enter `ON_DUTY` from `PENDING_ON_DUTY`, never
   directly from `OFF_DUTY` or from creation. Enforced by the FSM: no
   transition `OFF_DUTY → ON_DUTY` exists; there is only
   `OFF_DUTY → PENDING_ON_DUTY` and `PENDING_ON_DUTY → ON_DUTY`.
2. **The promotion processor reads every peer candidate by entity, not
   by aggregate.** The `PENDING_ON_DUTY → ON_DUTY` transition's
   processor calls `entityStore.GetAll(Doctor)` (or an equivalent that
   returns individual entities) and filters in-memory for states
   `ON_DUTY` and `PENDING_ON_DUTY`. It does **not** call
   `Count`/`CountByState`.

The model:

```text
states:      OFF_DUTY, PENDING_ON_DUTY, ON_DUTY
transitions: OFF_DUTY        → PENDING_ON_DUTY   (REQUEST_DUTY)
             PENDING_ON_DUTY → ON_DUTY           (PROMOTE)
             PENDING_ON_DUTY → OFF_DUTY          (WITHDRAW)
             ON_DUTY         → OFF_DUTY          (STEP_DOWN)

PROMOTE processor (pseudocode):
  all := entityStore.GetAll(Doctor)              // populates read-set
  onDuty   := filter(all, state = ON_DUTY)
  pending  := filter(all, state = PENDING_ON_DUTY)
  if len(onDuty) >= N: return error("cap reached")
  // Optional ordering rule to pick a winner deterministically on retry:
  // require self.id == min(pending.id) or similar.
  self.state = ON_DUTY
```

The critical step is `GetAll(Doctor)`. On the postgres plugin that call
walks the entities table and, per
`plugins/postgres/entity_store.go:232-236`, calls `recordReadIfInTx` for
every returned row. Every current `Doctor` — whatever its state —
enters the transaction's read-set with its observed version.

### A.4 Why this is a fence — step by step

Now replay the scenario. Carol and Dave start in `PENDING_ON_DUTY`, not
`OFF_DUTY`.

```text
Initial: Alice, Bob ON_DUTY. Carol, Dave PENDING_ON_DUTY. Cap N = 3.

T1: PROMOTE Carol
    GetAll → {Alice@v, Bob@v, Carol@v, Dave@v} all enter read-set
    onDuty = {Alice, Bob}; pending = {Carol, Dave}
    len(onDuty)=2 < 3 → criterion passes
    write Carol @ state=ON_DUTY  (Carol promoted from read-set to write-set)
    T1 commit snapshot: read-set = {Alice, Bob, Dave}, write-set = {Carol}

T2: PROMOTE Dave
    GetAll → {Alice@v, Bob@v, Carol@v, Dave@v} all enter read-set
    onDuty = {Alice, Bob}; pending = {Carol, Dave}
    len(onDuty)=2 < 3 → criterion passes
    write Dave @ state=ON_DUTY
    T2 commit snapshot: read-set = {Alice, Bob, Carol}, write-set = {Dave}
```

Compare read-sets to concurrent write-sets:

- `T1.readSet ∩ T2.writeSet = {Alice,Bob,Dave} ∩ {Dave} = {Dave}` — non-empty.
- `T2.readSet ∩ T1.writeSet = {Alice,Bob,Carol} ∩ {Carol} = {Carol}` — non-empty.

Whichever commits first wins; the other's `ValidateReadSet` finds the
peer it observed has been modified and returns `spi.ErrConflict`. FCW
has transformed what was a predicate-level anti-dependency into a
concrete entity-level read/write conflict it can see.

The mechanism is exactly what `plugins/postgres/txstate.go:116-129`
describes: at commit, every entity in the read-set is checked against
its latest committed version; any mismatch aborts.

### A.5 What the two invariants are buying

Each invariant plugs a specific leak.

**Invariant 1 (all admissions through `PENDING_ON_DUTY`)** exists
because a blind direct insert in `ON_DUTY` — `create Doctor with
state=ON_DUTY` — is a *new* entity. It does not exist at any concurrent
transaction's snapshot, so it cannot enter their read-sets. Two
concurrent blind inserts, each reading `Count(ON_DUTY) < N`, both
commit. The `PENDING_ON_DUTY` pre-registration makes every candidate a
discoverable entity in the transaction system *before* its promotion
becomes visible, so concurrent promotion transactions see each other's
candidates as peers in their read-sets.

**Invariant 2 (entity-level read, not aggregate)** exists because `Count`
and `CountByState` do not populate the read-set. An author who writes
`if CountByState(Doctor, [ON_DUTY]).sum() < N` inside a criterion gets
the exact naive-failure mode from A.1 even with `PENDING_ON_DUTY` in
the model. `GetAll`-and-filter preserves the per-entity identity that
FCW needs.

### A.6 The symmetric case: minimum-cap

The invariant "at least one doctor must be `ON_DUTY`" uses the same
structure in reverse. Introduce a `STEPPING_DOWN` state. The transition
`STEPPING_DOWN → OFF_DUTY` carries a processor that reads all doctors,
confirms at least one remaining would be in `ON_DUTY` or
`STEPPING_DOWN` but committing their own step-down, and writes the
self state.

Two doctors concurrently stepping down from `ON_DUTY` each capture the
other in the read-set (peer is `ON_DUTY`), each writes their own
entity. One commits; the other aborts on read-set validation because
the peer's `ON_DUTY → STEPPING_DOWN` or `STEPPING_DOWN → OFF_DUTY`
write landed first. The user retries, re-evaluates the criterion, and
now sees correctly that dropping below 1 would violate the invariant.

The `STEPPING_DOWN` intermediate state is the symmetric mirror of
`PENDING_ON_DUTY`: it serialises the conflict through a read-before-write
point where peer entities are observable.

### A.7 Residual phantoms the fence does NOT close

The fence is only as strong as invariants 1 and 2. Specific residuals:

- **Creation that bypasses `PENDING_ON_DUTY`.** If the API surface
  allows a doctor to be created directly with `state=ON_DUTY` (whether
  via admin tooling, import, or a badly modelled FSM), the pre-existing
  promotion transactions cannot see that new entity until it commits,
  and the fence is bypassed. The FSM must prohibit this transition;
  import and administrative paths must too.
- **Workflow author calls `CountByState` instead of `GetAll`.** Silent
  bypass — the criterion passes, no read-set is populated. Enforceable
  by code review and the §5 operational rule.
- **Criterion reads a subset of candidates.** If the processor filters
  the `GetAll` results before recording reads (e.g., by using a
  non-transactional pre-fetch), the read-set shrinks and peers outside
  the filter fall through. Always read then filter; never filter then
  read.

### A.8 On the "workflow fence" framing

The draft article's framing — that a well-modelled workflow "fences"
against write-skew without needing Cahill SSI — is correct **for
cyoda's isolation model** provided the two invariants in A.3 hold. The
mechanics:

- Workflows materialise peer candidates as identifiable entities
  (invariant 1).
- Workflow processors read those peers by entity, not by aggregate
  (invariant 2).
- Entity-level reads populate the read-set; FCW at commit enforces
  entity-level conflicts.
- The result is the same serialisable outcome Cahill SSI would give,
  but obtained through application-layer structure rather than
  b-tree-page-granular engine-level dependency tracking.

This is not a universal claim about workflows and databases — it is a
specific claim about what cyoda's SI+FCW contract delivers when the
workflow author observes §5 and follows the pattern in this appendix.
Workloads that cannot be modelled this way (ad-hoc predicate analytics
over uncoordinated entity sets, for instance) should use one of the
alternatives in §5.

The pattern generalises. Any invariant of the form "at most N entities
of type X in state Y" or "at least M entities of type X in state Y"
admits an analogous fence: introduce a pending/stepping intermediate
state for the rate-limited transition, and read all peer entities in
the processor. The cap check then runs against a materialised peer set
inside the transaction, and FCW converts concurrent violations into
retryable conflicts.
