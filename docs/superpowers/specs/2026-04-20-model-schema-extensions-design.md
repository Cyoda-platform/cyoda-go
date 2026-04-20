# Model Schema Extensions — Design

**Date:** 2026-04-20
**Branch:** `feat/model-schema-extensions`
**Status:** Draft, pending user approval before plan.

## 1. Problem

When a client runs concurrent entity updates on a model whose `ChangeLevel`
permits schema evolution, the Postgres plugin returns
`CONFLICT: transaction conflict — retry` on all-but-one update even though
the updates target distinct entities.

Root cause: `internal/domain/entity/handler.go:validateOrExtend` calls
`modelStore.Save(ctx, desc)` unconditionally when `desc.ChangeLevel != ""`,
inside the entity's `REPEATABLE READ` transaction. All concurrent updates
on the same model race to `UPDATE` the single `models` row; the first
commits, the rest receive `SQLSTATE 40001` which the plugin maps to
`spi.ErrConflict`.

The Memory and SQLite plugins do not surface the regression — their
`ModelStore.Save` implementations bypass the entity transaction (direct map
access / raw `*sql.DB` handle respectively).

## 2. Contract (new section for `docs/CONSISTENCY.md`)

Data operations require the model to be `LOCKED`. A locked model carries a
`ChangeLevel` that governs what additive schema evolution is permitted at
ingestion time.

Two invariants for additive model mutation:

1. **Non-interference.** An additive mutation of a model's schema must not
   cause a transaction conflict with concurrent data operations on the same
   model. The `SI+FCW` guarantee stated in `CONSISTENCY.md §1` remains
   entity-granular — model rows are infrastructure, not entities, and must
   not become serialization hotspots.
2. **Commit-bound visibility.** An additive mutation is visible to other
   readers **iff** the owning entity transaction commits. If the entity
   transaction rolls back, the schema mutation is never observed.

These two constraints are jointly satisfiable because extensions are
backwards-compatible, commutative, and idempotent. Two concurrent
extensions producing different additive deltas converge on read.

## 3. Scope

**Phase 1 (this spec):**

