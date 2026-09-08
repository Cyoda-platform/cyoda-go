# Write-visibility contract and gRPC delete-all fields — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retire the inert `waitForConsistencyAfter` parameter and state the write-visibility contract; make every delete-all request honor `pointInTime` and `verbose` on both HTTP and gRPC; remove the meaningless gRPC `pageSize` field.

**Architecture:** One routing rule in the entity service decides whether a delete takes the whole-model fast path or the per-entity path; both doors call the same service function. The HTTP OpenAPI spec and the gRPC JSON schema lose their fictional fields and the generated code is regenerated. Tests follow the four existing harnesses: entity-package unit tests on the memory backend, `internal/e2e` over real Postgres, `e2e/parity` scenarios across memory/sqlite/postgres, and `internal/grpc` envelope tests.

**Tech Stack:** Go 1.26, oapi-codegen (via `go generate ./api`), go-jsonschema (via `scripts/generate-events.sh`), testcontainers (e2e), `make test` / `make test-full`.

**Spec:** `docs/superpowers/specs/2026-09-08-write-visibility-contract-and-delete-all-fields-design.md`

## Global Constraints

- No issue numbers (`#501`, `#379`, …) in shipped source, comments, help topics, OpenAPI, or schemas. Commit messages and this plan may cite them.
- TDD: every task writes the failing test first and runs it red before implementing.
- Commit after every task; commit messages end with the two attribution trailers shown in Task 1.
- `verbose` lists every id the delete **attempted** (the matched set); failed ids also appear in `idToError` / `errorsById`. Never "removed ids".
- `pointInTime` on a delete selects the committed state as at that instant and ignores the ambient transaction (existing conditional-path semantics).
- Iteration command is `make test`; the end-of-deliverable command is `make test-full` (Docker required). Never hand-roll `go test ./...` as verification. A single package (`go test ./internal/domain/entity/...`) is fine while iterating.
- Do not add `-count=1`.
- Worktree: `/Users/paul/go-projects/cyoda-light/cyoda-go/.claude/worktrees/feat-501-wait-for-consistency`, branch `worktree-feat-501-wait-for-consistency`, based on `release/v0.8.4`.

---

### Task 1: Delete fast-path routing rule (service layer)

**Files:**
- Modify: `internal/domain/entity/service.go:1223-1262` (`DeleteEntitiesConditional` doc comment and the `if cond == nil {` fast-path guard)
- Test: `internal/domain/entity/service_delete_unconditional_test.go` (new)

**Interfaces:**
- Consumes: `func (h *Handler) DeleteEntitiesConditional(ctx context.Context, entityName, modelVersion string, condBody []byte, pointInTime *time.Time, verbose bool, batchSize int) (*DeleteResult, error)` — unchanged signature. `DeleteResult{EntityModelID string; MatchedCount, RemovedCount int; IDToError map[string]string; IDs []string}`.
- Produces: the rule "fast path only when `cond == nil && pointInTime == nil && !verbose` (and `batchSize == 0`, already the case)". Task 2 relies on this so the gRPC door can call `DeleteEntitiesConditional` unconditionally.

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/entity/service_delete_unconditional_test.go`:

```go
package entity

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/plugins/memory"
)

// The whole-model DeleteAll fast path is taken only when the request needs
// nothing per entity. A pointInTime or verbose=true on an unconditional
// delete goes through the enumerate-then-delete path so the instant is
// honored and the attempted ids are listed.

func TestDeleteEntitiesConditional_Unconditional_PointInTime_SparesLaterCreates(t *testing.T) {
	factory := memory.NewStoreFactory()
	t.Cleanup(func() { factory.Close() })
	ctx := newDeleteBatchedCtx(t, factory)
	h := buildDeleteBatchedHandler(t, factory, mustTxMgr(t, factory))

	before := seedPersons(t, h, ctx, 3)
	// Memory stamps versions from the process clock, so time.Now() is the
	// server's clock here; the sleeps separate the instants.
	time.Sleep(2 * time.Millisecond)
	pit := time.Now()
	time.Sleep(2 * time.Millisecond)
	after := seedPersons(t, h, ctx, 2)

	res, err := h.DeleteEntitiesConditional(ctx, "Person", "1", nil, &pit, true, 0)
	if err != nil {
		t.Fatalf("DeleteEntitiesConditional: %v", err)
	}
	if res.MatchedCount != 3 || res.RemovedCount != 3 {
		t.Fatalf("matched/removed = %d/%d, want 3/3", res.MatchedCount, res.RemovedCount)
	}
	if len(res.IDToError) != 0 {
		t.Errorf("idToError = %v, want empty", res.IDToError)
	}
	assertSameIDSet(t, res.IDs, before)

	store, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}
	for _, id := range before {
		if _, gErr := store.Get(ctx, id); !errors.Is(gErr, spi.ErrNotFound) {
			t.Errorf("entity %s existed at the instant and must be gone; Get err = %v", id, gErr)
		}
	}
	for _, id := range after {
		if _, gErr := store.Get(ctx, id); gErr != nil {
			t.Errorf("entity %s was created after the instant and must survive; Get err = %v", id, gErr)
		}
	}
}

func TestDeleteEntitiesConditional_Unconditional_PointInTime_AlreadyGoneIDInIDToError(t *testing.T) {
	factory := memory.NewStoreFactory()
	t.Cleanup(func() { factory.Close() })
	ctx := newDeleteBatchedCtx(t, factory)
	h := buildDeleteBatchedHandler(t, factory, mustTxMgr(t, factory))

	ids := seedPersons(t, h, ctx, 2)
	time.Sleep(2 * time.Millisecond)
	pit := time.Now()
	time.Sleep(2 * time.Millisecond)
	if _, err := h.DeleteEntity(ctx, ids[1]); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	res, err := h.DeleteEntitiesConditional(ctx, "Person", "1", nil, &pit, true, 0)
	if err != nil {
		t.Fatalf("DeleteEntitiesConditional: %v", err)
	}
	if res.MatchedCount != 2 || res.RemovedCount != 1 {
		t.Fatalf("matched/removed = %d/%d, want 2/1", res.MatchedCount, res.RemovedCount)
	}
	if _, ok := res.IDToError[ids[1]]; !ok {
		t.Errorf("idToError = %v, want an entry for the already-gone id %s", res.IDToError, ids[1])
	}
	assertSameIDSet(t, res.IDs, ids) // attempted ids, including the failed one
}

func TestDeleteEntitiesConditional_Unconditional_Verbose_ListsAttemptedIDs(t *testing.T) {
	factory := memory.NewStoreFactory()
	t.Cleanup(func() { factory.Close() })
	ctx := newDeleteBatchedCtx(t, factory)
	h := buildDeleteBatchedHandler(t, factory, mustTxMgr(t, factory))
	ids := seedPersons(t, h, ctx, 3)

	res, err := h.DeleteEntitiesConditional(ctx, "Person", "1", nil, nil, true, 0)
	if err != nil {
		t.Fatalf("DeleteEntitiesConditional: %v", err)
	}
	if res.MatchedCount != 3 || res.RemovedCount != 3 {
		t.Fatalf("matched/removed = %d/%d, want 3/3", res.MatchedCount, res.RemovedCount)
	}
	assertSameIDSet(t, res.IDs, ids)
}

func TestDeleteEntitiesConditional_Unconditional_Plain_ReturnsEmptyIDs(t *testing.T) {
	factory := memory.NewStoreFactory()
	t.Cleanup(func() { factory.Close() })
	ctx := newDeleteBatchedCtx(t, factory)
	h := buildDeleteBatchedHandler(t, factory, mustTxMgr(t, factory))
	seedPersons(t, h, ctx, 2)

	res, err := h.DeleteEntitiesConditional(ctx, "Person", "1", nil, nil, false, 0)
	if err != nil {
		t.Fatalf("DeleteEntitiesConditional: %v", err)
	}
	if res.MatchedCount != 2 || res.RemovedCount != 2 {
		t.Fatalf("matched/removed = %d/%d, want 2/2", res.MatchedCount, res.RemovedCount)
	}
	if res.IDs == nil || len(res.IDs) != 0 {
		t.Errorf("IDs = %#v, want a non-nil empty slice when verbose is false", res.IDs)
	}
}

// A joined participant's buffered (uncommitted) entities never existed in
// committed state at any instant, so a pointInTime selection cannot see
// them; the plain fast path (no instant) still deletes them. Both are the
// conditional path's existing semantics, now reachable on the unconditional
// form.
func TestDeleteEntitiesConditional_Joined_PointInTime_SparesBufferedEntities(t *testing.T) {
	h, factory, txMgr, base := newTxJoinTestHandler(t)

	ownerTxID, ownerCtx, err := txMgr.Begin(base)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	joinedCtx, err := txMgr.Join(base, ownerTxID)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	created, err := h.CreateEntity(joinedCtx, sampleWidgetInput())
	if err != nil {
		t.Fatalf("create in joined tx: %v", err)
	}
	bufferedID := created.EntityIDs[0]

	time.Sleep(2 * time.Millisecond)
	pit := time.Now()

	res, err := h.DeleteEntitiesConditional(joinedCtx, "Widget", "1", nil, &pit, true, 0)
	if err != nil {
		t.Fatalf("DeleteEntitiesConditional (joined, pointInTime): %v", err)
	}
	if res.MatchedCount != 0 || len(res.IDs) != 0 {
		t.Fatalf("matched = %d, ids = %v; a buffered entity is not committed state at any instant", res.MatchedCount, res.IDs)
	}
	if err := txMgr.Commit(ownerCtx, ownerTxID); err != nil {
		t.Fatalf("owner commit: %v", err)
	}
	store, err := factory.EntityStore(base)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}
	if _, err := store.Get(base, bufferedID); err != nil {
		t.Errorf("buffered entity must survive a pointInTime delete and commit; Get err = %v", err)
	}
}

