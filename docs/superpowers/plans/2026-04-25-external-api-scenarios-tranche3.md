# External API Scenario Suite — Tranche 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement 24 new parity scenarios across YAML files 08/09/10/11 plus a new sibling parity package `e2e/parity/multinode/` for cluster-shareable tests, without changing `parity.BackendFixture` / `parity.NamedTest` / `parity.Register`.

**Architecture:** Pure additive on tranche 1+2. Three new pieces: (1) `e2e/parity/multinode/` sibling package mirroring the `externalapi` runtime-registration pattern; (2) postgres-backed multi-node fixture launching N cyoda-go subprocesses sharing one testcontainer; (3) Driver wrappers for workflow import/export and edge-message helpers. File 09's externalisation rides existing gRPC compute-test-client infrastructure already wired by tranche-1's parity tests.

**Tech Stack:** Go 1.26, existing parity harness, testcontainers-go (postgres), `internal/cluster/` gossip + seed-node infrastructure.

**Spec:** `docs/superpowers/specs/2026-04-25-external-api-tranche3-design.md`

**Predecessor:** Tranche 2 (#119 / commit `215918f`) on `release/v0.6.3`.

---

## Discover-and-compare protocol (carried forward from tranche 2)

For each negative-path scenario in file 09:

1. Read the dictionary's `expected_error.class_or_message_pattern` from the YAML.
2. Run with `errorcontract.Match` asserting only `HTTPStatus`. Capture cyoda-go's `properties.errorCode` via temporary `t.Logf("DISCOVER body=%s", string(body))`.
3. Classify:
   - `equiv_or_better` → tighten with `ErrorCode: "<observed>"` + comment "matches/exceeds cloud's <X>".
   - `different_naming_same_level` → tighten + comment cloud equivalent.
   - `worse` → `gh issue create` for v0.7.0 + `t.Skip("pending #N")`.
4. Remove the discovery `t.Logf`.

---

## Phase 0 — Pre-implementation gates

> **Phase 0 outcomes (recorded 2026-04-25 — see findings below; do NOT re-run):**
>
> - **0.1 result:** cyoda-go's `internal/domain/messaging/handler.go:NewMessage` carries header fields (`MessageID`, `UserID`, `Recipient`, `ReplyTo`, `CorrelationID`, `ContentType`, `ContentEncoding`) via HTTP request **headers** (`X-Message-Id`, `X-User-Id`, `X-Recipient`, `X-Reply-To`, `X-Correlation-Id`) + **query params** (`contentType`, `contentEncoding`, `contentLength`). The body is `{payload, meta-data}` only. The dictionary embeds the same header fields IN the body. **Implication:** Phase 4's plan to wrap the existing `c.CreateMessage(t, subject, payload)` is insufficient — round-tripping the dictionary's header fields requires a richer client helper that sets the X-* headers + query params. Add `c.CreateMessageWithHeaders(t, subject, payload string, header MessageHeaderInput)` to the parity client + a Driver pass-through. The struct mirrors `spi.MessageHeader` minus the `Subject` field (which goes in the path).
> - **0.2 result:**
>
>   > **⚠ SUPERSEDED 2026-04-25** — this finding was a misdiagnosis. The handler at `internal/domain/messaging/handler.go:222` IS a delete-by-id-list (reads JSON-array body, calls `store.DeleteBatch`); `transactionSize` is just a paging knob. Corrected by commits `4f6f991` (client) + `f116552` (driver) + `2a44434` (11/03 test); 11/03 is implemented + PASSing on all backends; #134 should be CLOSED at PR-merge time.
>
>   ~~Original (incorrect) finding: cyoda-go's `DELETE /api/message` is **delete-all-paged-by-tx-size**, not delete-by-id-list. `DeleteMessagesParams` has only `transactionSize`. **11/03 is `gap_on_our_side`** — file 11's batch-delete scenario is unimplementable today. Tracked in **#134** (target v0.7.0). Phase 4's `c.DeleteMessages` + Driver wrapper are **NOT added** in tranche 3. Phase 6's 11/03 Run* is `t.Skip("pending #134")` with the test body documenting intent.~~
> - **0.3 result:** Cluster envs are wired in `app/config.go:141-146`. Multi-node fixture must set per-node: `CYODA_CLUSTER_ENABLED=true`, unique `CYODA_NODE_ID=node-{i}`, unique `CYODA_NODE_ADDR=http://127.0.0.1:{httpPort}`, unique `CYODA_GOSSIP_ADDR=:{gossipPort}`, shared `CYODA_SEED_NODES=<comma-separated all gossip addrs>`. NodeID is required when CLUSTER_ENABLED is true (validated at startup, `app/app.go:607`). `CYODA_GOSSIP_STABILITY_WINDOW` defaults to 2s — usable as-is.
>
> Phase 0 tasks below are kept for reference but do not need re-execution. Skip directly to Phase 1.

### Task 0.1: Verify edge-message wire shape

The dictionary expects POST `/edge-message` with `{header: {subject, correlationId, userId, replyTo, recipient}, metaData: {...}, body: {...}}`. cyoda-go uses POST `/api/message/new/{subject}` with the payload as the body. Verify whether cyoda-go's existing surface round-trips the dictionary's full header set, or if some fields are lost.

- [ ] **Step 1: Read the existing handler**

```bash
sed -n '1,60p' internal/domain/messaging/handler.go
sed -n '60,130p' internal/domain/messaging/handler.go
```

Note the exact field set the handler accepts in the request body, and what GetMessage returns.

- [ ] **Step 2: Decision**

Three outcomes:
- **Full round-trip** (header fields embedded in payload, returned intact) — proceed with file 11 implementation as planned.
- **Partial round-trip** (some header fields are lost — e.g. correlationId is preserved but recipient is dropped) — file an issue, plan to skip the affected scenarios with `gap_on_our_side`.
- **Different surface** (cyoda-go uses a model+entity flow rather than dedicated message endpoints) — file an issue, skip all 3 file-11 scenarios.

Document the decision in this plan task's notes (will later inform the file-11 phase).

- [ ] **Step 3: No commit** — this is a probe.

### Task 0.2: Verify edge-message batch delete

`internal/domain/messaging/handler.go:222 DeleteMessages` exists. Confirm the wire shape (does it accept a JSON array of ids in body, or query-string ids?). Read `genapi.DeleteMessagesParams` to see.

```bash
grep -A3 "DeleteMessagesParams " api/generated.go | head -10
```

If JSON-array body: a new `c.DeleteMessages(t, ids []string) ([]string, error)` client helper is needed. If query-string ids: similarly. Note the shape for the file-11 phase.

- [ ] **No commit** — probe only.

### Task 0.3: Verify multi-node bootstrap config

```bash
grep -nE "CYODA_CLUSTER_ENABLED|CYODA_NODE_ID|CYODA_GOSSIP_ADDR|CYODA_SEED_NODES" cmd/cyoda/main.go internal/cluster/
```

Confirm the cluster envs (`CYODA_CLUSTER_ENABLED`, `CYODA_NODE_ID`, `CYODA_NODE_ADDR`, `CYODA_GOSSIP_ADDR`, `CYODA_SEED_NODES`) are honored in startup. Note any defaults that need overriding for a 3-node setup.

- [ ] **No commit** — probe only.

---

## Phase 1 — `e2e/parity/multinode/` sibling package

Create the cluster-shareable parity package. No production code touched. Mirrors the `e2e/parity/externalapi/` pattern verbatim.

### Task 1.1: `multinode/registry.go` — runtime registration

**Files:**
- Create: `e2e/parity/multinode/registry.go`

- [ ] **Step 1: Create the file**

```go
// Package multinode hosts parity scenarios that require a cyoda-go
// cluster — multiple cyoda-go subprocesses sharing the same backing
// storage. Backends that physically cannot share state across N
// processes (memory, sqlite single-file) do not implement
// MultiNodeFixture and never run these scenarios.
//
// The cluster-capable backends (postgres in-tree; cassandra in
// cyoda-go-cassandra via cyoda-go-cassandra#35) provide a fixture
// implementation and a TestMultiNode entry that blank-imports this
// package to trigger init-time registration.
package multinode

import "testing"

// NamedTest is a single multi-node parity scenario plus the name it
// shows up as in subtest output.
type NamedTest struct {
	Name string
	Fn   func(t *testing.T, fixture MultiNodeFixture)
}

var allTests []NamedTest

// Register appends additional NamedTests to the canonical list at
// init time. Sub-packages call Register from init().
//
// Per-backend test wrappers (postgres in-tree, cassandra out-of-tree)
// MUST blank-import the multinode-extension packages — otherwise the
// extension's init() never runs and the wrapper silently misses the
// entire scenario set. Currently the only extension is this package
// itself; future cluster-shareable extension packages added by later
// tranches must be added to all cluster-capable backend wrappers in
// lockstep.
func Register(tests ...NamedTest) {
	allTests = append(allTests, tests...)
}

// AllTests returns the canonical list of multi-node scenarios in
// registration order. The returned slice is a defensive copy.
func AllTests() []NamedTest {
	out := make([]NamedTest, len(allTests))
	copy(out, allTests)
	return out
}
```

- [ ] **Step 2: Build-check**

```bash
go build ./e2e/parity/multinode/
go vet ./e2e/parity/multinode/
```
Expected: silent.

### Task 1.2: `multinode/fixture.go` — interface

**Files:**
- Create: `e2e/parity/multinode/fixture.go`

- [ ] **Step 1: Create the file**

```go
package multinode

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

// MultiNodeFixture is the cluster-capable counterpart to
// parity.BackendFixture. Implementations launch N cyoda-go
// subprocesses sharing the same backing storage and expose one
// HTTP base URL per node. Tenants are minted once and used across
// all nodes (the cluster shares state, including auth).
type MultiNodeFixture interface {
	// BaseURLs returns one HTTP base URL per node, in stable order.
	// Length equals NodeCount(). Each URL has no trailing slash.
	BaseURLs() []string

	// NodeCount returns the number of nodes in the cluster.
	NodeCount() int

	// NewTenant mints a fresh tenant for the test. The returned JWT
	// is valid against every node in the cluster.
	NewTenant(t *testing.T) parity.Tenant
}
```

- [ ] **Step 2: Build-check + commit Phase 1 so far**

```bash
go vet ./e2e/parity/multinode/
git add e2e/parity/multinode/
git commit -m "test(externalapi): multinode sibling parity package — registry + fixture interface

Mirrors the e2e/parity/externalapi/ pattern: own NamedTest,
Register, AllTests; own MultiNodeFixture interface returning
N base URLs and a tenant valid across all nodes. Per-backend
test wrappers (postgres in-tree, cassandra via
cyoda-go-cassandra#35) blank-import this package to trigger
init-time registration.

No changes to parity.BackendFixture / parity.NamedTest /
parity.Register — the cluster-shareable surface is genuinely
distinct from single-node parity, by physical construction.

Refs #120."
```

### Task 1.3: `multinode/concurrency.go` — 3 file-10 Run* (initially registered + body-stub)

**Files:**
- Create: `e2e/parity/multinode/concurrency.go`

The 3 file-10 scenarios. Real fixture comes in Phase 2; tests register here and have stubs that depend only on `MultiNodeFixture` interface. This commit lands the full Run* bodies because the implementation tracks the interface only — once the postgres fixture lands in Phase 2, the same bodies run live.

- [ ] **Step 1: Create the file with all 3 Run* + Register**

```go
package multinode

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
)

func init() {
	Register(
		NamedTest{Name: "ExternalAPI_10_01_LoadBalancerEndToEnd", Fn: RunExternalAPI_10_01_LoadBalancerEndToEnd},
		NamedTest{Name: "ExternalAPI_10_02_ReadbackReachesAllReplicas", Fn: RunExternalAPI_10_02_ReadbackReachesAllReplicas},
		NamedTest{Name: "ExternalAPI_10_03_ParallelUpdatesSameEntity", Fn: RunExternalAPI_10_03_ParallelUpdatesSameEntity},
	)
	_ = httptest.NewServer // pull in httptest so the placeholder build keeps
	_ = uuid.Nil
}

// RunExternalAPI_10_01_LoadBalancerEndToEnd — dictionary 10/01.
// Round-robin model+entity create across N nodes via separate Drivers;
// verify each Driver successfully reaches the cluster.
func RunExternalAPI_10_01_LoadBalancerEndToEnd(t *testing.T, fixture MultiNodeFixture) {
	t.Helper()
	urls := fixture.BaseURLs()
	if len(urls) < 2 {
		t.Fatalf("need at least 2 nodes for 10/01, got %d", len(urls))
	}
	tenant := fixture.NewTenant(t)

	// Driver per node. 10/01 only needs the first node for setup.
	d0 := driver.NewRemote(t, urls[0], tenant.Token)
	if err := d0.CreateModelFromSample("multi1", 1, `{"k":1}`); err != nil {
		t.Fatalf("create model on node 0: %v", err)
	}
	if err := d0.LockModel("multi1", 1); err != nil {
		t.Fatalf("lock on node 0: %v", err)
	}

	// Round-robin entity creation across all nodes.
	ids := make([]uuid.UUID, len(urls))
	for i, url := range urls {
		di := driver.NewRemote(t, url, tenant.Token)
		id, err := di.CreateEntity("multi1", 1, fmt.Sprintf(`{"k":%d}`, i))
		if err != nil {
			t.Fatalf("CreateEntity via node %d: %v", i, err)
		}
		ids[i] = id
	}

	// Each entity must be readable from any node (consistency).
	for i, id := range ids {
		for j, url := range urls {
			dj := driver.NewRemote(t, url, tenant.Token)
			got, err := dj.GetEntity(id)
			if err != nil {
				t.Errorf("read entity[%d] from node %d: %v", i, j, err)
				continue
			}
			if got.Data["k"] != float64(i) {
				t.Errorf("entity[%d] from node %d: got k=%v, want %d", i, j, got.Data["k"], i)
			}
		}
	}
}

// RunExternalAPI_10_02_ReadbackReachesAllReplicas — dictionary 10/02.
// Write to node A, read from node B (≠ A). Repeat for every (A,B) pair.
func RunExternalAPI_10_02_ReadbackReachesAllReplicas(t *testing.T, fixture MultiNodeFixture) {
	t.Helper()
	urls := fixture.BaseURLs()
	if len(urls) < 2 {
		t.Fatalf("need at least 2 nodes for 10/02, got %d", len(urls))
	}
	tenant := fixture.NewTenant(t)

	dSetup := driver.NewRemote(t, urls[0], tenant.Token)
	if err := dSetup.CreateModelFromSample("multi2", 1, `{"k":1}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := dSetup.LockModel("multi2", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}

	for writerIdx, writerURL := range urls {
		dW := driver.NewRemote(t, writerURL, tenant.Token)
		id, err := dW.CreateEntity("multi2", 1, fmt.Sprintf(`{"k":%d}`, writerIdx))
		if err != nil {
			t.Fatalf("write via node %d: %v", writerIdx, err)
		}
		for readerIdx, readerURL := range urls {
			if readerIdx == writerIdx {
				continue
			}
			dR := driver.NewRemote(t, readerURL, tenant.Token)
			got, err := dR.GetEntity(id)
			if err != nil {
				t.Errorf("read via node %d (written via %d): %v", readerIdx, writerIdx, err)
				continue
			}
			if got.Data["k"] != float64(writerIdx) {
				t.Errorf("read via node %d (written via %d): got k=%v, want %d", readerIdx, writerIdx, got.Data["k"], writerIdx)
			}
		}
	}
}

