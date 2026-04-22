# Data-Ingestion QA — Initiative Overview

**Status:** Living document. Owns the A/B/C/D decomposition.
**Owner:** Paul Schleger (paul@cyoda.com)
**Created:** 2026-04-21 (consolidated from the 2026-04-20 / 2026-04-21 brainstorming transcripts after Sub-projects A.1 + A.2 shipped)

## 1. Purpose

This document is the single durable record of the **Data-Ingestion QA** initiative's scope decomposition. Each sub-project (A, B, C, D) has its own spec and plan; this document says what each one owns, what it defers, and in what order they are built. It exists because the decomposition was originally established mid-conversation: subsequent specs reference each other by name but no single artifact captured the big picture. Fresh sessions (human or agent) should be able to pick up the initiative from this document alone.

When scope questions arise — "does this belong in B or C?", "why isn't HTTP hardening in A?" — the answer lives here. Sub-project specs may refine their own boundaries, but they must not silently expand into another sub-project's territory without updating this overview.

## 2. The ingestion pipeline

Data ingestion in cyoda-go is the path from an HTTP request body to a durably-stored, fold-replayable entity. It traverses six architecturally-distinct subsystems:

1. **Schema transformation** (`Extend → Diff → Apply`) — widening a model in response to new data shapes.
2. **Importer** (`json/xml → ModelNode` via `importer.Walk`) — classifying leaf values into the `DataType` lattice (numeric families, polymorphism, nesting).
3. **Concurrency** — multi-writer schema races, cross-node schema propagation, cache invalidation, gossip loss.
4. **Plugin persistence** — storage-backend correctness: savepoint semantics, byte-identical fold across backends, extension-log bounds, migration compatibility.
5. **Boundary parsing** — malformed JSON/XML, Unicode edge cases, large payloads, body-size limits, streaming vs buffered decode.
6. **HTTP/gRPC entry** — format dispatch, batch vs single, error surfacing, client-facing status codes.

These subsystems share a QA methodology (property-based tests with deterministic seeds + enumerated fixture catalogs + fuzz at byte-level boundaries) but their failure modes, blast radii, and test strategies are independent. That is why they decompose into separate sub-projects rather than one monolithic suite.

## 3. Scope choice (why full ownership)

Three framings were considered when the initiative began:

- **(Tightest)** Close only the Sub-project-1 class of bug that triggered the audit. Fast but leaves the rest.
- **(Medium)** Harden the schema-ingestion happy path end-to-end (subsystems 1, 2, 4). Keeps boundary parsing and concurrency on existing coverage.
- **(Widest) — CHOSEN.** Full ownership of data-ingestion QA across all six subsystems.

Rationale for the widest scope: this code path is operationally critical and cannot be left to chance. Each sub-project gets its own spec, plan, and PR. Delivery order is sequenced so that each builds on its predecessors' invariants.

## 4. Sub-project decomposition