func TestDeleteEntitiesConditional_Joined_NoInstant_DeletesBufferedEntities(t *testing.T) {
	h, factory, txMgr, base := newTxJoinTestHandler(t)

	ownerTxID, ownerCtx, err := txMgr.Begin(base)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	joinedCtx, err := txMgr.Join(base, ownerTxID)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	created, err := h.CreateEntity(joinedCtx, sampleWidgetInput())
	if err != nil {
		t.Fatalf("create in joined tx: %v", err)
	}
	bufferedID := created.EntityIDs[0]

	if _, err := h.DeleteEntitiesConditional(joinedCtx, "Widget", "1", nil, nil, false, 0); err != nil {
		t.Fatalf("DeleteEntitiesConditional (joined, fast path): %v", err)
	}
	if err := txMgr.Commit(ownerCtx, ownerTxID); err != nil {
		t.Fatalf("owner commit: %v", err)
	}
	store, err := factory.EntityStore(base)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}
	if _, err := store.Get(base, bufferedID); !errors.Is(err, spi.ErrNotFound) {
		t.Errorf("fast path deletes same-tx buffered entities; Get err = %v, want ErrNotFound", err)
	}
}

func assertSameIDSet(t *testing.T, got, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if len(g) != len(w) {
		t.Fatalf("ids = %v (len %d), want %v (len %d)", g, len(g), w, len(w))
	}
	for i := range g {
		if g[i] != w[i] {
			t.Fatalf("ids = %v, want %v", g, w)
		}
	}
}
```

(The import path for the SPI is whatever `service_delete_batched_test.go` in the same package uses — copy it verbatim; the package identifier is `spi`.)

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `go test ./internal/domain/entity/ -run 'TestDeleteEntitiesConditional_(Unconditional|Joined)' -v`
Expected: `SparesLaterCreates` FAILS (the two later entities are deleted; `IDs` empty), `AlreadyGoneIDInIDToError` FAILS (`matched/removed = 1/1`), `Verbose_ListsAttemptedIDs` FAILS (`IDs` empty), `Joined_PointInTime_SparesBufferedEntities` FAILS (buffered entity deleted). `Plain_ReturnsEmptyIDs` and `Joined_NoInstant_DeletesBufferedEntities` PASS already (they pin the unchanged fast path).

- [ ] **Step 3: Implement the routing rule**

In `internal/domain/entity/service.go`, replace the fast-path guard and its comment:

```go
	// Whole-model fast path: taken only when the request needs nothing per
	// entity — no condition, no instant, no id listing. A pointInTime must
	// select the committed state as at that instant (DeleteAll cannot), and
	// verbose must list the attempted ids (DeleteAll enumerates nothing), so
	// either routes through the per-entity path below with a nil condition,
	// which the zero-value selection plan reads as "every entity".
	if cond == nil && pointInTime == nil && !verbose {
		all, err := h.DeleteAllEntities(ctx, entityName, modelVersion)
		if err != nil {
			return nil, err
		}
		return &DeleteResult{
			EntityModelID: all.EntityModelID,
			MatchedCount:  all.TotalCount,
			RemovedCount:  all.TotalCount,
			IDToError:     map[string]string{},
			IDs:           []string{},
		}, nil
	}
```

Also update the function's doc comment paragraph that begins `// DeleteEntitiesConditional deletes entities of a model. An empty condBody` so it reads:

```go
// DeleteEntitiesConditional deletes entities of a model. An empty condBody
// selects every entity. A present condBody is parsed and only matching
// entities are deleted. pointInTime, when supplied, selects the committed
// state as at that instant (the ambient transaction is ignored, as on every
// point-in-time read) and deletes the current rows; verbose lists every
// attempted id. Selection reuses the search condition primitive so no
// special engine rights are claimed (design §6.1). Selection and deletion
// run inside one transaction; the selection drains an Iterate iterator
// scoped to that SAME transaction (txCtx), so buffered writes already made
// in it are visible to a non-point-in-time selection — and, per the SPI's
// no-interleave rule, the iterator is fully drained and closed BEFORE the
// first delete, never interleaved with one.
```

- [ ] **Step 4: Run the tests to confirm they pass**

Run: `go test ./internal/domain/entity/ -run 'TestDeleteEntitiesConditional_(Unconditional|Joined)' -v`
Expected: all six PASS.

Then the whole package: `go test ./internal/domain/entity/...`
Expected: PASS (in particular `TestDeleteEntitiesVerbose` in `handler_test.go` still passes — it asserts counts only).

- [ ] **Step 5: Commit**

```bash
git add internal/domain/entity/service.go internal/domain/entity/service_delete_unconditional_test.go
git commit -m "fix(entity): unconditional delete honors pointInTime and verbose

The whole-model fast path is taken only when nothing per entity is
needed. A pointInTime on an empty-body delete used to be dropped, so the
delete removed entities created after the instant; verbose returned an
empty id list beside a non-zero count.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task 2: gRPC delete-all passes `pointInTime`/`verbose` through and fills `entityIds`

**Files:**
- Modify: `internal/grpc/entity.go:446-533` (case `EntityDeleteAllRequest`)
- Test: `internal/grpc/entity_deleteall_fields_test.go` (new)

**Interfaces:**
- Consumes: Task 1's routing rule; `events.EntityDeleteAllRequestJson{Model, PageSize int, TransactionSize *int, PointInTime *time.Time, Verbose bool}` (Task 3 removes `PageSize`; this task does not reference it); `events.EntityDeleteAllResponseJson{ID, Success, Warnings, RequestID, ModelID string, NumDeleted int, EntityIds []string, ErrorsByID}`; helpers in `internal/grpc/rpc_test.go` (`makeCE`, `mockManageStream`, `validateResponse`, `parseResponsePayload`) and `internal/grpc/entity_deleteall_txsize_test.go` (`newDeleteAllTxSizeEnv`, `seedDeleteAllTxSizePersons`).
- Produces: one code path in the handler: `delRes, err := s.entityHandler.DeleteEntitiesConditional(ctx, name, version, nil, req.PointInTime, req.Verbose, size)` with `size == 0` when `transactionSize` is absent.

- [ ] **Step 1: Write the failing tests**

Create `internal/grpc/entity_deleteall_fields_test.go`:

```go
package grpc

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/api/grpc/events"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// seedDeleteAllPersonIDs mirrors seedDeleteAllTxSizePersons but returns the
// created ids, in creation order, so a test can compare them with the
// response's entityIds. It imports+locks "person" on the first call only.
func seedDeleteAllPersonIDs(t *testing.T, svc *CloudEventsServiceImpl, ctx context.Context, n int, importModel bool) []string {
	t.Helper()
	if importModel {
		seedDeleteAllTxSizePersons(t, svc, ctx, 0)
	}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ce := makeCE(EntityCreateRequest, map[string]any{
			"id":         "seed",
			"dataFormat": "JSON",
			"payload": map[string]any{
				"model": map[string]any{"name": "person", "version": 1},
				"data":  map[string]any{"name": "Alice"},
			},
		})
		resp, err := svc.EntityManage(ctx, ce)
		if err != nil {
			t.Fatalf("seed create[%d]: %v", i, err)
		}
		payload := parseResponsePayload(t, resp)
		txInfo := payload["transactionInfo"].(map[string]any)
		ids = append(ids, txInfo["entityIds"].([]any)[0].(string))
	}
	return ids
}

func deleteAllViaGRPC(t *testing.T, svc *CloudEventsServiceImpl, ctx context.Context, fields map[string]any) events.EntityDeleteAllResponseJson {
	t.Helper()
	req := map[string]any{
		"id":    "test",
		"model": map[string]any{"name": "person", "version": 1},
	}
	for k, v := range fields {
		req[k] = v
	}
	stream := &mockManageStream{ctx: ctx}
	if err := svc.EntityManageCollection(makeCE(EntityDeleteAllRequest, req), stream); err != nil {
		t.Fatalf("EntityManageCollection: %v", err)
	}
	if len(stream.sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(stream.sent))
	}
	var typed events.EntityDeleteAllResponseJson
	validateResponse(t, stream.sent[0], &typed)
	return typed
}

