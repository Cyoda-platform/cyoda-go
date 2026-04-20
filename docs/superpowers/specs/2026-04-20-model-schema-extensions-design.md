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

Invariants for additive model mutation:

1. **Non-interference.** An additive mutation of a model's schema must not
   cause a transaction conflict with concurrent data operations on the same
   model. The `SI+FCW` guarantee stated in `CONSISTENCY.md §1` remains
   entity-granular — model rows are infrastructure, not entities, and must
   not become serialization hotspots.
2. **Commit-bound visibility.** An additive mutation is visible to other
   readers **iff** the owning entity transaction commits. If the entity
   transaction rolls back, the schema mutation is never observed.
3. **Commutativity.** For any two well-formed deltas `d1, d2` over a
   shared base schema `B`, `Apply(Apply(B, d1), d2) ≡ Apply(Apply(B, d2), d1)`.
   Concurrent extensions converge regardless of apply order.
4. **Validation-monotonicity.** Any document that validates against a
   schema `B` also validates against `Apply(B, d)` for every delta `d`.
   "Additive" in the operational sense means *strictly broadening the
   accepted set* — never tightening it. Ops that narrow acceptance
   (e.g. adding to `required`) are excluded from the op-kind catalog
   by construction.

5. **State-machine disjointness of `Save` and `ExtendSchema`.** The
   existing model lifecycle enforces that these two writes never run
   concurrently on the same model:
   - `Save` requires `UNLOCKED` (admin path; replaces schema wholesale).
   - `ExtendSchema` requires `LOCKED` with `ChangeLevel != ""`
     (ingestion path; appends a delta).

   No storage-layer locks, advisory locks, or cross-operation snapshots
   are needed to exclude them. The guarantee is plugin-agnostic and
   holds identically for every backend.

**Operator contract on `Unlock`.** `UNLOCKED → LOCKED` is permitted
only when no live data exists for the model (all soft-deleted); the
reverse `LOCKED → UNLOCKED` transition is an operator action that
requires the application to have drained writers to this model first.
Concurrent `ExtendSchema` during `Unlock` is **undefined behaviour,
not a supported mode** — the plugin's "extension log non-empty
after unlock" defensive assertion (§4.4) exists to catch operator
misuse during development, not to serve as a production race guard.

**Accepted policy, not a violation.** `SetChangeLevel` under the
`LOCKED → LOCKED` transition may tighten the permitted level while an
`ExtendSchema` under the old level is in flight. Commit-bound
visibility still holds: the extension either commits against the
now-tightened policy (audit-trailed, subsequent reads see the resulting
schema) or it rolls back. This is called out in `CONSISTENCY.md` as
accepted behaviour, not a race to guard against.

## 3. Scope

**Phase 1 (this spec):**