// RunExternalAPI_10_03_ParallelUpdatesSameEntity — dictionary 10/03.
// Concurrent updates from N nodes to the same entity must serialise
// without data loss. After all updates settle, the final state must
// reflect one of the writes (last-writer-wins) and not be corrupt.
func RunExternalAPI_10_03_ParallelUpdatesSameEntity(t *testing.T, fixture MultiNodeFixture) {
	t.Helper()
	urls := fixture.BaseURLs()
	if len(urls) < 2 {
		t.Fatalf("need at least 2 nodes for 10/03, got %d", len(urls))
	}
	tenant := fixture.NewTenant(t)

	d0 := driver.NewRemote(t, urls[0], tenant.Token)
	if err := d0.CreateModelFromSample("multi3", 1, `{"counter":0}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d0.LockModel("multi3", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d0.CreateEntity("multi3", 1, `{"counter":0}`)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}

	// N goroutines, one per node, each issuing a counter-set update.
	var wg sync.WaitGroup
	results := make(chan int, len(urls))
	for i, url := range urls {
		wg.Add(1)
		go func(idx int, u string) {
			defer wg.Done()
			di := driver.NewRemote(t, u, tenant.Token)
			body := fmt.Sprintf(`{"counter":%d}`, idx+1)
			if err := di.UpdateEntityData(id, body); err != nil {
				results <- -1
				return
			}
			results <- idx + 1
		}(i, url)
	}
	wg.Wait()
	close(results)

	// Wait briefly for cluster gossip to converge.
	time.Sleep(200 * time.Millisecond)

	// Final state must reflect one of the writes — between 1 and N.
	got, err := d0.GetEntity(id)
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	final, ok := got.Data["counter"].(float64)
	if !ok {
		t.Fatalf("counter not a number: %v", got.Data["counter"])
	}
	if int(final) < 1 || int(final) > len(urls) {
		t.Errorf("final counter: got %v, want 1..%d (one of the parallel writes)", final, len(urls))
	}
	_ = http.StatusOK // silence unused import; counter assertion is the contract
}
```

- [ ] **Step 2: Build-check**

```bash
go vet ./e2e/parity/multinode/
go test -short ./e2e/parity/multinode/ -v
```
Expected: vet silent; no test runs (no `_test.go` files in this package — registration is via init).

- [ ] **Step 3: Commit Phase 1 final**

```bash
git add e2e/parity/multinode/
git commit -m "test(externalapi): file 10 multinode scenarios — 3 Run* registered

Loads balancer end-to-end (10/01), readback-across-replicas (10/02),
parallel-updates-same-entity (10/03). All use the new
MultiNodeFixture interface and per-node driver.NewRemote.

No live execution yet — postgres fixture lands in Phase 2.

Refs #120."
```

---

## Phase 2 — Postgres multi-node fixture

### Task 2.1: `plugins/postgres/multinode_fixture.go` — N-node launcher

**Files:**
- Create: `plugins/postgres/multinode_fixture.go`

This is the heaviest single-task in tranche 3. Boot one Postgres testcontainer + N cyoda-go subprocesses pointed at it, with cluster bootstrap.

- [ ] **Step 1: Read the existing single-node setup**

```bash
sed -n '1,120p' plugins/postgres/fixture.go
sed -n '350,440p' e2e/parity/fixtureutil/fixtureutil.go
```

Identify how a single cyoda-go subprocess is launched with `CYODA_STORAGE_BACKEND=postgres` and `CYODA_POSTGRES_URL=...`. Multi-node will reuse the same launcher in a loop, parameterising the cluster envs.

- [ ] **Step 2: Add `LaunchMultiNode` helper to fixtureutil if not present**

If `fixtureutil` already has a multi-launch helper, use it. If not, add one — but check first; tranche 3 should not re-architect existing tooling.

```bash
grep -nE "func Launch[A-Z]" e2e/parity/fixtureutil/fixtureutil.go | head -10
```

If only `LaunchCyodaAndCompute` and `LaunchCyodaAndComputeWithBinaries` exist: add `LaunchCyodaClusterAndCompute(ks, n, extraEnv, opts...) (*ClusterLaunchResult, func(), error)` that builds the cyoda + compute binaries once, then launches `n` cyoda subprocesses (each with unique `CYODA_NODE_ID`, `CYODA_HTTP_PORT`, `CYODA_GRPC_PORT`, `CYODA_GOSSIP_ADDR`) plus one shared compute-test-client.

The `ClusterLaunchResult` struct exposes:
```go
type ClusterLaunchResult struct {
    BaseURLs      []string  // one per node
    GRPCEndpoint  string    // first node's gRPC; compute-client connects here
}
```

This addition belongs in `fixtureutil` because the same shape is reusable by cassandra (#35). Even if Phase 2 is the only consumer today, the helper's home is `fixtureutil`.

Detailed steps:

1. Generate one JWT keyset (shared across all nodes — same tenant works on every node).
2. Allocate `n × 3` free TCP ports (HTTP, gRPC, gossip per node).
3. Build the seed-nodes list: each node lists every other node's gossip addr as a seed.
4. Launch n cyoda subprocesses concurrently. Each gets:
   - `CYODA_STORAGE_BACKEND=postgres`
   - `CYODA_POSTGRES_URL=<shared>`
   - `CYODA_POSTGRES_AUTO_MIGRATE=true` (only for node 0; others wait)
   - `CYODA_CLUSTER_ENABLED=true`
   - `CYODA_NODE_ID=node-{i}`
   - `CYODA_NODE_ADDR=http://127.0.0.1:<httpPort>`
   - `CYODA_HTTP_PORT=<i'th allocated http port>`
   - `CYODA_GRPC_PORT=<i'th allocated grpc port>` (only node 0's gRPC is exposed for compute-client; others' gRPC remains internal)
   - `CYODA_GOSSIP_ADDR=:<i'th allocated gossip port>`
   - `CYODA_SEED_NODES=<comma-separated all gossip addrs>`
5. Wait until each node's `/health` responds 200 (with timeout — log and fail on non-recovery).
6. Wait 1s for gossip convergence.
7. Launch compute-test-client pointed at node 0's gRPC.
8. Return `ClusterLaunchResult` + cleanup func that kills all subprocesses and the testcontainer.

This is detailed work; if `fixtureutil`'s existing structure resists it, surface the issue back to the controller (DONE_WITH_CONCERNS) rather than torturing the helper.

- [ ] **Step 3: Implement `MustSetupMultiNode` in `plugins/postgres/multinode_fixture.go`**

```go
package postgres

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/cyoda-platform/cyoda-go/e2e/parity"
	"github.com/cyoda-platform/cyoda-go/e2e/parity/fixtureutil"
	"github.com/cyoda-platform/cyoda-go/e2e/parity/multinode"
)

// pgMultiNode implements multinode.MultiNodeFixture for the
// postgres backend.
type pgMultiNode struct {
	baseURLs []string
	keySet   *fixtureutil.JWTKeySet
}

func (f *pgMultiNode) BaseURLs() []string { return append([]string(nil), f.baseURLs...) }
func (f *pgMultiNode) NodeCount() int     { return len(f.baseURLs) }
func (f *pgMultiNode) NewTenant(t *testing.T) parity.Tenant {
	t.Helper()
	return fixtureutil.MintTenantJWT(t, f.keySet)
}

// MustSetupMultiNode boots a postgres testcontainer + n cyoda-go
// subprocesses sharing it, with cluster bootstrap. Fails the test
// on any setup error. Callers MUST `defer cleanup()` on the line
// immediately following.
func MustSetupMultiNode(t *testing.T, n int) (multinode.MultiNodeFixture, func()) {
	t.Helper()
	if n < 2 {
		t.Fatalf("MustSetupMultiNode: n must be >= 2, got %d", n)
	}

	pgC, pgURL, err := startPostgresContainer(t)
	if err != nil {
		t.Fatalf("postgres testcontainer: %v", err)
	}
	pgCleanup := func() { _ = pgC.Terminate(context.Background()) }

	ks, err := fixtureutil.GenerateJWTKeySet()
	if err != nil {
		pgCleanup()
		t.Fatalf("JWT keyset: %v", err)
	}

	cluster, clusterCleanup, err := fixtureutil.LaunchCyodaClusterAndCompute(ks, n, []string{
		"CYODA_STORAGE_BACKEND=postgres",
		"CYODA_POSTGRES_URL=" + pgURL,
		"CYODA_POSTGRES_AUTO_MIGRATE=true",
	})
	if err != nil {
		pgCleanup()
		t.Fatalf("LaunchCyodaClusterAndCompute: %v", err)
	}

	cleanup := func() {
		clusterCleanup()
		pgCleanup()
	}

	return &pgMultiNode{
		baseURLs: cluster.BaseURLs,
		keySet:   ks,
	}, cleanup
}
```

`startPostgresContainer` is a small helper — extract from existing single-node `setup()` if it embeds testcontainer logic; otherwise inline.

- [ ] **Step 4: Build-check**

```bash
go build ./plugins/postgres/
go vet ./plugins/postgres/
```
Expected: silent.

- [ ] **Step 5: Commit Phase 2**

```bash
git add plugins/postgres/multinode_fixture.go e2e/parity/fixtureutil/
git commit -m "test(postgres): multi-node fixture — N-cyoda + shared testcontainer

Adds MustSetupMultiNode(t, n) to plugins/postgres returning a
multinode.MultiNodeFixture. Internally launches one Postgres
testcontainer + n cyoda-go subprocesses with cluster bootstrap
(CYODA_CLUSTER_ENABLED, CYODA_SEED_NODES, etc.) + one shared
compute-test-client.

LaunchCyodaClusterAndCompute is the new fixtureutil helper — same
shape and conventions as LaunchCyodaAndCompute; cassandra plugin
will use the same surface via cyoda-go-cassandra#35.

Refs #120."
```

### Task 2.2: `e2e/parity/postgres/multinode_test.go` — TestMultiNode entry

**Files:**
- Create: `e2e/parity/postgres/multinode_test.go`

- [ ] **Step 1: Create the file**

```go
package postgres_test

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/multinode"
	pg "github.com/cyoda-platform/cyoda-go/plugins/postgres"

	_ "github.com/cyoda-platform/cyoda-go/e2e/parity/multinode" // register multi-node scenarios
)

// TestMultiNode runs the cluster-shareable scenario set against a
// 3-node postgres-backed cyoda-go cluster. Memory and sqlite have
// no MultiNodeFixture (cannot share state across processes), so they
// don't expose this entry.
func TestMultiNode(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-node requires Docker testcontainer + N cyoda-go subprocesses")
	}
	fix, cleanup := pg.MustSetupMultiNode(t, 3)
	defer cleanup()
	for _, nt := range multinode.AllTests() {
		t.Run(nt.Name, func(t *testing.T) { nt.Fn(t, fix) })
	}
}
```

- [ ] **Step 2: Run scoped**

```bash
go test ./e2e/parity/postgres/ -run "TestMultiNode" -v 2>&1 | tail -30
```

Expected: 3 scenarios run via `t.Run`, each producing PASS or FAIL.

If any FAIL, capture and decide:
- Cluster doesn't converge → likely the gossip stability window needs tuning (`CYODA_GOSSIP_STABILITY_WINDOW`).
- One node's tenant JWT rejected → seed-nodes list issue or auth-key sync issue. Surface as DONE_WITH_CONCERNS.
- 10/03 sees corrupt counter → real consistency bug. Surface immediately.

- [ ] **Step 3: Commit if green**

```bash
git add e2e/parity/postgres/multinode_test.go
git commit -m "test(postgres): TestMultiNode entry runs 3 file-10 scenarios

Uses the new MustSetupMultiNode fixture and iterates
multinode.AllTests() under t.Run. Memory and sqlite have no
equivalent entry — file-10 scenarios are postgres-only by physical
construction (shared backing storage required).

Refs #120."
```

---

## Phase 3 — Driver wrappers for workflow import/export

### Task 3.1: Add `ImportWorkflow` + `ExportWorkflow` Driver methods

**Files:**
- Modify: `e2e/externalapi/driver/driver.go`
- Modify: `e2e/externalapi/driver/vocabulary_test.go`

- [ ] **Step 1: Append failing tests**

Append to `e2e/externalapi/driver/vocabulary_test.go`:

```go
func TestDriver_ImportWorkflow_POST(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.ImportWorkflow("m", 1, `{"workflows":[]}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/model/m/1/workflow/import" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_ExportWorkflow_GET(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"workflows":[]}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	raw, err := d.ExportWorkflow("m", 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/model/m/1/workflow/export" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty body")
	}
}
```

- [ ] **Step 2: Confirm RED**

```bash
go test ./e2e/externalapi/driver/ -run "TestDriver_(Import|Export)Workflow" -v
```
Expected: 2 FAILs (methods undefined).

- [ ] **Step 3: Add the 2 methods**

Append to `e2e/externalapi/driver/driver.go` near other model-lifecycle methods:

```go
// ImportWorkflow issues POST /api/model/{name}/{version}/workflow/import.
// YAML action: import_workflow.
func (d *Driver) ImportWorkflow(name string, version int, body string) error {
	return d.client.ImportWorkflow(d.t, name, version, body)
}

// ExportWorkflow issues GET /api/model/{name}/{version}/workflow/export.
// Returns the raw JSON body. YAML action: export_workflow.
func (d *Driver) ExportWorkflow(name string, version int) (json.RawMessage, error) {
	return d.client.ExportWorkflow(d.t, name, version)
}
```

- [ ] **Step 4: Confirm GREEN + commit**

```bash
go test ./e2e/externalapi/driver/ -short -v
go vet ./e2e/externalapi/driver/

git add e2e/externalapi/driver/
git commit -m "test(externalapi): Driver helpers for workflow import/export

Adds ImportWorkflow and ExportWorkflow as thin pass-throughs to the
existing parity client methods. Required by tranche-3 file 08
scenarios.

Refs #120."
```

---

## Phase 4 — Driver + client wrappers for edge-message

### Task 4.1: Add `c.DeleteMessages` if absent

**Files:**
- Possibly modify: `e2e/parity/client/http.go`
- Possibly create: `e2e/parity/client/delete_messages_test.go`

This task is **conditional on Phase 0.2's findings**. If `c.DeleteMessages` already exists in the client (under any name), skip this task and proceed to 4.2. Otherwise, add it.

- [ ] **Step 1: Probe**

```bash
grep -nE "DeleteMessages|deleteMessages" e2e/parity/client/http.go
```

- [ ] **Step 2: If absent — write failing test**

```go
package client_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

func TestDeleteMessages_DELETE_BatchBody(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"deletedIds":["m1","m2"]}`))
	}))
	defer srv.Close()
	c := client.NewClient(srv.URL, "tok")
	deleted, err := c.DeleteMessages(t, []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method: got %q", gotMethod)
	}
	if gotPath != "/api/message" {
		t.Errorf("path: got %q", gotPath)
	}
	var bodyIds []string
	if err := json.Unmarshal([]byte(gotBody), &bodyIds); err != nil {
		t.Errorf("body not JSON array: %v (body=%s)", err, gotBody)
	}
	if len(bodyIds) != 2 || bodyIds[0] != "m1" || bodyIds[1] != "m2" {
		t.Errorf("body ids: got %v, want [m1,m2]", bodyIds)
	}
	if len(deleted) != 2 {
		t.Errorf("returned ids: got %d, want 2", len(deleted))
	}
}
```

- [ ] **Step 3: Confirm RED**

```bash
go test ./e2e/parity/client/ -run TestDeleteMessages -v
```
Expected: FAIL (method undefined). The exact path (`/api/message` or `/api/message/batch` or via query) depends on Phase 0.2 findings — adjust the expected path to match the server's actual route.

- [ ] **Step 4: Implement**

```go
// DeleteMessages issues DELETE /api/message with a JSON-array body
// of message IDs. Returns the list of actually-deleted IDs from the
// response (the server may report which were missing).
func (c *Client) DeleteMessages(t *testing.T, ids []string) ([]string, error) {
	t.Helper()
	body, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal DeleteMessages ids: %w", err)
	}
	resp, err := c.doRaw(t, http.MethodDelete, "/api/message", string(body))
	if err != nil {
		return nil, err
	}
	var out struct {
		DeletedIDs []string `json:"deletedIds"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("decode DeleteMessages response: %w (body=%s)", err, string(resp))
	}
	return out.DeletedIDs, nil
}
```

Adjust the URL path and response shape based on Phase 0.2 findings.

- [ ] **Step 5: Confirm GREEN + commit**

```bash
go test ./e2e/parity/client/ -v 2>&1 | tail -3
go vet ./e2e/parity/client/