func countPersonsViaGRPC(t *testing.T, svc *CloudEventsServiceImpl, ctx context.Context) int {
	t.Helper()
	ce := makeCE(EntityGetAllRequest, map[string]any{
		"id":    "test",
		"model": map[string]any{"name": "person", "version": 1},
	})
	stream := &mockEntityStream{ctx: ctx}
	if err := svc.EntitySearchCollection(ce, stream); err != nil {
		t.Fatalf("EntitySearchCollection: %v", err)
	}
	return len(stream.sent)
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// pointInTime on the delete-all event selects the committed state as at
// that instant: entities created after it survive, and the whole-model
// DeleteAll fast path is bypassed.
func TestRPC_EntityDeleteAll_PointInTime_SparesLaterCreates(t *testing.T) {
	svc, ctx, spyStore, _ := newDeleteAllTxSizeEnv(t)
	before := seedDeleteAllPersonIDs(t, svc, ctx, 3, true)
	time.Sleep(2 * time.Millisecond)
	pit := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	seedDeleteAllPersonIDs(t, svc, ctx, 2, false)

	typed := deleteAllViaGRPC(t, svc, ctx, map[string]any{
		"pointInTime": pit.Format(time.RFC3339Nano),
		"verbose":     true,
	})
	if !typed.Success {
		t.Fatalf("expected success=true, error=%+v", typed.Error)
	}
	if typed.NumDeleted != 3 {
		t.Errorf("NumDeleted = %d, want 3", typed.NumDeleted)
	}
	if got, want := sortedCopy(typed.EntityIds), sortedCopy(before); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("EntityIds = %v, want %v", got, want)
	}
	if spyStore.wasCalled() {
		t.Error("EntityStore.DeleteAll must NOT be called when pointInTime is set")
	}
	if n := countPersonsViaGRPC(t, svc, ctx); n != 2 {
		t.Errorf("entities remaining = %d, want the 2 created after the instant", n)
	}
}

func TestRPC_EntityDeleteAll_Verbose_ListsAttemptedIDs_SingleTx(t *testing.T) {
	svc, ctx, spyStore, rtm := newDeleteAllTxSizeEnv(t)
	ids := seedDeleteAllPersonIDs(t, svc, ctx, 3, true)
	commitsBefore := rtm.commitCount()

	typed := deleteAllViaGRPC(t, svc, ctx, map[string]any{"verbose": true})
	if !typed.Success {
		t.Fatalf("expected success=true, error=%+v", typed.Error)
	}
	if typed.NumDeleted != 3 {
		t.Errorf("NumDeleted = %d, want 3", typed.NumDeleted)
	}
	if got, want := sortedCopy(typed.EntityIds), sortedCopy(ids); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("EntityIds = %v, want %v", got, want)
	}
	if spyStore.wasCalled() {
		t.Error("EntityStore.DeleteAll must NOT be called when verbose is true (ids must be enumerated)")
	}
	if commits := rtm.commitCount() - commitsBefore; commits != 1 {
		t.Errorf("commits = %d, want exactly 1 (single transaction)", commits)
	}
}

func TestRPC_EntityDeleteAll_Verbose_ListsAttemptedIDs_Batched(t *testing.T) {
	svc, ctx, _, _ := newDeleteAllTxSizeEnv(t)
	ids := seedDeleteAllPersonIDs(t, svc, ctx, 5, true)

	typed := deleteAllViaGRPC(t, svc, ctx, map[string]any{"verbose": true, "transactionSize": 2})
	if !typed.Success {
		t.Fatalf("expected success=true, error=%+v", typed.Error)
	}
	if typed.NumDeleted != 5 {
		t.Errorf("NumDeleted = %d, want 5", typed.NumDeleted)
	}
	if got, want := sortedCopy(typed.EntityIds), sortedCopy(ids); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("EntityIds = %v, want %v", got, want)
	}
}

// Without verbose the required entityIds field is present and empty, and
// the fast path is still taken — the existing single-tx contract.
func TestRPC_EntityDeleteAll_NotVerbose_EmptyEntityIds_FastPath(t *testing.T) {
	svc, ctx, spyStore, _ := newDeleteAllTxSizeEnv(t)
	seedDeleteAllPersonIDs(t, svc, ctx, 2, true)

	typed := deleteAllViaGRPC(t, svc, ctx, nil)
	if !typed.Success {
		t.Fatalf("expected success=true, error=%+v", typed.Error)
	}
	if typed.EntityIds == nil || len(typed.EntityIds) != 0 {
		t.Errorf("EntityIds = %#v, want present and empty", typed.EntityIds)
	}
	if !spyStore.wasCalled() {
		t.Error("EntityStore.DeleteAll must be called on a plain delete-all")
	}
}

func TestRPC_EntityDeleteAll_PointInTime_ModelNotFound_Envelope(t *testing.T) {
	svc, ctx := newTestEnv(t)
	pit := time.Now().UTC().Format(time.RFC3339Nano)
	ce := makeCE(EntityDeleteAllRequest, map[string]any{
		"id":          "test",
		"model":       map[string]any{"name": "nosuchmodel", "version": 1},
		"pointInTime": pit,
	})
	stream := &mockManageStream{ctx: ctx}
	if err := svc.EntityManageCollection(ce, stream); err != nil {
		t.Fatalf("EntityManageCollection: %v", err)
	}
	var typed events.EntityDeleteAllResponseJson
	validateResponse(t, stream.sent[0], &typed)
	if typed.Success {
		t.Fatal("expected success=false for an unknown model")
	}
	if typed.Error == nil || typed.Error.Code != "CLIENT_ERROR" {
		t.Fatalf("Error = %+v, want code CLIENT_ERROR", typed.Error)
	}
	if !strings.HasPrefix(typed.Error.Message, common.ErrCodeModelNotFound+":") {
		t.Errorf("Error.Message = %q, want prefix %q", typed.Error.Message, common.ErrCodeModelNotFound+":")
	}
}

