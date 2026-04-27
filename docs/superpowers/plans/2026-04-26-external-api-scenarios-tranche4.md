# External API Scenario Suite — Tranche 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 14 new parity scenarios across dictionary files 13 (numeric-types) + 14 (polymorphism), plus 5 async-search Client primitives + 5 Driver pass-throughs required by 4 of those scenarios. Bundle into one PR targeting `release/v0.6.3`.

**Architecture:** Pure additive on tranche 1+2+3. Three new code surfaces: (1) async-search helpers in `e2e/parity/client/http.go` + `e2e/externalapi/driver/driver.go`; (2) `e2e/parity/externalapi/numeric_types.go` with 9 `Run*` for file 13; (3) `e2e/parity/externalapi/polymorphism.go` with 5 `Run*` for file 14. Mapping doc flipped from `pending:tranche-4` to status-of-record. No production code changes.

**Tech Stack:** Go 1.26, existing parity harness (memory + sqlite + postgres backends), testcontainers-go (postgres only).

**Spec:** `docs/superpowers/specs/2026-04-26-external-api-tranche4-design.md`

**Predecessor:** Tranche 3 (#135) on `release/v0.6.3`.

---

## Discover-and-compare protocol (carried forward from tranches 2+3)

For the negative-path scenario in file 14 (`poly/06`):

1. Read the dictionary's `expected_error.error_class` and `http_status` from the YAML.
2. Run with `errorcontract.Match` asserting only `HTTPStatus`. Capture cyoda-go's `properties.errorCode` via temporary `t.Logf("DISCOVER 14_06 status=%d body=%s", status, string(body))`.
3. Classify:
   - `equiv_or_better` → tighten with `ErrorCode: "<observed>"` + comment "matches/exceeds cloud's `InvalidTypesInClientConditionException`".
   - `different_naming_same_level` → tighten + comment cloud equivalent.
   - `worse` → surface to controller; do NOT file a follow-up issue without confirmation (per memory rule).
4. Remove the discovery `t.Logf` before commit.

---

## Phase 0 — Pre-implementation probe

### Task 0.1: Async-search wire-shape probe (REQUIRED before Phase 1)

cyoda-go's OpenAPI says `/api/search/async/{name}/{version}`, `/api/search/async/{jobId}`, `/api/search/async/{jobId}/status`, `/api/search/async/{jobId}/cancel` exist (confirmed at `api/openapi.yaml:5001-5274`). The exact response shapes — particularly the result-page envelope at `GET /api/search/async/{jobId}` and the status enum values — must be captured before writing helpers.

**No commit; outcome recorded inline below.**

- [ ] **Step 1: Read the OpenAPI definitions for the four async-search routes**

```bash
grep -nE "/search/async" api/openapi.yaml
```

Find the response schemas referenced by each operation (`200` content / `application/json` schema name).

- [ ] **Step 2: Read the corresponding handler in cyoda-go**

```bash
grep -nE "func.*SubmitAsyncSearch|func.*GetAsyncSearchResults|func.*GetAsyncSearchStatus|func.*CancelAsyncSearch" internal/api/handlers/*.go internal/domain/search/*.go 2>/dev/null
```

If not found via that pattern, search for `/api/search/async` route registration.

- [ ] **Step 3: Read the generated types in `api/generated.go`**

```bash
grep -nE "AsyncSearchResponse|AsyncSearchStatus|AsyncSearchPage|JobStatus|SearchJobStatus" api/generated.go | head -20
```

- [ ] **Step 4: Record findings in this plan**

Replace the `[Phase 0.1 outcome — record after probe]` block below with concrete:
- Submit response shape (typically `{"jobId": "..."}` — confirm).
- Status enum values (e.g. `PENDING`, `RUNNING`, `SUCCESSFUL`, `FAILED`, `CANCELLED`).
- Status response shape (typically `{"status": "..."}` — confirm wrapper).
- Results response shape (typically `{"content": [...], "totalElements": N, "page": N, "size": N}` — confirm exact field names + whether pagination metadata is present).
- Cancel response shape (likely `{"cancelled": true}` or 204 No Content — confirm).

**[Phase 0.1 outcome — record after probe]**

> **0.1 result (recorded 2026-04-25 — do NOT re-run):**
>
> **Submit** (`POST /api/search/async/{name}/{version}`):
> - HTTP status: `200 OK`
> - Response body: a bare JSON string (UUID), **not** a wrapper object.
>   Actual wire shape: `"550e8400-e29b-41d4-a716-446655440000"` (quoted string, Content-Type: application/json).
> - The plan assumed `{"jobId": "..."}` — **DEVIATES**. The handler calls `common.WriteJSON(w, http.StatusOK, jobID)` where `jobID` is a plain `string`. Phase 1 Task 1.1 must unmarshal the body as a bare `string`, not as `{"jobId": "..."}`.
>
> **Status** (`GET /api/search/async/{jobId}/status`):
> - HTTP status: `200 OK`
> - Response body: richer envelope (NOT a naked string, NOT just `{"status": "..."}`).
>   Actual wire shape (from `buildStatusResponse`):
>   ```json
>   {
>     "searchJobStatus":       "RUNNING|SUCCESSFUL|FAILED|CANCELLED",
>     "createTime":            "2026-04-25T12:00:00.000000000Z",
>     "entitiesCount":         42,
>     "calculationTimeMillis": 123,
>     "expirationDate":        "2026-04-26T12:00:00.000000000Z",
>     "finishTime":            "2026-04-25T12:05:00.000000000Z"   // omitted if still running
>   }
>   ```
> - Full status enum emitted by cyoda-go: `RUNNING`, `SUCCESSFUL`, `FAILED`, `CANCELLED`, `NOT_FOUND`.
>   Note: `NOT_FOUND` is a valid enum value returned in the status field (rare race condition). The plan assumed `PENDING` — **DEVIATES**. There is NO `PENDING` status; the job is `RUNNING` from the moment it is created.
> - The plan assumed `{"status": "..."}` wrapper — **DEVIATES**. The key is `searchJobStatus`, not `status`.
> - Phase 1 Task 1.2 must: (a) read field `searchJobStatus`, not `status`; (b) not include `PENDING` in the enum; (c) parse the full status envelope for the `AsyncSearchStatus` struct (fields: `SearchJobStatus string`, `CreateTime time.Time`, `EntitiesCount int64`, `CalculationTimeMillis int64`, `ExpirationDate time.Time`, `FinishTime *time.Time`).
>
> **Results** (`GET /api/search/async/{jobId}`):
> - HTTP status: `200 OK`
> - Response body: nested `page` object (NOT the flat shape the plan assumed).
>   Actual wire shape (from handler):
>   ```json
>   {
>     "content": [ { "type": "ENTITY", "data": {...}, "meta": {...} }, ... ],
>     "page": {
>       "number":        0,
>       "size":          1000,
>       "totalElements": 42,
>       "totalPages":    1
>     }
>   }
>   ```
> - `content` items are entity envelopes with fields: `type` (always `"ENTITY"`), `data` (raw JSON), `meta` (`id`, `state`, `creationDate`, `lastUpdateTime`, optional `transactionId`, optional `transitionForLatestSave`).
> - The plan assumed a flat `{"content": [...], "totalElements": N, "page": N, "size": N}` — **DEVIATES**. Pagination metadata is nested under `"page"` as a sub-object, matching a Spring-style `Page` with `{number, size, totalElements, totalPages}`. Phase 1 Task 1.3 must define `AsyncSearchPage` with a nested `Page PageMetadata` struct, not flat fields.
> - Default page size is `1000` (not 10 as OpenAPI description says). Max page size enforced server-side.
>
> **Cancel** (`PUT /api/search/async/{jobId}/cancel`):
> - HTTP method: **`PUT`** (not `POST` as the plan assumed) — **DEVIATES**. The generated interface comment and OpenAPI spec both confirm `PUT`.
> - HTTP status on success: `200 OK`
> - Response body on successful cancellation:
>   ```json
>   {
>     "isCancelled":            true,
>     "cancelled":              true,
>     "currentSearchJobStatus": "CANCELLED"
>   }
>   ```
> - Response body on failure (job already finished, returns `400 Bad Request`):
>   ```json
>   {
>     "detail":     "snapshot by id=<jobId> is not running. current status=SUCCESSFUL",
>     "properties": { "currentStatus": "SUCCESSFUL", "snapshotId": "<jobId>" },
>     "status":     400,
>     "title":      "Bad Request",
>     "type":       "about:blank"
>   }
>   ```
> - The plan assumed `POST` method and body shape unspecified — **DEVIATES on method** (must be PUT). Phase 1 Task 1.4 must use `PUT` not `POST`.
>
> **Summary of Phase 1 adjustments required:**
> - Task 1.1 (SubmitAsyncSearch): unmarshal response as bare `string`, not `{"jobId": "..."}`.
> - Task 1.2 (GetAsyncSearchStatus): field name is `searchJobStatus` (not `status`); parse full status envelope; enum is {RUNNING, SUCCESSFUL, FAILED, CANCELLED, NOT_FOUND} — no PENDING.
> - Task 1.3 (GetAsyncSearchResults): `AsyncSearchPage` must have nested `Page` sub-struct with `{Number, Size, TotalElements, TotalPages}`; content items are entity envelopes with `type/data/meta`.
> - Task 1.4 (CancelAsyncSearch): use HTTP `PUT` (not `POST`); success shape is `{isCancelled, cancelled, currentSearchJobStatus}`.
> - The `AwaitAsyncSearch` polling helper (Task 1.5) should poll on `searchJobStatus` field and treat {SUCCESSFUL, FAILED, CANCELLED, NOT_FOUND} as terminal states (no PENDING).

- [ ] **Step 5: Sanity check — does the probe match the spec's assumed shapes?**

Spec assumes:
- `Submit` → `(jobId string, error)`
- `Status` → `(status string, error)` where status ∈ {PENDING, RUNNING, SUCCESSFUL, FAILED, CANCELLED}
- `Results` → `(AsyncSearchPage, error)` with `Content []EntityResult`
- `Cancel` → `error`
- `Await` → polling loop returning `(AsyncSearchPage, error)`

If the probe reveals different field names, status enum values, or a fundamentally different envelope, ADJUST the Phase 1 task bodies before writing them. Do NOT proceed to Phase 1 until shapes are pinned.

---

## Phase 1 — Async-search Client + Driver helpers

### Task 1.1: `(*Client).SubmitAsyncSearch` — TDD red/green

**Files:**
- Modify: `e2e/parity/client/http.go` (append near `SyncSearch` at line 778)
- Create or extend: `e2e/parity/client/async_search_test.go`

- [ ] **Step 1: Write failing test**

Create or append to `e2e/parity/client/async_search_test.go`:

```go
package client_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

func TestClient_SubmitAsyncSearch_POST(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jobId":"job-abc-123"}`))
	}))
	defer srv.Close()

	c := client.NewClient(srv.URL, "tok")
	jobID, err := c.SubmitAsyncSearch(t, "orders", 1, `{"type":"group","conditions":[]}`)
	if err != nil {
		t.Fatalf("SubmitAsyncSearch: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if gotPath != "/api/search/async/orders/1" {
		t.Errorf("path: got %q", gotPath)
	}
	if !strings.Contains(gotBody, `"type":"group"`) {
		t.Errorf("body: got %q", gotBody)
	}
	if jobID != "job-abc-123" {
		t.Errorf("jobID: got %q want job-abc-123", jobID)
	}
}
```

- [ ] **Step 2: Confirm RED**

```bash
go test ./e2e/parity/client/ -run TestClient_SubmitAsyncSearch -v
```

Expected: build error `c.SubmitAsyncSearch undefined`.

- [ ] **Step 3: Implement**

Append to `e2e/parity/client/http.go` (adjacent to `SyncSearch`):

```go
// SubmitAsyncSearch issues POST /api/search/async/{name}/{version} with the
// given condition JSON. Returns the jobId for status/results polling.
// Canonical: api/openapi.yaml /search/async/{entityName}/{modelVersion}.
func (c *Client) SubmitAsyncSearch(t *testing.T, modelName string, modelVersion int, condition string) (string, error) {
	t.Helper()
	path := fmt.Sprintf("/api/search/async/%s/%d", modelName, modelVersion)
	raw, err := c.doRaw(t, http.MethodPost, path, condition)
	if err != nil {
		return "", err
	}
	var resp struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode SubmitAsyncSearch response: %w (body=%s)", err, string(raw))
	}
	if resp.JobID == "" {
		return "", fmt.Errorf("SubmitAsyncSearch returned empty jobId (body=%s)", string(raw))
	}
	return resp.JobID, nil
}
```

If the Phase 0.1 probe revealed a different response field name (e.g. `id` instead of `jobId`), adjust the struct tag accordingly.

- [ ] **Step 4: Confirm GREEN**

```bash
go test ./e2e/parity/client/ -run TestClient_SubmitAsyncSearch -v
go vet ./e2e/parity/client/
```

Both silent / pass.

- [ ] **Step 5: Commit**

```bash
git add e2e/parity/client/
git commit -m "$(cat <<'EOF'
test(parity/client): add SubmitAsyncSearch