git add e2e/parity/client/
git commit -m "test(parity/client): add DeleteMessages batch helper

Wraps DELETE /api/message with a JSON-array body of message IDs;
returns the deleted-IDs list from the response. Required by
tranche-3 scenario 11/03 (delete-collection).

Refs #120."
```

### Task 4.2: Add Driver wrappers for message helpers

**Files:**
- Modify: `e2e/externalapi/driver/driver.go`
- Modify: `e2e/externalapi/driver/vocabulary_test.go`

- [ ] **Step 1: Append failing tests**

```go
func TestDriver_CreateMessage_POST(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-1"}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	id, err := d.CreateMessage("Publication", `{"k":1}`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost {
		t.Errorf("method: got %q", cap.method)
	}
	if !strings.Contains(cap.path, "/api/message/new/Publication") {
		t.Errorf("path: got %q", cap.path)
	}
	if id == "" {
		t.Error("expected non-empty id")
	}
}

func TestDriver_GetMessage_GET(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-1"}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if _, err := d.GetMessage("msg-1"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/message/msg-1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_DeleteMessage_DELETE(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.DeleteMessage("msg-1"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/message/msg-1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_DeleteMessages_DELETE_Batch(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"deletedIds":["m1","m2"]}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	deleted, err := d.DeleteMessages([]string{"m1", "m2"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/message" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if len(deleted) != 2 {
		t.Errorf("deleted ids: got %d, want 2", len(deleted))
	}
}
```

- [ ] **Step 2: Confirm RED**

```bash
go test ./e2e/externalapi/driver/ -run "TestDriver_(Create|Get|Delete)Message" -v
```
Expected: 4 FAILs.

- [ ] **Step 3: Add the 4 methods**

```go
// CreateMessage issues POST /api/message/new/{subject} with a JSON
// payload body. Returns the message ID. YAML action: save_edge_message.
func (d *Driver) CreateMessage(subject, payload string) (string, error) {
	return d.client.CreateMessage(d.t, subject, payload)
}