- Split ModelStore's physical representation in the Postgres plugin into
  stable metadata + an append-only log of additive extensions. External
  plugins follow the same SPI contract with their own internal
  representation (specified in each plugin's own design doc).
- Introduce `ModelStore.ExtendSchema(ctx, ref, delta)` at the SPI boundary.
- A `CachingModelStore` decorator that memoizes any `LOCKED` descriptor
  (any `ChangeLevel`), with a three-layer coherence strategy: gossip
  drop-invalidation (fast path), validator-triggered refresh-on-stale
  (correctness safety net), and a generous TTL lease (operational
  bound). Gossip is a latency optimization; correctness rests on the
  catalog invariants plus the refresh step.
- Collapse all existing plugin migrations into a single
  `0001_initial_schema.up.sql` per plugin (greenfield — no released
  versions to preserve).

**Out of scope (deferred to a later phase if profiling justifies it):**

- LRU caps, cache size tuning.
- Push-delta gossip (sending delta contents cross-node) — Phase 1 uses
  notify-and-drop, which combined with self-healing is sufficient.
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
  - A **savepoint** carrying the fully-folded schema *including* the
    triggering delta (i.e., the state as of the `seq` just written).
    Plugin-internal optimization; emitted in the same batch as the
    triggering delta.

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

The SPI carries one opaque value type and one new method. Typed-op
structure and apply/merge logic live entirely in
`internal/domain/model/schema`; plugins store-and-forward bytes and
never interpret them.

```go
// SchemaDelta is an opaque, plugin-agnostic serialization of an
// additive schema change. The bytes are produced by schema.Diff
// (main repo) and consumed by schema.Apply (main repo, invoked by
// plugins via the injected ApplyFunc on their factory config — see
// §4.4). Plugins persist the bytes verbatim and never inspect them.
type SchemaDelta []byte

type ModelStore interface {
    // ... existing methods unchanged ...

    // ExtendSchema appends an additive schema delta to the model.
    //
    // Contract:
    //   - Semantically equivalent to "append the delta to the model's
    //     extension log, participating in the active entity
    //     transaction." A plugin whose storage doesn't natively model
    //     a log may implement this by applying the delta to the
    //     in-store schema so long as the externally observable
    //     behaviour matches: visible iff the entity tx commits; no
    //     conflict with concurrent data ops; result equal to what an
    //     append-and-fold would produce.
    //   - `Save` remains the full-replace path for admin operations
    //     and is disjoint from `ExtendSchema` via the state machine
    //     (see §2).
    //   - Concurrent ExtendSchema calls on distinct entity transactions
    //     targeting the same model MUST NOT conflict with each other
    //     at the storage layer.
    //   - Any two well-formed deltas must fold commutatively into the
    //     same final schema regardless of apply order, and every delta
    //     must be validation-monotone. Enforcement is handler-side
    //     (op catalog and merge rules live in schema; see below);
    //     plugins have no contract obligation beyond store-and-forward.
    ExtendSchema(ctx context.Context, ref ModelRef, delta SchemaDelta) error
}
```

**Why bytes, not typed ops.** Exposing `SchemaOp`/`SchemaOpKind` at
the SPI would force every plugin (and its CI) to track catalog
revisions. With opaque bytes, adding an op-kind is a main-repo-only
change; Postgres, Cassandra, Memory, SQLite are forward-compatible by
construction. The cost is that wire-compatibility across cyoda-go
versions becomes a schema-package concern — noted in §8.

#### Op catalog and merge rules (`internal/domain/model/schema`)

The catalog is internal to the main repo, but the contract each op
honours is part of the design. Each op-kind specifies a merge rule;
the rule is how `Apply` folds two deltas acting on the same schema
path in an order-independent way. Listing them here so that readers
of the SPI know what's being promised before they read the tests in
§7.

| Kind | Shape | Merge rule on same path |
|---|---|---|
| `add_property(path, name, def)` | insert key into an object | *Idempotent on exact payload.* Same `(path, name, def)` applied twice = once. Divergent `def` for the same `name` merges by schema-union (polymorphic broadening — the resulting property accepts values that satisfy either `def`). |
| `add_enum_value(path, v)` | append to `enum` list | *Set-union over values.* Concurrent adds of different values both land; duplicates collapse. |
| `broaden_type(path, t)` | extend `type` from scalar or union to include `t` | *Set-union over type primitives.* `type: "string"` + `broaden(path, "null")` + `broaden(path, "number")` → `type: ["string", "null", "number"]` regardless of order. |
| `add_array_item_type(path, def)` | extend tuple-`items` / `prefixItems` / homogeneous-`items` variant set | *Set-union keyed by `def` signature hash.* Structurally identical adds dedup; different adds accumulate. |
| `extend_one_of(path, branch)` / `extend_any_of(path, branch)` | append branch to `oneOf`/`anyOf` | *Set-union keyed by branch-signature hash.* |

Op catalog is finalized at plan-time by auditing `schema.Extend`
output classes (§9). Every candidate must satisfy **both**
commutativity and validation-monotonicity, verified by the property
tests in §7; any op that tightens the accepted set (e.g.
`add_required`) is rejected — it is not additive in the operational
sense regardless of JSON Schema taxonomy.

`Save` keeps its existing full-replace semantic and runs only in the
UNLOCKED state (§2). The relationship between `Save` and any
extension log is not part of the SPI contract — plugins that keep
one handle it internally as a lifecycle concern (§4.4).

### 4.3 Handler-side changes (`internal/domain/model/schema`)

Two new functions paired with the existing `Extend`:

```go
// Diff emits the serialized delta expressing `new` as an additive
// change over `old`. Caller guarantees `new` is produced by
// schema.Extend with a valid ChangeLevel.
//
// Returns:
//   - (delta, nil) when new ≠ old: a non-empty SchemaDelta.
//   - (nil, nil) when new == old semantically: no-op. Callers MUST
//     check for nil before calling ExtendSchema, or pass the zero
//     value; ExtendSchema on a nil/empty delta is a caller bug.
//   - (nil, err) if the change cannot be expressed by the op catalog
//     — a contract bug in Extend caught by the Extend-completeness
//     test (§7), not a runtime mode.
//
// Diff does not rely on pointer identity of inputs.
func Diff(old, new *ModelNode) (spi.SchemaDelta, error)

// Apply replays the serialized delta onto `base`, producing the folded
// schema. The same function is injected into plugins at store-factory
// construction so they can fold during Get without importing internal
// packages.
func Apply(base *ModelNode, delta spi.SchemaDelta) (*ModelNode, error)
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
delta, err := schema.Diff(modelNode, extended)
if err != nil { return err }
if delta == nil {                       // no-op signalled by Diff
    return nil
}
return modelStore.ExtendSchema(ctx, desc.Ref, delta)
```

No pointer-equality check on `extended`. The no-op short-circuit is a
documented `Diff` return, not an `Extend` pointer-identity
side effect, so a future implementation that allocates a fresh
equivalent node doesn't silently commit a no-op delta.

**Diff summary of the rewrite** (for reviewers comparing against
today's code): the `ChangeLevel != ""` branch no longer calls
`modelStore.Save`. That call was the defect — it wrote the `models`
row inside the entity transaction on every update, producing the
REPEATABLE-READ hot-row conflict on concurrent updates. `ExtendSchema`
replaces it and is append-only by contract.

The existing failure-mode translation in `classifyValidateOrExtendErr`
stays; `common.Internal` still unwraps `spi.ErrConflict` to a `409` for
the (rare) legitimate concurrent-extension case.

**Read-side self-healing on strict validate and search.**

When the handler validates against a possibly-cached schema —
`schema.Validate` in the `ChangeLevel == ""` branch of
`validateOrExtend` above, and field-path validation in search queries
(`internal/domain/search`, `internal/match`) — a stale cache on the
receiving node can surface as an "unknown schema element" error even
though the extension has committed elsewhere. Bounded, one-shot
refresh handles this:

```go
desc, _ := modelStore.Get(ctx, ref)
err := schema.Validate(desc, input)
if err != nil && hasUnknownSchemaElement(err) {
    desc, _ = modelStore.RefreshAndGet(ctx, ref) // drops cache, reloads via singleflight
    err = schema.Validate(desc, input)
}
return err
```

**Error classifier (`hasUnknownSchemaElement`).** Typed sentinel, not
string-match. `schema.Validate` returns a structured multi-error type
whose constituent errors each expose a classified kind
(`UnknownProperty`, `UnknownEnumValue`, `UnknownTypeVariant`,
`TypeMismatch`, `OutOfRange`, `PatternMismatch`, …). The classifier
returns true iff **any** constituent error is in the "unknown element"
family. This handles the multi-field case where one field is unknown
and another fails for a non-schema-stale reason: refresh, revalidate
once, and return whatever the post-refresh validation says. One
refresh still caps the blast radius.

Bounds:

- **At most one refresh per request.** No loops, no DoS surface.
- **Only the `unknown schema element` family.** Type mismatches,
  range violations, pattern mismatches surface directly — a client
  submitting genuinely invalid input must not trigger source-of-truth
  traffic.
- If the refresh sees the same schema (the error was legitimate), the
  second validation produces the same error and the caller sees the
  correct answer.

`RefreshAndGet` is exposed on `CachingModelStore` (§4.5). Handlers use
it via a small optional-interface type assertion so non-caching paths
don't need the method.

### 4.4 Per-plugin realization

#### Postgres

Two tables:

- `models` — stable metadata only. One row per `(tenant, ref)`. Mutated by
  `Save` (UNLOCKED state), `Lock`, `Unlock`, `SetChangeLevel`. Columns
  include `base_schema` (JSONB).

  **Lifecycle coupling with the extension log.** Because `Save`
  requires `UNLOCKED` and `ExtendSchema` requires `LOCKED`, the two
  never run concurrently on the same model (§2). On `Unlock`, the
  plugin performs a `DELETE FROM model_schema_extensions ... RETURNING
  count` and, at development build-time, asserts `count == 0`. Per the
  operator contract in §2, concurrent writers during `Unlock` are not
  a supported mode; the assertion catches operator misuse during
  development and is not a production race guard. A non-zero count in
  production is logged as an anomaly and the rows are discarded — the
  `Unlock` itself still succeeds.
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
`ApplyFunc func(base *ModelNode, delta spi.SchemaDelta) (*ModelNode, error)`
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
    inner        spi.ModelStore
    broadcaster  spi.ClusterBroadcaster // nil-safe
    clock        Clock                  // injected; real time by default
    lease        time.Duration          // baseline (e.g. 1h); real expiry jittered ±10%
    mu           sync.RWMutex
    cache        map[cacheKey]entry
    refresh      *singleflight.Group    // collapses concurrent RefreshAndGet per ref
}

type entry struct {
    desc      *spi.ModelDescriptor
    expiresAt time.Time // lease + per-entry jitter
}
```

**Admission.** Cache **any** `LOCKED` descriptor, for any
`ChangeLevel`. This widens admission relative to today's code (and
relative to an earlier draft of this spec), which only cached
immutable models. Justification: §2's state-machine disjointness
between `Save` and `ExtendSchema`, plus commutativity and
validation-monotonicity on every op-kind, make a stale cache
*self-healing* — there is no catastrophic failure mode to protect
against by restricting admission. `UNLOCKED` descriptors still pass
through uncached — short lifetimes, always on the admin path.

**Policy.**

- On `Get`: cache hit with `clock.Now() < expiresAt` → return. Cache
  hit past lease → drop entry, fall through to miss. Miss → delegate
  to `inner`; if returned descriptor is `LOCKED`, store with
  `expiresAt = Now() + lease`.
- On every local mutating call (`Save`, `Lock`, `Unlock`,
  `SetChangeLevel`, `ExtendSchema`, `Delete`): delegate to `inner`
  first, then drop the cache entry, then (if broadcaster is non-nil)
  publish the invalidation.
- On incoming gossip invalidation: drop the cache entry.

**`RefreshAndGet`.** Extra method on the decorator (not the SPI)
consumed by the validator path (§4.3):

```go
// Drops the cache entry for ref, re-reads from inner, stores the
// fresh result (with a freshly-jittered expiresAt), and returns it.
// Concurrent RefreshAndGet calls on the same ref collapse via
// singleflight — a post-partition gossip catch-up where every
// in-flight request independently hits "unknown schema element"
// produces exactly one source-of-truth read, not N. One refresh per
// request is still enforced by the handler (§4.3).
func (c *CachingModelStore) RefreshAndGet(ctx context.Context, ref ModelRef) (*spi.ModelDescriptor, error)
```

**Three-layer coherence, in decreasing importance:**

1. **Catalog invariants (correctness floor).** Commutativity and
   validation-monotonicity (§2) mean a node operating on a stale
   cached schema is never catastrophically wrong:
   - *Write-side self-healing.* A stale-cache node sees an
     apparently-new field, computes a delta, calls `ExtendSchema`.
     The resulting log folds to the same final schema as if the node
     had seen the prior extension. At worst one redundant log entry.
   - *Read-side self-healing.* A stale-cache node may fail validation
     on an element the stale schema doesn't contain; the validator's
     bounded refresh step (§4.3) reloads from the plugin and
     revalidates once. Succeeds if the extension has committed;
     surfaces a legitimate error otherwise.
2. **Gossip invalidation (performance optimization).** The
   `model.invalidate` topic carries a small `(tenantID, ref)` payload;
   receivers drop their cache entry. Without gossip, staleness is
   bounded by the TTL lease and by validator-triggered refresh.
   Gossip just makes the window shorter for most requests.
3. **TTL lease (operational hygiene).** Bounds the lifetime of every
   entry regardless of invalidation paths. Lease is generous (~1h) —
   short enough to reclaim memory for unused models and defend
   against unknown failure modes, long enough that it isn't on any
   latency-sensitive path in steady state. **Per-entry ±10% jitter**
   is applied to `expiresAt` so a burst of populates (cluster
   restart, warm-up) does not produce a correlated expiry stampede
   an hour later.

**What's deliberately not present.** No epoch counter, no
populate-during-invalidation race guard: staleness is not a
correctness problem under the self-healing model, so we avoid the
complexity of preventing the races I previously worried about. The
worst impact of a populate-race is a stale entry living until the
next gossip-invalidation, the next validator refresh, or TTL
expiry — bounded and self-correcting.

**Gossip topic:** `model.invalidate` (new constant). Payload: small
codec carrying `tenantID` and `ref` bytes.

**Wiring.** `StoreFactory.ModelStore(ctx)` returns
`CachingModelStore{inner: pluginModelStore, broadcaster: ..., clock:
..., lease: ...}`. Call sites see `spi.ModelStore`; the validator
path obtains `RefreshAndGet` via optional-interface type assertion.
Single-node deployments pass `nil` for the broadcaster; gossip stays
off, TTL + refresh still give correctness.

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
- **Commutativity property tests:** the generator varies over
  **three axes** — `(kind1, kind2) × path-relationship ×
  payload-variation`, where path-relationship ∈
  `{disjoint, equal, prefix}` (parent/child, ancestor/descendant,
  sibling) and payload-variation includes same-value, set-member
  overlap, and disjoint value sets. For every sample,
  `Apply(Apply(b, d1), d2) ≡ Apply(Apply(b, d2), d1)`. Path overlap
  is where the subtle bugs hide — `add_property /p` composed with
  `add_enum_value /p/enum` is a case the earlier draft's kind-pair
  axis alone would miss.
- **Validation-monotonicity property tests:** for every op-kind `k`,
  every document that validates against some base `B` must also
  validate against `Apply(B, d)` for any delta `d` of kind `k`.
  Prevents a tightening op sneaking into the catalog.
- **Extend-completeness test:** for every classified output of
  `schema.Extend` (enumerated at plan-time per §9), `Diff` must be
  able to express the change as a `SchemaDelta`. A change that
  `Extend` permits but `Diff` cannot encode — or a change that
  `Extend` permits but violates monotonicity — is a design bug; the
  test fails until either the op catalog grows to cover it or
  `Extend` is constrained to not produce it.

**Integration — concurrent update regression (`plugins/postgres`):**

- RED reproducer: N=8 concurrent `UpdateEntity` calls on distinct
  entities of a `ChangeLevel`-enabled model, asserting all N commit.
- GREEN after fix: same test, no `spi.ErrConflict` surfaces.

**Integration — cache + self-healing (`internal/cluster/modelcache`):**

- Two decorators sharing a fake broadcaster. `ExtendSchema` on
  decorator A drops the entry on decorator B.
- Cache admits any `LOCKED` descriptor regardless of `ChangeLevel`;
  `UNLOCKED` bypasses.
- **TTL lease expiry** drops an entry once `clock.Now() > expiresAt`;
  next `Get` repopulates.
- **Canonical read-side self-healing scenario** (end-to-end integration):
  1. Node A commits an `ExtendSchema` adding `newField` and broadcasts
     invalidation.
  2. Node B is constructed with gossip disconnected, so it does not
     receive the invalidation. Its cache still holds the pre-extension
     descriptor.
  3. A search filtering on `newField` arrives at Node B. First
     validation fails with the "unknown schema element" class.
  4. Handler calls `RefreshAndGet`; sees the committed extension;
     revalidates; serves the query.
  5. Same test with the extension NOT committed: refresh sees the same
     schema; the error surfaces to the caller as legitimate.
- **Refresh bounds.** Refresh fires only on the unknown-element error
  class; other validation failures (type, range, pattern) do not
  trigger a refetch. At most one refresh per handler invocation.

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
- **SetChangeLevel during in-flight ExtendSchema** (accepted policy
  per §2). An extension under the old level may commit against a now-
  tightened level. Commit-bound visibility preserves the audit trail;
  subsequent reads see the schema consistent with at-the-time
  authority. Documented in `CONSISTENCY.md` as accepted behaviour,
  not a bug to guard against.
- **Extension log growth.** Nothing caps how large a single delta can
  be, nor the lifetime-total count of deltas on a `LOCKED` model.
  `Unlock` drains the log, but `Unlock` requires all live data to be
  soft-deleted — for long-lived high-ingestion models it may never
  fire. Savepoints keep read cost bounded (§4.1) but do not reclaim
  row count. Log growth is therefore bounded by application-level
  `Unlock` cadence, which is a deployment concern, not a storage
  concern. A compaction hook (replace the log prefix up to the most
  recent savepoint with a single consolidated row) is a plausible
  later-phase addition; out of scope here.
- **Cross-version log compatibility.** Phase 1 does not guarantee that
  a log written by a cyoda-go version `v(n+1)` with a new op-kind
  can be folded by a running `v(n)` node. Safe rolling upgrades
  require version skew only across versions that share the op
  catalog; adding an op-kind is a coordinated release. Pre-GA this is
  fine; post-GA will need a compatibility story (e.g., op-kind
  advertisement in cluster metadata, or a default-to-refresh fallback
  when an unknown kind is encountered during fold).
- **Gate 6 — resolving vs deferring.** Every item flagged during this
  brainstorm was addressed inline or scoped explicitly out. No silent
  TODOs.

## 9. Open questions for plan-time

- Exact package for `CachingModelStore`: `internal/cluster/modelcache`
  vs. `internal/domain/model/cache` vs. `internal/storage/cache`. Lean
  first because the decorator's cross-cutting concern is the cluster.
- Whether `N = 64` savepoint interval should be a config knob at all in
  Phase 1 or hardcoded. Lean hardcoded for simplicity; surface later.
- Codec for the gossip payload: **reuse the existing cluster dispatch
  codec** unless plan-time profiling shows it's heavyweight for this
  two-field payload. Introducing a second wire format for one topic
  is worse than the (tiny) overhead of the shared codec.
- **Op catalog enumeration** (`internal/domain/model/schema`). The
  final set of op-kinds is derived by
  auditing every code path through `schema.Extend` at every ChangeLevel
  and classifying its output. Plan-time task: produce a matrix of
  (ChangeLevel, input shape → Extend output diff) and confirm each row
  maps to one or more op-kinds. Any unmappable row fails the
  Extend-completeness test and must be resolved (add an op-kind, or
  constrain Extend, or surface the constraint to the user).