POST /api/search/async/{name}/{version} returning the jobId for
status/results polling. Required by tranche-4 numeric and
polymorphism scenarios that exercise the async-search code path.

Refs #121.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 1.2: `(*Client).GetAsyncSearchStatus` — TDD red/green

**Files:**
- Modify: `e2e/parity/client/http.go`
- Modify: `e2e/parity/client/async_search_test.go`

- [ ] **Step 1: Append failing test**

```go
func TestClient_GetAsyncSearchStatus_GET(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"SUCCESSFUL"}`))
	}))
	defer srv.Close()

	c := client.NewClient(srv.URL, "tok")
	status, err := c.GetAsyncSearchStatus(t, "job-abc-123")
	if err != nil {
		t.Fatalf("GetAsyncSearchStatus: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method: got %q want GET", gotMethod)
	}
	if gotPath != "/api/search/async/job-abc-123/status" {
		t.Errorf("path: got %q", gotPath)
	}
	if status != "SUCCESSFUL" {
		t.Errorf("status: got %q want SUCCESSFUL", status)
	}
}
```

- [ ] **Step 2: Confirm RED**

```bash
go test ./e2e/parity/client/ -run TestClient_GetAsyncSearchStatus -v
```

Expected: build error `c.GetAsyncSearchStatus undefined`.

- [ ] **Step 3: Implement**

Append to `e2e/parity/client/http.go`:

```go
// GetAsyncSearchStatus issues GET /api/search/async/{jobId}/status.
// Returns one of PENDING, RUNNING, SUCCESSFUL, FAILED, CANCELLED
// (exact enum confirmed in Phase 0.1 probe).
func (c *Client) GetAsyncSearchStatus(t *testing.T, jobID string) (string, error) {
	t.Helper()
	path := fmt.Sprintf("/api/search/async/%s/status", jobID)
	raw, err := c.doRaw(t, http.MethodGet, path, "")
	if err != nil {
		return "", err
	}
	var resp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode GetAsyncSearchStatus response: %w (body=%s)", err, string(raw))
	}
	return resp.Status, nil
}
```

Adjust the wrapper field name per Phase 0.1 findings if needed.

- [ ] **Step 4: Confirm GREEN + commit**

```bash
go test ./e2e/parity/client/ -run TestClient_GetAsyncSearchStatus -v
go vet ./e2e/parity/client/

git add e2e/parity/client/
git commit -m "$(cat <<'EOF'
test(parity/client): add GetAsyncSearchStatus

GET /api/search/async/{jobId}/status returning the job's current
state. Required by tranche-4 async-search await loops.

Refs #121.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 1.3: `(*Client).GetAsyncSearchResults` + `AsyncSearchPage` type — TDD red/green

**Files:**
- Modify: `e2e/parity/client/http.go`
- Modify: `e2e/parity/client/async_search_test.go`

- [ ] **Step 1: Append failing test**

```go
func TestClient_GetAsyncSearchResults_GET(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"content":[{"id":"00000000-0000-0000-0000-000000000001","data":{"k":1}},{"id":"00000000-0000-0000-0000-000000000002","data":{"k":2}}]}`))
	}))
	defer srv.Close()

	c := client.NewClient(srv.URL, "tok")
	page, err := c.GetAsyncSearchResults(t, "job-abc-123")
	if err != nil {
		t.Fatalf("GetAsyncSearchResults: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method: got %q want GET", gotMethod)
	}
	if gotPath != "/api/search/async/job-abc-123" {
		t.Errorf("path: got %q", gotPath)
	}
	if len(page.Content) != 2 {
		t.Errorf("content len: got %d want 2", len(page.Content))
	}
}
```

- [ ] **Step 2: Confirm RED**

```bash
go test ./e2e/parity/client/ -run TestClient_GetAsyncSearchResults -v
```

Expected: build error.

- [ ] **Step 3: Implement**

Append to `e2e/parity/client/http.go`:

```go
// AsyncSearchPage is the response envelope returned by
// GET /api/search/async/{jobId}. The Content field carries the
// matched entities; pagination metadata (TotalElements/Page/Size)
// is populated when present in the response. Unrecognised fields
// are ignored.
type AsyncSearchPage struct {
	Content       []EntityResult `json:"content"`
	TotalElements int            `json:"totalElements,omitempty"`
	Page          int            `json:"page,omitempty"`
	Size          int            `json:"size,omitempty"`
}

// GetAsyncSearchResults issues GET /api/search/async/{jobId} and
// returns the result page.
func (c *Client) GetAsyncSearchResults(t *testing.T, jobID string) (AsyncSearchPage, error) {
	t.Helper()
	path := fmt.Sprintf("/api/search/async/%s", jobID)
	raw, err := c.doRaw(t, http.MethodGet, path, "")
	if err != nil {
		return AsyncSearchPage{}, err
	}
	var page AsyncSearchPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return AsyncSearchPage{}, fmt.Errorf("decode GetAsyncSearchResults response: %w (body=%s)", err, string(raw))
	}
	return page, nil
}
```

Adjust the struct shape per Phase 0.1 findings (e.g. if cyoda-go uses `pageable: {pageNumber, pageSize, totalElements}` Spring-style envelope).

- [ ] **Step 4: Confirm GREEN + commit**

```bash
go test ./e2e/parity/client/ -run TestClient_GetAsyncSearchResults -v
go vet ./e2e/parity/client/

git add e2e/parity/client/
git commit -m "$(cat <<'EOF'
test(parity/client): add GetAsyncSearchResults + AsyncSearchPage type

GET /api/search/async/{jobId} returning the result envelope
({content, totalElements, page, size}). Pagination fields are
optional and present only when the server includes them.

Refs #121.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 1.4: `(*Client).CancelAsyncSearch` — TDD red/green

**Files:**
- Modify: `e2e/parity/client/http.go`
- Modify: `e2e/parity/client/async_search_test.go`

- [ ] **Step 1: Append failing test**

```go
func TestClient_CancelAsyncSearch_POST(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cancelled":true}`))
	}))
	defer srv.Close()

	c := client.NewClient(srv.URL, "tok")
	if err := c.CancelAsyncSearch(t, "job-abc-123"); err != nil {
		t.Fatalf("CancelAsyncSearch: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if gotPath != "/api/search/async/job-abc-123/cancel" {
		t.Errorf("path: got %q", gotPath)
	}
}
```

- [ ] **Step 2: Confirm RED**

```bash
go test ./e2e/parity/client/ -run TestClient_CancelAsyncSearch -v
```

Expected: build error.

- [ ] **Step 3: Implement**

Append to `e2e/parity/client/http.go`:

```go
// CancelAsyncSearch issues POST /api/search/async/{jobId}/cancel.
// Returns nil on 2xx; the response body shape is not consumed.
func (c *Client) CancelAsyncSearch(t *testing.T, jobID string) error {
	t.Helper()
	path := fmt.Sprintf("/api/search/async/%s/cancel", jobID)
	_, err := c.doRaw(t, http.MethodPost, path, "")
	return err
}
```

If the OpenAPI requires DELETE rather than POST, adjust per Phase 0.1.

- [ ] **Step 4: Confirm GREEN + commit**

```bash
go test ./e2e/parity/client/ -run TestClient_CancelAsyncSearch -v
go vet ./e2e/parity/client/

git add e2e/parity/client/
git commit -m "$(cat <<'EOF'
test(parity/client): add CancelAsyncSearch

POST /api/search/async/{jobId}/cancel. Not used by tranche-4
scenarios but rounds out the async-search surface for parity with
the OpenAPI definition and future scenarios.

Refs #121.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 1.5: `(*Client).AwaitAsyncSearchResults` wrapper — TDD red/green