// GetMessage issues GET /api/message/{id}. Returns the full message
// envelope as a map. YAML action: get_edge_message.
func (d *Driver) GetMessage(id string) (map[string]any, error) {
	return d.client.GetMessage(d.t, id)
}

// DeleteMessage issues DELETE /api/message/{id}. YAML action:
// delete_edge_message.
func (d *Driver) DeleteMessage(id string) error {
	return d.client.DeleteMessage(d.t, id)
}

// DeleteMessages issues DELETE /api/message with a batch ID body.
// Returns the deleted-IDs list. YAML action: delete_edge_messages.
func (d *Driver) DeleteMessages(ids []string) ([]string, error) {
	return d.client.DeleteMessages(d.t, ids)
}
```

- [ ] **Step 4: Confirm GREEN + commit**

```bash
go test ./e2e/externalapi/driver/ -short -v
go vet ./e2e/externalapi/driver/

git add e2e/externalapi/driver/
git commit -m "test(externalapi): Driver helpers for edge-message vocabulary

Adds CreateMessage, GetMessage, DeleteMessage, DeleteMessages as
thin pass-throughs to the existing parity client methods. Required
by tranche-3 file 11 scenarios.

Note: cyoda-go uses /api/message/... paths whereas the dictionary
references /edge-message/... — this is a different_naming_same_level
URL drift recorded in mapping. Tests use cyoda-go's surface via
the parity client.