func TestRPC_EntityDeleteAll_MalformedPointInTime_InvalidArgument(t *testing.T) {
	svc, ctx := newTestEnv(t)
	ce := makeCE(EntityDeleteAllRequest, map[string]any{
		"id":          "test",
		"model":       map[string]any{"name": "person", "version": 1},
		"pointInTime": "not-a-timestamp",
	})
	stream := &mockManageStream{ctx: ctx}
	err := svc.EntityManageCollection(ce, stream)
	if err == nil {
		t.Fatal("expected a gRPC status error for a malformed pointInTime")
	}
	if st, _ := status.FromError(err); st.Code() != codes.InvalidArgument {
		t.Errorf("status code = %v, want InvalidArgument (%v)", st.Code(), err)
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `go test ./internal/grpc/ -run 'TestRPC_EntityDeleteAll_(PointInTime|Verbose|NotVerbose|MalformedPointInTime)' -v`
Expected: `PointInTime_SparesLaterCreates` FAILS (NumDeleted 5, EntityIds empty, DeleteAll called), both `Verbose_*` FAIL (EntityIds empty), `NotVerbose_EmptyEntityIds_FastPath` PASSES, `PointInTime_ModelNotFound_Envelope` PASSES (404 already flows), `MalformedPointInTime_InvalidArgument` PASSES (decode already fails). Red on the three that matter.

- [ ] **Step 3: Collapse the handler onto one path**

In `internal/grpc/entity.go`, replace the whole `case EntityDeleteAllRequest:` block body (from `dec := json.NewDecoder(...)` to the final `return stream.Send(respCE)` before `default:`) with:

```go
	case EntityDeleteAllRequest:
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.UseNumber()
		var req events.EntityDeleteAllRequestJson
		if err := dec.Decode(&req); err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid payload: %v", err)
		}

		// An explicit transactionSize switches to the batched,
		// enumerate-then-delete path: validate it's a positive integer and
		// reject a joined (tx-token'd) request the same way resolveEventTimeout
		// rejects transactionTimeoutMs on one — honoring it would let a
		// participant unilaterally fragment a transaction the owner still
		// controls. A nil field (the schema bakes no default) means one
		// transaction. pointInTime and verbose are passed through unchanged:
		// the service decides whether the whole-model fast path is still
		// permissible (only when nothing per entity is needed).
		size := 0
		if req.TransactionSize != nil {
			size = *req.TransactionSize
			if size < 1 {
				respCE, ceErr := entityDeleteAllError(ctx, ce.Id, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest,
					"transactionSize must be a positive integer"))
				if ceErr != nil {
					return status.Errorf(codes.Internal, "failed to build error response: %v", ceErr)
				}
				return stream.Send(respCE)
			}
			if spi.GetTransaction(ctx) != nil {
				respCE, ceErr := entityDeleteAllError(ctx, ce.Id, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest,
					"transactionSize is not supported on a request that joins an open transaction"))
				if ceErr != nil {
					return status.Errorf(codes.Internal, "failed to build error response: %v", ceErr)
				}
				return stream.Send(respCE)
			}
		}

		delRes, err := s.entityHandler.DeleteEntitiesConditional(ctx, req.Model.Name, fmt.Sprintf("%d", req.Model.Version), nil, req.PointInTime, req.Verbose, size)
		if err != nil {
			slog.Error("operation failed", "pkg", "grpc", "rpc", "entityManageCollection", "type", eventType, "ceId", ce.Id, "error", err.Error())
			respCE, ceErr := entityDeleteAllError(ctx, ce.Id, err)
			if ceErr != nil {
				return status.Errorf(codes.Internal, "failed to build error response: %v", ceErr)
			}
			return stream.Send(respCE)
		}

		// entityIds is required on the wire: the attempted ids when verbose,
		// otherwise an empty list (never null).
		entityIDs := delRes.IDs
		if entityIDs == nil {
			entityIDs = []string{}
		}
		diag := common.GetDiagnostics(ctx)
		resp := events.EntityDeleteAllResponseJson{
			ID:         ce.Id,
			Success:    true,
			Warnings:   diag.GetWarnings(),
			RequestID:  ce.Id,
			ModelID:    delRes.EntityModelID,
			NumDeleted: delRes.RemovedCount,
			EntityIds:  entityIDs,
			ErrorsByID: deleteAllErrorsByID(delRes.IDToError),
		}
		respCE, err := NewCloudEvent(EntityDeleteAllResponse, resp)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to build response: %v", err)
		}
		return stream.Send(respCE)
```

If `DeleteAllEntities` is now unreferenced from `internal/grpc`, leave the service method in place (the HTTP fast path and Task 1 use it).

- [ ] **Step 4: Run the gRPC package**

Run: `go test ./internal/grpc/...`
Expected: PASS, including the pre-existing `TestRPC_EntityDeleteAll_TransactionSize_Absent_SingleTx` (spy called, one commit — the fast path is still reached through the service) and `TestRPC_EntityDeleteAll_NonConvergence_Envelope`.

- [ ] **Step 5: Commit**

```bash
git add internal/grpc/entity.go internal/grpc/entity_deleteall_fields_test.go
git commit -m "fix(grpc): delete-all honors pointInTime and verbose; entityIds populated

Both delete-all paths now go through the service's conditional delete
with a nil condition, so the instant is honored, the attempted ids are
returned when verbose, and a plain request still takes the whole-model
fast path.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task 3: Remove `pageSize` from `EntityDeleteAllRequest`; fix the response description

**Files:**
- Modify: `docs/cyoda/schema/entity/EntityDeleteAllRequest.json` (drop the `pageSize` property)
- Modify: `docs/cyoda/schema/entity/EntityDeleteAllResponse.json` (`entityIds` description)
- Regenerate: `api/grpc/events/types.go` via `scripts/generate-events.sh`
- Test: `internal/grpc/entity_deleteall_txsize_test.go` (add one decode-pin test next to `TestEntityDeleteAllRequestJson_TransactionSize_NoDefault`)

**Interfaces:**
- Produces: `events.EntityDeleteAllRequestJson` without a `PageSize` field. No code in the repo reads it (verified: only the generated unmarshaller). External Go importers constructing the struct with `PageSize:` stop compiling — accepted pre-1.0, recorded in Task 8.

- [ ] **Step 1: Write the failing decode-pin test**

Append to `internal/grpc/entity_deleteall_txsize_test.go`:

```go
// TestEntityDeleteAllRequestJson_PageSize_Tolerated pins that pageSize is no
// longer part of the request type (there is nothing for it to mean —
// selection is streamed) while a client still sending it is tolerated: the
// generated unmarshaller ignores unknown fields.
func TestEntityDeleteAllRequestJson_PageSize_Tolerated(t *testing.T) {
	payload := []byte(`{"id":"test","model":{"name":"person","version":1},"pageSize":10,"verbose":true}`)

	var req events.EntityDeleteAllRequestJson
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal with a legacy pageSize field must succeed: %v", err)
	}
	if !req.Verbose {
		t.Error("Verbose = false, want true (the rest of the payload must still bind)")
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "pageSize") {
		t.Errorf("re-encoded request still carries pageSize: %s", raw)
	}
}

// TestRPC_EntityDeleteAll_PageSize_Tolerated pins the door-level half: a
// legacy client that still sends pageSize gets a normal success envelope.
func TestRPC_EntityDeleteAll_PageSize_Tolerated(t *testing.T) {
	svc, ctx, _, _ := newDeleteAllTxSizeEnv(t)
	seedDeleteAllPersonIDs(t, svc, ctx, 2, true)

	typed := deleteAllViaGRPC(t, svc, ctx, map[string]any{"pageSize": 10})
	if !typed.Success {
		t.Fatalf("expected success=true with a legacy pageSize field, error=%+v", typed.Error)
	}
	if typed.NumDeleted != 2 {
		t.Errorf("NumDeleted = %d, want 2", typed.NumDeleted)
	}
}
```

(`deleteAllViaGRPC` and `seedDeleteAllPersonIDs` come from Task 2's `entity_deleteall_fields_test.go`. `strings` and `encoding/json` are already imported in `entity_deleteall_txsize_test.go`; if not, add them.)

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/grpc/ -run TestEntityDeleteAllRequestJson_PageSize_Tolerated -v`
Expected: FAIL at the `pageSize` re-encode assertion (the field still exists and is emitted as `"pageSize":10`).

- [ ] **Step 3: Edit the schemas**

In `docs/cyoda/schema/entity/EntityDeleteAllRequest.json` delete the whole `"pageSize"` property block:

```json
    "pageSize": {
      "type": "integer",
      "description": "Page size.",
      "default": 10
    },
```

In `docs/cyoda/schema/entity/EntityDeleteAllResponse.json` change the `entityIds` description to:

```json
      "description": "IDs the delete attempted, when verbose was requested; an id whose delete failed also appears in errorsById. Empty when verbose is false.",
```

- [ ] **Step 4: Regenerate the Go types**

Run: `./scripts/generate-events.sh`
Expected: exits 0 and prints its "retyped 3 fields" guard line. `git diff --stat api/grpc/events/types.go` shows only the `EntityDeleteAllRequestJson` struct (field + comment gone), its `UnmarshalJSON` (the `pageSize` default block gone), and the `EntityDeleteAllResponseJson.EntityIds` comment. If the diff touches anything else, stop and inspect — the script's post-fixes (Success omitempty strip, `decodeWithUseNumber`, the three `json.RawMessage` retypes) must survive untouched.

- [ ] **Step 5: Build and run the tests**

Run: `go build ./... && go test ./internal/grpc/... ./api/...`
Expected: PASS. Nothing else in the repo referenced `PageSize` (confirm with `grep -rn "PageSize" internal/grpc api/grpc/events` — only `EntityGetAllRequest`/snapshot request types, which are unrelated, remain).

- [ ] **Step 6: Commit**

```bash
git add docs/cyoda/schema/entity/EntityDeleteAllRequest.json docs/cyoda/schema/entity/EntityDeleteAllResponse.json api/grpc/events/types.go internal/grpc/entity_deleteall_txsize_test.go
git commit -m "fix(grpc)!: remove pageSize from EntityDeleteAllRequest; entityIds means attempted ids

Selection is streamed, so a page size has nothing to control; the field
was decoded and ignored. The generated Go type loses the field (a client
still sending it is tolerated).

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task 4: Retire `waitForConsistencyAfter` from the OpenAPI spec; reword the delete descriptions

**Files:**
- Modify: `api/openapi.yaml` — the 7 `- name: waitForConsistencyAfter` parameter blocks (at roughly lines 2142, 2370, 2584, 2765, 2936, 3127, 3306) and the `deleteEntities` `pointInTime`/`verbose` descriptions (roughly lines 1932-1948)
- Regenerate: `api/generated.go` via `go generate ./api`
- Modify: `internal/domain/entity/handler_test.go` — delete `TestWaitForConsistencyFalse` (lines ~1934-1965)

**Interfaces:**
- Produces: the 7 generated `*Params` structs (`CreateParams`, `CreateCollectionParams`, `UpdateCollectionParams`, `UpdateSingleParams`, `UpdateSingleWithLoopbackParams`, `PatchSingleParams`, `PatchSingleWithLoopbackParams`) lose the `WaitForConsistencyAfter *bool` field. No handler reads it, so nothing else changes.

- [ ] **Step 1: Write the failing compile-time pin**

Create `api/params_retired_test.go`:

```go
package api

import (
	"reflect"
	"testing"
)

// The consistency-wait flag is retired: a successful write response already
// means the write is visible to every subsequent read, so there is nothing
// for a flag to select. This pins that no generated params struct declares it
// again.
func TestNoParamsStructDeclaresWaitForConsistencyAfter(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(CreateParams{}),
		reflect.TypeOf(CreateCollectionParams{}),
		reflect.TypeOf(UpdateCollectionParams{}),
		reflect.TypeOf(UpdateSingleParams{}),
		reflect.TypeOf(UpdateSingleWithLoopbackParams{}),
		reflect.TypeOf(PatchSingleParams{}),
		reflect.TypeOf(PatchSingleWithLoopbackParams{}),
	} {
		if _, has := typ.FieldByName("WaitForConsistencyAfter"); has {
			t.Errorf("%s still declares WaitForConsistencyAfter", typ.Name())
		}
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./api/ -run TestNoParamsStructDeclaresWaitForConsistencyAfter -v`
Expected: FAIL for all 7 structs.

- [ ] **Step 3: Edit the spec**

Delete each of the 7 blocks in `api/openapi.yaml` in full — from the `- name: waitForConsistencyAfter` line through its `default: false` line (12 lines each; two variants of the description exist, both go). For example the block at the `createCollection` operation:

```yaml
        - name: waitForConsistencyAfter
          in: query
          description: |
            If true, waits for consistency after operation completes.
            Accepted for Cyoda Cloud API parity. Behavior is
            storage-engine-plugin dependent — not every plugin honors this
            field; consult the runtime plugin's documentation for the
            supported behavior.
          required: false
          schema:
            type: boolean
            default: false
```

Then in the `deleteEntities` operation replace the two descriptions:

```yaml
        - name: pointInTime
          in: query
          description: |
            Select the entities that existed at this instant, in ISO 8601
            format (e.g. '2035-01-01T12:00:00Z'), and delete their current
            rows. Absent means the current committed state. An entity
            selected at the instant but already gone is reported in
            idToError.
          required: false
          schema:
            type: string
            format: date-time
          example: 2035-01-01T12:00:00Z
        - name: verbose
          in: query
          description: |
            Include the list of entity IDs the delete attempted in the
            response; an ID whose delete failed also appears in idToError.
            When false, only statistics are returned.
          required: false
          schema:
            type: boolean
            default: false
          example: true
```

Confirm: `grep -c waitForConsistencyAfter api/openapi.yaml` prints `0`.

- [ ] **Step 4: Regenerate and remove the dead test**

Run: `go generate ./api`
Expected: `api/generated.go` diff removes exactly the 7 `WaitForConsistencyAfter` fields and their 7 binding blocks (`grep -c WaitForConsistencyAfter api/generated.go` prints `0`).

Delete `TestWaitForConsistencyFalse` from `internal/domain/entity/handler_test.go` (the whole function; the e2e test in Task 5 covers tolerance for all 7 ops).

- [ ] **Step 5: Build, vet, run the pin and the affected packages**

Run: `go build ./... && go vet ./api/ ./internal/domain/entity/ && go test ./api/ ./internal/domain/entity/...`
Expected: PASS.

- [ ] **Step 6: Confirm the oasdiff gate is unaffected**

Run:

```bash
git show release/v0.8.4:api/openapi.yaml > /tmp/openapi-base.yaml
oasdiff breaking /tmp/openapi-base.yaml api/openapi.yaml --fail-on ERR --err-ignore .github/oasdiff-err-ignore.txt
```

Expected: exit 0; the 7 `request-parameter-removed` lines are reported as **warning**, no error. Do not add ignore entries.

- [ ] **Step 7: Commit**

```bash
git add api/openapi.yaml api/generated.go api/params_retired_test.go internal/domain/entity/handler_test.go
git commit -m "feat(api)!: retire waitForConsistencyAfter; a successful write is always visible on return

The flag toggled nothing: cyoda-go returns from a write only after it is
visible to every subsequent read on every node. Requests still carrying
it are accepted and the parameter is ignored. deleteEntities' pointInTime
and verbose descriptions now say what the server does.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task 5: Running-backend e2e coverage (HTTP, real Postgres)

**Files:**
- Test: `internal/e2e/write_visibility_test.go` (new)
- Test: `internal/e2e/entity_delete_unconditional_test.go` (new)

**Interfaces:**
- Consumes helpers already in the `e2e_test` package: `doAuth(t, method, path, body) *http.Response`, `readBody(t, resp) string`, `importModelWithSample(t, name, ver, sample)`, `lockModelE2E(t, name, ver)`, `setupModelWithWorkflow(t, name, workflowJSON)`, `createEntityE2E(t, name, ver, payload) string`, `createEntityE2EWithTxID(t, name, ver, payload) (id, txID string)`, `getEntityTxID(t, id) string`, `patchEntity(t, path, contentType, ifMatch, body) *http.Response`, `latestChangeTimeE2E(t, id) time.Time`, `midpointBetweenE2E(t, earlier, later time.Time) string`, `commontest.ExpectErrorCode(t, resp, code)`.

- [ ] **Step 1: Write the retired-flag test**

Create `internal/e2e/write_visibility_test.go`:

```go
package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// A successful write response means the write is visible to every
// subsequent read. The former consistency-wait flag is retired; a request
// still carrying it — with any value — is accepted and the flag ignored.
// One case per write operation × value.
func TestWriteVisibility_RetiredFlagIgnored_AllWriteOps(t *testing.T) {
	const model = "e2e-wvis-flag"
	setupModelWithWorkflow(t, model, `{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1.1", "name": "wvis-wf", "initialState": "NONE", "active": true,
			"states": {
				"NONE":    {"transitions": [{"name": "init", "next": "CREATED", "manual": false}]},
				"CREATED": {"transitions": [{"name": "touch", "next": "CREATED", "manual": true}]}
			}
		}]
	}`)

	for _, val := range []string{"true", "false", "maybe"} {
		q := "?waitForConsistencyAfter=" + val

		t.Run("create/"+val, func(t *testing.T) {
			resp := doAuth(t, http.MethodPost, fmt.Sprintf("/api/entity/JSON/%s/1%s", model, q), `{"name":"A","amount":1,"status":"new"}`)
			id := firstEntityID(t, resp)
			assertEntityAmount(t, id, 1)
		})
		t.Run("createCollection/"+val, func(t *testing.T) {
			resp := doAuth(t, http.MethodPost, "/api/entity/JSON"+q,
				fmt.Sprintf(`[{"model":{"name":%q,"version":1},"payload":"{\"name\":\"B\",\"amount\":2,\"status\":\"new\"}"}]`, model))
			id := firstEntityID(t, resp)
			assertEntityAmount(t, id, 2)
		})
		t.Run("updateSingle/"+val, func(t *testing.T) {
			id := createEntityE2E(t, model, 1, `{"name":"C","amount":3,"status":"new"}`)
			resp := doAuth(t, http.MethodPut, fmt.Sprintf("/api/entity/JSON/%s/touch%s", id, q), `{"name":"C","amount":30,"status":"new"}`)
			expect200(t, resp)
			assertEntityAmount(t, id, 30)
		})
		t.Run("updateSingleWithLoopback/"+val, func(t *testing.T) {
			id := createEntityE2E(t, model, 1, `{"name":"D","amount":4,"status":"new"}`)
			resp := doAuth(t, http.MethodPut, fmt.Sprintf("/api/entity/JSON/%s%s", id, q), `{"name":"D","amount":40,"status":"new"}`)
			expect200(t, resp)
			assertEntityAmount(t, id, 40)
		})
		t.Run("updateCollection/"+val, func(t *testing.T) {
			id := createEntityE2E(t, model, 1, `{"name":"E","amount":5,"status":"new"}`)
			resp := doAuth(t, http.MethodPut, "/api/entity/JSON"+q,
				fmt.Sprintf(`[{"id":%q,"payload":"{\"name\":\"E\",\"amount\":50,\"status\":\"new\"}"}]`, id))
			expect200(t, resp)
			assertEntityAmount(t, id, 50)
		})
		t.Run("patchSingleWithLoopback/"+val, func(t *testing.T) {
			id, txID := createEntityE2EWithTxID(t, model, 1, `{"name":"F","amount":6,"status":"new"}`)
			resp := patchEntity(t, fmt.Sprintf("/api/entity/JSON/%s%s", id, q), "application/merge-patch+json", txID, `{"amount":60}`)
			expect200(t, resp)
			assertEntityAmount(t, id, 60)
		})
		t.Run("patchSingle/"+val, func(t *testing.T) {
			id, txID := createEntityE2EWithTxID(t, model, 1, `{"name":"G","amount":7,"status":"new"}`)
			resp := patchEntity(t, fmt.Sprintf("/api/entity/JSON/%s/touch%s", id, q), "application/merge-patch+json", txID, `{"amount":70}`)
			expect200(t, resp)
			assertEntityAmount(t, id, 70)
		})
	}
}

func expect200(t *testing.T, resp *http.Response) {
	t.Helper()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
}

// firstEntityID reads a create response ([{transactionId, entityIds}]) and
// returns its first entity id, failing on any other status.
func firstEntityID(t *testing.T, resp *http.Response) string {
	t.Helper()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	var out []struct {
		EntityIDs []string `json:"entityIds"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out) == 0 || len(out[0].EntityIDs) == 0 {
		t.Fatalf("unexpected create response: %v: %s", err, body)
	}
	return out[0].EntityIDs[0]
}

