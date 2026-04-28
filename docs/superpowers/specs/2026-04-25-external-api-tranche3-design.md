# External API Scenario Suite — Tranche 3 Design

- **Issue:** [#120](https://github.com/Cyoda-platform/cyoda-go/issues/120) (tranche 3 of 5)
- **Date:** 2026-04-25
- **Target branch:** `release/v0.6.3`
- **Predecessors:** Tranche 1 (#118 / `6164b82`), Tranche 2 (#119 / `215918f`) on the same release branch.

## 1. Purpose

Implement the next four YAML files of cyoda-cloud's External API Scenario
Dictionary against cyoda-go: `08-workflow-import-export` (6 scenarios),
`09-workflow-externalization` (12), `10-concurrency-and-multinode` (3),
`11-edge-message` (3). 24 new scenarios total.

Tranche 3 introduces one new piece of test infrastructure — a sibling
parity package for cluster-shareable tests — and otherwise extends
existing surfaces with thin Driver helpers.

## 2. Scope

### 2.1 In scope

- 24 new parity test functions across files 08 / 09 / 10 / 11.
- New sibling package `e2e/parity/multinode/` for cluster-shareable
  scenarios (file 10).
- New postgres-backed multi-node fixture at
  `plugins/postgres/multinode_fixture.go`.
- New test entry `e2e/parity/postgres/multinode_test.go` running
  `multinode.AllTests()` against the multi-node fixture.
- Driver vocabulary additions: workflow import/export wrappers,
  edge-message wrappers, possibly a batch-delete client helper.
- Discover-and-compare error-code discipline applied to every
  negative-path scenario in file 09 (timeout, retry, exception).
- Updates to `e2e/externalapi/dictionary-mapping.md` flipping all
  `pending:tranche-3` rows to status-of-record.

### 2.2 Out of scope

- Multi-node fixtures for memory / sqlite backends — physically
  unviable (each subprocess holds its own state). File-10 scenarios
  are postgres-only by construction.
- Multi-node fixture for cassandra backend — lives in
  cyoda-go-cassandra; tracked by issue #35 in that repo.
- Server-side fixes for any `worse`-class divergences discovered in
  file 09 — filed as standalone issues (target v0.7.0), not
  implemented here.
- Edge-message wire-shape harmonisation — `/edge-message` (dictionary)
  vs `/api/message/new/{subject}` (cyoda-go) is a `different_naming_
  same_level` URL drift; tests use cyoda-go's surface, mapping records
  the drift, no server-side rewrite.
- gRPC `joinCalculationMember` exposure on Driver — file 09 scenarios
  exercise the gRPC dispatch indirectly (workflow with externalised
  processor, observed via HTTP entity GET). The compute-test-client
  is the gRPC participant; tests don't drive it directly.

## 3. Architecture (delta from tranche 2)

### 3.1 New sibling package: `e2e/parity/multinode/`

Mirrors the `e2e/parity/externalapi/` pattern from tranche 1 — its own
runtime registry picked up by cluster-capable backends via
blank-import. Package contents:

```
e2e/parity/multinode/
├── registry.go        # type NamedTest, func Register(...), func AllTests()
├── fixture.go         # type MultiNodeFixture interface
└── concurrency.go     # 3 Run* for file 10, init() Register
```

`MultiNodeFixture` interface (intentionally minimal):

```go
type MultiNodeFixture interface {
    BaseURLs() []string                    // one URL per node
    NodeCount() int
    NewTenant(t *testing.T) parity.Tenant  // shared across nodes
}
```

`parity.BackendFixture`, `parity.NamedTest`, `parity.Register` are
unchanged. The new package is a sibling, not an extension.

Run* functions construct one `*driver.Driver` per node URL via
`driver.NewRemote(t, baseURL, tenant.Token)`, then exercise cross-node
behavior.

### 3.2 Postgres multi-node fixture

`plugins/postgres/multinode_fixture.go` exports:

```go
func MustSetupMultiNode(t *testing.T, n int) (multinode.MultiNodeFixture, func())
```

Behavior:
- Boot one Postgres testcontainer.
- Launch N cyoda-go subprocesses, each pointed at the same DB,
  configured for cluster operation (gossip + seed-node bootstrap per
  the existing `internal/cluster/` infrastructure).
- Returned fixture exposes the N base URLs and a tenant-mint helper
  that produces JWTs valid across all nodes.
- Cleanup tears down the N subprocesses and the testcontainer.

The first node runs the auto-migration; subsequent nodes connect to an
already-migrated schema.

### 3.3 Cluster test entry

`e2e/parity/postgres/multinode_test.go`:

```go
package postgres_test

import (
    "testing"

    _ "github.com/cyoda-platform/cyoda-go/e2e/parity/multinode" // register tests
    "github.com/cyoda-platform/cyoda-go/e2e/parity/multinode"
    "github.com/cyoda-platform/cyoda-go/plugins/postgres"
)

func TestMultiNode(t *testing.T) {
    if testing.Short() { t.Skip("multi-node requires Docker testcontainer") }
    fix, cleanup := postgres.MustSetupMultiNode(t, 3)
    defer cleanup()
    for _, nt := range multinode.AllTests() {
        t.Run(nt.Name, func(t *testing.T) { nt.Fn(t, fix) })
    }
}
```

Memory and sqlite have no equivalent test entry → file-10 scenarios
never run on those backends. The cassandra plugin adds an analogous
entry (cyoda-go-cassandra#35).

### 3.4 Driver vocabulary additions

All thin pass-throughs to existing parity-client methods:

| Driver method | Underlying client method | Scenarios |
|---|---|---|
| `ImportWorkflow(name, version, body string) error` | `c.ImportWorkflow(t, ...)` | 08 (all) |
| `ExportWorkflow(name, version) (json.RawMessage, error)` | `c.ExportWorkflow(t, ...)` | 08 (all) |
| `CreateMessage(subject, payload string) (string, error)` | `c.CreateMessage(t, ...)` | 11 (all) |
| `GetMessage(id string) (map[string]any, error)` | `c.GetMessage(t, ...)` | 11 (all) |
| `DeleteMessage(id string) error` | `c.DeleteMessage(t, ...)` | 11/02 |
| `DeleteMessages(ids []string) ([]string, error)` | NEW `c.DeleteMessages(t, ...)` | 11/03 |

The `DeleteMessages` (batch) client helper is added if the existing
client doesn't already have one — verify in Phase 0.

File 09 needs no new Driver helpers; it composes existing model +
workflow + entity vocabulary plus the gRPC-side compute-test-client
that's already running as part of the parity fixture.

## 4. Per-file scope notes

### 4.1 File 08 — workflow import/export (6 scenarios)

All happy-path. Scenarios cover:
- `wf-imp-exp/01-import-then-export-round-trip`
- `wf-imp-exp/02-default-fields-roundtrip`
- `wf-imp-exp/03-advanced-criteria-and-processors`
- `wf-imp-exp/04-replace-mode`
- `wf-imp-exp/05-activate-mode`
- `wf-imp-exp/06-merge-mode`

Driver gains `ImportWorkflow`/`ExportWorkflow` wrappers; tests register
in the standard parity registry. Existing
`RunWorkflowImportExport` parity test in `e2e/parity/workflow.go`
exercises the spine; tranche-3 tests focus on the per-mode semantics.

Expected gap count: 0–1.

### 4.2 File 09 — externalised processors / criteria (12 scenarios)

All scenarios depend on the parity fixture's compute-test-client being
running and joined to the gRPC stream. The fixture already provisions
this (`fixtureutil.LaunchCyodaAndCompute`). Tests:

1. Set up a workflow whose state has an externalised processor or
   criteria reference (sync / async-same-tx / async-new-tx mode).
2. Use `fixture.ComputeTenant(t)` (parity API, exists) for tests that
   need processor dispatch.
3. Create an entity, observe via HTTP that the processor or criteria
   ran (entity state changed, transition fired, etc.).
4. For exception paths: assert the entity remains unchanged or the
   transaction rolled back, per the dictionary's expectation.

Discover-and-compare on every negative path. The cyoda-go workflow
engine emits errors via the same code path that #129/#130 surfaced —
expect ~2-4 `worse`-class divergences with new tracking issues filed
during implementation.

Expected gap count: 2–4.

### 4.3 File 10 — concurrency and multi-node (3 scenarios)

All run via `e2e/parity/multinode/` against the postgres-backed
multi-node fixture:

1. `multi/01-load-balancer-routing` — distribute requests across N
   nodes; verify each node receives traffic and responses are
   functionally equivalent.
2. `multi/02-cross-node-consistency` — write to node A, read from
   node B; verify the read sees the write (post-tx-commit window
   covers any gossip lag).
3. `multi/03-parallel-update-serialisation` — N concurrent writers
   to the same entity from different nodes; final state reflects
   serialised application of all updates.

These scenarios use `time.Sleep` only as last resort; preferred is
post-write tx-token-driven consistency wait.

Expected gap count: 0–1 (cluster bootstrap or gossip timing
imperfections may surface).

### 4.4 File 11 — edge-message (3 scenarios)

cyoda-go path: `/api/message/new/{subject}`, `/api/message/{id}`.
Dictionary path: `/edge-message`, `/edge-message/{id}`.
Different naming, same surface. Tests use cyoda-go's path via
existing parity client; mapping doc records the URL drift.

The dictionary's POST body shape (`{header, metaData, body}`) maps
into cyoda-go's `subject` (path) + `payload` (body). The
`correlationId`, `userId`, `replyTo`, `recipient` header fields are
encoded inside cyoda-go's payload. Verify the per-test wire shape
empirically in Phase 0; if cyoda-go cannot round-trip the full
header set, file an issue + skip the affected scenarios.

Scenarios:
1. `edge-msg/01-save-single` — POST + GET round-trip.
2. `edge-msg/02-delete-single` — POST + DELETE + GET-404.
3. `edge-msg/03-delete-collection` — 2× POST + DELETE batch + GET-404
   on each.

Expected gap count: 0–2 (depends on header round-trip parity and
batch-delete support).

## 5. Phase 0 — gate before implementation

Before per-file implementation:

1. **Edge-message wire-shape check.** Manually post a test message
   with a header to cyoda-go's `/api/message/new/{subject}`. Inspect
   the response and a subsequent GET. Verify whether all the
   dictionary's header fields (correlationId, userId, replyTo,
   recipient) round-trip. File issue + plan to skip if any are lost.
2. **Edge-message batch delete check.** Verify cyoda-go's existing
   `DeleteMessages` handler (per `internal/domain/messaging/handler.go`
   line 221) accepts a batch and returns the deleted ids. Add the
   client helper if absent.
3. **Multi-node bootstrap check.** Confirm `internal/cluster/`
   gossip + seed-node logic is production-enabled (not behind a
   `CYODA_CLUSTER_ENABLED` flag with a default-off). If gated, the
   multi-node fixture flips the flag in launch env.

These probes are quick and inform the per-file plan tasks.

## 6. Discover-and-compare carry-over

Same protocol as tranche 2 §4.2:
- `equiv_or_better` → tighten + comment.
- `different_naming_same_level` → tighten + comment cloud equivalent.
- `worse` → file issue + `t.Skip("pending #N")`, keep test body.

Tranche-3-specific likely outcomes:
- File 09 exception paths: cyoda-go's `WORKFLOW_FAILED` lineage (now
  partially tightened to `TRANSITION_NOT_FOUND` per tranche-2's #128
  fix) may still emit generic codes for retry-exhausted, async-tx-
  rollback, etc. Expect ~2-4 new issues.

## 7. Testing strategy

- TDD per Driver/client helper, per Run*, per multi-node fixture
  method.
- Each file lands as one or two commits; phase 0 gate is its own
  commit.
- `make test-all` green at end.
- `go test -race ./...` one-shot before PR.

## 8. Acceptance

From issue #120 plus this design:

- Files 08 / 09 / 11 scenarios green or skipped with tracked issue
  on memory / sqlite / postgres parity backends.
- File 10 scenarios green on postgres via the new
  `TestMultiNode` entry.
- `e2e/parity/multinode/` package compiles and registers without
  requiring `parity.BackendFixture` or `parity.NamedTest` changes.
- `dictionary-mapping.md` fully up to date for files 08/09/10/11.
- Every `t.Skip`-marked scenario references a tracked issue.

## 9. Workflow

Per `CLAUDE.md` feature workflow:

1. Worktree on `feat/issue-120-external-api-tranche3` off
   `release/v0.6.3` ✓ done
2. Brainstorming ✓ done
3. This design doc
4. `superpowers:writing-plans` → executable plan
5. `superpowers:subagent-driven-development` → TDD implementation
6. `superpowers:verification-before-completion`
7. `superpowers:requesting-code-review`
8. `antigravity-bundle-security-developer:cc-skill-security-review`
9. PR targeting `release/v0.6.3` with `Closes #120` in body

## 10. Cross-repo coordination

- Cyoda-go-cassandra#35 (just filed) — tracks cassandra-side
  multi-node fixture + blank-import for next dep bump.
- Cyoda-go-cassandra#34 (existing) — externalapi blank-import,
  unchanged.

When tranche-3 PR is opened, mention #35 in the PR body for
discoverability.