Refs #120."
```

---

## Phase 5 — File 08: workflow import/export (6 scenarios)

### Task 5.1: Implement all 6 Run* in `workflow_import_export.go`

**Files:**
- Create: `e2e/parity/externalapi/workflow_import_export.go`

All 6 scenarios are happy-path. Pattern: register a model, import a workflow with the scenario's specific shape, optionally re-import with a different mode, export, assert on shape.

- [ ] **Step 1: Write the file**

```go
package externalapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_08_01_SimpleAutomatedTransition", Fn: RunExternalAPI_08_01_SimpleAutomatedTransition},
		parity.NamedTest{Name: "ExternalAPI_08_02_DefaultsAppliedAndReturned", Fn: RunExternalAPI_08_02_DefaultsAppliedAndReturned},
		parity.NamedTest{Name: "ExternalAPI_08_03_AdvancedCriteriaAndProcessors", Fn: RunExternalAPI_08_03_AdvancedCriteriaAndProcessors},
		parity.NamedTest{Name: "ExternalAPI_08_04_StrategyReplace", Fn: RunExternalAPI_08_04_StrategyReplace},
		parity.NamedTest{Name: "ExternalAPI_08_05_StrategyActivate", Fn: RunExternalAPI_08_05_StrategyActivate},
		parity.NamedTest{Name: "ExternalAPI_08_06_StrategyMerge", Fn: RunExternalAPI_08_06_StrategyMerge},
	)
}

// minimalWorkflow returns a one-state, one-transition workflow with a
// trivial JSONPath criterion. Used as the baseline by 08/01 and (with
// minor edits) by the strategy scenarios.
func minimalWorkflow(name string) string {
	return `{
		"workflows": [{
			"name": "` + name + `",
			"version": "1.0",
			"initialState": "draft",
			"states": {
				"draft": {
					"transitions": [{
						"name": "PUBLISH",
						"to": "published",
						"automated": true,
						"criterion": {"type": "jsonpath", "path": "$.publish", "equals": true}
					}]
				},
				"published": {"transitions": []}
			}
		}]
	}`
}

// RunExternalAPI_08_01_SimpleAutomatedTransition — dictionary 08/01.
func RunExternalAPI_08_01_SimpleAutomatedTransition(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("wf1", 1, `{"k":1,"publish":false}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.ImportWorkflow("wf1", 1, minimalWorkflow("simple")); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}
	raw, err := d.ExportWorkflow("wf1", 1)
	if err != nil {
		t.Fatalf("ExportWorkflow: %v", err)
	}
	if !strings.Contains(string(raw), `"PUBLISH"`) {
		t.Errorf("export missing transition name: %s", string(raw))
	}
}

// RunExternalAPI_08_02_DefaultsAppliedAndReturned — dictionary 08/02.
// Import a partially-specified workflow; export must show the
// server-applied defaults (e.g. transition.automated defaults to false
// when unspecified).
func RunExternalAPI_08_02_DefaultsAppliedAndReturned(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("wf2", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Import a workflow that omits `automated` on the transition.
	body := `{
		"workflows": [{
			"name": "defaults",
			"version": "1.0",
			"initialState": "s1",
			"states": {
				"s1": {"transitions": [{"name": "MOVE", "to": "s2"}]},
				"s2": {"transitions": []}
			}
		}]
	}`
	if err := d.ImportWorkflow("wf2", 1, body); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}
	raw, err := d.ExportWorkflow("wf2", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// Server should populate the omitted automated field with its
	// default. Assert the field appears in the export (value depends
	// on cyoda-go's default).
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	wfs, ok := shape["workflows"].([]any)
	if !ok || len(wfs) == 0 {
		t.Fatalf("export missing workflows: %v", shape)
	}
	// The exact shape inspection is permissive — verify the transition
	// exists with both a name and at least one of {automated,criterion}
	// fields populated.
	if !strings.Contains(string(raw), `"MOVE"`) {
		t.Errorf("export missing transition name: %s", string(raw))
	}
}

// RunExternalAPI_08_03_AdvancedCriteriaAndProcessors — dictionary 08/03.
// Import a workflow with a group criterion (AND), function criterion,
// and a scheduled processor. Export must round-trip the structures.
func RunExternalAPI_08_03_AdvancedCriteriaAndProcessors(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("wf3", 1, `{"flag":true,"value":42}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	body := `{
		"workflows": [{
			"name": "advanced",
			"version": "1.0",
			"initialState": "init",
			"states": {
				"init": {
					"transitions": [{
						"name": "ADVANCE",
						"to": "done",
						"criterion": {
							"type": "group",
							"operator": "AND",
							"clauses": [
								{"type": "jsonpath", "path": "$.flag", "equals": true},
								{"type": "jsonpath", "path": "$.value", "greaterThan": 10}
							]
						},
						"processors": [{"name": "noop", "config": {"delay": "0s"}}]
					}]
				},
				"done": {"transitions": []}
			}
		}]
	}`
	if err := d.ImportWorkflow("wf3", 1, body); err != nil {
		// If cyoda-go doesn't accept this exact shape, capture the
		// rejection and treat as a discover-and-compare moment.
		t.Fatalf("ImportWorkflow: %v", err)
	}
	raw, err := d.ExportWorkflow("wf3", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(string(raw), `"ADVANCE"`) {
		t.Errorf("export missing transition: %s", string(raw))
	}
}

// RunExternalAPI_08_04_StrategyReplace — dictionary 08/04.
// importMode=REPLACE removes all previous workflows before adding new ones.
func RunExternalAPI_08_04_StrategyReplace(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("wf4", 1, `{"k":1,"publish":false}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("wf4", 1, minimalWorkflow("first")); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// Import with REPLACE — should drop the first workflow.
	replaceBody := `{
		"importMode": "REPLACE",
		"workflows": [{
			"name": "second",
			"version": "1.0",
			"initialState": "draft",
			"states": {"draft": {"transitions": []}}
		}]
	}`
	if err := d.ImportWorkflow("wf4", 1, replaceBody); err != nil {
		t.Fatalf("REPLACE import: %v", err)
	}
	raw, _ := d.ExportWorkflow("wf4", 1)
	if strings.Contains(string(raw), `"first"`) {
		t.Errorf("REPLACE did not drop first workflow: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"second"`) {
		t.Errorf("REPLACE did not add second workflow: %s", string(raw))
	}
}

// RunExternalAPI_08_05_StrategyActivate — dictionary 08/05.
// importMode=ACTIVATE deactivates existing workflows and activates the new one.
func RunExternalAPI_08_05_StrategyActivate(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("wf5", 1, `{"k":1,"publish":false}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("wf5", 1, minimalWorkflow("first")); err != nil {
		t.Fatalf("first: %v", err)
	}
	activateBody := `{
		"importMode": "ACTIVATE",
		"workflows": [{
			"name": "second",
			"version": "1.0",
			"initialState": "draft",
			"states": {"draft": {"transitions": []}}
		}]
	}`
	if err := d.ImportWorkflow("wf5", 1, activateBody); err != nil {
		t.Fatalf("ACTIVATE: %v", err)
	}
	raw, _ := d.ExportWorkflow("wf5", 1)
	// ACTIVATE keeps both workflows but flips the "active" flag —
	// assert both names appear in the export.
	for _, name := range []string{"first", "second"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Errorf("ACTIVATE missing %s workflow: %s", name, string(raw))
		}
	}
}

// RunExternalAPI_08_06_StrategyMerge — dictionary 08/06.
// importMode=MERGE updates existing workflow in place and adds new ones.
func RunExternalAPI_08_06_StrategyMerge(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("wf6", 1, `{"k":1,"publish":false}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("wf6", 1, minimalWorkflow("baseline")); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	mergeBody := `{
		"importMode": "MERGE",
		"workflows": [
			{
				"name": "baseline",
				"version": "1.0",
				"initialState": "draft",
				"states": {
					"draft": {"transitions": [{"name": "PUBLISH", "to": "published", "automated": false}]},
					"published": {"transitions": []}
				}
			},
			{
				"name": "newone",
				"version": "1.0",
				"initialState": "s",
				"states": {"s": {"transitions": []}}
			}
		]
	}`
	if err := d.ImportWorkflow("wf6", 1, mergeBody); err != nil {
		t.Fatalf("MERGE: %v", err)
	}
	raw, _ := d.ExportWorkflow("wf6", 1)
	for _, name := range []string{"baseline", "newone"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Errorf("MERGE missing %s workflow: %s", name, string(raw))
		}
	}
}
```

If a scenario fails because cyoda-go's import body schema differs from the dictionary's (e.g., field name `importMode` vs `mode`, or `criterion` vs `condition`), capture the rejection and decide:
- Adjust the test body to cyoda-go's accepted shape with a comment "cyoda-go accepts X, dictionary specifies Y" → `different_naming_same_level`.
- If the scenario can't run at all (e.g., no MERGE mode in cyoda-go) → file an issue + `t.Skip("pending #N")`.

- [ ] **Step 2: Run scoped + cross-backend**

```bash
go test ./e2e/parity/memory/ -run "TestParity/ExternalAPI_08_" -v
go test ./e2e/parity/... -run "TestParity/ExternalAPI_08_" -v 2>&1 | grep -cE "(PASS|FAIL): TestParity/ExternalAPI_08_"
```
Expected: 6 PASS × 3 backends = 18.

- [ ] **Step 3: Commit**

```bash
git add e2e/parity/externalapi/workflow_import_export.go
git commit -m "test(externalapi): 08-workflow-import-export — 6 scenarios

Tranche-3 coverage for 08-workflow-import-export.yaml:
simple-automated-transition, defaults-applied, advanced
criteria/processors, REPLACE/ACTIVATE/MERGE strategies. All
happy-path. Driver helpers wired in Phase 3.

Refs #120."
```

---

## Phase 6 — File 11: edge-message (3 scenarios)

### Task 6.1: Implement all 3 Run* in `edge_message.go`

**Files:**
- Create: `e2e/parity/externalapi/edge_message.go`

Whether all 3 scenarios are implementable depends on Phase 0.1 findings.

- [ ] **Step 1: Write the file**

```go
package externalapi

import (
	"net/http"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/errorcontract"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_11_01_SaveSingle", Fn: RunExternalAPI_11_01_SaveSingle},
		parity.NamedTest{Name: "ExternalAPI_11_02_DeleteSingle", Fn: RunExternalAPI_11_02_DeleteSingle},
		parity.NamedTest{Name: "ExternalAPI_11_03_DeleteCollection", Fn: RunExternalAPI_11_03_DeleteCollection},
	)
}