// assertEntityAmount is the read-your-write half of the contract: the GET
// issued immediately after the write's response sees the written value.
func assertEntityAmount(t *testing.T, id string, want float64) {
	t.Helper()
	data := getEntityData(t, id, "")
	if got := data["amount"]; got != want {
		t.Fatalf("GET after write: amount = %v, want %v", got, want)
	}
}
```

Check `getEntityData`'s signature in `internal/e2e/temporal_test.go:72` (`func getEntityData(t *testing.T, entityID, pointInTime string) map[string]any`) and that its returned map is the entity's `data`; if it nests differently, adjust `assertEntityAmount` to read the `amount` field where it is.

- [ ] **Step 2: Write the unconditional-delete tests**

Create `internal/e2e/entity_delete_unconditional_test.go`:

```go
package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common/commontest"
)

type deleteResp struct {
	EntityModelClassID string `json:"entityModelClassId"`
	DeleteResult       struct {
		IDToError                map[string]string `json:"idToError"`
		NumberOfEntitites        int               `json:"numberOfEntitites"`
		NumberOfEntititesRemoved int               `json:"numberOfEntititesRemoved"`
	} `json:"deleteResult"`
	IDs []string `json:"ids"`
}

func deleteModelEntities(t *testing.T, model, rawQuery string) (int, deleteResp, string) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s/1", model)
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	resp := doAuth(t, http.MethodDelete, path, "")
	body := readBody(t, resp)
	var out deleteResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("decode delete response: %v: %s", err, body)
		}
	}
	return resp.StatusCode, out, body
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func entityExists(t *testing.T, id string) bool {
	t.Helper()
	resp := doAuth(t, http.MethodGet, "/api/entity/"+id, "")
	readBody(t, resp)
	return resp.StatusCode == http.StatusOK
}