| Sub-project | Subsystems covered | Status | Spec | Plan |
|---|---|---|---|---|
| **A.1** — Numeric classifier parity with Cyoda Cloud | 2 (classifier only) | ✅ Shipped | [spec](2026-04-21-data-ingestion-qa-subproject-a1-design.md) | [plan](../plans/2026-04-21-data-ingestion-qa-subproject-a1.md) |
| **A.2** — Schema-transformation round-trip coverage | 1 (Extend/Diff/Apply) | ✅ Shipped | [spec](2026-04-21-data-ingestion-qa-subproject-a2-design.md) | [plan](../plans/2026-04-21-data-ingestion-qa-subproject-a2.md) |
| **A.3** — Polymorphic-slot kind conflicts | 1 + 2 (LEAF↔OBJECT↔ARRAY) | ⏸ Tracked — [issue #85](https://github.com/Cyoda-platform/cyoda-go/issues/85) | — | — |
| **B** — Plugin persistence + fold correctness | 4 | 🟡 Next | — | — |
| **C** — Concurrency + multi-node correctness | 3 | ⏳ Pending | — | — |
| **D** — Input boundary hardening | 5 + 6 | ⏳ Pending | — | — |

Sub-project A was split (A.1 → A.2 → A.3) once it became clear that numeric-classifier parity with Cyoda Cloud was a self-contained deliverable that every later sub-project depends on.

### 4.1 Sub-project A.1 — Numeric classifier parity (shipped)

**Owns:** The numeric type lattice and leaf classification. `ClassifyInteger`, `ClassifyDecimal`, `IsAssignableTo`, `CollapseNumeric`, the `Decimal` type, and strict-validate semantics.

**Deferred to later sub-projects:** ChangeLevel-gated extension behavior (A.2), search-predicate semantics for polymorphic fields (out of initiative), plugin-side numeric persistence fidelity (B).

### 4.2 Sub-project A.2 — Schema-transformation round-trip (shipped)

**Owns:** The master invariant `Apply(old, Diff(old, Extend(old, Walk(data), level))) ≡ Extend(old, Walk(data), level)` byte-for-byte, asserted via ~2400 property-test samples and a 44-entry fixture catalog. Invariants I1–I7 plus I1-bis (Diff-nil correspondence). Axis-2 kind matrix with 6 `t.Skip` cells deferred to A.3. Axis-3 ChangeLevel accept/reject semantics. Runtime budget meta-test.

**Deferred:** Polymorphic-slot kind conflicts (A.3). Cross-plugin byte-identical fold (B). Concurrency/durability axes (C). Malformed-input boundary hardening (D).

### 4.3 Sub-project A.3 — Polymorphic-slot kind conflicts (tracked, not scheduled)

**Owns:** The six `t.Skip("polymorphic-slot semantics pending — see issue #85")` cells from A.2's `axis2_kind_matrix_test.go`: LEAF↔OBJECT, LEAF↔ARRAY, OBJECT↔LEAF, OBJECT↔ARRAY, ARRAY↔LEAF, ARRAY↔OBJECT. Will introduce a new polymorphic-slot representation in `ModelNode`, new `SchemaOp` kinds (beyond the current additive catalog), and updated Extend/Diff/Apply semantics.

**Takes as RED spec:** A.2's skipped test cells become failing tests on day one; A.3 ships when they pass. No new test invariants required — existing I1–I7 extend naturally once the data model supports kind polymorphism.

**Not scheduled:** Requires design work before planning (polymorphic-slot representation, op-catalog extension, migration story for existing models). Filed as `issue #85` during A.2.

### 4.4 Sub-project B — Plugin persistence + fold correctness (next)

**Owns:** Plugin-layer storage correctness for every backend in the SPI (`memory`, `postgres`, `sqlite`, `cassandra`). The schema-transformation invariants asserted in A.2 use in-memory `schema.Marshal` byte equality; B asserts that the same invariants hold across the storage boundary.

**Concerns (to be refined in B's brainstorm):**
- Postgres savepoint semantics under `ExtendSchema` races: the first-committer-wins protocol from the model-schema-extensions design.
- Byte-identical fold across backends: given the same extension-log sequence, every backend must produce the same `ModelNode`. No backend may reorder, merge, or re-canonicalize entries on read.
- Extension-log bounds: append-only growth, fold-on-read performance, compaction triggers (if any).
- Migration compatibility: the collapsed-0001 greenfield migration and any future ones.
- Cross-storage atomicity on rejection: if a write fails partway (validate-then-extend-then-persist), no backend leaves itself in a state A.2's invariant I7 would call "mutated".

**Explicitly deferred to C:** Multi-node gossip, cache-staleness under contention, cross-node propagation races. B is single-node correctness; C adds the network.

**Explicitly deferred to D:** Request-layer validation. B assumes well-formed `ModelNode` and `SchemaDelta` inputs.

**Parity test registry:** The `e2e/parity` package is picked up by the out-of-repo Cassandra plugin (`../cyoda-go-cassandra`) on its next dependency update. B's parity additions land in that registry; the Cassandra plugin inherits them automatically.

### 4.5 Sub-project C — Concurrency + multi-node correctness

**Owns:** Multi-writer schema races, cross-node schema propagation, gossip-based cache invalidation, in-process cache staleness detection. Builds on B's single-node correctness and asserts the same invariants hold under contention and across nodes.

**Concerns (to be refined in C's brainstorm):**
- Multiple concurrent `ExtendSchema` calls hitting the same `(modelName, modelVersion)` — does first-committer-wins hold, does the loser see a fresh cache?
- Gossip-loss simulation — if a node misses an invalidation message, when does it recover?
- Cross-node validation consistency — can a read on node B see an entity whose classification depends on a schema version node B hasn't caught up to?
- Cache warming + poisoning — how does a cold node bootstrap its schema cache safely?

**Explicitly deferred to D:** All adversarial byte-level inputs. C's concurrency tests use well-formed payloads.

### 4.6 Sub-project D — Input boundary hardening

**Owns:** The HTTP/gRPC → parse → validate → reject path for adversarial inputs. Malformed JSON/XML, Unicode edge cases (combining marks, surrogate pairs, BOM), oversize bodies, slow-loris streaming, duplicate keys, nesting-depth bombs, batch-vs-single dispatch errors, and error-surface consistency (no stack traces, correct status codes, ticket UUIDs for 5xx).

**Methodology note:** D is the only sub-project where `testing.F` fuzz testing is in scope. A/B/C use property-based tests with deterministic seeds because their state spaces are structured; D's state space is the set of all byte sequences, where fuzz earns its keep.

## 5. Shared methodology

Conventions that every sub-project follows. Captured here so each individual spec doesn't have to restate them.

### 5.1 Property-based testing with deterministic seeds
- Use `math/rand/v2` with PCG seeding. Seeds are explicit, not time-derived.
- Seed discipline: every property test takes a seed parameter, logs it on failure, and can be replayed by passing `-seed=<N>`.
- No `range` over Go maps in determinism-critical paths (map iteration order is randomized). Use `sortedChildNames` or equivalent.

### 5.2 Fixture catalog + axis matrix
- Hand-curated named fixtures complement random generation: they document known edge cases and survive generator refactors.
- Axis matrix tests (kind×change-level, backend×operation, etc.) are exhaustive where the matrix is small (≤ ~64 cells) and sampled where larger.

### 5.3 Skip discipline
- A `t.Skip` is only acceptable if it references a filed tracking issue and the issue is linked in the skip message.
- Skips are short-term scaffolding. A sub-project with live skips is not "done" until the tracking issue has a plan.

### 5.4 Runtime budgets
- Full test suite (unit + integration + E2E + property tests) on CI: **hard-fail at 60 s** per sub-project's property suite. Local advisory target: 45 s.
- A sub-project that slows the aggregate suite by more than its budget must justify it in the spec.

### 5.5 Scope containment — Gate 6
- Issues surfaced mid-sub-project that belong to another sub-project are filed as issues and deferred. They are not silently folded in.
- Gate 6 ("resolve, don't defer") applies to in-scope issues: fix them now. Out-of-scope issues get tracked, not postponed by omission.

## 6. Invariants — cross-sub-project map

Invariants established by each sub-project. Later sub-projects inherit and must not violate earlier ones.

| Invariant | Established in | Name |
|---|---|---|
| Numeric widening is asymmetric per `IsAssignableTo` | A.1 | Widening lattice |
| Strict-validate rejects down-widenings (LONG ↛ INTEGER, DOUBLE ↛ INTEGER) | A.1 | Asymmetric validation |
| `Apply(old, Diff(old, new)) ≡ new` byte-for-byte on additive extensions | A.2 | I1 — Round-trip |
| `Diff(a, b) == nil ⇔ Marshal(a) == Marshal(b)` | A.2 | I1-bis — Diff-nil correspondence |
| Extend is commutative over compatible extensions | A.2 | I2 — Commutativity |
| Extend at level L is monotonically a superset of Extend at stricter L' | A.2 | I3 — Monotonicity |
| Extend and Apply are idempotent | A.2 | I4 — Idempotence |
| N-permutation invariance of extension sequence | A.2 | I5 — Permutation |
| Every non-no-op Extend produces a non-nil Diff with in-catalog ops | A.2 | I6 — Extend-completeness |
| `Extend` rejection does not mutate `old` in memory | A.2 | I7 — Atomicity (in-memory) |
| TBD | B | Cross-plugin byte-identical fold |
| TBD | B | Single-node cross-storage atomicity |
| TBD | C | Cross-node schema consistency |
| TBD | D | Error-surface determinism |

## 7. Open design questions (deferred to sub-project brainstorms)

These questions surface periodically and are **deliberately not answered here** — they belong to the sub-project brainstorm that owns them. Listed so they don't get rediscovered from scratch each time.

- **B** — Does `ExtendSchema` on Postgres retry on savepoint conflict, or surface to the caller?
- **B** — Can two backends diverge on `ModelNode` byte identity if both are "correct" (e.g., map ordering in a JSON serializer)? If yes, is byte equality the right invariant or should we canonicalize before comparison?
- **B** — Extension-log compaction: never, triggered-by-size, triggered-by-lock? (Design decision, not just implementation.)
- **C** — Is gossip loss recoverable from in-process state, or does it require a re-read from authoritative storage?
- **C** — What is the consistency model we commit to externally? Linearizable, read-your-writes, eventual? This wants a written answer before C's spec.
- **D** — Body-size limit: config-driven, per-endpoint, global? Current state is probably "whatever the HTTP server defaults to", which is not a design.

## 8. References

- Original brainstorming: conversation `c1688bdf-b64a-4f1c-b890-36688dd4e47b` in `~/.claude/projects/...`, lines 1659 (subsystem map) and 1711 (Sub-project A scope).
- Model-schema-extensions design: `docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md` — introduced `ExtendSchema` and the append-only schema-extension log that B will exercise.
- Cyoda Cloud reference: `docs/ComparableDataType.kt`, `docs/DataType.kt` — the widening lattice A.1 mirrors.
- `CLAUDE.md` development gates: Gate 1 (TDD), Gate 2 (E2E), Gate 3 (security), Gate 4 (docs), Gate 5 (verify), Gate 6 (resolve-don't-defer).

## 9. Maintenance

Update this document when:
- A sub-project's status changes (pending → next → shipped).
- Scope moves between sub-projects (update the decomposition table and both sub-projects' deferrals).
- A new invariant is added — append to §6.
- An open design question is answered — move it from §7 into the relevant sub-project's spec, delete it here.

Do not update this document for:
- Per-task progress within a sub-project (lives in plan + TodoWrite).
- Sub-project-internal design decisions (lives in that sub-project's spec).