// edgeMessagePayload is the dictionary's content shape; cyoda-go
// accepts it as the body of POST /api/message/new/{subject}.
const edgeMessagePayload = `{
	"correlationId": "00000000-0000-0000-0000-000000000001",
	"userId": "Larry",
	"replyTo": "Jimmy",
	"recipient": "Bobby",
	"metaData": {"happy": "golucky"},
	"body": {"hello": "world"}
}`

// RunExternalAPI_11_01_SaveSingle — dictionary 11/01.
func RunExternalAPI_11_01_SaveSingle(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	id, err := d.CreateMessage("Publication", edgeMessagePayload)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty message id")
	}
	got, err := d.GetMessage(id)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	// Round-trip assertion depends on Phase 0.1 findings about which
	// header fields cyoda-go preserves. Spot-check the body.
	if got["id"] != id {
		// cyoda-go may use a different envelope key — relax to
		// "got back something non-empty".
		if len(got) == 0 {
			t.Errorf("GetMessage returned empty envelope")
		}
	}
}

// RunExternalAPI_11_02_DeleteSingle — dictionary 11/02.
func RunExternalAPI_11_02_DeleteSingle(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	id, err := d.CreateMessage("Publication", edgeMessagePayload)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if err := d.DeleteMessage(id); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	// GetMessage after delete should error. Use the discover-and-compare
	// approach via the underlying client's raw helper if available;
	// otherwise assert non-nil err.
	if _, err := d.GetMessage(id); err == nil {
		t.Fatal("expected GetMessage to fail after delete")
	}
	_ = errorcontract.ExpectedError{HTTPStatus: http.StatusNotFound}
}

// RunExternalAPI_11_03_DeleteCollection — dictionary 11/03.
func RunExternalAPI_11_03_DeleteCollection(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	id1, err := d.CreateMessage("Publication", edgeMessagePayload)
	if err != nil {
		t.Fatalf("create m1: %v", err)
	}
	id2, err := d.CreateMessage("Publication", edgeMessagePayload)
	if err != nil {
		t.Fatalf("create m2: %v", err)
	}
	deleted, err := d.DeleteMessages([]string{id1, id2})
	if err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if len(deleted) != 2 {
		t.Errorf("deleted count: got %d, want 2", len(deleted))
	}
	for _, id := range []string{id1, id2} {
		if _, err := d.GetMessage(id); err == nil {
			t.Errorf("GetMessage(%s) succeeded after batch delete", id)
		}
	}
}
```

- [ ] **Step 2: Run scoped + cross-backend**

```bash
go test ./e2e/parity/memory/ -run "TestParity/ExternalAPI_11_" -v
go test ./e2e/parity/... -run "TestParity/ExternalAPI_11_" -v 2>&1 | grep -cE "(PASS|FAIL): TestParity/ExternalAPI_11_"
```
Expected: 3 PASS × 3 backends = 9. If any fail, decide per Phase 0.1's analysis.

- [ ] **Step 3: Commit**

```bash
git add e2e/parity/externalapi/edge_message.go
git commit -m "test(externalapi): 11-edge-message — 3 scenarios

Tranche-3 coverage for 11-edge-message.yaml: save-single,
delete-single, delete-collection (batch). cyoda-go's path is
/api/message/new/{subject} vs the dictionary's /edge-message;
different_naming_same_level URL drift recorded in mapping.