// An empty-body delete with pointInTime selects the entities that existed
// at the instant and deletes their current rows; entities created after
// the instant survive; an entity selected at the instant but already gone
// is reported per id. verbose lists every attempted id.
func TestDeleteEntities_Unconditional_PointInTime(t *testing.T) {
	const model = "e2e-deluncond-pit"
	importModelWithSample(t, model, 1, `{"n":0}`)
	lockModelE2E(t, model, 1)

	a := createEntityE2E(t, model, 1, `{"n":1}`)
	b := createEntityE2E(t, model, 1, `{"n":2}`)
	tB := latestChangeTimeE2E(t, b)
	time.Sleep(10 * time.Millisecond)
	c := createEntityE2E(t, model, 1, `{"n":3}`)
	tC := latestChangeTimeE2E(t, c)
	instant := midpointBetweenE2E(t, tB, tC) // server-clock boundary between b and c

	// b is selected at the instant but gone by the time the delete runs.
	if resp := doAuth(t, http.MethodDelete, "/api/entity/"+b, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete b: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	status, out, body := deleteModelEntities(t, model, "pointInTime="+instant+"&verbose=true")
	if status != http.StatusOK {
		t.Fatalf("delete as-at: %d: %s", status, body)
	}
	if out.DeleteResult.NumberOfEntitites != 2 || out.DeleteResult.NumberOfEntititesRemoved != 1 {
		t.Errorf("matched/removed = %d/%d, want 2/1: %s", out.DeleteResult.NumberOfEntitites, out.DeleteResult.NumberOfEntititesRemoved, body)
	}
	if _, ok := out.DeleteResult.IDToError[b]; !ok {
		t.Errorf("idToError = %v, want an entry for the already-gone id %s", out.DeleteResult.IDToError, b)
	}
	if got, want := sortedStrings(out.IDs), sortedStrings([]string{a, b}); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want the attempted set %v", got, want)
	}
	if entityExists(t, a) {
		t.Error("a existed at the instant and must be gone")
	}
	if !entityExists(t, c) {
		t.Error("c was created after the instant and must survive")
	}

	t.Run("future instant selects the current state", func(t *testing.T) {
		d := createEntityE2E(t, model, 1, `{"n":4}`)
		status, out, body := deleteModelEntities(t, model, "pointInTime=2099-01-01T00:00:00Z&verbose=true")
		if status != http.StatusOK {
			t.Fatalf("delete as-at future: %d: %s", status, body)
		}
		if out.DeleteResult.NumberOfEntititesRemoved != 2 { // c and d
			t.Errorf("removed = %d, want 2: %s", out.DeleteResult.NumberOfEntititesRemoved, body)
		}
		if entityExists(t, c) || entityExists(t, d) {
			t.Error("a future instant selects everything that exists now")
		}
	})
}

func TestDeleteEntities_Unconditional_Verbose_ListsAttemptedIDs(t *testing.T) {
	const model = "e2e-deluncond-verbose"
	importModelWithSample(t, model, 1, `{"n":0}`)
	lockModelE2E(t, model, 1)
	ids := []string{
		createEntityE2E(t, model, 1, `{"n":1}`),
		createEntityE2E(t, model, 1, `{"n":2}`),
		createEntityE2E(t, model, 1, `{"n":3}`),
	}

	status, out, body := deleteModelEntities(t, model, "verbose=true")
	if status != http.StatusOK {
		t.Fatalf("delete verbose: %d: %s", status, body)
	}
	if out.DeleteResult.NumberOfEntitites != 3 || out.DeleteResult.NumberOfEntititesRemoved != 3 {
		t.Errorf("matched/removed = %d/%d, want 3/3: %s", out.DeleteResult.NumberOfEntitites, out.DeleteResult.NumberOfEntititesRemoved, body)
	}
	if got, want := sortedStrings(out.IDs), sortedStrings(ids); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want %v", got, want)
	}

	t.Run("without verbose the ids field is absent", func(t *testing.T) {
		createEntityE2E(t, model, 1, `{"n":9}`)
		status, _, body := deleteModelEntities(t, model, "")
		if status != http.StatusOK {
			t.Fatalf("plain delete: %d: %s", status, body)
		}
		if strings.Contains(body, `"ids"`) {
			t.Errorf("plain delete must not carry ids: %s", body)
		}
	})
}

func TestDeleteEntities_Unconditional_PointInTime_ModelNotFound(t *testing.T) {
	status, _, body := deleteModelEntities(t, "e2e-deluncond-nosuch", "pointInTime=2030-01-01T00:00:00Z")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if !strings.Contains(body, "MODEL_NOT_FOUND") {
		t.Errorf("body must carry MODEL_NOT_FOUND: %s", body)
	}
}

func TestDeleteEntities_Unconditional_MalformedPointInTime_400(t *testing.T) {
	const model = "e2e-deluncond-badpit"
	importModelWithSample(t, model, 1, `{"n":0}`)
	lockModelE2E(t, model, 1)
	resp := doAuth(t, http.MethodDelete, fmt.Sprintf("/api/entity/%s/1?pointInTime=not-a-time", model), "")
	commontest.ExpectErrorCode(t, resp, "BAD_REQUEST")
}
```

- [ ] **Step 3: Run the two files against the running backend**

Run: `go test ./internal/e2e/ -run 'TestWriteVisibility_RetiredFlagIgnored_AllWriteOps|TestDeleteEntities_Unconditional_' -timeout 15m`
Expected: PASS (Tasks 1–4 are already in place, so these are green on first run; if any case fails, the failure is real — fix the implementation, not the test). Docker must be running (`make preflight`).

If the `updateSingle` case answers 400 because the model's `touch` transition is not reachable from `CREATED`, check the created entity's state with `getEntityState(t, id)` (`internal/e2e/collection_update_ifmatch_test.go`) and adjust the workflow so the manual transition starts from the post-create state.

- [ ] **Step 4: Commit**

```bash
git add internal/e2e/write_visibility_test.go internal/e2e/entity_delete_unconditional_test.go
git commit -m "test(e2e): retired consistency flag ignored on all write ops; unconditional delete honors pointInTime and verbose

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task 6: Cross-backend parity scenarios

**Files:**
- Modify: `e2e/parity/client/http.go` (add `DeleteEntitiesByModelVerbose` next to `DeleteEntitiesByModelAt`, ~line 1504)
- Create: `e2e/parity/entity_delete_all.go`
- Modify: `e2e/parity/registry.go` (two entries after `{"EntityDelete", RunEntityDelete},` and the header count on line 5)
- Modify: `e2e/parity/registry_count_test.go` (`wantParityScenarioCount` 270 → 272)

**Interfaces:**
- Produces: `func (c *Client) DeleteEntitiesByModelVerbose(t *testing.T, name string, version int, pointInTime *time.Time) (StreamDeleteResult, error)` — DELETE with `verbose=true` and, when non-nil, `pointInTime=<RFC3339Nano UTC>`; empty body.
- Consumes: `client.NewClient`, `fixture.NewTenant`, `setupSimpleWorkflow`, `c.CreateEntity`, `c.GetEntity`, `LatestChangeTime`, `MidpointBetween`, `StreamDeleteResult{MatchedCount, RemovedCount int; IDToError map[string]string; IDs []string}`.

- [ ] **Step 1: Add the client method**

In `e2e/parity/client/http.go`, after `DeleteEntitiesByModelAt`:

```go
// DeleteEntitiesByModelVerbose issues DELETE /api/entity/{name}/{version}?verbose=true
// with an empty body (every entity of the model) and, when pointInTime is
// non-nil, &pointInTime=<RFC3339Nano UTC> so the selection is the committed
// state as at that instant. Returns the decoded StreamDeleteResult.
func (c *Client) DeleteEntitiesByModelVerbose(t *testing.T, name string, version int, pointInTime *time.Time) (StreamDeleteResult, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s/%d?verbose=true", name, version)
	if pointInTime != nil {
		path += "&pointInTime=" + pointInTime.UTC().Format(time.RFC3339Nano)
	}
	raw, err := c.doRaw(t, http.MethodDelete, path, "")
	if err != nil {
		return StreamDeleteResult{}, err
	}
	var resp struct {
		DeleteResult struct {
			IDToError                map[string]string `json:"idToError"`
			NumberOfEntitites        int               `json:"numberOfEntitites"`
			NumberOfEntititesRemoved int               `json:"numberOfEntititesRemoved"`
		} `json:"deleteResult"`
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return StreamDeleteResult{}, fmt.Errorf("decode DeleteEntitiesByModelVerbose response: %w (body=%s)", err, string(raw))
	}
	return StreamDeleteResult{
		MatchedCount: resp.DeleteResult.NumberOfEntitites,
		RemovedCount: resp.DeleteResult.NumberOfEntititesRemoved,
		IDToError:    resp.DeleteResult.IDToError,
		IDs:          resp.IDs,
	}, nil
}
```