- Split ModelStore's physical representation in the Postgres plugin into
  stable metadata + an append-only log of additive extensions. External
  plugins follow the same SPI contract with their own internal
  representation (specified in each plugin's own design doc).
- Introduce `ModelStore.ExtendSchema(ctx, ref, delta)` at the SPI boundary.
- A minimal `CachingModelStore` decorator that memoizes the
  immutable-in-practice case (`State == LOCKED && ChangeLevel == ""`) and
  broadcasts drop-invalidation on any write.
- Collapse all existing plugin migrations into a single
  `0001_initial_schema.{up,down}.{sql,cql}` per plugin (greenfield — no
  released versions to preserve).

**Out of scope (deferred to a later phase if profiling justifies it):**

- Caching for mutable models (`ChangeLevel != ""` or `UNLOCKED`).
- Push-delta gossip (sending the patch contents cross-node) — Phase 1 uses
  notify-and-drop.
- TTL eviction, LRU caps, cache size tuning.
- Compaction / folding of the extension log by external tooling.

## 4. Architecture

### 4.1 Storage shape — conceptual

For each `(tenant, modelRef)` a plugin conceptually maintains:

- **Base row.** Stable metadata: `state`, `changeLevel`, `baseSchema`,
  `updateDate`. Mutated by admin operations (import, lock/unlock,
  set-change-level). Rare concurrency pressure.
- **Extension log.** Append-only sequence of entries, each either:
  - A **delta** carrying a typed-op schema delta (see §4.2) to apply on
    top of `baseSchema + preceding deltas`.
  - A **savepoint** carrying the fully-folded schema as of that point.
    Plugin-internal optimization; emitted in the same batch that creates
    the triggering delta.

On read, a plugin folds `baseSchema` with the deltas back to (but not
beyond) the most recent savepoint. With a savepoint every N=64 deltas,
the worst-case fold is bounded at ~64 apply-ops per `Get`.

**Delta format choice: typed ops, not JSON merge-patch.** RFC 7396
merge-patch replaces arrays wholesale, which would break commutativity
for schema fields that are arrays (`required`, `enum`, `type`-as-union,
`oneOf`/`anyOf`, tuple-`items`): two concurrent writers adding
different elements to the same array could lose each other's addition
depending on fold order. The typed-op format defined in §4.2 gives each
op-kind an order-independent merge rule (set-union for arrays,
idempotent-key-insert for object properties) so convergence is a
contract property, not an observation.

### 4.2 SPI changes (`cyoda-go-spi`)

A new method on `ModelStore` and typed-op value types. The SPI owns
only the value shapes; apply semantics live in the main repo (§4.3).

```go
// SchemaDelta is an ordered list of additive schema operations. The
// SPI stores it opaquely; schema.Apply (main repo) replays it onto a
// base schema. Each op-kind's merge is order-independent, making the
// overall fold commutative.
type SchemaDelta struct {
    Ops []SchemaOp
}

type SchemaOpKind string

const (
    SchemaOpAddProperty      SchemaOpKind = "add_property"
    SchemaOpAddRequired      SchemaOpKind = "add_required"
    SchemaOpAddEnumValue     SchemaOpKind = "add_enum_value"
    SchemaOpBroadenType      SchemaOpKind = "broaden_type"
    SchemaOpAddArrayItemType SchemaOpKind = "add_array_item_type"
    SchemaOpExtendOneOf      SchemaOpKind = "extend_one_of"
    SchemaOpExtendAnyOf      SchemaOpKind = "extend_any_of"
    // Enumeration finalized at plan-time by auditing schema.Extend
    // output classes (see §9).
)

type SchemaOp struct {
    Kind    SchemaOpKind
    Path    string // JSON pointer into the schema, e.g. "/properties/address"
    Payload []byte // op-specific data; shape determined by Kind
}

type ModelStore interface {
    // ... existing methods unchanged ...

    // ExtendSchema appends an additive schema delta to the model.
    //
    // Contract:
    //   - The plugin must append, not replace. `Save` remains the
    //     full-replace path for admin operations.
    //   - The append must participate in any active entity transaction:
    //     visible iff that transaction commits.
    //   - Concurrent ExtendSchema calls on distinct entity transactions
    //     targeting the same model MUST NOT conflict with each other at
    //     the storage layer.
    //   - Any two well-formed deltas must fold commutatively into the
    //     same final schema regardless of apply order. This is enforced
    //     by the op-kind catalog (each kind's merge rule is documented
    //     in schema.Apply).
    ExtendSchema(ctx context.Context, ref ModelRef, delta *SchemaDelta) error
}
```

**Why the SPI carries value types and not apply logic.** Plugins must
not depend on `internal/domain/model/schema` (plugin submodule
self-containment). The logic for `Diff` and `Apply` lives in the main
repo. Plugins that need to fold during `Get` are handed a
`schema.ApplyFunc` at construction time via their store-factory config
— see §4.4. Plugins store `SchemaDelta` bytes opaquely and never
interpret them.

`Save` keeps its existing full-replace semantic. Plugins with an
extension log clear the log on `Save`.

### 4.3 Handler-side changes (`internal/domain/model/schema`)

Two new functions paired with the existing `Extend`:

```go
// Diff emits the typed-op delta expressing `new` as an additive change
// over `old`. Caller guarantees `new` is produced by schema.Extend with
// a valid ChangeLevel. Diff returns an error if it encounters a change
// that cannot be expressed commutatively — that is a contract bug in
// Extend and should be caught by tests (see §7), not surface at runtime.
func Diff(old, new *ModelNode) (*spi.SchemaDelta, error)

// Apply replays the ops in `delta` onto `base`, producing the folded
// schema. The same function is injected into plugins at store-factory
// construction so they can fold during Get without importing internal
// packages.
func Apply(base *ModelNode, delta *spi.SchemaDelta) (*ModelNode, error)
```

**Commutativity is a test obligation.** For each op-kind defined in
§4.2, `schema/apply_test.go` contains a table-driven property check:
for any pair of deltas `d1, d2` over the same base, `Apply(Apply(b, d1), d2)`
must equal `Apply(Apply(b, d2), d1)` up to schema-equivalence. The op
catalog grows only via adding new kinds with their rule — the rule must
pass this test before being merged.

**Rewritten `validateOrExtend`** (`internal/domain/entity/handler.go`):

```go
if desc.ChangeLevel == "" {
    return validate(modelNode, parsedData) // unchanged strict path
}
incomingModel, err := importer.Walk(parsedData)
if err != nil { return err }
extended, err := schema.Extend(modelNode, incomingModel, desc.ChangeLevel)
if err != nil { return err }          // non-additive for the level
if extended == modelNode { return nil } // Extend short-circuits on no-op
delta, err := schema.Diff(modelNode, extended)
if err != nil { return err }
return modelStore.ExtendSchema(ctx, desc.Ref, delta)
```

The existing failure-mode translation in `classifyValidateOrExtendErr`
stays; `common.Internal` still unwraps `spi.ErrConflict` to a `409` for
the (rare) legitimate concurrent-extension case.

### 4.4 Per-plugin realization

#### Postgres

Two tables:

- `models` — stable metadata only. One row per `(tenant, ref)`. Mutated by
  `Save`, `Lock`, `Unlock`, `SetChangeLevel`. Columns include `base_schema`
  (JSONB).
- `model_schema_extensions` — append-only. Primary key
  `(tenant_id, model_name, model_version, seq BIGSERIAL)`. Columns: `kind`
  (`'delta' | 'savepoint'`), `payload` (JSONB — the serialized
  `spi.SchemaDelta` for deltas, the folded schema for savepoints),
  `tx_id`, `created_at`.

`ExtendSchema` is an `INSERT` (plus, conditionally, a second `INSERT` for
the savepoint) on the current `pgx.Tx`. Distinct inserts on distinct
`seq` values do not contend — no 40001 under concurrency for distinct
entity transactions. The insert participates in the entity commit
atomically.

`Get` reads the `models` row, then scans `model_schema_extensions` in
reverse to locate the most recent savepoint, then folds forward by
invoking the injected `schema.Apply` for each subsequent delta. A
covering index on `(tenant_id, model_name, model_version, seq DESC)`
keeps the scan bounded.

**Apply-func injection.** The postgres store factory grows an optional
`ApplyFunc func(base *ModelNode, delta *spi.SchemaDelta) (*ModelNode, error)`
configuration field; at initialization `StoreFactory` wires
`schema.Apply` into it. Plugins unit-tested with a stub `ApplyFunc` so
plugin tests don't depend on the main repo's schema package.

#### External plugins

External storage plugins implement the same SPI and honour the same
two invariants from §2. Their internal representation and
transaction-binding mechanisms are plugin-specific and are specified
in each plugin's own design documentation, out of scope here.

#### Memory and SQLite

Memory and SQLite are documented single-node-only. They do not face the
concurrency pressure the two-table split resolves, so they do not need a
physical log:

- `ExtendSchema` calls the injected `ApplyFunc` on the stored schema
  and replaces. Internally the entry appears as a normal `Save`.
- No savepoints, no log, no fold on read. Plugins accept the same
  `ApplyFunc` injection as Postgres; implementations are ~10 lines
  each beyond the SPI boilerplate.

This keeps the simple plugins simple while honouring the SPI.

### 4.5 Cache — `CachingModelStore` decorator

A new type in `internal/cluster/modelcache` (lean, finalized at plan-time):

```go
type CachingModelStore struct {
    inner       spi.ModelStore
    broadcaster spi.ClusterBroadcaster // nil-safe
    mu          sync.RWMutex
    cache       map[cacheKey]*spi.ModelDescriptor // only LOCKED + ""
    epochs      map[cacheKey]uint64              // bumped on any invalidation
}
```

**Policy.**

- On `Get`: cache hit → return copy. Cache miss → delegate to `inner`;
  if the returned descriptor has `State == LOCKED && ChangeLevel == ""`,
  store it under the epoch-guard rule below; otherwise pass through
  uncached.
- On every local mutating call (`Save`, `Lock`, `Unlock`,
  `SetChangeLevel`, `ExtendSchema`, `Delete`): delegate to `inner`
  first, then bump the ref's epoch and drop the cache entry, then
  (if broadcaster is non-nil) publish the invalidation.
- On incoming gossip invalidation: bump the ref's epoch and drop the
  entry. No further action.

**Epoch-guarded populate.** A populate-during-invalidation race
(reader issues DB read, invalidation arrives before reader stores the
result, reader then caches stale data) is prevented by capturing the
ref's current epoch *before* the underlying `Get` and re-checking
after:

```go
func (c *CachingModelStore) Get(ctx context.Context, ref ModelRef) (*ModelDescriptor, error) {
    if d := c.lookup(ref); d != nil {
        return d, nil
    }

    c.mu.RLock()
    snapshotEpoch := c.epochs[key(ref)]
    c.mu.RUnlock()

    desc, err := c.inner.Get(ctx, ref)
    if err != nil || desc.State != ModelLocked || desc.ChangeLevel != "" {
        return desc, err
    }

    c.mu.Lock()
    if c.epochs[key(ref)] == snapshotEpoch {
        c.cache[key(ref)] = desc
    } // else: concurrent invalidation — drop result rather than cache stale
    c.mu.Unlock()
    return desc, nil
}

func (c *CachingModelStore) invalidate(ref ModelRef) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.epochs[key(ref)]++
    delete(c.cache, key(ref))
}
```

**Race coverage — all three orderings:**

1. *Invalidation before the `Get` starts.* Epoch is already bumped;
   reader captures the post-bump epoch; stores successfully with the
   fresh DB result. Correct.
2. *Invalidation during the DB read.* Reader captured pre-bump epoch;
   invalidation bumps; reader's post-check sees mismatch; result is
   returned to the caller but not cached. Next `Get` repopulates with
   a new epoch snapshot. Correct.
3. *Invalidation after the reader stores.* Reader stores under old
   epoch; invalidation then bumps the epoch and deletes the entry.
   Correct — the cache ends up empty, as intended.

Epoch values only ever grow. Memory overhead is one `uint64` per
distinct ref seen; bounded by the model catalog size.

**Gossip topic:** `model.invalidate` (new constant).

**Payload:** small codec carrying `tenantID` and `ref` bytes. No model
contents on the wire.

**Subscription:** the decorator registers a handler on construction.
The handler invokes `invalidate(ref)` — same path as local mutation
post-write.

**Wiring.** `StoreFactory.ModelStore(ctx)` returns
`CachingModelStore{inner: pluginModelStore, broadcaster: ...}`. Call
sites see only `spi.ModelStore` — no caller knows the decorator exists.

**Construction.** Single-node deployments (memory, sqlite, or postgres
running without gossip configured) pass `nil` for the broadcaster and
all invalidation stays local. Multi-node deployments wire the real
`spi.ClusterBroadcaster` obtained from the cluster registry.

## 5. Migration collapse

Existing migrations in the plugins owned by this repo
(`plugins/postgres/migrations/000001` through `000005`,
`plugins/sqlite/migrations/000001`) are collapsed into a single
`0001_initial_schema.up.sql` per plugin. No intermediate migrations are
preserved — this is pre-release greenfield.

The new initial schema includes the two tables introduced here
(`models`, `model_schema_extensions`) plus everything from the prior
migrations, consolidated into one file. Down migrations symmetric.
External plugins handle their own migration collapse in their own
repositories.

## 6. Documentation updates

- **`docs/CONSISTENCY.md`**: new section "Model/Data Contract" — states
  the two invariants from §2. Placed between the current "entity-granular
  guarantee" introduction and the "what this catches" section so the
  contract is exposed early.
- **`docs/ARCHITECTURE.md`**:
  - §2.3 (Postgres plugin): one-paragraph note on the two-table model
    representation pointing to `CONSISTENCY.md §Model/Data Contract`.
  - §3 (Transaction Model): cross-reference that model mutation
    participates in entity transactions via `ExtendSchema`.
  - §4 (Multi-Node Routing): add the `model.invalidate` gossip topic to
    the list of broadcast channels.

## 7. Testing

**Unit — per-plugin (`plugins/*/model_store_test.go`):**

- `ExtendSchema` appends; `Get` folds deltas into the expected
  descriptor.
- Savepoints emitted on the Nth delta (Postgres only; external-plugin
  equivalents tested in their own repositories).
- `Save` clears the extension log.
- `Lock`, `Unlock`, `SetChangeLevel` unaffected.

**Unit — schema (`internal/domain/model/schema/{diff,apply}_test.go`):**

- `Diff` produces typed-op deltas; `Apply` folds them back to the
  expected schema.
- Round-trip: `Apply(old, Diff(old, Extend(old, incoming)))` equals
  `Extend(old, incoming)` for every ChangeLevel and every sample of
  incoming shapes enumerated in the test data.
- **Commutativity property tests:** for every op-kind pair
  `(k1, k2)` (including `k1 == k2`), randomly generated deltas
  `d1, d2` of those kinds over a shared base must satisfy
  `Apply(Apply(b, d1), d2) ≡ Apply(Apply(b, d2), d1)`. Table-driven
  with a small generator; fails noisily if an op-kind's merge rule is
  not order-independent.
- **Extend-completeness test:** for every classified output of
  `schema.Extend` (enumerated at plan-time per §9), `Diff` must be
  able to express the change as a `SchemaDelta`. A change that
  `Extend` permits but `Diff` cannot encode is a design bug and the
  test fails.

**Integration — concurrent update regression (`plugins/postgres`):**

- RED reproducer: N=8 concurrent `UpdateEntity` calls on distinct
  entities of a `ChangeLevel`-enabled model, asserting all N commit.
- GREEN after fix: same test, no `spi.ErrConflict` surfaces.

**Integration — cache + gossip (`internal/cluster/modelcache`):**

- Two decorators sharing a fake broadcaster. `ExtendSchema` on
  decorator A drops the entry on decorator B.
- Cache only stores `LOCKED + ""` entries; `UNLOCKED` and
  `ChangeLevel != ""` entries bypass.
- **Populate race tests** — one test per ordering from §4.5:
  (i) invalidation-before-read, (ii) invalidation-during-read (using a
  hook on the inner store to block mid-`Get` while an invalidation is
  published), (iii) invalidation-after-store. In every case the
  decorator must not leave a stale entry visible to subsequent `Get`s.

**E2E (`internal/e2e`):**

- New test exercising the regressed workload (bulk create + bulk update
  on a schema-evolving model) through the full HTTP stack with the
  Postgres testcontainer. Asserts no 409 under concurrency.

## 8. Risks and mitigations

- **External-plugin scope.** Some external plugins may need additional
  work to bind `ExtendSchema` to the entity commit path; such work is
  tracked in the respective plugin's own repository. Gate Phase 1 cut
  on all dependent repos landing their equivalent changes.
- **Fold cost under adversarial delta rate.** Savepoint every 64 bounds
  this; profile once real workloads exist. If needed, tune the interval
  via a plugin-level config knob (out of scope Phase 1).
- **Cache correctness if the "immutability" assumption is wrong.** The
  only mutators of a `LOCKED + ""` model are `Unlock`,
  `SetChangeLevel`, `ExtendSchema` (should not happen — `""` level),
  `Save`, `Delete`, and `Lock` (already locked). All go through the
  decorator, all invalidate. The one failure mode is a direct DB write
  by something outside the SPI; we document that as "don't do that."
- **Gate 6 — resolving vs deferring.** Every item flagged during this
  brainstorm was addressed inline or scoped explicitly out (Phase 2).
  No silent TODOs.

## 9. Open questions for plan-time

- Exact package for `CachingModelStore`: `internal/cluster/modelcache`
  vs. `internal/domain/model/cache` vs. `internal/storage/cache`. Lean
  first because the decorator's cross-cutting concern is the cluster.
- Whether `N = 64` savepoint interval should be a config knob at all in
  Phase 1 or hardcoded. Lean hardcoded for simplicity; surface later.
- Codec for the gossip payload: use existing cluster dispatch codec or
  a new minimal one. Lean reuse if it's suitable; inspect at plan-time.
- **SchemaOpKind enumeration.** The final set of op-kinds is derived by
  auditing every code path through `schema.Extend` at every ChangeLevel
  and classifying its output. Plan-time task: produce a matrix of
  (ChangeLevel, input shape → Extend output diff) and confirm each row
  maps to one or more op-kinds. Any unmappable row fails the
  Extend-completeness test and must be resolved (add an op-kind, or
  constrain Extend, or surface the constraint to the user).