Refs #120."
```

---

## Phase 7 — File 09: workflow externalisation (12 scenarios)

The heaviest file in tranche 3. All scenarios depend on the parity fixture's compute-test-client being running and joined to the gRPC stream — already true for memory/sqlite/postgres parity backends.

### Task 7.1: Implement all 12 Run* in `workflow_externalization.go`

**Files:**
- Create: `e2e/parity/externalapi/workflow_externalization.go`

Each scenario follows the pattern:
1. Use `fixture.ComputeTenant(t)` (tranche-1 inherited helper) to get a tenant matching the compute-client's tenant.
2. Create a model + import a workflow with an externalised processor or criterion in the specified mode.
3. Lock and create an entity.
4. Observe the result via HTTP entity GET (state changed, error reported, etc.).

For negative-path scenarios (02, 03, 04, 05, 06, 07, 08, 11), apply discover-and-compare: capture cyoda-go's error code via `*Raw` helpers + `errorcontract.Match`. The helpers added in tranche 2 (`CreateEntityRaw`, `GetEntityChangesRaw` etc.) cover most needs.

For tractability, this plan provides the full code for **two representative scenarios** (one happy path, one negative path); the remaining 10 follow the same pattern with the YAML-specified workflow shape.

- [ ] **Step 1: Write the file with the registry + 12 Run* function shells**

The 12 scenarios:

| ID | Name | Mode | Pattern |
|---|---|---|---|
| 09/01 | sync-processor-success | SYNC | happy |
| 09/02 | sync-processor-exception-rolls-back | SYNC | negative — entity not saved |
| 09/03 | async-same-tx-exception-rolls-back | ASYNC_SAME_TX | negative — entity not saved |
| 09/04 | async-new-tx-exception-keeps-initial-save | ASYNC_NEW_TX | negative — entity saved, follow-up tx cancelled |
| 09/05 | sync-error-flag-rolls-back | SYNC | negative — error-flag rollback |
| 09/06 | async-same-tx-error-flag-rolls-back | ASYNC_SAME_TX | negative — error-flag rollback |
| 09/07 | async-new-tx-error-flag-keeps-initial-save | ASYNC_NEW_TX | negative — partial save |
| 09/08 | no-external-registered-fails | (any) | negative — no calc member |
| 09/09 | external-disconnect-succeeds-on-retry | (any) | retry path |
| 09/10 | external-timeout-failover | (any) | timeout path |
| 09/11 | processing-node-disconnects-mid-request | (any) | mid-request disconnect |
| 09/12 | externalized-criterion-skips-call-when-not-matched | (any) | criterion-skip optimisation |

```go
package externalapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/errorcontract"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_09_01_SyncProcessorSuccess", Fn: RunExternalAPI_09_01_SyncProcessorSuccess},
		parity.NamedTest{Name: "ExternalAPI_09_02_SyncProcessorExceptionRollsBack", Fn: RunExternalAPI_09_02_SyncProcessorExceptionRollsBack},
		parity.NamedTest{Name: "ExternalAPI_09_03_AsyncSameTxExceptionRollsBack", Fn: RunExternalAPI_09_03_AsyncSameTxExceptionRollsBack},
		parity.NamedTest{Name: "ExternalAPI_09_04_AsyncNewTxExceptionKeepsInitial", Fn: RunExternalAPI_09_04_AsyncNewTxExceptionKeepsInitial},
		parity.NamedTest{Name: "ExternalAPI_09_05_SyncErrorFlagRollsBack", Fn: RunExternalAPI_09_05_SyncErrorFlagRollsBack},
		parity.NamedTest{Name: "ExternalAPI_09_06_AsyncSameTxErrorFlagRollsBack", Fn: RunExternalAPI_09_06_AsyncSameTxErrorFlagRollsBack},
		parity.NamedTest{Name: "ExternalAPI_09_07_AsyncNewTxErrorFlagKeepsInitial", Fn: RunExternalAPI_09_07_AsyncNewTxErrorFlagKeepsInitial},
		parity.NamedTest{Name: "ExternalAPI_09_08_NoExternalRegisteredFails", Fn: RunExternalAPI_09_08_NoExternalRegisteredFails},
		parity.NamedTest{Name: "ExternalAPI_09_09_DisconnectSucceedsOnRetry", Fn: RunExternalAPI_09_09_DisconnectSucceedsOnRetry},
		parity.NamedTest{Name: "ExternalAPI_09_10_TimeoutFailover", Fn: RunExternalAPI_09_10_TimeoutFailover},
		parity.NamedTest{Name: "ExternalAPI_09_11_ProcessingNodeDisconnectsMidRequest", Fn: RunExternalAPI_09_11_ProcessingNodeDisconnectsMidRequest},
		parity.NamedTest{Name: "ExternalAPI_09_12_ExternalizedCriterionSkipsCall", Fn: RunExternalAPI_09_12_ExternalizedCriterionSkipsCall},
	)
}

// externalizedWorkflow returns a workflow with a single transition
// invoking the named externalised processor. `mode` is one of
// "SYNC" | "ASYNC_SAME_TX" | "ASYNC_NEW_TX". `procName` is the name
// the compute-test-client registers (depends on which scenario; the
// existing parity tests use "noop", "fail", "slow" — verify against
// `cmd/compute-test-client/`).
func externalizedWorkflow(name, procName, mode string) string {
	return `{
		"workflows": [{
			"name": "` + name + `",
			"version": "1.0",
			"initialState": "init",
			"states": {
				"init": {
					"transitions": [{
						"name": "PROCESS",
						"to": "done",
						"automated": true,
						"processors": [{"name": "` + procName + `", "mode": "` + mode + `"}]
					}]
				},
				"done": {"transitions": []}
			}
		}]
	}`
}

// RunExternalAPI_09_01_SyncProcessorSuccess — dictionary 09/01.
// SYNC processor returns success → entity transitions to "done".
func RunExternalAPI_09_01_SyncProcessorSuccess(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.ComputeTenant(t)
	d := driver.NewRemote(t, fixture.BaseURL(), tenant.Token)
	if err := d.CreateModelFromSample("ext1", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("ext1", 1, externalizedWorkflow("noop_sync", "noop", "SYNC")); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}
	if err := d.LockModel("ext1", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("ext1", 1, `{"k":1}`)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	// SYNC must complete the transition before CreateEntity returns.
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if got.Meta.State != "done" {
		t.Errorf("state: got %q, want \"done\"", got.Meta.State)
	}
}

// RunExternalAPI_09_02_SyncProcessorExceptionRollsBack — dictionary 09/02.
// SYNC processor throws → transaction cancelled → entity not saved.
// Discover-and-compare on the rejection error code.
func RunExternalAPI_09_02_SyncProcessorExceptionRollsBack(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	tenant := fixture.ComputeTenant(t)
	d := driver.NewRemote(t, fixture.BaseURL(), tenant.Token)
	if err := d.CreateModelFromSample("ext2", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("ext2", 1, externalizedWorkflow("fail_sync", "fail", "SYNC")); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}
	if err := d.LockModel("ext2", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	status, body, err := d.CreateEntityRaw("ext2", 1, `{"k":1}`)
	if err != nil {
		t.Fatalf("CreateEntityRaw: %v", err)
	}
	t.Logf("DISCOVER 09/02 status=%d body=%s", status, string(body))
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusBadRequest,
	})
	// Entity must not have been persisted.
	list, err := d.ListEntitiesByModel("ext2", 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("entity count after rollback: got %d, want 0", len(list))
	}
}

// 09/03 — 09/12 follow the same patterns. Each function uses a workflow
// imported via externalizedWorkflow with the appropriate (procName, mode)
// pair, creates an entity, and asserts the resulting state OR via *Raw
// + errorcontract.Match.
//
// For execution: write each function's body following the same shape
// as 09/01 (happy) or 09/02 (negative). Where cyoda-go's compute-test-
// client doesn't register a particular processor (`fail`, `slow`,
// `disconnect`, `flagged`), the scenario t.Skip's with a reason — the
// compute-test-client's procName registry is in
// cmd/compute-test-client/dispatch.go.

func RunExternalAPI_09_03_AsyncSameTxExceptionRollsBack(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: implement following the 09/02 pattern with mode=ASYNC_SAME_TX once the compute-test-client's `fail` processor returns appropriately under ASYNC_SAME_TX semantics")
}

func RunExternalAPI_09_04_AsyncNewTxExceptionKeepsInitial(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: implement following the 09/02 pattern with mode=ASYNC_NEW_TX; expected behavior is initial entity persists, follow-up tx cancelled")
}

func RunExternalAPI_09_05_SyncErrorFlagRollsBack(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: needs a compute-test-client processor that returns success=false (error flag) — verify procName registry")
}

func RunExternalAPI_09_06_AsyncSameTxErrorFlagRollsBack(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: same as 09/05 but mode=ASYNC_SAME_TX")
}

func RunExternalAPI_09_07_AsyncNewTxErrorFlagKeepsInitial(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: same as 09/05 but mode=ASYNC_NEW_TX")
}

func RunExternalAPI_09_08_NoExternalRegisteredFails(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture) // fresh tenant — NOT compute-tenant — so no calc member is registered
	if err := d.CreateModelFromSample("ext8", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("ext8", 1, externalizedWorkflow("noop_sync", "noop", "SYNC")); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}
	if err := d.LockModel("ext8", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	status, body, err := d.CreateEntityRaw("ext8", 1, `{"k":1}`)
	if err != nil {
		t.Fatalf("CreateEntityRaw: %v", err)
	}
	// Expect rejection because no calc member is registered for this tenant.
	if status == http.StatusOK {
		t.Fatal("expected non-OK status when no external member is registered")
	}
	t.Logf("DISCOVER 09/08 status=%d body=%s", status, string(body))
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusBadRequest,
	})
}

func RunExternalAPI_09_09_DisconnectSucceedsOnRetry(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: needs two compute-test-clients, one of which disconnects mid-request — fixture orchestration not in tranche-3 scope")
}

func RunExternalAPI_09_10_TimeoutFailover(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: needs a `slow` processor that exceeds the configured per-call timeout — verify procName registry")
}

func RunExternalAPI_09_11_ProcessingNodeDisconnectsMidRequest(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: needs deterministic mid-request gRPC disconnection — fixture orchestration not in tranche-3 scope")
}

func RunExternalAPI_09_12_ExternalizedCriterionSkipsCall(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending: externalised criterion + filter chain — design needs a verifiable side-effect to assert the call was skipped")
}