- [ ] **Step 2: Write the scenarios**

Create `e2e/parity/entity_delete_all.go`:

```go
package parity

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
	"github.com/google/uuid"
)

func sortedIDStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	sort.Strings(out)
	return out
}

func sortedStrs(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// RunEntityDeleteAllPointInTime verifies that an empty-body delete with
// pointInTime selects the committed state as at that instant on every
// backend: entities created after the instant survive, an entity selected
// at the instant but already gone is reported per id, and verbose lists the
// attempted set.
func RunEntityDeleteAllPointInTime(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "entity-delete-all-pit-test"
	const modelVersion = 1
	setupSimpleWorkflow(t, c, modelName, modelVersion)

	a, err := c.CreateEntity(t, modelName, modelVersion, `{"name":"A","amount":1,"status":"new"}`)
	if err != nil {
		t.Fatalf("CreateEntity a: %v", err)
	}
	b, err := c.CreateEntity(t, modelName, modelVersion, `{"name":"B","amount":2,"status":"new"}`)
	if err != nil {
		t.Fatalf("CreateEntity b: %v", err)
	}
	tB := LatestChangeTime(t, c, b)
	time.Sleep(50 * time.Millisecond)
	cID, err := c.CreateEntity(t, modelName, modelVersion, `{"name":"C","amount":3,"status":"new"}`)
	if err != nil {
		t.Fatalf("CreateEntity c: %v", err)
	}
	tC := LatestChangeTime(t, c, cID)
	instant := MidpointBetween(t, tB, tC)

	if err := c.DeleteEntity(t, b); err != nil {
		t.Fatalf("DeleteEntity b: %v", err)
	}

	res, err := c.DeleteEntitiesByModelVerbose(t, modelName, modelVersion, &instant)
	if err != nil {
		t.Fatalf("DeleteEntitiesByModelVerbose: %v", err)
	}
	if res.MatchedCount != 2 || res.RemovedCount != 1 {
		t.Errorf("matched/removed = %d/%d, want 2/1", res.MatchedCount, res.RemovedCount)
	}
	if _, ok := res.IDToError[b.String()]; !ok {
		t.Errorf("idToError = %v, want an entry for the already-gone id %s", res.IDToError, b)
	}
	if got, want := sortedStrs(res.IDs), sortedIDStrings([]uuid.UUID{a, b}); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want the attempted set %v", got, want)
	}
	if _, err := c.GetEntity(t, a); err == nil {
		t.Error("a existed at the instant and must be gone")
	}
	if _, err := c.GetEntity(t, cID); err != nil {
		t.Errorf("c was created after the instant and must survive: %v", err)
	}
}

// RunEntityDeleteAllVerbose verifies that an empty-body delete with
// verbose=true lists every attempted id and removes every entity, on every
// backend.
func RunEntityDeleteAllVerbose(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "entity-delete-all-verbose-test"
	const modelVersion = 1
	setupSimpleWorkflow(t, c, modelName, modelVersion)

	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		id, err := c.CreateEntity(t, modelName, modelVersion, `{"name":"V","amount":1,"status":"new"}`)
		if err != nil {
			t.Fatalf("CreateEntity[%d]: %v", i, err)
		}
		ids = append(ids, id)
	}

	res, err := c.DeleteEntitiesByModelVerbose(t, modelName, modelVersion, nil)
	if err != nil {
		t.Fatalf("DeleteEntitiesByModelVerbose: %v", err)
	}
	if res.MatchedCount != 3 || res.RemovedCount != 3 {
		t.Errorf("matched/removed = %d/%d, want 3/3", res.MatchedCount, res.RemovedCount)
	}
	if got, want := sortedStrs(res.IDs), sortedIDStrings(ids); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want %v", got, want)
	}
	for _, id := range ids {
		if _, err := c.GetEntity(t, id); err == nil {
			t.Errorf("entity %s must be gone", id)
		}
	}
}
```

- [ ] **Step 3: Register and bump the count**

In `e2e/parity/registry.go`, after `{"EntityDelete", RunEntityDelete},` add:

```go
	{"EntityDeleteAllPointInTime", RunEntityDeleteAllPointInTime},
	{"EntityDeleteAllVerbose", RunEntityDeleteAllVerbose},
```

Change the header comment `// Total parity scenarios: 270` to `272`, and in `e2e/parity/registry_count_test.go` set `const wantParityScenarioCount = 272`.

- [ ] **Step 4: Run the parity suites**

Run: `go test ./e2e/parity/... -run 'TestParity/EntityDeleteAll|TestParityScenario' -timeout 20m`
Expected: PASS on memory, sqlite and postgres (the postgres fixture needs Docker). If one backend fails and the others pass, that is a backend divergence to fix in the plugin, not a test to weaken — stop and report it.

- [ ] **Step 5: Commit**

```bash
git add e2e/parity/client/http.go e2e/parity/entity_delete_all.go e2e/parity/registry.go e2e/parity/registry_count_test.go
git commit -m "test(parity): unconditional delete honors pointInTime and verbose on every backend

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task 7: Multi-node write-visibility assertion (list and search on the other node)

**Files:**
- Modify: `e2e/parity/multinode/concurrency.go:68-107` (`RunExternalAPI_10_02_ReadbackReachesAllReplicas`)

**Interfaces:**
- Consumes: `driver.NewRemote(t, url, token) *Driver`, `(*Driver).GetEntity(id) (parityclient.EntityResult, error)`, `(*Driver).ListEntitiesByModel(name string, version int) ([]parityclient.EntityResult, error)`, `(*Driver).SyncSearch(name string, version int, condition string) ([]parityclient.EntityResult, error)`; results expose `Meta.ID` (string).

- [ ] **Step 1: Extend the scenario**

Inside the `readerIdx` loop, directly after the existing `dR.GetEntity(id)` block and its `k` assertion, add:

```go
			// The contract is not only read-by-id: a listing and a search
			// issued on the other node immediately after the write's
			// response must contain the entity too.
			listed, err := dR.ListEntitiesByModel("multi2", 1)
			if err != nil {
				t.Errorf("list via node %d (written via %d): %v", readerIdx, writerIdx, err)
			} else if !containsEntityID(listed, id) {
				t.Errorf("list via node %d (written via %d): entity %s missing", readerIdx, writerIdx, id)
			}
			found, err := dR.SyncSearch("multi2", 1,
				fmt.Sprintf(`{"type":"simple","jsonPath":"$.k","operatorType":"EQUALS","value":%d}`, writerIdx))
			if err != nil {
				t.Errorf("search via node %d (written via %d): %v", readerIdx, writerIdx, err)
			} else if !containsEntityID(found, id) {
				t.Errorf("search via node %d (written via %d): entity %s missing", readerIdx, writerIdx, id)
			}
```

and add the helper at the end of the file:

```go
func containsEntityID(results []parityclient.EntityResult, id uuid.UUID) bool {
	for _, r := range results {
		if r.Meta.ID == id.String() {
			return true
		}
	}
	return false
}
```

Imports needed by the helper, added only if the file's import block lacks them: `parityclient "github.com/cyoda-platform/cyoda-go/e2e/parity/client"` and `"github.com/google/uuid"` (`concurrency.go` already imports `fmt` and the driver; `driver.go` uses the same `parityclient` alias).

Update the function's doc comment to: `// Write to node A, then GET, list and search from node B (≠ A). Repeat for every (A,B) pair. This is the running check of the write-visibility contract: a successful write response means the write is visible to every subsequent read on every node.`

- [ ] **Step 2: Run the multi-node suite**

Run: `go test ./e2e/parity/postgres/ -run 'TestMultiNode/ExternalAPI_10_02' -timeout 20m`
Expected: PASS. (Known flake in this suite: a dirty-postgres-migration failure unrelated to this change; re-run once if the failure is in fixture setup, and report it if it persists.)

- [ ] **Step 3: Commit**

```bash
git add e2e/parity/multinode/concurrency.go
git commit -m "test(multinode): listing and search on another node see a just-written entity

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task 8: Documentation, parity records, changelog

**Files:**
- Modify: `cmd/cyoda/help/content/crud.md` (7 flag lines at ~73, 92, 170, 190, 199, 260, 288; the `pointInTime`/`verbose` lines at ~330-331)
- Create: `docs/cloud-parity/write-visibility-contract.md`
- Modify: `docs/cloud-parity/README.md` (one row), `docs/cloud-parity/transaction-control-params.md` (the `pageSize` paragraph), `docs/cloud-parity/entity-patch.md:100-101`
- Modify: `e2e/externalapi/scenarios/00-endpoints.yaml:113-116`
- Modify: `CHANGELOG.md` (`### Breaking` at line 7, `### Fixed` at line 1216 under `[Unreleased]`)

