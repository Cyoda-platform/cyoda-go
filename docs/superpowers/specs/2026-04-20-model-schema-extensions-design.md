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
  - A **delta** carrying a JSON merge-patch (RFC 7396-style) to apply on
    top of `baseSchema + preceding deltas`.
  - A **savepoint** carrying the fully-folded schema as of that point.
    Plugin-internal optimization; emitted in the same batch that creates
    the triggering delta.

On read, a plugin folds `baseSchema` with the deltas back to (but not
beyond) the most recent savepoint. With a savepoint every N=64 deltas,
the worst-case fold is bounded at ~64 JSON merges per `Get`.

### 4.2 SPI changes (`cyoda-go-spi`)

A new method on `ModelStore` and a new small value type:

```go
// SchemaDelta is an additive change to a ModelDescriptor's schema,
// expressed as an RFC 7396 JSON merge-patch. Plugins store it
// opaquely; the patch is produced by the main repo's schema.Diff.
type SchemaDelta struct {
    Patch []byte
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
    //     the storage layer; extensions are commutative.
    ExtendSchema(ctx context.Context, ref ModelRef, delta *SchemaDelta) error
}
```

`Save` keeps its existing full-replace semantic. Plugins with an
extension log clear the log on `Save`.

### 4.3 Handler-side changes

**New function** in `internal/domain/model/schema`:

```go
// Diff returns the JSON merge-patch that carries `new` relative to `old`.
// Caller guarantees `new` is an additive-only extension of `old`, as
// produced by schema.Extend with a valid ChangeLevel.
func Diff(old, new *ModelNode) ([]byte, error)
```

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
patch, err := schema.Diff(modelNode, extended)
if err != nil { return err }
return modelStore.ExtendSchema(ctx, desc.Ref, &spi.SchemaDelta{Patch: patch})
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
  (`'delta' | 'savepoint'`), `payload` (JSONB — the merge-patch for
  deltas, the folded schema for savepoints), `tx_id`, `created_at`.

`ExtendSchema` is an `INSERT` (plus, conditionally, a second `INSERT` for
the savepoint) on the current `pgx.Tx`. Distinct inserts on distinct
`seq` values do not contend — no 40001 under concurrency for distinct
entity transactions. The insert participates in the entity commit
atomically.

`Get` reads the `models` row, then scans `model_schema_extensions` in
reverse to locate the most recent savepoint, then folds forward. A simple
covering index on `(tenant_id, model_name, model_version, seq DESC)`
keeps the scan bounded.

#### External plugins

External storage plugins implement the same SPI and honour the same
two invariants from §2. Their internal representation and
transaction-binding mechanisms are plugin-specific and are specified
in each plugin's own design documentation, out of scope here.

#### Memory and SQLite

Memory and SQLite are documented single-node-only. They do not face the
concurrency pressure the two-table split resolves, so they do not need a
physical log:

- `ExtendSchema` applies the merge-patch to the stored schema bytes and
  replaces. The entry appears as a normal `Save` internally.
- No savepoints, no log, no fold on read. Current implementations
  require only a minimal wrapper to accept a `SchemaDelta` and perform
  the apply.

This keeps the simple plugins simple while honouring the SPI.

### 4.5 Cache — `CachingModelStore` decorator

A new type in `internal/cluster/modelcache` (or equivalent; exact
location bike-sheddable in the plan):

```go
type CachingModelStore struct {
    inner       spi.ModelStore
    broadcaster spi.ClusterBroadcaster // nil-safe
    mu          sync.RWMutex
    cache       map[cacheKey]*spi.ModelDescriptor // only LOCKED + ""
}
```

**Policy.**

- On `Get`: cache hit → return copy. Cache miss → delegate to `inner`;
  if the returned descriptor has `State == LOCKED && ChangeLevel == ""`,
  store it; otherwise pass through uncached.
- On every mutating call (`Save`, `Lock`, `Unlock`, `SetChangeLevel`,
  `ExtendSchema`, `Delete`): delegate to `inner`, then drop the cache
  entry for that ref, then (if broadcaster is non-nil) publish the
  invalidation.

**Gossip topic:** `model.invalidate` (new constant).

**Payload:** small codec carrying `tenantID` and `ref` bytes. No model
contents on the wire.

**Subscription:** the decorator registers a handler on construction.
Incoming invalidations drop the local cache entry and do nothing else.
Ordering doesn't matter — the cache either has the entry or doesn't;
staleness can only manifest if an admin operation interleaves with a
just-in-flight `Get` on another node, and the worst case there is an
immediate cache repopulation.

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

**Unit — schema (`internal/domain/model/schema/diff_test.go`):**

- `Diff` produces merge-patches consumed correctly by apply.
- Round-trip: `Diff(old, Extend(old, incoming)) == merge-patch` such that
  applying to `old` yields `Extend(old, incoming)`.

**Integration — concurrent update regression (`plugins/postgres`):**

- RED reproducer: N=8 concurrent `UpdateEntity` calls on distinct
  entities of a `ChangeLevel`-enabled model, asserting all N commit.
- GREEN after fix: same test, no `spi.ErrConflict` surfaces.

**Integration — cache + gossip (`internal/cluster/modelcache`):**

- Two decorators sharing a fake broadcaster. `ExtendSchema` on decorator
  A drops the entry on decorator B.
- Cache only stores `LOCKED + ""` entries; `UNLOCKED` and
  `ChangeLevel != ""` entries bypass.

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