**Files:**
- Modify: `e2e/parity/client/http.go`
- Modify: `e2e/parity/client/async_search_test.go`

- [ ] **Step 1: Append failing test**

```go
import (
	// ... existing imports ...
	"sync/atomic"
	"time"
)

func TestClient_AwaitAsyncSearchResults_Success(t *testing.T) {
	var statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/search/async/orders/"):
			_, _ = w.Write([]byte(`{"jobId":"job-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/search/async/job-1/status":
			n := statusCalls.Add(1)
			if n < 2 {
				_, _ = w.Write([]byte(`{"status":"RUNNING"}`))
			} else {
				_, _ = w.Write([]byte(`{"status":"SUCCESSFUL"}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/search/async/job-1":
			_, _ = w.Write([]byte(`{"content":[{"id":"00000000-0000-0000-0000-000000000001","data":{}}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := client.NewClient(srv.URL, "tok")
	page, err := c.AwaitAsyncSearchResults(t, "orders", 1, `{}`, 5*time.Second)
	if err != nil {
		t.Fatalf("AwaitAsyncSearchResults: %v", err)
	}
	if len(page.Content) != 1 {
		t.Errorf("content len: got %d want 1", len(page.Content))
	}
	if statusCalls.Load() < 2 {
		t.Errorf("expected at least 2 status polls, got %d", statusCalls.Load())
	}
}

func TestClient_AwaitAsyncSearchResults_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/search/async/"):
			_, _ = w.Write([]byte(`{"jobId":"job-stuck"}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(`{"status":"RUNNING"}`))
		}
	}))
	defer srv.Close()

	c := client.NewClient(srv.URL, "tok")
	_, err := c.AwaitAsyncSearchResults(t, "stuck", 1, `{}`, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("expected timeout-related error, got %v", err)
	}
}

func TestClient_AwaitAsyncSearchResults_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/search/async/"):
			_, _ = w.Write([]byte(`{"jobId":"job-fail"}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(`{"status":"FAILED"}`))
		}
	}))
	defer srv.Close()

	c := client.NewClient(srv.URL, "tok")
	_, err := c.AwaitAsyncSearchResults(t, "failing", 1, `{}`, 5*time.Second)
	if err == nil {
		t.Fatal("expected error on FAILED status, got nil")
	}
	if !strings.Contains(err.Error(), "FAILED") {
		t.Errorf("expected error to mention FAILED, got %v", err)
	}
}
```

- [ ] **Step 2: Confirm RED**

```bash
go test ./e2e/parity/client/ -run TestClient_AwaitAsyncSearchResults -v
```

Expected: build error.

- [ ] **Step 3: Implement**

Append to `e2e/parity/client/http.go`:

```go
// AwaitAsyncSearchResults submits an async search, polls the status until
// terminal, and returns the result page. Polls every 100ms. Treats
// SUCCESSFUL as terminal-success; FAILED and CANCELLED as terminal-error;
// PENDING and RUNNING as continue-polling. Returns an error if the timeout
// elapses before reaching a terminal status.
func (c *Client) AwaitAsyncSearchResults(t *testing.T, modelName string, modelVersion int, condition string, timeout time.Duration) (AsyncSearchPage, error) {
	t.Helper()
	jobID, err := c.SubmitAsyncSearch(t, modelName, modelVersion, condition)
	if err != nil {
		return AsyncSearchPage{}, fmt.Errorf("submit: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := c.GetAsyncSearchStatus(t, jobID)
		if err != nil {
			return AsyncSearchPage{}, fmt.Errorf("status (jobId=%s): %w", jobID, err)
		}
		switch status {
		case "SUCCESSFUL":
			return c.GetAsyncSearchResults(t, jobID)
		case "FAILED", "CANCELLED":
			return AsyncSearchPage{}, fmt.Errorf("async search reached terminal status %s (jobId=%s)", status, jobID)
		case "PENDING", "RUNNING", "":
			// continue polling
		default:
			return AsyncSearchPage{}, fmt.Errorf("unexpected async search status %q (jobId=%s)", status, jobID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return AsyncSearchPage{}, fmt.Errorf("timeout (%s) waiting for async search jobId=%s", timeout, jobID)
}
```

Add `"time"` to the imports if not already present.

- [ ] **Step 4: Confirm GREEN + commit**

```bash
go test ./e2e/parity/client/ -run TestClient_AwaitAsyncSearchResults -v
go vet ./e2e/parity/client/

git add e2e/parity/client/
git commit -m "$(cat <<'EOF'
test(parity/client): add AwaitAsyncSearchResults wrapper

Submits an async search, polls status every 100ms until terminal,
returns the result page. Treats SUCCESSFUL as success;
FAILED/CANCELLED return error; PENDING/RUNNING continue. Bounded
by caller-supplied timeout. Used by tranche-4 scenarios that
exercise the async-search code path (13/11, 14/01, 14/05, 14/06).

Refs #121.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 1.6: Driver pass-throughs — 5 methods, single TDD round

**Files:**
- Modify: `e2e/externalapi/driver/driver.go`
- Modify: `e2e/externalapi/driver/vocabulary_test.go`

The Driver layer is intentionally a thin sugar over Client. All 5 pass-throughs land together (one commit, one test round) — they share the same trivial structure.

- [ ] **Step 1: Append failing tests to `vocabulary_test.go`**

```go
func TestDriver_SubmitAsyncSearch_POST(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jobId":"job-x"}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	jobID, err := d.SubmitAsyncSearch("orders", 1, `{}`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/search/async/orders/1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if jobID != "job-x" {
		t.Errorf("jobID: got %q", jobID)
	}
}

func TestDriver_GetAsyncSearchStatus_GET(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"RUNNING"}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	status, err := d.GetAsyncSearchStatus("job-x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/search/async/job-x/status" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if status != "RUNNING" {
		t.Errorf("status: got %q", status)
	}
}

func TestDriver_GetAsyncSearchResults_GET(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"content":[]}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	page, err := d.GetAsyncSearchResults("job-x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/search/async/job-x" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if page.Content == nil {
		// nil slice OK; just verify no decode error
	}
}

func TestDriver_CancelAsyncSearch_POST(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.CancelAsyncSearch("job-x"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/search/async/job-x/cancel" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_AwaitAsyncSearchResults_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/search/async/m/"):
			_, _ = w.Write([]byte(`{"jobId":"j"}`))
		case r.URL.Path == "/api/search/async/j/status":
			_, _ = w.Write([]byte(`{"status":"SUCCESSFUL"}`))
		case r.URL.Path == "/api/search/async/j":
			_, _ = w.Write([]byte(`{"content":[{"id":"00000000-0000-0000-0000-000000000001","data":{}}]}`))
		}
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	page, err := d.AwaitAsyncSearchResults("m", 1, `{}`, 2*time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(page.Content) != 1 {
		t.Errorf("content len: got %d", len(page.Content))
	}
}
```

CRITICAL — verify the existing `vocabulary_test.go` has `capturedReq`, `fakeServer(t, cap)`, and the imports `net/http`, `net/http/httptest`, `strings`, `time`. If `time` isn't in the existing import block, add it.

- [ ] **Step 2: Confirm RED**

```bash
go test ./e2e/externalapi/driver/ -run "TestDriver_(Submit|Get|Cancel|Await)AsyncSearch" -v
```

Expected: 5 build errors (methods undefined).

- [ ] **Step 3: Implement — append to `e2e/externalapi/driver/driver.go`**

Place near the existing `SyncSearch` if there is one, or near other search-related methods. Use the existing parityclient import alias.

```go
// SubmitAsyncSearch issues POST /api/search/async/{name}/{version}.
// Returns the jobId for status/results polling.
func (d *Driver) SubmitAsyncSearch(name string, version int, condition string) (string, error) {
	return d.client.SubmitAsyncSearch(d.t, name, version, condition)
}

// GetAsyncSearchStatus issues GET /api/search/async/{jobId}/status.
func (d *Driver) GetAsyncSearchStatus(jobID string) (string, error) {
	return d.client.GetAsyncSearchStatus(d.t, jobID)
}

// GetAsyncSearchResults issues GET /api/search/async/{jobId}.
func (d *Driver) GetAsyncSearchResults(jobID string) (parityclient.AsyncSearchPage, error) {
	return d.client.GetAsyncSearchResults(d.t, jobID)
}

// CancelAsyncSearch issues POST /api/search/async/{jobId}/cancel.
func (d *Driver) CancelAsyncSearch(jobID string) error {
	return d.client.CancelAsyncSearch(d.t, jobID)
}

// AwaitAsyncSearchResults submits an async search, polls until terminal,
// returns the result page. See client.AwaitAsyncSearchResults for semantics.
func (d *Driver) AwaitAsyncSearchResults(name string, version int, condition string, timeout time.Duration) (parityclient.AsyncSearchPage, error) {
	return d.client.AwaitAsyncSearchResults(d.t, name, version, condition, timeout)
}
```

Add `"time"` to driver.go imports if not present. Use the existing parityclient alias (check the existing import block — likely `parityclient "github.com/cyoda-platform/cyoda-go/e2e/parity/client"`).

- [ ] **Step 4: Confirm GREEN + commit**

```bash
go test ./e2e/externalapi/driver/ -short -v 2>&1 | tail -10
go vet ./e2e/externalapi/driver/

git add e2e/externalapi/driver/
git commit -m "$(cat <<'EOF'
test(externalapi): Driver async-search pass-throughs

Adds Driver wrappers for SubmitAsyncSearch, GetAsyncSearchStatus,
GetAsyncSearchResults, CancelAsyncSearch, and AwaitAsyncSearchResults.
Symmetric with the existing SyncSearch pattern. Required by
tranche-4 scenarios in files 13 and 14.

Refs #121.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2 — File 13 numeric-types (9 scenarios)

### Task 2.1: Implement all 9 Run* in `numeric_types.go`

**Files:**
- Create: `e2e/parity/externalapi/numeric_types.go`

The 9 scenarios are independent — no shared workflow shape needed. Each follows roughly: register model, lock, create entity, GET entity (or export model), assert.

- [ ] **Step 1: Create the file with registry + 9 Run* bodies**

```go
package externalapi

// External API Scenario Suite — 13-numeric-types
//
// Cyoda assigns each JSON number a DataType when a model is registered.
// External REST/gRPC default ParsingSpec(parseStrings=true) with
// intScope=INTEGER, decimalScope=DOUBLE. Scenarios that depend on
// narrowed scopes (numeric/03, numeric/05) are not externally reachable
// — recorded as internal_only_skip in the mapping doc.
//
// Comparison conventions (per dictionary):
//   - DOUBLE values: byte-identical (entity_equals_json).
//   - BIG_DECIMAL: stripTrailingZeros normalisation (entity_equals_json_numeric).
//   - UNBOUND_DECIMAL: toPlainString normalisation.
//   - BIG_INTEGER / UNBOUND_INTEGER: byte-identical via JSON number form.

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_13_01_IntegerLandsInDoubleField", Fn: RunExternalAPI_13_01_IntegerLandsInDoubleField},
		parity.NamedTest{Name: "ExternalAPI_13_04_DefaultIntegerScopeINTEGER", Fn: RunExternalAPI_13_04_DefaultIntegerScopeINTEGER},
		parity.NamedTest{Name: "ExternalAPI_13_05ext_PolymorphicMergeWithDefaultScopes", Fn: RunExternalAPI_13_05ext_PolymorphicMergeWithDefaultScopes},
		parity.NamedTest{Name: "ExternalAPI_13_06_DoubleAtMaxBoundary", Fn: RunExternalAPI_13_06_DoubleAtMaxBoundary},
		parity.NamedTest{Name: "ExternalAPI_13_07_BigDecimal20Plus18", Fn: RunExternalAPI_13_07_BigDecimal20Plus18},
		parity.NamedTest{Name: "ExternalAPI_13_08_UnboundDecimalGT18Frac", Fn: RunExternalAPI_13_08_UnboundDecimalGT18Frac},
		parity.NamedTest{Name: "ExternalAPI_13_09_BigInteger38Digit", Fn: RunExternalAPI_13_09_BigInteger38Digit},
		parity.NamedTest{Name: "ExternalAPI_13_10_UnboundInteger40Digit", Fn: RunExternalAPI_13_10_UnboundInteger40Digit},
		parity.NamedTest{Name: "ExternalAPI_13_11_SearchIntegerAgainstDouble", Fn: RunExternalAPI_13_11_SearchIntegerAgainstDouble},
	)
}

// simpleViewFieldType decodes a SIMPLE_VIEW model export and returns the
// type descriptor for the given path/key. The export shape is
// {"$": {".key": "TYPE_NAME", ...}, ...}.
//
// Returns the raw descriptor string ("DOUBLE", "BIG_DECIMAL",
// "[INTEGER, STRING]", etc.) or an error if the path/key is absent.
func simpleViewFieldType(t *testing.T, exported json.RawMessage, path, key string) (string, error) {
	t.Helper()
	var shape map[string]map[string]any
	if err := json.Unmarshal(exported, &shape); err != nil {
		// SIMPLE_VIEW may be wrapped in {"model": {...}}. Try that.
		var wrapped struct {
			Model map[string]map[string]any `json:"model"`
		}
		if err2 := json.Unmarshal(exported, &wrapped); err2 != nil {
			return "", fmt.Errorf("unmarshal SIMPLE_VIEW: %w (also %v)", err, err2)
		}
		shape = wrapped.Model
	}
	pathMap, ok := shape[path]
	if !ok {
		return "", fmt.Errorf("path %q not in SIMPLE_VIEW (have %v)", path, keysOf(shape))
	}
	descriptor, ok := pathMap[key]
	if !ok {
		return "", fmt.Errorf("key %q not under path %q (have %v)", key, path, keysOfAny(pathMap))
	}
	descStr, ok := descriptor.(string)
	if !ok {
		return "", fmt.Errorf("descriptor at %q.%q is not a string: %T %v", path, key, descriptor, descriptor)
	}
	return descStr, nil
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// RunExternalAPI_13_01_IntegerLandsInDoubleField — dictionary 13/01.
// Model locked with {"price": 13.111} → $.price lands as DOUBLE.
// POST {"price": 13} (JSON integer) must be accepted and listing yields 1.
func RunExternalAPI_13_01_IntegerLandsInDoubleField(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("simple3", 1, `{"price": 13.111}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("simple3", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := d.CreateEntity("simple3", 1, `{"price": 13}`); err != nil {
		t.Fatalf("create entity (integer into DOUBLE): %v", err)
	}
	list, err := d.ListEntitiesByModel("simple3", 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("entity count: got %d want 1", len(list))
	}
}

// RunExternalAPI_13_04_DefaultIntegerScopeINTEGER — dictionary 13/04.
// Without a ParsingSpec override (only mode external surfaces support),
// {"key1":"abc","key2":123} must land as STRING / INTEGER.
func RunExternalAPI_13_04_DefaultIntegerScopeINTEGER(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("testModel", 2, `{"key1":"abc","key2":123}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "testModel", 2)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got, err := simpleViewFieldType(t, exported, "$", ".key1"); err != nil {
		t.Errorf("$.key1 lookup: %v", err)
	} else if got != "STRING" {
		t.Errorf("$.key1: got %q want STRING", got)
	}
	if got, err := simpleViewFieldType(t, exported, "$", ".key2"); err != nil {
		t.Errorf("$.key2 lookup: %v", err)
	} else if got != "INTEGER" {
		t.Errorf("$.key2: got %q want INTEGER", got)
	}
}

// RunExternalAPI_13_05ext_PolymorphicMergeWithDefaultScopes — dictionary 13/05ext.
// Two-sample merge with default scopes → polymorphic types
// [INTEGER, STRING], [INTEGER, BOOLEAN], [BOOLEAN].
func RunExternalAPI_13_05ext_PolymorphicMergeWithDefaultScopes(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("testModel_3e", 1, `{"key1":"abc","key2":123}`); err != nil {
		t.Fatalf("create model v1: %v", err)
	}
	// Re-import as the same model to merge a second sample.
	if err := d.CreateModelFromSample("testModel_3e", 1, `{"key1":456,"key2":false,"key3":true}`); err != nil {
		t.Fatalf("merge model v2: %v", err)
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "testModel_3e", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, c := range []struct {
		key  string
		want string
	}{
		{".key1", "[INTEGER, STRING]"},
		{".key2", "[INTEGER, BOOLEAN]"},
		{".key3", "BOOLEAN"},
	} {
		if got, err := simpleViewFieldType(t, exported, "$", c.key); err != nil {
			t.Errorf("%s lookup: %v", c.key, err)
		} else if got != c.want {
			t.Errorf("%s: got %q want %q", c.key, got, c.want)
		}
	}
}

// RunExternalAPI_13_06_DoubleAtMaxBoundary — dictionary 13/06.
// Field declared DOUBLE accepts Double.MAX_VALUE; entity round-trips.
func RunExternalAPI_13_06_DoubleAtMaxBoundary(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"v": 1.7976931348623157E308}`
	if err := d.CreateModelFromSample("numDouble", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("numDouble", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("numDouble", 1, sample)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	v, ok := got.Data["v"].(float64)
	if !ok {
		t.Fatalf("readback $.v not a number: %T %v", got.Data["v"], got.Data["v"])
	}
	if v != 1.7976931348623157e308 {
		t.Errorf("readback $.v: got %g want 1.7976931348623157e308", v)
	}
}

// RunExternalAPI_13_07_BigDecimal20Plus18 — dictionary 13/07.
// 38-significant-digit decimal lands as BIG_DECIMAL; round-trip via
// stripTrailingZeros numeric comparison.
func RunExternalAPI_13_07_BigDecimal20Plus18(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"v": 12345678901234567800.123456789012345670}`
	if err := d.CreateModelFromSample("numBigDecimal", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("numBigDecimal", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("numBigDecimal", 1, sample)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// stripTrailingZeros comparison via math/big.
	want, _, _ := new(big.Float).Parse("12345678901234567800.123456789012345670", 10)
	gotNum, ok := got.Data["v"].(json.Number)
	if !ok {
		// json.Unmarshal may return float64 for numbers without a custom decoder.
		// Fall back to string conversion of whatever we got.
		gotStr := fmt.Sprintf("%v", got.Data["v"])
		gotF, _, err := new(big.Float).Parse(gotStr, 10)
		if err != nil {
			t.Fatalf("parse readback %q as number: %v", gotStr, err)
		}
		if want.Cmp(gotF) != 0 {
			t.Errorf("BIG_DECIMAL round-trip: got %s want %s (stripTrailingZeros equivalent)", gotF.Text('f', -1), want.Text('f', -1))
		}
	} else {
		gotF, _, err := new(big.Float).Parse(string(gotNum), 10)
		if err != nil {
			t.Fatalf("parse readback %q: %v", string(gotNum), err)
		}
		if want.Cmp(gotF) != 0 {
			t.Errorf("BIG_DECIMAL round-trip: got %s want %s", gotF.Text('f', -1), want.Text('f', -1))
		}
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "numBigDecimal", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got, err := simpleViewFieldType(t, exported, "$", ".v"); err != nil {
		t.Errorf("$.v lookup: %v", err)
	} else if got != "BIG_DECIMAL" {
		t.Errorf("$.v type: got %q want BIG_DECIMAL", got)
	}
}

// RunExternalAPI_13_08_UnboundDecimalGT18Frac — dictionary 13/08.
// 19-fractional-digit value lands as UNBOUND_DECIMAL; round-trip via
// toPlainString numeric comparison.
func RunExternalAPI_13_08_UnboundDecimalGT18Frac(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"v": 12345678901234567800.1234567890123456789}`
	if err := d.CreateModelFromSample("numUnboundDecimal", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("numUnboundDecimal", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("numUnboundDecimal", 1, sample)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want, _, _ := new(big.Float).Parse("12345678901234567800.1234567890123456789", 10)
	gotStr := fmt.Sprintf("%v", got.Data["v"])
	gotF, _, err := new(big.Float).Parse(gotStr, 10)
	if err != nil {
		t.Fatalf("parse readback %q: %v", gotStr, err)
	}
	if want.Cmp(gotF) != 0 {
		t.Errorf("UNBOUND_DECIMAL round-trip: got %s want %s (toPlainString equivalent)", gotF.Text('f', -1), want.Text('f', -1))
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "numUnboundDecimal", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got, err := simpleViewFieldType(t, exported, "$", ".v"); err != nil {
		t.Errorf("$.v lookup: %v", err)
	} else if got != "UNBOUND_DECIMAL" {
		t.Errorf("$.v type: got %q want UNBOUND_DECIMAL", got)
	}
}

// RunExternalAPI_13_09_BigInteger38Digit — dictionary 13/09.
func RunExternalAPI_13_09_BigInteger38Digit(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"v": 12345678901234567890123456789012345678}`
	if err := d.CreateModelFromSample("numBigInteger", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("numBigInteger", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("numBigInteger", 1, sample)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want, _ := new(big.Int).SetString("12345678901234567890123456789012345678", 10)
	gotStr := fmt.Sprintf("%v", got.Data["v"])
	gotI, ok := new(big.Int).SetString(gotStr, 10)
	if !ok {
		t.Fatalf("parse readback %q as big.Int failed", gotStr)
	}
	if want.Cmp(gotI) != 0 {
		t.Errorf("BIG_INTEGER round-trip: got %s want %s", gotI.String(), want.String())
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "numBigInteger", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got, err := simpleViewFieldType(t, exported, "$", ".v"); err != nil {
		t.Errorf("$.v lookup: %v", err)
	} else if got != "BIG_INTEGER" {
		t.Errorf("$.v type: got %q want BIG_INTEGER", got)
	}
}

// RunExternalAPI_13_10_UnboundInteger40Digit — dictionary 13/10.
func RunExternalAPI_13_10_UnboundInteger40Digit(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"v": 1234567890123456789012345678901234567890}`
	if err := d.CreateModelFromSample("numUnboundInteger", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("numUnboundInteger", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("numUnboundInteger", 1, sample)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want, _ := new(big.Int).SetString("1234567890123456789012345678901234567890", 10)
	gotStr := fmt.Sprintf("%v", got.Data["v"])
	gotI, ok := new(big.Int).SetString(gotStr, 10)
	if !ok {
		t.Fatalf("parse readback %q as big.Int failed", gotStr)
	}
	if want.Cmp(gotI) != 0 {
		t.Errorf("UNBOUND_INTEGER round-trip: got %s want %s", gotI.String(), want.String())
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "numUnboundInteger", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got, err := simpleViewFieldType(t, exported, "$", ".v"); err != nil {
		t.Errorf("$.v lookup: %v", err)
	} else if got != "UNBOUND_INTEGER" {
		t.Errorf("$.v type: got %q want UNBOUND_INTEGER", got)
	}
}

// RunExternalAPI_13_11_SearchIntegerAgainstDouble — dictionary 13/11.
// Ingest 4 entities with $.price as DOUBLE values >= 70. Search with an
// INTEGER condition value (70) via async + direct must each return 4.
func RunExternalAPI_13_11_SearchIntegerAgainstDouble(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("orders", 1, `{"price": 100.0}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("orders", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	for _, price := range []float64{70.5, 80.0, 100.0, 200.5} {
		body := fmt.Sprintf(`{"price": %g}`, price)
		if _, err := d.CreateEntity("orders", 1, body); err != nil {
			t.Fatalf("create entity price=%g: %v", price, err)
		}
	}
	const condition = `{
		"type": "group", "operator": "AND",
		"conditions": [
			{"type": "simple", "jsonPath": "$.price", "operatorType": "GREATER_OR_EQUAL", "value": 70}
		]
	}`

	// Direct (sync) search.
	directResults, err := d.SyncSearch("orders", 1, condition)
	if err != nil {
		t.Fatalf("SyncSearch: %v", err)
	}
	if len(directResults) != 4 {
		t.Errorf("direct: got %d results want 4", len(directResults))
	}

	// Async search via Await wrapper.
	page, err := d.AwaitAsyncSearchResults("orders", 1, condition, 10*time.Second)
	if err != nil {
		t.Fatalf("AwaitAsyncSearchResults: %v", err)
	}
	if len(page.Content) != 4 {
		t.Errorf("async: got %d results want 4", len(page.Content))
	}
	// Silence unused imports if any path is unreachable above.
	_ = strings.Contains
}
```

CRITICAL preliminary checks before writing this verbatim:
- Confirm `Driver.ListEntitiesByModel` exists (used by 13/01) — `grep -n "ListEntitiesByModel" e2e/externalapi/driver/driver.go`. If absent, use `Driver.SyncSearch("simple3", 1, "{\"type\":\"group\",\"conditions\":[]}")` returning all entities, OR add the method as a Driver pass-through to `Client.ListEntities` if that exists.
- Confirm `Driver.ExportModel("SIMPLE_VIEW", name, version)` returns `json.RawMessage` — already verified at `e2e/externalapi/driver/driver.go:95`.
- Confirm `Driver.GetEntity(id).Data` is `map[string]any` — already verified.
- Confirm `Driver.CreateModelFromSample` re-importing the same `(name, version)` triggers a merge (used by 13/05ext). If it triggers a "model exists" error instead, the test should chain through `UpdateModelFromSample` if that exists, OR file an issue if cyoda-go genuinely lacks model merge on the external surface. Probe via `grep "UpdateModelFromSample\|merge" e2e/externalapi/driver/driver.go internal/domain/model/importer/`.

If any preliminary check reveals a missing surface, surface as DONE_WITH_CONCERNS rather than inventing methods.

- [ ] **Step 2: Build + run scoped against memory**

```bash
go vet ./e2e/parity/externalapi/
go build ./e2e/parity/externalapi/
go test ./e2e/parity/memory/ -run "TestParity/ExternalAPI_13_" -v 2>&1 | tail -50
```

Expected: 9 PASS. If any FAIL, capture and decide:
- Decimal round-trip mismatch → likely cyoda-go's JSON serialization differs; adapt the comparison strategy (e.g. use `entity_equals_json_numeric` for all decimal scenarios).
- SIMPLE_VIEW shape doesn't match `simpleViewFieldType` extraction → adapt the helper to the actual shape.
- Model re-import rejected → use the alternative API surface or skip 13/05ext with a real reason (file an issue first if a true server gap).

- [ ] **Step 3: Run scoped across all 3 backends**

```bash
go test ./e2e/parity/sqlite/ -run "TestParity/ExternalAPI_13_" -v 2>&1 | grep -E "(PASS|FAIL|SKIP):" | head -20
go test ./e2e/parity/postgres/ -run "TestParity/ExternalAPI_13_" -v 2>&1 | grep -E "(PASS|FAIL|SKIP):" | head -20
```

Expected: 9 PASS each backend = 27 PASS total. If a scenario passes on memory but fails on sqlite or postgres, STOP — backend divergence bug.

- [ ] **Step 4: Commit**

```bash
git add e2e/parity/externalapi/numeric_types.go
git commit -m "$(cat <<'EOF'
test(externalapi): 13-numeric-types — 9 scenarios

Tranche-4 coverage for 13-numeric-types.yaml externally-reachable
subset:

- 13/01 IntegerLandsInDoubleField — JSON integer accepted for
  DOUBLE-locked field
- 13/04 DefaultIntegerScopeINTEGER — default ParsingSpec lands
  $.key2=123 as INTEGER (not BYTE)
- 13/05ext PolymorphicMergeWithDefaultScopes — two-sample merge
  → [INTEGER, STRING], [INTEGER, BOOLEAN], [BOOLEAN]
- 13/06 DoubleAtMaxBoundary — Double.MAX_VALUE round-trip
- 13/07 BigDecimal20Plus18 — 38-digit decimal as BIG_DECIMAL,
  stripTrailingZeros comparison
- 13/08 UnboundDecimalGT18Frac — 19-fractional-digit as
  UNBOUND_DECIMAL, toPlainString comparison
- 13/09 BigInteger38Digit — 38-digit integer as BIG_INTEGER
- 13/10 UnboundInteger40Digit — 40-digit integer as
  UNBOUND_INTEGER
- 13/11 SearchIntegerAgainstDouble — INTEGER condition value
  matches DOUBLE field via async + direct, both return 4

Internal-only scenarios (13/03, 13/05) and the cross-ref (13/02
to neg/02) are recorded in the mapping doc only.

Refs #121.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3 — File 14 polymorphism (5 scenarios incl. discover-and-compare)

### Task 3.1: Implement all 5 Run* in `polymorphism.go`

**Files:**
- Create: `e2e/parity/externalapi/polymorphism.go`

- [ ] **Step 1: Create the file with registry + 5 Run* bodies**

```go
package externalapi

// External API Scenario Suite — 14-polymorphism
//
// Polymorphism in cyoda-go: a field that observes more than one concrete
// DataType is exported as Polymorphic([TYPE1, TYPE2, ...]).
// SIMPLE_VIEW exporter at internal/domain/model/exporter/simple_view.go:137
// emits this shape.
//
// Sources of polymorphism exercised here:
//   1. Mixed object-or-string at same JSONPath (poly/01).
//   2. Sealed PolymorphicValue array variants (poly/03 — STRING/DOUBLE/
//      BOOLEAN/UUID).
//   3. Sealed PolymorphicTimestamp array variants (poly/04 — LocalDate/
//      YearMonth/ZonedDateTime).
//   4. Numeric-string vs UUID-string in the same scalar field (poly/05
//      REST half).
//   5. Wrong-type rejection on monomorphic DOUBLE (poly/06 negative path).

import (
	"encoding/json"
	"fmt"
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
		parity.NamedTest{Name: "ExternalAPI_14_01_MixedObjectOrStringAtSamePath", Fn: RunExternalAPI_14_01_MixedObjectOrStringAtSamePath},
		parity.NamedTest{Name: "ExternalAPI_14_03_PolymorphicValueArray", Fn: RunExternalAPI_14_03_PolymorphicValueArray},
		parity.NamedTest{Name: "ExternalAPI_14_04_PolymorphicTimestampArray", Fn: RunExternalAPI_14_04_PolymorphicTimestampArray},
		parity.NamedTest{Name: "ExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf", Fn: RunExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf},
		parity.NamedTest{Name: "ExternalAPI_14_06_RejectWrongTypeCondition", Fn: RunExternalAPI_14_06_RejectWrongTypeCondition},
	)
}

// RunExternalAPI_14_01_MixedObjectOrStringAtSamePath — dictionary 14/01.
// $.some-array[*].some-object is an object in element 0 and a string in
// element 1. Both an object-key condition and a string-equals condition
// must return non-empty results via async + direct.
func RunExternalAPI_14_01_MixedObjectOrStringAtSamePath(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"label":"name","some-array":[{"some-label":"hello","some-object":{"some-key":"some-key","some-other-key":"some-other-key"}},{"some-label":"hello","some-object":"abc"}]}`
	if err := d.CreateModelFromSample("polymorphic", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("polymorphic", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := d.CreateEntity("polymorphic", 1, sample); err != nil {
		t.Fatalf("create entity: %v", err)
	}
	const objectBranch = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.some-array[*].some-object.some-key","operatorType":"EQUALS","value":"some-key"}
		]
	}`
	const stringBranch = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.some-array[*].some-object","operatorType":"EQUALS","value":"abc"}
		]
	}`

	for _, c := range []struct {
		label, condition string
	}{
		{"object-branch", objectBranch},
		{"string-branch", stringBranch},
	} {
		direct, err := d.SyncSearch("polymorphic", 1, c.condition)
		if err != nil {
			t.Errorf("%s direct: %v", c.label, err)
			continue
		}
		if len(direct) == 0 {
			t.Errorf("%s direct returned empty", c.label)
		}
		page, err := d.AwaitAsyncSearchResults("polymorphic", 1, c.condition, 10*time.Second)
		if err != nil {
			t.Errorf("%s async: %v", c.label, err)
			continue
		}
		if len(page.Content) == 0 {
			t.Errorf("%s async returned empty", c.label)
		}
	}
}

// RunExternalAPI_14_03_PolymorphicValueArray — dictionary 14/03.
// AllFieldsModel.polymorphicArray accepts (StringValue, DoubleValue,
// BooleanValue, UUIDValue). Readback verbatim + SIMPLE_VIEW reports
// [STRING, DOUBLE, BOOLEAN, UUID] for $.polymorphicArray[*].value.
func RunExternalAPI_14_03_PolymorphicValueArray(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"polymorphicArray":[{"value":"abc"},{"value":3.14},{"value":true},{"value":"550e8400-e29b-41d4-a716-446655440000"}]}`
	if err := d.CreateModelFromSample("AllFieldsModel", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("AllFieldsModel", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("AllFieldsModel", 1, sample)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Verbatim round-trip: re-marshal and compare structural JSON.
	gotJSON, err := json.Marshal(got.Data)
	if err != nil {
		t.Fatalf("re-marshal got: %v", err)
	}
	var wantTree, gotTree any
	_ = json.Unmarshal([]byte(sample), &wantTree)
	_ = json.Unmarshal(gotJSON, &gotTree)
	wantNorm, _ := json.Marshal(wantTree)
	gotNorm, _ := json.Marshal(gotTree)
	if string(wantNorm) != string(gotNorm) {
		t.Errorf("round-trip differs:\n  want: %s\n  got:  %s", string(wantNorm), string(gotNorm))
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "AllFieldsModel", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// SIMPLE_VIEW uses path keys like "$.polymorphicArray[*]" with
	// child entries ".value": "<descriptor>".
	if got, err := simpleViewFieldType(t, exported, "$.polymorphicArray[*]", ".value"); err != nil {
		t.Errorf("$.polymorphicArray[*].value lookup: %v", err)
	} else {
		// Polymorphic descriptor — must mention all 4 expected types.
		for _, want := range []string{"STRING", "DOUBLE", "BOOLEAN", "UUID"} {
			if !strings.Contains(got, want) {
				t.Errorf("$.polymorphicArray[*].value: %q missing %q", got, want)
			}
		}
	}
}

// RunExternalAPI_14_04_PolymorphicTimestampArray — dictionary 14/04.
// objectArray[*].timestamp accepts LocalDate / YearMonth / ZonedDateTime.
// Readback verbatim + SIMPLE_VIEW reports [LOCAL_DATE, YEAR_MONTH,
// ZONED_DATE_TIME].
func RunExternalAPI_14_04_PolymorphicTimestampArray(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"objectArray":[{"timestamp":"2024-01-15"},{"timestamp":"2024-03"},{"timestamp":"2024-06-20T10:15:30+01:00[Europe/Paris]"}]}`
	if err := d.CreateModelFromSample("AllFieldsModelTS", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("AllFieldsModelTS", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("AllFieldsModelTS", 1, sample)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	gotJSON, _ := json.Marshal(got.Data)
	var wantTree, gotTree any
	_ = json.Unmarshal([]byte(sample), &wantTree)
	_ = json.Unmarshal(gotJSON, &gotTree)
	wantNorm, _ := json.Marshal(wantTree)
	gotNorm, _ := json.Marshal(gotTree)
	if string(wantNorm) != string(gotNorm) {
		t.Errorf("round-trip differs:\n  want: %s\n  got:  %s", string(wantNorm), string(gotNorm))
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "AllFieldsModelTS", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got, err := simpleViewFieldType(t, exported, "$.objectArray[*]", ".timestamp"); err != nil {
		t.Errorf("$.objectArray[*].timestamp lookup: %v", err)
	} else {
		// cyoda-go may classify these as STRING (no temporal sub-type detection).
		// Discover via t.Logf if needed; for now expect all three temporal types
		// or a single STRING with a different_naming_same_level skip.
		// If the assertion fails on real data, classify as worse and surface;
		// do not weaken to "any non-empty".
		for _, want := range []string{"LOCAL_DATE", "YEAR_MONTH", "ZONED_DATE_TIME"} {
			if !strings.Contains(got, want) {
				t.Errorf("$.objectArray[*].timestamp: %q missing %q (cyoda-go may not distinguish temporal subtypes — surface as %s on first failure)", got, want, "different_naming_same_level if cyoda-go reports STRING")
			}
		}
	}
}

// RunExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf — dictionary 14/05 (REST half).
// The dictionary's RSocket leg is unreachable (no cyoda-go analogue);
// only the REST-equivalent direct-search is exercised. Recorded as
// (skipped) for the RSocket step in the mapping doc.
func RunExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	// Register the model from a sample whose station_id is one polymorphic
	// scalar (the dictionary's bike-stations dataset isn't preloaded — we
	// register a minimal equivalent).
	const sampleNumeric = `{"station_id":"1436495119852630436","name":"station-num"}`
	const sampleUUID = `{"station_id":"a3a48d5c-a135-11e9-9cda-0a87ae2ba916","name":"station-uuid"}`
	if err := d.CreateModelFromSample("stations", 1, sampleNumeric); err != nil {
		t.Fatalf("create model v1: %v", err)
	}
	if err := d.CreateModelFromSample("stations", 1, sampleUUID); err != nil {
		t.Fatalf("merge model v2: %v", err)
	}
	if err := d.LockModel("stations", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := d.CreateEntity("stations", 1, sampleNumeric); err != nil {
		t.Fatalf("create entity numeric: %v", err)
	}
	if _, err := d.CreateEntity("stations", 1, sampleUUID); err != nil {
		t.Fatalf("create entity uuid: %v", err)
	}
	const condition = `{
		"type":"group","operator":"OR",
		"conditions":[
			{"type":"simple","jsonPath":"$.station_id","operatorType":"EQUALS","value":"1436495119852630436"},
			{"type":"simple","jsonPath":"$.station_id","operatorType":"EQUALS","value":"a3a48d5c-a135-11e9-9cda-0a87ae2ba916"}
		]
	}`
	results, err := d.SyncSearch("stations", 1, condition)
	if err != nil {
		t.Fatalf("SyncSearch: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("direct: got %d results want 2 (one per station_id branch)", len(results))
	}
}

// RunExternalAPI_14_06_RejectWrongTypeCondition — dictionary 14/06.
// $.price is DOUBLE; condition value "abc" must be rejected with HTTP 400.
// Discover-and-compare on the errorCode (dictionary expects
// InvalidTypesInClientConditionException).
func RunExternalAPI_14_06_RejectWrongTypeCondition(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("ordersWrong", 1, `{"price": 100.0}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("ordersWrong", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	const badCondition = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.price","operatorType":"GREATER_OR_EQUAL","value":"abc"}
		]
	}`
	// Direct search must reject. Use the SyncSearch path via a Raw helper
	// if available; otherwise via doRaw on the underlying client.
	status, body, err := d.SyncSearchRaw("ordersWrong", 1, badCondition)
	if err != nil {
		t.Fatalf("SyncSearchRaw transport: %v", err)
	}
	t.Logf("DISCOVER 14_06 direct status=%d body=%s", status, string(body))
	errorcontract.Match(t, status, body, errorcontract.ExpectedError{
		HTTPStatus: http.StatusBadRequest,
	})

	// Async search submission must also reject (per dictionary).
	asyncStatus, asyncBody, err := d.SubmitAsyncSearchRaw("ordersWrong", 1, badCondition)
	if err != nil {
		t.Fatalf("SubmitAsyncSearchRaw transport: %v", err)
	}
	t.Logf("DISCOVER 14_06 async status=%d body=%s", asyncStatus, string(asyncBody))
	errorcontract.Match(t, asyncStatus, asyncBody, errorcontract.ExpectedError{
		HTTPStatus: http.StatusBadRequest,
	})
}
```

CRITICAL preliminary checks:
- **`Driver.SyncSearchRaw`** and **`Driver.SubmitAsyncSearchRaw`** may not exist. Check: `grep -nE "SyncSearchRaw|SubmitAsyncSearchRaw" e2e/externalapi/driver/driver.go`. If absent:
  - **Option A:** add them as Driver pass-throughs to `Client.doRaw` calls (mirror the existing `*Raw` pattern from `CreateEntityRaw`, `LockModelRaw`). This is the right move per Gate 6.
  - **Option B:** call `d.SyncSearch(...)` and `d.SubmitAsyncSearch(...)` and rely on the `error` return wrapping the body — extract the status from the error string. This is fragile; prefer Option A.
  - Pick A. Add the two `*Raw` Driver methods + their underlying client methods if missing. Add this work as a sub-task BEFORE Step 2.
- **`d.AwaitAsyncSearchResults`** signature uses `time.Duration` — confirm `"time"` is already imported in `polymorphism.go` (yes, from the snippet above).
- **Same `simpleViewFieldType` helper** is used here as in `numeric_types.go`. Since both files are in package `externalapi`, the helper from Phase 2 is in scope. Don't redefine.

If the Raw helpers need to be added, the sub-task is:

```go
// In e2e/parity/client/http.go — append:
func (c *Client) SyncSearchRaw(t *testing.T, modelName string, modelVersion int, condition string) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/search/direct/%s/%d", modelName, modelVersion)
	return c.doRawWithStatus(t, http.MethodPost, path, condition)
}

func (c *Client) SubmitAsyncSearchRaw(t *testing.T, modelName string, modelVersion int, condition string) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/search/async/%s/%d", modelName, modelVersion)
	return c.doRawWithStatus(t, http.MethodPost, path, condition)
}

// In e2e/externalapi/driver/driver.go — append:
func (d *Driver) SyncSearchRaw(name string, version int, condition string) (int, []byte, error) {
	return d.client.SyncSearchRaw(d.t, name, version, condition)
}
func (d *Driver) SubmitAsyncSearchRaw(name string, version int, condition string) (int, []byte, error) {
	return d.client.SubmitAsyncSearchRaw(d.t, name, version, condition)
}
```

If `c.doRawWithStatus` doesn't exist, the existing `CreateEntityRaw` pattern shows what to mirror (it uses an internal helper that returns status + body without panicking on non-2xx). Find that helper via `grep -nE "doRawWithStatus|CreateEntityRaw" e2e/parity/client/http.go` and reuse.

Land the Raw additions in a separate commit BEFORE the polymorphism.go file commit:

```
test(parity/client+driver): add SyncSearchRaw + SubmitAsyncSearchRaw

Mirrors the existing *Raw pattern from CreateEntityRaw/LockModelRaw.
Required by 14/06's discover-and-compare on the wrong-type
condition rejection.

Refs #121.
```

- [ ] **Step 2: Build + run scoped against memory**

```bash
go vet ./e2e/parity/externalapi/
go build ./e2e/parity/externalapi/
go test ./e2e/parity/memory/ -run "TestParity/ExternalAPI_14_" -v 2>&1 | tail -50
```

Expected: 5 PASS. If any FAIL:
- 14/03 round-trip differs (e.g. UUID variant comes back as a different shape) → adapt assertion or surface as polymorphism gap.
- 14/04 timestamp types not distinguished → cyoda-go may classify all as STRING. Capture observed descriptor; tighten OR surface as worse-class divergence + skip.
- 14/06 status not 400 → the rejection happens elsewhere or returns a different code; capture and adjust.

- [ ] **Step 3: Discover-and-compare on 14/06**

After memory passes (or partially passes):

```bash
go test ./e2e/parity/memory/ -run "TestParity/ExternalAPI_14_06" -v 2>&1 | grep DISCOVER
```

Read both DISCOVER lines (direct + async). Find `properties.errorCode` in the response body. Classify against dictionary's `InvalidTypesInClientConditionException`:
- `equiv_or_better` → tighten with `errorcontract.ExpectedError{HTTPStatus: 400, ErrorCode: "<observed>"}` and add a comment.
- `worse` → surface to the controller; do NOT file a follow-up issue.

Remove both `t.Logf("DISCOVER ...")` lines before commit.

- [ ] **Step 4: Run scoped across all 3 backends**

```bash
go test ./e2e/parity/sqlite/ -run "TestParity/ExternalAPI_14_" -v 2>&1 | grep -E "(PASS|FAIL|SKIP):"
go test ./e2e/parity/postgres/ -run "TestParity/ExternalAPI_14_" -v 2>&1 | grep -E "(PASS|FAIL|SKIP):"
```

Expected: 5 PASS each backend = 15 PASS total. Backend divergence = bug; STOP.

- [ ] **Step 5: Commit**

```bash
git add e2e/parity/externalapi/polymorphism.go
git commit -m "$(cat <<'EOF'
test(externalapi): 14-polymorphism — 5 scenarios

Tranche-4 coverage for 14-polymorphism.yaml externally-reachable
subset:

- 14/01 MixedObjectOrStringAtSamePath — both branches return
  non-empty via async + direct
- 14/03 PolymorphicValueArray — round-trip 4 variants (STRING,
  DOUBLE, BOOLEAN, UUID); SIMPLE_VIEW reports all 4 in field type
- 14/04 PolymorphicTimestampArray — round-trip 3 temporal
  variants; SIMPLE_VIEW reports all 3
- 14/05 TrinoSearchOnPolymorphicScalarRESTHalf — REST direct
  search returns 2 (RSocket leg unreachable; recorded in mapping)
- 14/06 RejectWrongTypeCondition — discover-and-compare classified
  <equiv_or_better|different_naming|worse>: errorCode <observed>
  vs dictionary InvalidTypesInClientConditionException

Internal-only (poly/02 — TreeNode) and shape-only (poly/07)
recorded in the mapping doc.

Refs #121<append issue numbers if any worse-class divergences
were filed during discover-and-compare>.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Probe: 14/01 entity validator scope

**Status:** DONE — bounded fix, fix in tranche 4.

#### Rejecting validator location

`internal/domain/model/schema/validate.go:73–76` — function `validateObject`.

```go
func validateObject(model *ModelNode, data any, path string) []ValidationError {
    obj, ok := data.(map[string]any)
    if !ok {
        return []ValidationError{{Path: path, Message: fmt.Sprintf("expected object, got %T", data)}}
    }
```

This is the exact line that emits `"expected object, got string"` for the 14/01 case.

#### Root cause

When the walker (`internal/domain/model/importer/walker.go:walkArray`) processes
`$.some-array`, it calls `schema.Merge(element0, element1)` where `element0` is the
`KindObject` node for `some-object={"some-key":…}` and `element1` is a `KindLeaf/String`
node for `some-object="abc"`.

`schema.Merge` (`internal/domain/model/schema/merge.go:43–49`) resolves the kind conflict
via `mergeKind(KindObject, KindLeaf) → KindObject` (leaf always loses), while correctly
unioning the TypeSets (`types = {String}` ends up on the merged node). The model's
serialized wire form (`codec.go:toWire`) stores `kind:"OBJECT"` with `types:["STRING"]`.

On validation, `validateNode` dispatches on `Kind()` alone → sees `KindObject` → calls
`validateObject` → the `data.(map[string]any)` assertion fails for the string branch → error.

The TypeSet on the merged node correctly contains `String`, but `validateNode` never
reaches `validateLeaf` (which does consult the full TypeSet) because the Kind dispatch
short-circuits to `validateObject` first.

#### Does the validator have TypeSet access?

Yes. `ModelNode.Types()` is available on every node. `validateLeaf` already iterates
`model.Types().Types()` and accepts any participating type. The fix does not need new
plumbing — it needs a guard at the top of `validateObject` (and symmetrically
`validateArray`) that checks whether the incoming data is a non-object (or non-array)
value that matches one of the node's explicitly-recorded leaf types.

#### Estimated LOC and scope

**~20 lines in `internal/domain/model/schema/validate.go`** plus a corresponding
test addition in `validate_test.go` (~25 lines).

Specifically:
1. Add a helper `validatePolymorphicLeafFallback(model, data, path)` (~8 lines): if
   `model.Types()` is non-empty and `data` is not the structural type implied by
   `model.Kind()`, check whether `inferDataType(data)` satisfies any type in the
   TypeSet. Return `nil` on match; fall through to structural validation otherwise.
2. Call the helper at the top of `validateObject` (2 lines) and `validateArray` (2 lines)
   before the type assertion — total guard cost ~4 lines per function.
3. Test: `TestValidatePolymorphicObjectOrStringAtSamePath` exercises a `KindObject` node
   with `types={String}` accepting a string value, and a `KindObject` node with no leaf
   types still rejecting a non-object.

No SPI changes. No plumbing changes. No new files. The TypeSet is already populated
correctly by `Merge` and survives the codec round-trip; the validator just needs to
consult it before dispatching on Kind.

#### Recommendation

**Fix in tranche 4.** The change is fully bounded (~45 lines total across validate.go +
validate_test.go), the TypeSet is already correct, no new plumbing is required, and the
test (14/01) is already written and `t.Skip`-annotated waiting for this fix. The
`t.Skip` can be removed as part of the same commit.

---

## Phase 4 — Mapping doc finalisation

### Task 4.1: Flip 14 implemented + 4 skipped rows to status-of-record

**Files:**
- Modify: `e2e/externalapi/dictionary-mapping.md`

- [ ] **Step 1: Read the existing tranche-4 rows**

```bash
grep -nE "^\| (numeric/|poly/)" e2e/externalapi/dictionary-mapping.md | head -30
```

Confirms the current state — all 18 rows (10 numeric + 8 poly counting internal/shape-only) carry `pending:tranche-4` or `internal_only_skip` / `shape_only_skip`.

- [ ] **Step 2: Edit the file 13 section**

Replace each `numeric/0X` / `numeric/05ext` row's status column:

| source_id | new status | notes |
|---|---|---|
| `numeric/01-compatible-int-lands-in-double-field` | `new:RunExternalAPI_13_01_IntegerLandsInDoubleField` | tranche 4 |
| `numeric/02-incompatible-decimal-after-int-cross-ref` | `cross_ref:neg/02` | tranche 2 — listed in 12/02 row; no independent test |
| `numeric/03-parsing-spec-intScope-byte` | `internal_only_skip` (unchanged) | requires `EntityModelFacade.upsert(ParsingSpec(intScope=BYTE))`; not on external surface |
| `numeric/04-default-intScope-integer-external` | `new:RunExternalAPI_13_04_DefaultIntegerScopeINTEGER` | tranche 4 |
| `numeric/05-polymorphic-field-after-merge` | `internal_only_skip` (unchanged) | same reason as numeric/03 |
| `numeric/05ext-polymorphic-field-after-merge-external` | `new:RunExternalAPI_13_05ext_PolymorphicMergeWithDefaultScopes` | tranche 4 |
| `numeric/06-double-at-max-boundary-round-trip` | `new:RunExternalAPI_13_06_DoubleAtMaxBoundary` | tranche 4 |
| `numeric/07-big-decimal-high-precision-round-trip` | `new:RunExternalAPI_13_07_BigDecimal20Plus18` | tranche 4; `stripTrailingZeros` numeric comparison |
| `numeric/08-unbound-decimal-arbitrary-precision` | `new:RunExternalAPI_13_08_UnboundDecimalGT18Frac` | tranche 4; `toPlainString` numeric comparison |
| `numeric/09-big-integer-38-digits` | `new:RunExternalAPI_13_09_BigInteger38Digit` | tranche 4 |
| `numeric/10-unbound-integer-40-digits` | `new:RunExternalAPI_13_10_UnboundInteger40Digit` | tranche 4 |
| `numeric/11-search-condition-integer-against-double-field` | `new:RunExternalAPI_13_11_SearchIntegerAgainstDouble` | tranche 4; uses async + direct search |

- [ ] **Step 3: Edit the file 14 section**

| source_id | new status | notes |
|---|---|---|
| `poly/01-mixed-object-or-string-at-same-path` | `new:RunExternalAPI_14_01_MixedObjectOrStringAtSamePath` | tranche 4 |
| `poly/02-tree-node-mixed-children-round-trip` | `internal_only_skip` (unchanged) | TreeNode internal save/reconstruct API; not on external surface |
| `poly/03-polymorphic-value-array-in-all-fields-model` | `new:RunExternalAPI_14_03_PolymorphicValueArray` | tranche 4; SIMPLE_VIEW asserts all 4 PolymorphicValue variants |
| `poly/04-polymorphic-timestamp-array-in-all-fields-model` | `new:RunExternalAPI_14_04_PolymorphicTimestampArray` | tranche 4; if cyoda-go classifies temporal subtypes as STRING, note `different_naming_same_level` per discovered behavior |
| `poly/05-trino-search-on-polymorphic-scalar` | `new:RunExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf` | tranche 4; REST half only — RSocket leg unreachable (no cyoda-go analogue) |
| `poly/06-reject-condition-with-wrong-scalar-type` | `new:RunExternalAPI_14_06_RejectWrongTypeCondition` | tranche 4 — `<equiv_or_better\|different_naming_same_level\|worse>` per discover-and-compare; errorCode `<observed>` |
| `poly/07-error-body-shape-for-invalid-polymorphic-types` | `shape_only_skip` (unchanged) | provoked via test controller endpoint not shipped externally |

If 14/04 surfaced as worse-class (cyoda-go reports STRING for all 3 temporal types), the row notes the divergence and the test status becomes `t.Skip("pending #N")` with `gap_on_our_side (#N)` in the mapping. Same for 14/06 if worse.

- [ ] **Step 4: Verify count**

```bash
grep -cE "^\| (numeric/|poly/)" e2e/externalapi/dictionary-mapping.md
```

Expected: **18** rows (10 + 8 — counting all rows including internal/shape-only).

```bash
grep -cE "^\| (numeric/|poly/).*new:Run" e2e/externalapi/dictionary-mapping.md
```

Expected: **14** rows with `new:Run...` (9 numeric + 5 poly).

- [ ] **Step 5: Commit**

```bash
git add e2e/externalapi/dictionary-mapping.md
git commit -m "$(cat <<'EOF'
docs(externalapi): mapping — flip tranche-4 rows to status-of-record

Files 13 + 14 — 14 implemented + 4 internal/shape-only skips
(unchanged):

- File 13: 9 implemented (all PASS); numeric/02 cross-ref to
  neg/02; numeric/03 + numeric/05 stay internal_only_skip
  (ParsingSpec narrowing not on external surface).
- File 14: 5 implemented (all PASS); 14/06 discover-and-compare
  classified <equiv_or_better|different_naming_same_level|worse>:
  cyoda-go errorCode <observed> vs dictionary
  InvalidTypesInClientConditionException. poly/02 + poly/07 stay
  internal_only_skip / shape_only_skip.

Refs #121<append issue numbers if any worse-class divergences
were filed>.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 5 — Verification + reviews + PR

### Task 5.1: Full verification pass

- [ ] **Step 1: `go vet ./...` silent**

```bash
go vet ./...
```

Expected: silent.

- [ ] **Step 2: `go test -short ./... -v` all passing**

```bash
go test -short ./... 2>&1 | grep -E "^(FAIL|--- FAIL)" | head -10
```

Expected: empty (no failures).

- [ ] **Step 3: `make test-short-all` across plugin submodules**

```bash
make test-short-all 2>&1 | grep -E "^(FAIL|--- FAIL)" | head -10
```

Expected: empty.

- [ ] **Step 4: Full live `go test ./e2e/parity/...` across 3 backends**

```bash
go test ./e2e/parity/... -v 2>&1 | grep -cE "(PASS|SKIP): TestParity/ExternalAPI_1(3|4)_"
go test ./e2e/parity/... 2>&1 | grep -E "^(FAIL|--- FAIL)" | head -10
```

Expected count: 14 implemented × 3 backends = **42** PASS lines for tranche-4 scenarios (file 13 + 14). Zero FAILs.

- [ ] **Step 5: Race detector once across parity suite**

```bash
go test -race ./e2e/parity/... 2>&1 | grep -iE "(WARNING: DATA RACE|FAIL|--- FAIL)" | head -10
```

Expected: empty.

### Task 5.2: Code review

- [ ] Dispatch the `superpowers:code-reviewer` agent against the full branch range:

```
git log release/v0.6.3..HEAD --oneline
git diff release/v0.6.3..HEAD --stat
```

Review focus: spec coverage (all 14 implemented + 4 skipped rows accurate); `discover-and-compare` cleanup (no `t.Logf` left); schema-adaptation comments inline; no production code changes; no new GitHub issues filed (per memory rule); polymorphic/SIMPLE_VIEW assertion correctness.

### Task 5.3: Security review

- [ ] Dispatch the `antigravity-bundle-security-developer:cc-skill-security-review` agent:

Scope: pure test-infrastructure additions; no JWT / token / credential logging in any new code; the new async-search helpers don't leak the Authorization header; condition payloads in 14/06 are deliberately invalid input but don't carry sensitive content.

### Task 5.4: Open PR to `release/v0.6.3`

- [ ] **Step 1: Push branch with inline credential helper**

```bash
git -c "credential.helper=!f() { echo username=x-access-token; echo password=$GH_TOKEN; }; f" push -u origin feat/issue-121-external-api-tranche4
```

- [ ] **Step 2: Open PR**

```bash
gh pr create --base release/v0.6.3 --head feat/issue-121-external-api-tranche4 \
  --title "test: external API scenario suite — tranche 4 (#121)" \
  --body "$(cat <<'EOF'
## Summary

External API scenario suite tranche 4: 14 new parity scenarios across files **13** (numeric-types) and **14** (polymorphism), plus 5 async-search Client primitives + 5 Driver pass-throughs required by 4 of those scenarios.

**Targeting `release/v0.6.3`** (not `main`). Tranches 1+2+3 already on `release/v0.6.3`.

## What landed

| File | Scenarios | Result |
|---|---|---|
| 13 — numeric-types | 9 implemented | 9 PASS × 3 backends = **27 PASS** |
| 14 — polymorphism | 5 implemented | 5 PASS × 3 backends = **15 PASS** |
| **Total tranche 4** | **14** | **42 PASS** |

Plus async-search helpers: `SubmitAsyncSearch`, `GetAsyncSearchStatus`, `GetAsyncSearchResults`, `CancelAsyncSearch`, `AwaitAsyncSearchResults` (Client + Driver pass-throughs).

Combined tranche 1+2+3+4: ~261 ExternalAPI scenarios run across memory + sqlite + postgres + multinode, **0 FAIL**.

## Skipped rows (unchanged)

- `numeric/03`, `numeric/05` — `internal_only_skip`: ParsingSpec narrowing not on external REST/gRPC surface.
- `numeric/02` — `cross_ref:neg/02`: covered by tranche-2.
- `poly/02` — `internal_only_skip`: TreeNode save/reconstruct API.
- `poly/07` — `shape_only_skip`: test controller endpoint not shipped externally.

## Discover-and-compare classification (file 14)

- **14/06** (wrong-type condition): cyoda-go emits `<observed errorCode>` @ HTTP 400 — classified as **`<equiv_or_better|different_naming_same_level>`** vs dictionary's `InvalidTypesInClientConditionException`.

No new server-side issues filed (per memory rule).

## Verification

- `go vet ./...` — silent
- `make test-short-all` — all passing across root + plugins/{memory,sqlite,postgres}
- `go test ./e2e/parity/...` — 42 tranche-4 PASS, 0 FAIL
- `go test -race ./e2e/parity/...` — clean (no DATA RACE)
- Code review approved
- Security review approved

## Test plan

- [ ] Squash-merge to `release/v0.6.3`
- [ ] Manually close #121 (GitHub auto-close doesn't fire for release-branch merges)
- [ ] Future tranche 5 (or v0.6.3 RC bundle): consider upstream contributions for cyoda-go's existing `NumericClassification*` parity scenarios that the dictionary lacks (per #121 "once this tranche lands")

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Confirm PR URL printed**

The PR URL is the final deliverable.

---

## Self-review

**Spec coverage check:**

| Spec section | Plan task |
|---|---|
| §3 Phase 0.1 async-search wire probe | Phase 0 / Task 0.1 |
| §3 Phase 0.2/0.3 deferred to per-scenario discovery | Phases 2 + 3 |
| §4.1 5 client + 5 driver async-search methods | Phase 1 / Tasks 1.1-1.6 |
| §4.2 numeric_types.go (9 Run*) | Phase 2 / Task 2.1 |
| §4.2 polymorphism.go (5 Run*) | Phase 3 / Task 3.1 |
| §4.3 discover-and-compare on poly/06 | Phase 3 / Task 3.1 Step 3 |
| §5 per-scenario notes | Phase 2 / Phase 3 task bodies |
| §6 testing strategy | Phase 5 |
| §7 acceptance | Phase 5 entirety |

**Placeholder scan:**
- The `[Phase 0.1 outcome — record after probe]` block is a deliberate placeholder filled by Step 4 of Task 0.1 — it's a probe-record-then-act pattern, not a TODO.
- Commit messages contain `<append issue numbers if any worse-class divergences were filed>` which is a deliberate placeholder for the discover-and-compare phase outcome (similar to tranche-3 plan's pattern).
- `<observed>`, `<equiv_or_better|different_naming_same_level|worse>` placeholders in mapping doc + commit messages are deliberate — filled in after the discover-and-compare run.

No vague "TBD" / "implement later" / "add appropriate error handling" placeholders.

**Type consistency:**
- `AsyncSearchPage` defined in Task 1.3, used by Tasks 1.5 (wrapper return), 1.6 (Driver pass-through), 2.1 (file 13/11), 3.1 (file 14/01).
- All client methods take `(t *testing.T, ...)` first; all driver methods omit `t` (use `d.t`).
- `Driver.AwaitAsyncSearchResults` signature matches `Client.AwaitAsyncSearchResults` — same `(name, version, condition, timeout)` order and `(parityclient.AsyncSearchPage, error)` return.
- `simpleViewFieldType` helper defined in `numeric_types.go` is reused in `polymorphism.go` (same package).
- The `SyncSearchRaw` + `SubmitAsyncSearchRaw` Driver methods are conditional on the existing surface — Task 3.1 explicitly checks before relying on them and includes the add-them sub-task if absent.

Plan ready for execution.