- [ ] **Step 1: Help topic**

In `cmd/cyoda/help/content/crud.md` delete every line reading:

```
- `waitForConsistencyAfter` (query, optional): boolean, default `false` — accepted for Cyoda Cloud parity; parsed but currently has no behavioural effect in cyoda-go.
```

(`grep -c waitForConsistencyAfter cmd/cyoda/help/content/crud.md` must print `0` afterwards.)

Replace the two `deleteEntities` lines:

```
- `pointInTime` (query, optional): RFC 3339 — select the entities that existed at this instant (committed state; the ambient transaction is ignored) and delete their current rows. Absent means the current committed state. An entity selected at the instant but already gone is reported in `deleteResult.idToError`.
- `verbose` (query, optional): boolean, default `false` — when `true`, the response `ids` array lists every entity ID the delete attempted (an empty body lists them too); an ID whose delete failed also appears in `deleteResult.idToError`.
```

Run the help tests: `go test ./cmd/cyoda/help/...` — expected PASS.

- [ ] **Step 2: Parity record**

Create `docs/cloud-parity/write-visibility-contract.md`:

```markdown
# Write visibility: a successful write is visible to every subsequent read — Cloud twin-alignment spec

This document is the contract Cyoda Cloud implements to stay aligned with
cyoda-go's write visibility. cyoda-go is the authoritative implementation.

## The contract

**A successful write response means the write is visible to subsequent reads
on every node.** Point reads, listings, searches and point-in-time reads
issued after the response — on any node — see the write. There is no opt-in
and no client-side wait.

## `waitForConsistencyAfter` is retired

The boolean query parameter is removed from the seven entity write
operations (`create`, `createCollection`, `updateCollection`,
`updateSingle`, `updateSingleWithLoopback`, `patchSingle`,
`patchSingleWithLoopback`). It asked for a guarantee cyoda-go gives
unconditionally, so it could toggle nothing; keeping it would have been
advertised behaviour that does not exist. A request that still carries the
parameter, with any value, is accepted and the parameter is ignored. No gRPC
event ever carried an equivalent field.

On Cloud the flag exists because a new transaction reads at the consistency
time captured when it was created, and concurrent transactions can hold that
time below the first transaction's submit time; the flag polls after commit
until the consistency time has passed the write so that a second transaction
can read it. That is a mechanism for satisfying the contract above, and it
belongs inside the storage layer.

## What a backend must do

1. Return from commit only after the write is visible to every read path
   (by id, listing, search, point-in-time) on every node.
2. Expose no post-commit rollout to any reader: a transaction's effects
   become visible atomically, never one row or one index at a time.
3. Give a new transaction a snapshot that is never below a commit already
   acknowledged to any client.

The in-tree backends (memory, sqlite, postgres) meet all three by
construction: commit returns after the write is visible; nothing runs
asynchronously after commit; there is no entity cache in front of the store;
multi-node postgres shares one store and keeps no node-local read state.

## Alignment consequence for Cloud

Cloud's HTTP write path must satisfy the contract without a client opt-in —
its consistency wait becomes unconditional — or the divergence is recorded
on the Cloud side. Cloud's own gRPC writes already wait by default.

## Related delete semantics

`pointInTime` on `DELETE /entity/{entityName}/{modelVersion}` (and on the
gRPC `EntityDeleteAllRequest`) selects the entities that existed at that
instant in committed state — the ambient transaction is ignored, as on every
point-in-time read — and deletes their current rows; an entity selected at
the instant but already gone is reported per id. This now holds on the
empty-body (whole-model) form too, which previously ignored the instant.
A joined request that created entities in the open transaction and then
issues a whole-model delete *with* an instant does not delete those buffered
entities; without an instant it still does.

`verbose` lists every id the delete attempted (the matched set); an id whose
delete failed also appears in `idToError` (HTTP) / `errorsById` (gRPC). The
empty-body form lists them too; it used to return an empty list beside a
non-zero count.
```

Add to `docs/cloud-parity/README.md`'s table, after the `transaction-control-params.md` row:

```markdown
| `write-visibility-contract.md` | A successful write response means the write is visible to every subsequent read on every node; `waitForConsistencyAfter` retired (accepted and ignored if sent); the three obligations a backend must meet; Cloud's wait becomes unconditional; `pointInTime`/`verbose` honored on the whole-model delete form |
```

In `docs/cloud-parity/transaction-control-params.md`, after the paragraph ending `… importers at this stage).`, add:

```markdown
`EntityDeleteAllRequest.pageSize` is removed outright. Selection is streamed,
so a page size has nothing to control, and Cloud ignores the field too (its
`transactionSize` drives both its read page and its batch). The generated Go
type loses the `PageSize` field — compile-breaking for the same importers, on
the same pre-1.0 terms. A client still sending the field is tolerated: the
request decoder ignores unknown fields.
```

In `docs/cloud-parity/entity-patch.md` change:

```markdown
The same query parameters PUT accepts (`transactionTimeoutMillis`,
`waitForConsistencyAfter`) apply unchanged. The response shape is the existing
```

to:

```markdown
The same query parameters PUT accepts (`transactionTimeoutMillis`) apply
unchanged. The response shape is the existing
```

- [ ] **Step 3: Scenario vocabulary**

In `e2e/externalapi/scenarios/00-endpoints.yaml` delete the four lines:

```yaml
  waitForConsistencyAfter:
    type: boolean
    default: false
    purpose: block response until indexing catches up
```

Run `go test ./e2e/externalapi/...` — expected PASS (nothing consumes `query_params`; the scenario URLs that still send the flag are Cloud-facing and are left as they are).

- [ ] **Step 4: Changelog**

Under `## [Unreleased]` → `### Breaking` (line 7), insert as the first bullet:

```markdown
- **`waitForConsistencyAfter` is retired from the seven entity write
  operations.** A successful write response already means the write is
  visible to every subsequent read on every node, so the flag could toggle
  nothing. A request that still carries it — with any value, including a
  malformed one that used to answer `400` — is accepted and the parameter is
  ignored. The contract and what every backend must do to meet it are
  recorded in `docs/cloud-parity/write-visibility-contract.md`.

- **`pageSize` is removed from the gRPC `EntityDeleteAllRequest` event.**
  Selection is streamed, so there was nothing for it to control; it was
  decoded and ignored. The generated Go type in `api/grpc/events` loses the
  field. A client still sending it is tolerated.
```

Under `### Fixed` (line 1216), insert as the first bullet:

```markdown
- **A whole-model delete honors `pointInTime` and `verbose` on both doors.**
  `DELETE /entity/{entityName}/{modelVersion}` with an empty body and the
  gRPC `EntityDeleteAllRequest` took a fast path that ignored the instant —
  deleting entities created after it — and returned an empty id list beside
  a non-zero count. The fast path is now taken only when nothing per entity
  is needed; otherwise the delete selects the committed state as at the
  instant and lists every attempted id, exactly as the conditional form
  always did. The gRPC response's `entityIds` is populated for the first
  time.
```

- [ ] **Step 5: Exit checks and commit**

Run the exit check over the shipped surface — these paths must have zero hits:

```bash
grep -rn "waitForConsistencyAfter" api/openapi.yaml api/generated.go cmd/ internal/domain/ internal/grpc/ internal/api/ plugins/ docs/cloud-parity/entity-patch.md e2e/externalapi/scenarios/00-endpoints.yaml
```

Expected: no output. Places that legitimately keep the name: the vendored Cloud spec `docs/cyoda/openapi.yml`, the Cloud-facing recon scenarios under `test/recon/` and `e2e/externalapi/scenarios/04-*`/`06-*`/`10-*`, the e2e tolerance test, `CHANGELOG.md`, and the parity records. Also `grep -rn "#501\|#379" cmd api internal plugins e2e docs/cloud-parity` must print nothing.

```bash
git add cmd/cyoda/help/content/crud.md docs/cloud-parity/write-visibility-contract.md docs/cloud-parity/README.md docs/cloud-parity/transaction-control-params.md docs/cloud-parity/entity-patch.md e2e/externalapi/scenarios/00-endpoints.yaml CHANGELOG.md
git commit -m "docs: write-visibility contract; delete-all pointInTime/verbose; changelog

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task 9: Whole-repository verification

**Files:** none modified unless a failure surfaces.

- [ ] **Step 1: Static analysis**

Run: `go vet ./... && (cd plugins/memory && go vet ./...) && (cd plugins/sqlite && go vet ./...) && (cd plugins/postgres && go vet ./...)`
Expected: clean.

- [ ] **Step 2: Full suite**

Run: `make preflight && make test-full`
Expected: every tier green — unit, parity (memory/sqlite/postgres), `internal/e2e`, and the three plugin submodules. Read the `scripts/testreport` summary lines; "ran no tests" for a required suite is a failure.

- [ ] **Step 3: Race detector, once**

Run: `make race`
Expected: clean.

- [ ] **Step 4: Report**

Record the three command outcomes verbatim in the final report. If anything failed, fix it with its own failing test and re-run the tier that failed, then the full suite again.