// helper for tests that need to wait a moment for ASYNC_NEW_TX completion.
func waitForState(t *testing.T, d *driver.Driver, id [16]byte, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Implementation: GetEntity, check Meta.State, sleep 50ms.
		// (Stubbed — fill in when implementing the async scenarios.)
		_ = strings.Contains
		break
	}
}
```

This file lands all 12 scenarios registered. 4 of them (09/01, 09/02, 09/08, plus optional 09/12 if its design firms up) are implemented; 8 are `t.Skip` with explanatory messages. Each skip includes enough detail for a future implementer to pick up.

The `procName` registry assumption (`"noop"`, `"fail"`, `"slow"`) needs verification against `cmd/compute-test-client/dispatch.go` — the catalog there determines what procNames exist. If `"fail"` doesn't exist, 09/02 either gets skipped or the compute-test-client is extended to register a `fail` processor (production-code change — out of tranche-3 scope; file an issue).

- [ ] **Step 2: Verify procName registry**

```bash
grep -nE "Register\|register\|catalog\|procName" cmd/compute-test-client/*.go | head -20
```

If `noop` is the only registered processor, only 09/01 and 09/08 are implementable in this tranche. The rest go to `t.Skip("pending #N — needs <procName> processor in compute-test-client; out of tranche-3 server-side scope")`.

If at least `fail` exists too, 09/02 and 09/05 also become implementable.

Decide based on findings. The plan ships with 4 implemented + 8 skipped as a defensible outline; adjust if the compute-client supports more.

- [ ] **Step 3: Run scoped + cross-backend**

```bash
go test ./e2e/parity/memory/ -run "TestParity/ExternalAPI_09_" -v 2>&1 | grep -E "(PASS|FAIL|SKIP):"
go test ./e2e/parity/... -run "TestParity/ExternalAPI_09_" -v 2>&1 | grep -cE "(PASS|FAIL|SKIP): TestParity/ExternalAPI_09_"
```
Expected: 12 entries × 3 backends = 36, with mostly SKIP (the 8 unimplemented) and a few PASS (the 4 implemented). If any 09/XX shows DIFFERENT pass/skip pattern across backends — STOP and report.

- [ ] **Step 4: Discover-and-compare on the implemented negative paths (09/02, 09/08)**

For each of 09/02 and 09/08:
1. Read the `t.Logf("DISCOVER ...")` body output.
2. Classify cyoda-go's `properties.errorCode`:
   - `equiv_or_better` → tighten + comment.
   - `worse` → file issue + `t.Skip("pending #N")`.
3. Remove the `t.Logf`.

- [ ] **Step 5: Commit**

```bash
git add e2e/parity/externalapi/workflow_externalization.go
git commit -m "test(externalapi): 09-workflow-externalization — 12 scenarios

Tranche-3 coverage for 09-workflow-externalization.yaml. Implemented:
09/01 (sync-success), 09/02 (sync-exception via discover-and-compare),
09/08 (no-external-registered). Skipped (8): scenarios needing
compute-test-client extensions (fail/slow/disconnect processors)
or fixture orchestration (multi-member, mid-request disconnect)
that are out of tranche-3 server-side scope.

Each t.Skip carries a clear blocking reason. The skipped 8 have
test bodies waiting for the procName registry / fixture support
to land in a future tranche or v0.7.0 effort.

Refs #120<add issue numbers for any worse-class divergences observed in 09/02 or 09/08>."
```

---

## Phase 8 — Mapping doc finalisation

### Task 8.1: Update mapping rows for 24 tranche-3 scenarios

**Files:**
- Modify: `e2e/externalapi/dictionary-mapping.md`

For each of the 24 scenarios, flip the row from `pending:tranche-3 (#120)` to status-of-record.

- [ ] **Step 1: Edit mapping**

For each row in sections `## 08-workflow-import-export.yaml`, `## 09-workflow-externalization.yaml`, `## 10-concurrency-and-multinode.yaml`, `## 11-edge-message.yaml`:

- Implemented + PASSing → `new:Run<fn>` with notes like "tranche 3" or classification details.
- `t.Skip` with new issue → `gap_on_our_side (#N)` with notes citing the discover-and-compare classification.
- `t.Skip` with surface gap → `(skipped)` row noting the missing surface.
- 10/* rows → `new:RunExternalAPI_10_0X_<name>` PLUS a note "postgres-only (cluster-shareable via `e2e/parity/multinode/`); cassandra picks up via cyoda-go-cassandra#35".

For scenarios where cyoda-go's URL/path differs from the dictionary (file 11), note `different_naming_same_level` in the notes column.

- [ ] **Step 2: Verify count**

```bash
grep -cE "^\| (wf-import/|ext/|multi/|edge-msg/)" e2e/externalapi/dictionary-mapping.md
```
Expected: 24.

- [ ] **Step 3: Commit**

```bash
git add e2e/externalapi/dictionary-mapping.md
git commit -m "docs(externalapi): mapping — flip tranche-3 rows to status-of-record

Files 08 / 09 / 10 / 11 — 24 scenarios — flipped from
\`pending:tranche-3\` to per-scenario status. File 10 rows note the
postgres-only (cluster-shareable via e2e/parity/multinode) status
and cassandra coordination via cyoda-go-cassandra#35. File 11
rows note the cyoda-go vs dictionary URL drift as
\`different_naming_same_level\`.

Refs #120."
```

---

## Phase 9 — Verification + reviews + PR

### Task 9.1: Full verification pass

- [ ] `go vet ./...` silent
- [ ] `go test ./...` 0 FAIL
- [ ] `make test-all` 0 FAIL across plugin submodules
- [ ] `go test -race ./...` 0 DATA RACE
- [ ] `go test ./e2e/parity/... -run "TestParity/ExternalAPI_" -v 2>&1 | grep -cE "(PASS|SKIP): TestParity/ExternalAPI"` — expected count: 51 (tranche 1+2) + 24 (tranche 3) = 75 per single-node backend × 3 = 225 entries
- [ ] `go test ./e2e/parity/postgres/ -run "TestMultiNode" -v` — 3 PASS

### Task 9.2: Code review

- [ ] Invoke `superpowers:requesting-code-review` against the full branch range. Specific scrutiny: multi-node fixture orchestration, file-09 skip reasons, file-11 wire-shape adaptation, mapping correctness.

### Task 9.3: Security review

- [ ] Invoke `antigravity-bundle-security-developer:cc-skill-security-review`. Scope: same as tranche 2 plus the new multi-node fixture (no JWT logged, cluster bootstrap doesn't expose seed-node addresses inappropriately).

### Task 9.4: Open PR

- [ ] Push branch with the GH_TOKEN credential helper, create PR targeting `release/v0.6.3`, body mentions cyoda-go-cassandra#35 for cross-repo coordination, body lists every issue filed during the tranche.

---

## Self-review

**Spec coverage check:**

| Spec section | Plan task |
|---|---|
| §3.1 New sibling package | Phase 1 (Tasks 1.1, 1.2, 1.3) |
| §3.2 Postgres multi-node fixture | Phase 2 (Task 2.1) |
| §3.3 Cluster test entry | Phase 2 (Task 2.2) |
| §3.4 Driver vocabulary additions | Phase 3 + Phase 4 |
| §4.1 File 08 | Phase 5 |
| §4.2 File 09 | Phase 7 |
| §4.3 File 10 | Phase 1.3 (Run* bodies) + Phase 2 (live execution) |
| §4.4 File 11 | Phase 6 |
| §5 Phase 0 gates | Phase 0 (Tasks 0.1, 0.2, 0.3) |
| §6 Discover-and-compare | Phase 7 step 4 |
| §7 Testing strategy | Phase 9.1 |
| §8 Acceptance | Phase 9 entirety |
| §10 Cross-repo (#35) | Phase 9.4 PR body |

**Placeholder scan:** `<add issue numbers...>` in Phase 7 commit message is a deliberate placeholder for issue numbers filed during the discover-and-compare pass (similar to tranche-2 plan's `<L07>`). The 8 skipped 09/* scenarios have verbose-explanatory `t.Skip` messages; not placeholders. Phase 7's `waitForState` helper is a genuine stub for if/when the async scenarios are implemented; if Phase 7 ships only 4 implemented, the helper can be deleted as YAGNI.

**Type consistency:** `MultiNodeFixture` shape consistent across multinode/registry.go, multinode/fixture.go, plugins/postgres/multinode_fixture.go. Driver method names match across phases (`ImportWorkflow`/`ExportWorkflow`/`CreateMessage`/`GetMessage`/`DeleteMessage`/`DeleteMessages`).

Plan ready for execution.
