# Server-Boundary Resilience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close #473: a frozen compute node is evicted in bounded time and never wedges a dispatcher; every goroutine on the gRPC door is panic-contained; both HTTP servers have receive-side timeouts; panic recovery covers every HTTP path; Postgres pool saturation is visible on `/metrics`; `/health` semantics are documented.

**Architecture:** On the gRPC door, `Member` becomes an unbuffered outbox drained by one writer goroutine that is the only caller of the raw stream send; eviction is a per-member closed channel every goroutine selects on, and write progress is a liveness signal alongside inbound activity. On the HTTP door the two servers are built by one function from four configurable timeouts, `Recovery` moves outermost and re-raises `http.ErrAbortHandler`, and the admin mux gets the same wrapper. The Postgres plugin registers observable OTel instruments reading `pool.Stat()`.

**Tech Stack:** Go 1.26, grpc-go (`keepalive` package), net/http, OpenTelemetry metric API + Prometheus exporter, pgx v5 `pgxpool`, testcontainers (existing E2E harness).

**Spec:** `docs/superpowers/specs/2026-09-07-473-server-boundary-resilience-design.md` — read it first; every decision (D1–D17) is argued there.

## Global Constraints

- TDD: every task starts with a failing test. No production code without a red test (CLAUDE.md Gate 1).
- No test hooks in production code. Where a behaviour cannot be driven without one, record the waiver in the task (it is called out where it applies).
- Verification: `go test ./internal/grpc/...` etc. while iterating; `make test` at the end of each stream; `make test-full` + `make race` + `go vet ./...` once before the PR. Never `-count=1`.
- `log/slog` only. Errors wrapped with `%w`. No issue numbers in code, logs, responses or help content (PR/commit messages only).
- Env vars: new ones land in `app/config.go` `DefaultConfig()`, `cmd/cyoda/help/config_registry.go`, and the help topic together; `TestRootConfigVars_MatchDefaults` and `TestConfig_EnvVarCoverage` enforce it.
- Commits: small, one per task, message ends with
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and
  `Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF`.
- Branch: `worktree-fix-473-server-boundary-resilience`, based on `release/v0.8.4`. PR targets `release/v0.8.4`.

## Streams

Tasks are grouped into three independent streams and a closing stream. Streams A, B and C touch disjoint files except `app/app.go` (A5 changes the `NewServer` call and the `SetOnChange` callback; B2 changes the handler chain) and `app/config.go` (A5 adds keep-alive validation; B3 adds `HTTPConfig`). Run A and C concurrently; run B after A5 has landed on the branch, or in a separate worktree and rebase.

- **Stream A — gRPC door:** A1 → A2 → A3 → A4 → A5 → A6 → A7 → A8 → A9
- **Stream B — HTTP door:** B1 → B2 → B3 → B4
- **Stream C — pool metrics:** C1 → C2
- **Stream D — closing:** D1 (CHANGELOG, playbook, ledger, cloud-parity) → D2 (verification, reviews, PR)

---

## Stream A — gRPC door

### Task A1: Member outbox, writer, eviction

**Files:**
- Modify: `internal/grpc/members.go` (whole `Member` type and `MemberRegistry.Register`/`Unregister`)
- Modify: `internal/grpc/recovery.go` (split `recoverPanic` into `panicStatus` + latch)
- Modify: `internal/grpc/members_test.go`, `internal/grpc/dispatch_test.go:21-32`, `internal/grpc/cluster_test.go`, `internal/grpc/scheduled_function_rpc_test.go` (call sites of `Register`, `TrackRequest`)
- Test: `internal/grpc/members_outbox_test.go` (new)

**Interfaces:**
- Produces:
  ```go
  var ErrMemberEvicted = errors.New("compute member evicted")
  func (r *MemberRegistry) Register(memberID string, tenantID spi.TenantID, tags []string, send SendFunc, greet *cepb.CloudEvent) *Member
  func (m *Member) Send(ctx context.Context, ce *cepb.CloudEvent) error      // nil | ErrMemberEvicted | ctx.Err()
  func (m *Member) TrySend(ce *cepb.CloudEvent) bool
  func (m *Member) Evict(err error)                                          // idempotent; first error wins; fails all pending
  func (m *Member) Evicted() <-chan struct{}
  func (m *Member) EvictErr() error                                          // valid after Evicted() fired
  func (m *Member) WriteInFlightSince() time.Time                            // zero when idle
  func (m *Member) WriterDone() <-chan struct{}
  func (m *Member) TrackRequest(requestID string) (chan *ProcessingResponse, error)
  func panicStatus(rec any, method string) error                             // recovery.go: ticket+log, NO latch
  ```
- `FailAllPending` becomes unexported (`failAllPending`), called only by `Evict`.

- [ ] **Step 1: Write the failing tests**

`internal/grpc/members_outbox_test.go`:

```go
package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
)

func testCE(t *testing.T) *cepb.CloudEvent {
	t.Helper()
	ce, err := NewCloudEvent(CalculationMemberKeepAliveEvent, map[string]any{"success": true})
	if err != nil {
		t.Fatalf("NewCloudEvent: %v", err)
	}
	return ce
}

// The greet is the first event on the wire, ahead of anything a dispatcher
// enqueues the instant the member becomes visible in the registry.
func TestMember_GreetIsFirstOnTheWire(t *testing.T) {
	reg := NewMemberRegistry()
	sent := make(chan *cepb.CloudEvent, 8)
	greet, _ := NewCloudEvent(CalculationMemberGreetEvent, map[string]any{"memberId": "m1", "success": true})
	m := reg.Register("m1", "tenant-1", []string{"go"}, func(ce *cepb.CloudEvent) error { sent <- ce; return nil }, greet)
	defer reg.Unregister("m1")

	if err := m.Send(context.Background(), testCE(t)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	first := <-sent
	if first.Type != CalculationMemberGreetEvent {
		t.Fatalf("first event on the wire = %s, want greet", first.Type)
	}
}

// Send returns nil only once the writer holds the event: with the writer
// wedged, a caller is released by its own deadline, not by the stream.
func TestMember_SendHonoursDeadlineWhileWriterWedged(t *testing.T) {
	reg := NewMemberRegistry()
	release := make(chan struct{})
	m := reg.Register("m1", "tenant-1", nil, func(*cepb.CloudEvent) error { <-release; return nil }, nil)
	defer func() { close(release); reg.Unregister("m1") }()

	// First send is taken by the writer and wedges inside the raw send.
	if err := m.Send(context.Background(), testCE(t)); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := m.Send(ctx, testCE(t))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Send err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Send blocked %v past its 50ms deadline", time.Since(start))
	}
	if m.WriteInFlightSince().IsZero() {
		t.Fatal("WriteInFlightSince should be non-zero while the writer is wedged")
	}
}

// A sender parked on the outbox is released by its own cancellation, and
// nothing it queued is ever written. (The writer's own ctx check on a
// received item is belt-and-braces for the unbuffered handoff and is not
// separately observable; it is reviewed, not unit-tested.)
func TestMember_SendReturnsWhenCallerGivesUp(t *testing.T) {
	reg := NewMemberRegistry()
	release := make(chan struct{})
	sent := make(chan *cepb.CloudEvent, 8)
	m := reg.Register("m1", "tenant-1", nil, func(ce *cepb.CloudEvent) error {
		sent <- ce
		<-release
		return nil
	}, nil)
	defer reg.Unregister("m1")

	_ = m.Send(context.Background(), testCE(t)) // wedges the writer
	<-sent

	// Hand the writer an item whose ctx is already cancelled at the moment
	// the writer picks it up: enqueue with a live ctx, then cancel.
	ctx, cancel := context.WithCancel(context.Background())
	enqueued := make(chan error, 1)
	go func() { enqueued <- m.Send(ctx, testCE(t)) }()
	time.Sleep(20 * time.Millisecond) // sender is parked on the unbuffered outbox
	cancel()
	if err := <-enqueued; !errors.Is(err, context.Canceled) {
		t.Fatalf("Send err = %v, want Canceled", err)
	}
	close(release) // writer resumes; nothing else is queued
	select {
	case ce := <-sent:
		t.Fatalf("writer sent %s for a caller that had given up", ce.Type)
	case <-time.After(100 * time.Millisecond):
	}
}

// Evict releases a sender blocked on the outbox and a waiter blocked on a
// tracked request, and both see the member as gone.
func TestMember_EvictReleasesSendersAndWaiters(t *testing.T) {
	reg := NewMemberRegistry()
	release := make(chan struct{})
	m := reg.Register("m1", "tenant-1", nil, func(*cepb.CloudEvent) error { <-release; return nil }, nil)
	defer close(release)

	_ = m.Send(context.Background(), testCE(t)) // wedge the writer
	ch, err := m.TrackRequest("req-1")
	if err != nil {
		t.Fatalf("TrackRequest: %v", err)
	}
	sendErr := make(chan error, 1)
	go func() { sendErr <- m.Send(context.Background(), testCE(t)) }()
	time.Sleep(20 * time.Millisecond)

	want := status.Error(codes.DeadlineExceeded, "member not draining")
	m.Evict(want)
	m.Evict(errors.New("second call must not win"))

	if err := <-sendErr; !errors.Is(err, ErrMemberEvicted) {
		t.Fatalf("blocked Send err = %v, want ErrMemberEvicted", err)
	}
	select {
	case resp := <-ch:
		if !resp.Disconnected {
			t.Fatal("pending waiter should see Disconnected")
		}
	case <-time.After(time.Second):
		t.Fatal("pending waiter was not released")
	}
	<-m.Evicted()
	if m.EvictErr() != want {
		t.Fatalf("EvictErr = %v, want the first error", m.EvictErr())
	}
	if _, err := m.TrackRequest("req-2"); !errors.Is(err, ErrMemberEvicted) {
		t.Fatalf("TrackRequest after Evict err = %v, want ErrMemberEvicted", err)
	}
	if m.TrySend(testCE(t)) {
		t.Fatal("TrySend after Evict must be false")
	}
}

// The writer exits after eviction once the raw send returns, and a raw send
// failure evicts on its own.
func TestMember_WriterExitsAndSendFailureEvicts(t *testing.T) {
	reg := NewMemberRegistry()
	m := reg.Register("m1", "tenant-1", nil, func(*cepb.CloudEvent) error { return errors.New("wire broke") }, nil)
	_ = m.Send(context.Background(), testCE(t))
	select {
	case <-m.Evicted():
	case <-time.After(time.Second):
		t.Fatal("send failure did not evict")
	}
	if st, _ := status.FromError(m.EvictErr()); st.Code() != codes.Unavailable {
		t.Fatalf("EvictErr code = %v, want Unavailable", st.Code())
	}
	select {
	case <-m.WriterDone():
	case <-time.After(time.Second):
		t.Fatal("writer did not exit after eviction")
	}
	reg.Unregister("m1")
}

// A panic inside the raw send is contained: ticket status, member evicted,
// process alive.
func TestMember_WriterPanicIsContained(t *testing.T) {
	reg := NewMemberRegistry()
	m := reg.Register("m1", "tenant-1", nil, func(*cepb.CloudEvent) error { panic("transport bug") }, nil)
	defer reg.Unregister("m1")
	_ = m.Send(context.Background(), testCE(t))
	select {
	case <-m.Evicted():
	case <-time.After(time.Second):
		t.Fatal("panic in writer did not evict")
	}
	st, _ := status.FromError(m.EvictErr())
	if st.Code() != codes.Internal || !contains(st.Message(), "[ticket: ") {
		t.Fatalf("EvictErr = %v, want Internal with ticket", m.EvictErr())
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

(Use `strings.Contains` instead of the two helpers if `strings` is already imported in the package's tests; the helpers exist only to keep this file self-contained.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/grpc/ -run 'TestMember_' 2>&1 | head -20`
Expected: compile errors (`Register` arity, `Send` arity, `ErrMemberEvicted` undefined).

- [ ] **Step 3: Split `recoverPanic` in `recovery.go`**

Replace the `recoverPanic` function with:

```go
// panicStatus logs the full panic detail (value plus stack) at ERROR under a
// freshly minted ticket UUID and returns a sanitized gRPC status error — the
// panic value and stack never reach the client, only a generic message
// carrying the same ticket, so an operator handed the client-visible ticket
// can grep the server log for the matching ERROR line. It does NOT touch the
// health flag: whether a recovered panic latches the node unhealthy depends
// on what the code was doing (engine or store work on the application's
// behalf latches; a goroutine that only ticks, enqueues or reads the wire and
// self-heals by evicting its member does not), so the latch is the caller's
// decision, made in recoverPanic.
func panicStatus(rec any, method string) error {
	panicErr := fmt.Errorf("panic: %v", rec)
	ticket := uuid.New().String()
	slog.Error("panic recovered", "pkg", "grpc", "method", method,
		"ticket", ticket, "err", panicErr, "stack", string(debug.Stack()))
	message := fmt.Sprintf("%s: internal error [ticket: %s]", common.ErrCodeServerError, ticket)
	return status.Error(codes.Internal, message)
}

// recoverPanic is panicStatus plus the health-flag latch: the interceptors'
// policy, for panics raised while serving an RPC (nil-safe: some test
// constructors do not wire a flag).
func recoverPanic(rec any, method string, healthFlag *atomic.Bool) error {
	err := panicStatus(rec, method)
	if healthFlag != nil {
		healthFlag.Store(false)
	}
	return err
}
```

Keep the existing doc comment paragraphs about what latching achieves on `UnaryRecoveryInterceptor`.

- [ ] **Step 4: Rewrite `Member` in `members.go`**

Replace everything from `// Member represents a connected calculation member.` through `LastSeen()` with:

```go
// ErrMemberEvicted is returned by Send, TrySend and TrackRequest once the
// member's stream is being torn down. A dispatcher that sees it reports
// COMPUTE_MEMBER_DISCONNECTED (retryable) rather than waiting out its timeout.
var ErrMemberEvicted = errors.New("compute member evicted")

// outboxItem is one event waiting for the writer, together with the context
// of the caller that queued it. The writer skips an item whose caller has
// already given up: a dispatcher that timed out has rolled back, so sending
// its request would only make the compute node do work nobody is waiting for.
type outboxItem struct {
	ce  *cepb.CloudEvent
	ctx context.Context
}

// Member represents a connected calculation member.
//
// Every write to the member's gRPC stream is performed by exactly one
// goroutine, the writer (writeLoop), which drains an unbuffered outbox.
// Callers hand events to the writer through Send/TrySend and never touch the
// stream. That is what makes concurrent dispatch, keep-alive and greet safe on
// one stream (grpc-go forbids concurrent SendMsg), and it is what keeps a
// frozen consumer from wedging anyone but the writer: a raw send blocked on a
// full HTTP/2 write window holds no lock any other goroutine wants.
//
// Eviction is a closed channel. The writer, the keep-alive loop, the receive
// goroutine, the stream handler and every blocked sender select on it; the
// stream handler returning is what makes grpc-go cancel the stream and unblock
// a raw send stuck in the write window.
type Member struct {
	ID          string
	TenantID    spi.TenantID
	Tags        []string
	ConnectedAt time.Time

	send       SendFunc // raw stream write; called ONLY by writeLoop
	outbox     chan outboxItem
	evicted    chan struct{}
	evictOnce  sync.Once
	evictErr   error // written once inside evictOnce, before evicted is closed
	writerDone chan struct{}
	// writeStartedAt is the unix-nanosecond time the in-flight raw send began,
	// or 0 while the writer is idle. The keep-alive loop reads it: one write
	// that has been in flight longer than the keep-alive timeout means the
	// member is not draining, whatever its inbound traffic says.
	writeStartedAt atomic.Int64

	lastSeen   time.Time
	lastSeenMu sync.RWMutex

	pendingReqs map[string]chan *ProcessingResponse
	pendingMu   sync.Mutex
	// closed is set under pendingMu by Evict. TrackRequest refuses once it is
	// set, which closes the window between a dispatcher choosing this member
	// and registering its request against it.
	closed bool
}

func newMember(id string, tenantID spi.TenantID, tags []string, send SendFunc) *Member {
	now := time.Now()
	return &Member{
		ID:          id,
		TenantID:    tenantID,
		Tags:        tags,
		ConnectedAt: now,
		send:        send,
		outbox:      make(chan outboxItem),
		evicted:     make(chan struct{}),
		writerDone:  make(chan struct{}),
		lastSeen:    now,
		pendingReqs: make(map[string]chan *ProcessingResponse),
	}
}

// Send hands ce to the writer. It returns nil once the writer holds the
// event, ErrMemberEvicted if the member is gone, or ctx.Err() if the caller's
// deadline passed first. It never blocks on the stream itself.
func (m *Member) Send(ctx context.Context, ce *cepb.CloudEvent) error {
	select {
	case <-m.evicted:
		return ErrMemberEvicted
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.outbox <- outboxItem{ce: ce, ctx: ctx}:
		return nil
	case <-m.evicted:
		return ErrMemberEvicted
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TrySend is Send without waiting: true if the writer took the event
// immediately, false if it is busy or the member is gone. The keep-alive loop
// uses it so a ping never waits behind a stalled write.
func (m *Member) TrySend(ce *cepb.CloudEvent) bool {
	select {
	case <-m.evicted:
		return false
	default:
	}
	select {
	case m.outbox <- outboxItem{ce: ce, ctx: context.Background()}:
		return true
	default:
		return false
	}
}

// Evict marks the member gone: it refuses new tracked requests, fails every
// pending one with Disconnected, records err as the status the stream handler
// returns, and closes the evicted channel. Idempotent; the first error wins.
func (m *Member) Evict(err error) {
	m.evictOnce.Do(func() {
		m.evictErr = err
		m.failAllPending("member disconnected")
		close(m.evicted)
	})
}

// Evicted is closed once Evict has run.
func (m *Member) Evicted() <-chan struct{} { return m.evicted }

// EvictErr is the error passed to the first Evict. Only valid after Evicted()
// has fired; the channel close is what publishes the write.
func (m *Member) EvictErr() error { return m.evictErr }

// WriteInFlightSince is when the writer's current raw send began, or the zero
// time when no send is in flight.
func (m *Member) WriteInFlightSince() time.Time {
	n := m.writeStartedAt.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// WriterDone is closed when the writer goroutine has exited.
func (m *Member) WriterDone() <-chan struct{} { return m.writerDone }

// writeLoop is the member's single writer. first, when non-nil, is written
// before anything queued (the greet), so it is the first event on the wire.
func (m *Member) writeLoop(first *cepb.CloudEvent) {
	defer close(m.writerDone)
	defer func() {
		if rec := recover(); rec != nil {
			m.Evict(panicStatus(rec, "Member.writeLoop"))
		}
	}()
	if first != nil && !m.write(first) {
		return
	}
	for {
		select {
		case item := <-m.outbox:
			if item.ctx.Err() != nil {
				continue
			}
			if !m.write(item.ce) {
				return
			}
		case <-m.evicted:
			return
		}
	}
}

// write performs one raw send, bracketing it with writeStartedAt so the
// keep-alive loop can see a stall. A failed send evicts the member.
func (m *Member) write(ce *cepb.CloudEvent) bool {
	m.writeStartedAt.Store(time.Now().UnixNano())
	err := m.send(ce)
	m.writeStartedAt.Store(0)
	if err != nil {
		m.Evict(status.Error(codes.Unavailable, "send failed: "+err.Error()))
		return false
	}
	return true
}

// TrackRequest creates a buffered channel for the given requestID and stores
// it in the pending requests map. Returns ErrMemberEvicted once the member is
// gone, so a dispatcher that chose this member an instant before it left
// learns so immediately instead of waiting out its timeout.
func (m *Member) TrackRequest(requestID string) (chan *ProcessingResponse, error) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if m.closed {
		return nil, ErrMemberEvicted
	}
	ch := make(chan *ProcessingResponse, 1)
	m.pendingReqs[requestID] = ch
	return ch, nil
}
```

Keep `CompleteRequest`, `AbandonRequest`, `PendingCount`, `UpdateLastSeen`, `LastSeen` as they are. Rename `FailAllPending` to `failAllPending` and make it set `m.closed = true` inside the locked section before swapping the map. Add imports `context`, `errors`, `sync/atomic`, `google.golang.org/grpc/codes`, `google.golang.org/grpc/status`.

Replace `Register` and `Unregister`:

```go
// Register creates the member, starts its writer with greet as the first
// event on the wire, and only then publishes the member to the registry. A
// dispatch routed the instant the member becomes visible therefore queues
// behind the greet. greet may be nil (test fixtures).
func (r *MemberRegistry) Register(memberID string, tenantID spi.TenantID, tags []string, send SendFunc, greet *cepb.CloudEvent) *Member {
	m := newMember(memberID, tenantID, tags, send)
	go m.writeLoop(greet)
	r.mu.Lock()
	r.members[memberID] = m
	r.mu.Unlock()
	r.notifyChange()
	return m
}

// Unregister removes the member with the given ID and evicts it, which fails
// all its pending requests and stops its writer.
func (r *MemberRegistry) Unregister(memberID string) {
	r.mu.Lock()
	m, ok := r.members[memberID]
	if ok {
		delete(r.members, memberID)
	}
	r.mu.Unlock()
	if ok {
		m.Evict(status.Error(codes.Unavailable, "member unregistered"))
	}
	r.notifyChange()
}
```

- [ ] **Step 5: Fix the existing call sites so the package compiles**

- `dispatch_test.go:21-32` `setupTestDispatcher`: `member := registry.Register("member-test", testTenantID, []string{"python"}, func(...){...}, nil)`; return `member.ID` where it returned `memberID`. Keep the `sentCh` buffer.
- `members_test.go`: every `reg.Register("tenant-1", tags, noopSend)` becomes `reg.Register("m-<n>", "tenant-1", tags, noopSend, nil)` (unique IDs per registration within a test) and uses the returned `*Member` instead of `reg.Get(id)` where convenient; `ch := m.TrackRequest("req-1")` becomes `ch, err := m.TrackRequest("req-1"); if err != nil { t.Fatal(err) }`. `TestMemberRegistry_UnregisterFailsPending` keeps its assertions (Unregister → Evict → Disconnected).
- `cluster_test.go`, `scheduled_function_rpc_test.go`: same mechanical change to `Register`.
- `streaming_test.go:531,588,640,709,910`, `dispatch_test.go:1116`: two-value `TrackRequest`.
- `streaming_greet_send_test.go:103`: `member.Send(context.Background(), kaCE)`.
- `dispatch.go:121,128`: temporary minimal edit so it compiles — `ch, _ := member.TrackRequest(requestID)` and `member.Send(ctx, ce)`; Task A2 replaces this properly.
- `streaming.go`: temporary — `Register(uuid.NewString(), tenantID, joinEvent.Tags, sendFn, nil)` returning `member`; `memberID := member.ID`; greet via `member.Send(ctx, greetCE)`; keep-alive `member.Send(ctx, kaCE)`. Task A3 rewrites this file.

- [ ] **Step 6: Run the package tests**

Run: `go test ./internal/grpc/... 2>&1 | tail -30`
Expected: all PASS, including the six new `TestMember_*` tests and the existing `TestStreaming_GreetIsSerialisedWithConcurrentDispatch` (the greet is now written by the writer; the dispatch waits on the outbox).

- [ ] **Step 7: Race check on the package**

Run: `go test -race ./internal/grpc/ -run 'TestMember_|TestStreaming_Greet|TestMemberRegistry' 2>&1 | tail -5`
Expected: PASS, no `WARNING: DATA RACE`.

- [ ] **Step 8: Commit**

```bash
git add internal/grpc/
git commit -m "fix(grpc): one writer per member — an outbox drained by a single goroutine replaces sendMu

Send hands events to the writer and honours the caller's deadline; eviction
is a closed channel every goroutine selects on and fails pending waiters
immediately. The greet is the writer's first event so no dispatch can
precede it. A panic inside the raw send is contained by ticket without
latching the node.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task A2: Dispatch — one deadline over enqueue and wait, immediate DISCONNECTED

**Files:**
- Modify: `internal/grpc/dispatch.go:108-169` (`dispatchCalloutToMember`)
- Test: `internal/grpc/dispatch_test.go` (append)

**Interfaces:**
- Consumes: `Member.Send(ctx, ce)`, `Member.TrackRequest` (A1).
- Produces: unchanged public signatures; error shapes per spec §3.4.

- [ ] **Step 1: Write the failing tests** (append to `dispatch_test.go`)

```go
// A member unregistered after it was chosen but before the request is
// tracked yields COMPUTE_MEMBER_DISCONNECTED at once, not DISPATCH_TIMEOUT
// after the full response timeout.
func TestDispatch_MemberGoneBeforeTrack_IsDisconnectedImmediately(t *testing.T) {
	dispatcher, registry, memberID, _ := setupTestDispatcher(t)
	member := registry.Get(memberID)
	registry.Unregister(memberID)

	start := time.Now()
	_, err := dispatcher.dispatchCalloutToMember(testContext(), member, EntityProcessorCalculationRequest,
		map[string]any{"requestId": "r1"}, "r1", "", 30_000, "processor", "p")
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v; should not wait out the response timeout", time.Since(start))
	}
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != common.ErrCodeComputeMemberDisconnected {
		t.Fatalf("err = %v, want COMPUTE_MEMBER_DISCONNECTED", err)
	}
}

// A writer that never drains makes the enqueue itself time out, reported as
// DISPATCH_TIMEOUT with the "member not draining" message, within the
// dispatch's own timeout.
func TestDispatch_EnqueueTimeout_IsDispatchTimeoutNotDraining(t *testing.T) {
	registry := NewMemberRegistry()
	release := make(chan struct{})
	defer close(release)
	member := registry.Register("m-wedged", testTenantID, []string{"python"},
		func(*cepb.CloudEvent) error { <-release; return nil }, nil)
	defer registry.Unregister("m-wedged")
	_ = member.Send(context.Background(), mustCE(t)) // wedge the writer

	uuids := common.NewTestUUIDGenerator()
	signer, _ := token.NewSigner(make32(t))
	dispatcher := NewProcessorDispatcher(registry, uuids, signer, "node-test", time.Minute)

	start := time.Now()
	_, err := dispatcher.dispatchCalloutToMember(testContext(), member, EntityProcessorCalculationRequest,
		map[string]any{"requestId": "r1"}, "r1", "", 100, "processor", "p")
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != common.ErrCodeDispatchTimeout {
		t.Fatalf("err = %v, want DISPATCH_TIMEOUT", err)
	}
	if !strings.Contains(appErr.Message, "member not draining") {
		t.Fatalf("message %q should say the member is not draining", appErr.Message)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("dispatcher blocked %v on a wedged member; the 100ms deadline should have released it", d)
	}
}

// A parent context cancelled during the enqueue wait is reported as the
// caller's cancellation, never as a timeout.
func TestDispatch_ParentCancelDuringEnqueue_IsCtxErr(t *testing.T) {
	registry := NewMemberRegistry()
	release := make(chan struct{})
	defer close(release)
	member := registry.Register("m-wedged", testTenantID, []string{"python"},
		func(*cepb.CloudEvent) error { <-release; return nil }, nil)
	defer registry.Unregister("m-wedged")
	_ = member.Send(context.Background(), mustCE(t))

	uuids := common.NewTestUUIDGenerator()
	signer, _ := token.NewSigner(make32(t))
	dispatcher := NewProcessorDispatcher(registry, uuids, signer, "node-test", time.Minute)

	ctx, cancel := context.WithCancel(testContext())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err := dispatcher.dispatchCalloutToMember(ctx, member, EntityProcessorCalculationRequest,
		map[string]any{"requestId": "r1"}, "r1", "", 30_000, "processor", "p")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func mustCE(t *testing.T) *cepb.CloudEvent {
	t.Helper()
	ce, err := NewCloudEvent(CalculationMemberKeepAliveEvent, map[string]any{"success": true})
	if err != nil {
		t.Fatal(err)
	}
	return ce
}
```

Check the existing `TestDispatch_*AbandonOnSendFailure` (`dispatch_test.go:~1085`): a failing `SendFunc` now evicts the member via the writer, so the dispatcher must return `COMPUTE_MEMBER_DISCONNECTED`. Update its assertion from "failed to send" to that code, keeping the `PendingCount() == 0` check.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/grpc/ -run 'TestDispatch_(MemberGoneBeforeTrack|EnqueueTimeout|ParentCancelDuringEnqueue)' 2>&1 | tail -20`
Expected: FAIL (first test gets DISPATCH_TIMEOUT after 30s or the wrong code; second gets no "not draining").

- [ ] **Step 3: Rewrite the body of `dispatchCalloutToMember` from `ch := member.TrackRequest(requestID)` to the end**

```go
	if timeoutMs <= 0 {
		timeoutMs = defaultResponseTimeoutMs
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	// One deadline bounds the whole callout: handing the request to the
	// member's writer AND waiting for the response. A member whose writer is
	// stalled cannot hold a dispatcher past this.
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch, err := member.TrackRequest(requestID)
	if err != nil {
		// The member left between being chosen and the request being tracked.
		slog.Warn("member gone before dispatch", "pkg", "grpc", "memberId", member.ID, "label", label, "name", name, "requestId", requestID)
		return nil, disconnectedErr(label)
	}
	// Spec D11: every exit that does not consume the response must clear the
	// tracking entry, or a late compute-node reply finds a dangling channel
	// and the map entry leaks. The response arm's normal completion path
	// already cleared it; clearing again is a no-op.
	defer member.AbandonRequest(requestID)

	if err := member.Send(callCtx, ce); err != nil {
		switch {
		case errors.Is(err, ErrMemberEvicted):
			slog.Error("member evicted while enqueueing dispatch", "pkg", "grpc", "memberId", member.ID, "label", label, "name", name, "requestId", requestID)
			return nil, disconnectedErr(label)
		case ctx.Err() != nil:
			return nil, ctx.Err()
		default:
			slog.Error("dispatch timeout", "pkg", "grpc", "phase", "enqueue", "memberId", member.ID, "label", label, "name", name, "requestId", requestID, "timeout", timeout)
			return nil, common.Operational(http.StatusServiceUnavailable, common.ErrCodeDispatchTimeout,
				fmt.Sprintf("%s dispatch timed out after %dms: member not draining", label, timeoutMs)).AsRetryable()
		}
	}

	select {
	case resp := <-ch:
		// (existing response-arm body, unchanged)
	case <-callCtx.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Error("dispatch timeout", "pkg", "grpc", "phase", "response", "memberId", member.ID, "label", label, "name", name, "requestId", requestID, "timeout", timeout)
		return nil, common.Operational(http.StatusServiceUnavailable, common.ErrCodeDispatchTimeout,
			fmt.Sprintf("%s dispatch timed out after %dms: no response", label, timeoutMs)).AsRetryable()
	}
}

// disconnectedErr is the retryable 503 a caller gets when the compute member
// it was routed to is gone; another member (or the same one, reconnected) may
// serve the retry.
func disconnectedErr(label string) error {
	return common.Operational(http.StatusServiceUnavailable, common.ErrCodeComputeMemberDisconnected,
		fmt.Sprintf("compute member disconnected during %s dispatch", label)).AsRetryable()
}
```

Use `disconnectedErr(label)` in the existing `resp.Disconnected` arm too (same code and message as today). Add `errors` to the imports.

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/grpc/... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/grpc/dispatch.go internal/grpc/dispatch_test.go
git commit -m "fix(grpc): one deadline bounds enqueue and wait; a member gone before tracking fails fast

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task A3: Stream handler — single receive goroutine, eviction-driven exit, write-progress liveness

**Files:**
- Modify: `internal/grpc/streaming.go` (whole file below the join parsing)
- Modify: `internal/grpc/server.go` (`CloudEventsServiceImpl` gets `healthFlag`; keep-alive fields become required)
- Modify: `internal/grpc/streaming_test.go` (replace `SetKeepAliveConfig` calls; `newServiceForTest`)
- Test: `internal/grpc/streaming_eviction_test.go` (new)

**Interfaces:**
- Consumes: A1's `Member` API, `panicStatus`.
- Produces:
  ```go
  func newServiceForTest() *CloudEventsServiceImpl                   // 10s/30s keep-alive
  func newServiceWithKeepAlive(interval, timeout time.Duration) *CloudEventsServiceImpl
  func (s *CloudEventsServiceImpl) keepAliveLoop(ctx context.Context, member *Member)
  func (s *CloudEventsServiceImpl) receiveLoop(stream ..., member *Member, recvCh chan<- *cepb.CloudEvent)
  func (s *CloudEventsServiceImpl) handleProcessorResponse(member *Member, payload json.RawMessage) // and Criteria/Function
  ```
- `SetKeepAliveConfig`, `DefaultKeepAliveInterval`, `DefaultKeepAliveTimeout`, `keepAliveInterval_()`, `keepAliveTimeout_()` are deleted.

- [ ] **Step 1: Write the failing tests**

`internal/grpc/streaming_eviction_test.go`:

```go
package grpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
)

// wedgingStream parks every send after the greet until released, and keeps
// the client "alive" by feeding inbound keep-alives on demand.
type wedgingStream struct {
	*mockBidiStream
	greeted chan struct{}
	release chan struct{}
	sends   int
}

func newWedgingStream(ctx context.Context) *wedgingStream {
	return &wedgingStream{mockBidiStream: newMockBidiStream(ctx), greeted: make(chan struct{}), release: make(chan struct{})}
}

func (s *wedgingStream) Send(ce *cepb.CloudEvent) error {
	s.sends++
	if s.sends == 1 {
		err := s.mockBidiStream.Send(ce)
		close(s.greeted)
		return err
	}
	<-s.release
	return s.mockBidiStream.Send(ce)
}

type bidiStream = googlegrpc.BidiStreamingServer[cepb.CloudEvent, cepb.CloudEvent]

func startStream(t *testing.T, svc *CloudEventsServiceImpl, stream bidiStream) (chan error, *Member) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- svc.StartStreaming(stream) }()
	var member *Member
	for i := 0; i < 400 && member == nil; i++ {
		if ms := svc.registry.List(); len(ms) == 1 {
			member = ms[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if member == nil {
		t.Fatal("member never registered")
	}
	return done, member
}

// A member whose own keep-alive goroutine keeps pinging but whose application
// has stopped reading is evicted by write progress, within the keep-alive
// timeout, and the stream ends with "member not draining".
func TestStreaming_PingingButNotReading_IsEvictedByWriteProgress(t *testing.T) {
	svc := newServiceWithKeepAlive(20*time.Millisecond, 120*time.Millisecond)
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()
	stream := newWedgingStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", []string{"go"}))
	done, member := startStream(t, svc, stream)
	<-stream.greeted

	// Wedge the writer with a dispatch, then keep the inbound side chatty.
	go func() { _ = member.Send(context.Background(), mustCE(t)) }()
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				stream.enqueue(makeKeepAliveEvent(t))
			}
		}
	}()
	defer close(stop)

	select {
	case err := <-done:
		st, _ := status.FromError(err)
		if st.Code() != codes.DeadlineExceeded || !strings.Contains(st.Message(), "not draining") {
			t.Fatalf("stream ended with %v, want DeadlineExceeded 'member not draining'", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pinging-but-not-reading member was never evicted")
	}
	close(stream.release)
}

// A clean client disconnect ends the stream promptly with the Recv error and
// unregisters the member (regression guard for the single receive goroutine).
func TestStreaming_ClientClose_EndsPromptly(t *testing.T) {
	svc := newServiceWithKeepAlive(time.Hour, 2*time.Hour)
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()
	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", nil))
	done, member := startStream(t, svc, stream)
	_ = stream.waitForSent(t, 2*time.Second)

	stream.closeRecv()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not end on client close")
	}
	if svc.registry.Get(member.ID) != nil {
		t.Fatal("member still registered after close")
	}
	select {
	case <-member.WriterDone():
	case <-time.After(time.Second):
		t.Fatal("writer still running after stream end")
	}
}

// A panic inside stream.Recv is contained: ticket status, member evicted, the
// health flag untouched (this goroutine does no engine work).
func TestStreaming_RecvPanic_IsContainedWithoutLatch(t *testing.T) {
	svc := newServiceWithKeepAlive(time.Hour, 2*time.Hour)
	flag := &atomic.Bool{}
	flag.Store(true)
	svc.healthFlag = flag
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()
	stream := &panickingRecvStream{mockBidiStream: newMockBidiStream(ctx)}
	stream.enqueue(makeJoinEvent(t, "tenant-1", nil))
	done, _ := startStream(t, svc, stream)
	_ = stream.waitForSent(t, 2*time.Second)
	stream.armPanic()

	select {
	case err := <-done:
		st, _ := status.FromError(err)
		if st.Code() != codes.Internal || !strings.Contains(st.Message(), "[ticket: ") {
			t.Fatalf("err = %v, want Internal with ticket", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after Recv panic")
	}
	if !flag.Load() {
		t.Fatal("a receive-goroutine panic must not latch the health flag")
	}
}

// panickingRecvStream returns queued events normally until armed, then panics
// on the next Recv.
type panickingRecvStream struct {
	*mockBidiStream
	armed atomic.Bool
}

func (s *panickingRecvStream) armPanic() { s.armed.Store(true); s.enqueue(makeKeepAliveEventNoT()) }
func (s *panickingRecvStream) Recv() (*cepb.CloudEvent, error) {
	ce, err := s.mockBidiStream.Recv()
	if s.armed.Load() {
		panic("recv exploded")
	}
	return ce, err
}

// Processor, criteria and function responses count as liveness.
func TestStreaming_ResponseRefreshesLastSeen(t *testing.T) {
	svc := newServiceWithKeepAlive(time.Hour, 2*time.Hour)
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()
	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", nil))
	done, member := startStream(t, svc, stream)
	_ = stream.waitForSent(t, 2*time.Second)
	before := member.LastSeen()
	time.Sleep(5 * time.Millisecond)

	ce, _ := NewCloudEvent(EntityProcessorCalculationResponse, map[string]any{"requestId": "unknown", "success": true})
	stream.enqueue(ce)
	deadline := time.Now().Add(2 * time.Second)
	for !member.LastSeen().After(before) {
		if time.Now().After(deadline) {
			t.Fatal("processor response did not refresh LastSeen")
		}
		time.Sleep(2 * time.Millisecond)
	}
	stream.closeRecv()
	<-done
}
```

Add the imports the file needs (`io`, `sync/atomic`). `bidiStream` is a type alias to add in the test file: `type bidiStream = googlegrpc.BidiStreamingServer[cepb.CloudEvent, cepb.CloudEvent]`. `makeKeepAliveEventNoT` is `makeKeepAliveEvent` without the `*testing.T` (build the event, ignore the impossible error). In `streaming_test.go` replace `svc.SetKeepAliveConfig(a, b)` with `svc := newServiceWithKeepAlive(a, b)` at lines 256, 316, 356, 987 and add:

```go
func newServiceWithKeepAlive(interval, timeout time.Duration) *CloudEventsServiceImpl {
	return &CloudEventsServiceImpl{registry: NewMemberRegistry(), keepAliveInterval: interval, keepAliveTimeout: timeout}
}
```

and make `newServiceForTest()` return `newServiceWithKeepAlive(10*time.Second, 30*time.Second)`. Grep for other constructors of `CloudEventsServiceImpl{` in tests (`rpc_test.go:45 newTestEnv` and friends) and give each the two keep-alive fields.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/grpc/ -run 'TestStreaming_(PingingButNotReading|ClientClose_EndsPromptly|RecvPanic|ResponseRefreshes)' 2>&1 | tail -20`
Expected: compile failure (`newServiceWithKeepAlive`, `healthFlag` undefined) or FAIL.

- [ ] **Step 3: Add `healthFlag` to the service and rewrite `streaming.go`**

`server.go`: add `healthFlag *atomic.Bool` to `CloudEventsServiceImpl` and set it in `NewServer` (`healthFlag: healthFlag`). Delete lines 19-44 of `streaming.go` (the `Default*` vars, `SetKeepAliveConfig`, the two accessors).

Replace `StartStreaming` from step 5 onwards, plus `keepAliveLoop`, with:

```go
	// 5. Build the greet and register. Register starts the member's writer
	// with the greet as its first event and only then publishes the member,
	// so a dispatch routed the instant the member is visible queues behind
	// the greet. The raw stream.Send closure below is the ONLY raw write on
	// this stream, and only the writer ever calls it.
	memberID := uuid.NewString()
	greetPayload := events.CalculationMemberGreetEventJson{
		ID:                  memberID,
		MemberID:            memberID,
		JoinedLegalEntityID: string(tenantID),
		Success:             true,
	}
	greetCE, err := NewCloudEvent(CalculationMemberGreetEvent, greetPayload)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create greet event: %v", err)
	}
	member := s.registry.Register(memberID, tenantID, joinEvent.Tags, func(ce *cepb.CloudEvent) error {
		return stream.Send(ce)
	}, greetCE)
	defer s.registry.Unregister(memberID)
	slog.Info("member joined", "pkg", "grpc", "memberId", memberID, "tenantId", string(tenantID), "tags", joinEvent.Tags)

	// 6. Keep-alive loop and receive goroutine. Both evict the member to end
	// the stream; neither ever blocks on it.
	kaCtx, kaCancel := context.WithCancel(ctx)
	defer kaCancel()
	go s.keepAliveLoop(kaCtx, member)
	recvCh := make(chan *cepb.CloudEvent)
	go s.receiveLoop(stream, member, recvCh)

	// 7. Main loop. Eviction — by keep-alive timeout, write stall, send
	// failure, client close, or a contained panic — is the only exit.
	// Returning is what makes grpc-go cancel the stream and unblock a raw
	// send stuck in the HTTP/2 write window.
	for {
		select {
		case <-member.Evicted():
			err := member.EvictErr()
			slog.Info("member stream ended", "pkg", "grpc", "memberId", memberID, "reason", err)
			return err
		case msg := <-recvCh:
			evtType, evtPayload, err := ParseCloudEvent(msg)
			if err != nil {
				slog.Warn("malformed CloudEvent from member", "pkg", "grpc", "memberId", memberID, "error", err)
				continue
			}
			slog.Debug("CloudEvent received from member", "pkg", "grpc", "memberId", memberID, "type", evtType, "ceId", msg.Id, "payload", logging.PayloadPreview(evtPayload, 200))

			switch evtType {
			case CalculationMemberKeepAliveEvent:
				// Liveness-only: an inbound keep-alive refreshes the member's
				// LastSeen and nothing more. The server pings on its own ticker
				// (keepAliveLoop); it must NOT echo a keep-alive back. Echoing
				// against a client that also echoes inbound keep-alives produces
				// a zero-delay, unbounded ping-pong storm pinning both processes
				// at 100% CPU.
				member.UpdateLastSeen()
			case EntityProcessorCalculationResponse:
				member.UpdateLastSeen()
				s.handleProcessorResponse(member, evtPayload)
			case EntityCriteriaCalculationResponse:
				member.UpdateLastSeen()
				s.handleCriteriaResponse(member, evtPayload)
			case EntityFunctionCalculationResponse:
				member.UpdateLastSeen()
				s.handleFunctionResponse(member, evtPayload)
			case EventAckResponse:
				member.UpdateLastSeen()
			default:
				slog.Warn("unknown event type from member", "pkg", "grpc", "memberId", memberID, "type", evtType)
			}
		}
	}
}

// receiveLoop is the one goroutine that reads the member's stream. Every
// Recv error, including a clean close, evicts the member with that error so
// the stream handler returns it. A panic here is contained by ticket and
// evicts; it does not latch the node, because this goroutine does no engine
// or store work and the member simply reconnects.
func (s *CloudEventsServiceImpl) receiveLoop(stream googlegrpc.BidiStreamingServer[cepb.CloudEvent, cepb.CloudEvent], member *Member, recvCh chan<- *cepb.CloudEvent) {
	defer func() {
		if rec := recover(); rec != nil {
			member.Evict(panicStatus(rec, "StartStreaming.receive"))
		}
	}()
	for {
		msg, err := stream.Recv()
		if err != nil {
			member.Evict(err)
			return
		}
		select {
		case recvCh <- msg:
		case <-member.Evicted():
			return
		}
	}
}

// keepAliveLoop pings the member every interval and evicts it when it has
// shown no inbound activity for timeout, OR when one write has been in
// flight for longer than timeout — a member whose own keep-alive goroutine
// keeps pinging while its application has stopped reading is still frozen,
// and write progress is the signal that catches it. The loop never blocks on
// the stream: a ping the writer cannot take right now is skipped.
func (s *CloudEventsServiceImpl) keepAliveLoop(ctx context.Context, member *Member) {
	defer func() {
		if rec := recover(); rec != nil {
			member.Evict(panicStatus(rec, "keepAliveLoop"))
		}
	}()
	ticker := time.NewTicker(s.keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-member.Evicted():
			return
		case <-ticker.C:
			if time.Since(member.LastSeen()) > s.keepAliveTimeout {
				slog.Info("member timed out", "pkg", "grpc", "memberId", member.ID)
				member.Evict(status.Error(codes.DeadlineExceeded, "keep-alive timeout"))
				return
			}
			if since := member.WriteInFlightSince(); !since.IsZero() && time.Since(since) > s.keepAliveTimeout {
				slog.Warn("member not draining", "pkg", "grpc", "memberId", member.ID, "stalledFor", time.Since(since))
				member.Evict(status.Error(codes.DeadlineExceeded, "member not draining"))
				return
			}
			kaCE, err := NewCloudEvent(CalculationMemberKeepAliveEvent, events.CalculationMemberKeepAliveEventJson{
				ID: member.ID, MemberID: member.ID, Success: true,
			})
			if err != nil {
				slog.Error("failed to create keep-alive event", "pkg", "grpc", "memberId", member.ID, "error", err)
				continue
			}
			if member.TrySend(kaCE) {
				slog.Debug("keep-alive sent", "pkg", "grpc", "memberId", member.ID)
			} else {
				slog.Debug("writer busy, ping skipped", "pkg", "grpc", "memberId", member.ID)
			}
		}
	}
}
```

Change the three `handle*Response(memberID string, …)` functions to take `member *Member` and drop their `s.registry.Get(memberID)` block. Add `"github.com/google/uuid"` to imports.

**TDD waiver, recorded:** the keep-alive loop's own recover (`keepAliveLoop`) has no reachable trigger without a test hook (its body only ticks, reads two timestamps and calls `TrySend`). It is the same three-line pattern as `receiveLoop` and `writeLoop`, which are both tested; it is reviewed, not unit-tested.

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/grpc/... 2>&1 | tail -30`
Expected: PASS. `TestStreaming_KeepAliveTimeout` (`streaming_test.go:984`) still passes via the `LastSeen` rule.

- [ ] **Step 5: Race check**

Run: `go test -race ./internal/grpc/ -run 'TestStreaming_|TestMember_' 2>&1 | tail -5`
Expected: PASS, no race.

- [ ] **Step 6: Commit**

```bash
git add internal/grpc/
git commit -m "fix(grpc): a member that stops draining is evicted within the keep-alive timeout

Write progress is a liveness signal alongside inbound activity; the keep-alive
loop never blocks on the stream. One long-lived receive goroutine replaces the
per-iteration one, and a panic in either loop is contained by ticket and evicts
without latching the node. Responses refresh liveness, as the help topic already
claimed.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task A4: Tag-change versioning

**Files:**
- Modify: `internal/grpc/members.go` (`TagChangeFunc`, `MemberRegistry`, `Register`, `Unregister`, `notifyChange`, `computeTags`)
- Modify: `app/app.go:553-561` (callback returns the `UpdateTags` error)
- Test: `internal/grpc/members_tags_test.go` (new)

**Interfaces:**
- Produces: `type TagChangeFunc func(tags map[string][]string) error`.

- [ ] **Step 1: Write the failing tests**

```go
package grpc

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// An older snapshot never overwrites a newer one, and a failed publish does
// not advance the version, so the next attempt republishes.
func TestMemberRegistry_TagPublishIsVersioned(t *testing.T) {
	reg := NewMemberRegistry()
	var mu sync.Mutex
	var published []map[string][]string
	reg.SetOnChange(func(tags map[string][]string) error {
		mu.Lock()
		defer mu.Unlock()
		// The v1 snapshot ({t:[a]}) fails to publish, whichever goroutine
		// order the scheduler picks; v2 ({t:[a,b]}) and v3 ({t:[b]}) succeed.
		if ts := tags["t"]; len(ts) == 1 && ts[0] == "a" {
			return errors.New("gossip down")
		}
		published = append(published, tags)
		return nil
	})

	reg.Register("m1", "t", []string{"a"}, noopSend, nil)   // v1: fails
	reg.Register("m2", "t", []string{"b"}, noopSend, nil)   // v2
	reg.Unregister("m1")                                    // v3: newest
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(published) == 0 {
		t.Fatal("nothing published")
	}
	last := published[len(published)-1]
	if got := last["t"]; len(got) != 1 || got[0] != "b" {
		t.Fatalf("last published tags = %v, want [b] (the newest membership)", got)
	}
	reg.Unregister("m2")
}

// Publishes deliver monotonically increasing versions even when goroutines
// race: drive many changes and assert the final published state is the final
// membership.
func TestMemberRegistry_TagPublishLatestWinsUnderFlap(t *testing.T) {
	reg := NewMemberRegistry()
	var mu sync.Mutex
	var last map[string][]string
	reg.SetOnChange(func(tags map[string][]string) error {
		time.Sleep(time.Millisecond) // widen the race window
		mu.Lock()
		last = tags
		mu.Unlock()
		return nil
	})
	for i := 0; i < 50; i++ {
		id := "m" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		reg.Register(id, "t", []string{"flap"}, noopSend, nil)
		reg.Unregister(id)
	}
	reg.Register("stable", "t", []string{"stable"}, noopSend, nil)
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if got := last["t"]; len(got) != 1 || got[0] != "stable" {
		t.Fatalf("final published tags = %v, want [stable]", got)
	}
	reg.Unregister("stable")
}
```

Existing tests that call `SetOnChange` with a `func(map[string][]string)` must return `error` now (grep `SetOnChange(` in `internal/grpc/*_test.go`).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/grpc/ -run 'TestMemberRegistry_TagPublish' 2>&1 | tail -20`
Expected: compile error on the callback type, then FAIL on the first test (an older goroutine can publish last).

- [ ] **Step 3: Implement**

In `members.go`:

```go
// TagChangeFunc is called when the set of connected members changes, with the
// computed aggregate tags (tenantID → deduplicated tag list). A non-nil error
// means the tags were not published; the registry does not count that version
// as published, so the next change republishes.
type TagChangeFunc func(tags map[string][]string) error

type MemberRegistry struct {
	mu       sync.RWMutex
	members  map[string]*Member
	onChange TagChangeFunc
	// tagsVersion increments under mu on every membership change. Publishes
	// carry the version of the snapshot they took; publishMu serialises them
	// and publishedVersion records the newest one that succeeded, so a slow
	// goroutine holding an older snapshot can never overwrite a newer one.
	tagsVersion      uint64
	publishMu        sync.Mutex
	publishedVersion uint64
}
```

`Register` and `Unregister`: increment `r.tagsVersion++` inside their `r.mu.Lock()` section. Replace `notifyChange` and `computeTags`:

```go
// notifyChange publishes the current aggregate tags in a goroutine. The
// snapshot and its version are taken under one read lock so they agree.
func (r *MemberRegistry) notifyChange() {
	r.mu.RLock()
	fn := r.onChange
	version := r.tagsVersion
	tags := r.computeTagsLocked()
	r.mu.RUnlock()
	if fn == nil {
		return
	}
	go func() {
		defer func() {
			if rv := recover(); rv != nil {
				slog.Error("onChange callback panicked", "pkg", "grpc/members", "panic", rv)
			}
		}()
		r.publishMu.Lock()
		defer r.publishMu.Unlock()
		if version <= r.publishedVersion {
			return // a newer snapshot has already been published
		}
		if err := fn(tags); err != nil {
			slog.Error("failed to publish member tags", "pkg", "grpc/members", "version", version, "err", err)
			return
		}
		r.publishedVersion = version
	}()
}

// computeTagsLocked builds an aggregate map of tenantID → deduplicated tags
// from all currently connected members. Caller holds r.mu (read or write).
func (r *MemberRegistry) computeTagsLocked() map[string][]string {
	// (body of the old computeTags without the RLock/RUnlock)
}
```

`app/app.go:554-560`:

```go
		a.memberRegistry.SetOnChange(func(tags map[string][]string) error {
			gossipReg, ok := a.nodeRegistry.(*registry.Gossip)
			if !ok {
				return nil
			}
			if err := gossipReg.UpdateTags(tags); err != nil {
				return fmt.Errorf("update gossip tags: %w", err)
			}
			return nil
		})
```

- [ ] **Step 4: Run**

Run: `go test ./internal/grpc/... ./app/ 2>&1 | tail -20` (app tests take a while; `-run 'Tag|Registry'` is fine for iteration)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/grpc/members.go internal/grpc/members_tags_test.go app/app.go
git commit -m "fix(grpc): tag publication is versioned — an older snapshot never overwrites a newer one

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task A5: Transport keepalive, config wiring, validation, help topics

**Files:**
- Modify: `internal/grpc/server.go` (`KeepAliveConfig`, `NewServer` param, keepalive options)
- Modify: `app/app.go:805` (`NewServer` call), `app/config.go` (`ValidateGRPCKeepAlive`, `Validate`)
- Modify: `internal/grpc/recovery_test.go:479-481` and any other `NewServer(` caller (`grep -rn 'NewServer(' internal app`)
- Modify: `cmd/cyoda/help/config_registry.go:76-77` (Topic `cluster` → `grpc`), `cmd/cyoda/help/content/config/grpc.md`, `config/cluster.md:30-31` (rows out), `cmd/cyoda/help/content/grpc.md:370-377`
- Modify: `docs/ARCHITECTURE.md:1557-1559` (row wording)
- Test: `internal/grpc/server_keepalive_test.go` (new), `app/config_validate_test.go` (append), `app/app_grpc_keepalive_wiring_test.go` (new)

**Interfaces:**
- Produces:
  ```go
  type KeepAliveConfig struct{ Interval, Timeout time.Duration }   // internal/grpc
  func NewServer(..., healthFlag *atomic.Bool, keepAlive KeepAliveConfig) *Server
  func ValidateGRPCKeepAlive(c GRPCConfig) error                    // app
  ```

- [ ] **Step 1: Write the failing tests**

`internal/grpc/server_keepalive_test.go`:

```go
package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	cyodapb "github.com/cyoda-platform/cyoda-go/api/grpc/cyoda"
)

// The keep-alive config reaches the service: with a 20ms/60ms configuration
// a silent member is evicted in well under a second (it would take 30s at
// the old hard-wired defaults).
func TestNewServer_KeepAliveConfigReachesService(t *testing.T) {
	srv := NewServer(&fixedAuthService{uc: m2mUser()}, NewMemberRegistry(), nil, nil, nil, nil, nil, nil,
		"n", false, 0, true, nil, KeepAliveConfig{Interval: 20 * time.Millisecond, Timeout: 60 * time.Millisecond})
	if srv.service.keepAliveInterval != 20*time.Millisecond || srv.service.keepAliveTimeout != 60*time.Millisecond {
		t.Fatalf("service keep-alive = %v/%v, want 20ms/60ms", srv.service.keepAliveInterval, srv.service.keepAliveTimeout)
	}
}

// A client pinging every 5s must not be GOAWAY'd (grpc-go's default policy
// would cut it off for pinging more often than every 5 minutes).
func TestNewServer_TolerantOfFivesecondClientPings(t *testing.T) {
	srv := NewServer(&fixedAuthService{uc: m2mUser()}, NewMemberRegistry(), nil, nil, nil, nil, nil, nil,
		"n", false, 0, true, nil, KeepAliveConfig{Interval: time.Second, Timeout: 5 * time.Second})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := googlegrpc.NewClient(lis.Addr().String(),
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
		googlegrpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 5 * time.Second, Timeout: time.Second, PermitWithoutStream: true}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := cyodapb.NewCloudEventsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	stream, err := client.StartStreaming(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the stream open across two client ping intervals; a GOAWAY would
	// surface as a Recv error before the deadline.
	recvErr := make(chan error, 1)
	go func() { _, err := stream.Recv(); recvErr <- err }()
	select {
	case err := <-recvErr:
		t.Fatalf("stream ended early: %v (enforcement policy too strict?)", err)
	case <-time.After(11 * time.Second):
	}
}

func m2mUser() *spi.UserContext {
	return &spi.UserContext{UserID: "m", UserName: "m", Tenant: spi.Tenant{ID: "t", Name: "t"}, Roles: []string{"ROLE_M2M"}}
}
```

(The stream stays open with no join because `StartStreaming` blocks on `Recv` for the first message; that is enough to observe a transport-level GOAWAY. Use the `grpc-go` ping-strike behaviour: with `MinTime: 5s` and a client `Time: 5s`, grpc-go's jitter can put a ping just under 5s; to be robust set the client `Time` to 6s and keep the 11s wait.)

`app/config_validate_test.go` (append):

```go
func TestValidateGRPCKeepAlive_RejectsNonPositive(t *testing.T) {
	for _, c := range []app.GRPCConfig{{KeepAliveInterval: 0, KeepAliveTimeout: 30}, {KeepAliveInterval: 10, KeepAliveTimeout: -1}} {
		if err := app.ValidateGRPCKeepAlive(c); err == nil {
			t.Errorf("ValidateGRPCKeepAlive(%+v) = nil, want error", c)
		}
	}
	if err := app.ValidateGRPCKeepAlive(app.GRPCConfig{KeepAliveInterval: 10, KeepAliveTimeout: 30}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}
```

`app/app_grpc_keepalive_wiring_test.go` (white-box, package `app`; `a.GRPCServer()` at `app/app.go:902` returns `*internalgrpc.Server`):

```go
package app

import (
	"testing"
	"time"

	internalgrpc "github.com/cyoda-platform/cyoda-go/internal/grpc"
)

// CYODA_KEEPALIVE_INTERVAL / _TIMEOUT reach the gRPC server. They used to be
// parsed and dropped on the floor.
func TestNew_KeepAliveConfigReachesGRPCServer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ContextPath = ""
	cfg.GRPC.KeepAliveInterval = 7
	cfg.GRPC.KeepAliveTimeout = 21
	a := New(cfg)
	t.Cleanup(func() { a.Shutdown(); _ = a.Close() })

	want := internalgrpc.KeepAliveConfig{Interval: 7 * time.Second, Timeout: 21 * time.Second}
	if got := a.GRPCServer().KeepAlive(); got != want {
		t.Fatalf("gRPC server keep-alive = %+v, want %+v", got, want)
	}
}
```

This needs `func (s *Server) KeepAlive() KeepAliveConfig` on `internal/grpc.Server` (added in step 3).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/grpc/ -run 'TestNewServer_' 2>&1 | tail; go test ./app/ -run 'TestValidateGRPCKeepAlive|KeepAliveReaches' 2>&1 | tail`
Expected: compile errors (`KeepAliveConfig`, `ValidateGRPCKeepAlive` undefined).

- [ ] **Step 3: Implement**

`internal/grpc/server.go`:

```go
// KeepAliveConfig governs both keep-alive layers on the member stream: the
// application-level CloudEvent ping and eviction (Interval between pings,
// Timeout of inbound silence or write stall before eviction) and grpc-go's
// transport keepalive (an HTTP/2 PING after Interval of idleness, the
// connection closed if unacknowledged within Timeout), which catches a peer
// whose TCP is alive but whose process is gone.
type KeepAliveConfig struct {
	Interval time.Duration
	Timeout  time.Duration
}
```

In `NewServer`, add the parameter `keepAlive KeepAliveConfig` after `healthFlag`, and before `grpcServer := googlegrpc.NewServer(opts...)`:

```go
	// Transport keepalive. MaxConnectionIdle/Age stay infinite: an age
	// would GOAWAY healthy compute nodes on a timer and fail their in-flight
	// dispatches. The enforcement policy is deliberately permissive —
	// grpc-go's default (MinTime 5m) would GOAWAY an external compute node
	// that pings more often than every five minutes.
	opts = append(opts,
		googlegrpc.KeepaliveParams(keepalive.ServerParameters{Time: keepAlive.Interval, Timeout: keepAlive.Timeout}),
		googlegrpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: true}),
	)
```

Set `keepAliveInterval: keepAlive.Interval, keepAliveTimeout: keepAlive.Timeout, healthFlag: healthFlag` on the service literal. Add `func (s *Server) KeepAlive() KeepAliveConfig`. Import `google.golang.org/grpc/keepalive`.

`app/config.go`:

```go
// ValidateGRPCKeepAlive rejects a keep-alive interval or timeout that is not
// positive. Both drive tickers and transport deadlines; zero or negative would
// panic the ticker or disable eviction, neither of which is a configuration
// anyone means. Config is a QA'd artefact: an invalid value is a startup error.
func ValidateGRPCKeepAlive(c GRPCConfig) error {
	if c.KeepAliveInterval <= 0 {
		return fmt.Errorf("CYODA_KEEPALIVE_INTERVAL must be >= 1 (seconds), got %d", c.KeepAliveInterval)
	}
	if c.KeepAliveTimeout <= 0 {
		return fmt.Errorf("CYODA_KEEPALIVE_TIMEOUT must be >= 1 (seconds), got %d", c.KeepAliveTimeout)
	}
	return nil
}
```

Add it to `Config.Validate()` before the search validators. `app/app.go:805`: append `, internalgrpc.KeepAliveConfig{Interval: time.Duration(cfg.GRPC.KeepAliveInterval) * time.Second, Timeout: time.Duration(cfg.GRPC.KeepAliveTimeout) * time.Second}`. Update `recovery_test.go:479` (`KeepAliveConfig{Interval: 10 * time.Second, Timeout: 30 * time.Second}`) and any other caller.

Docs:
- `config_registry.go:76-77`: `Topic: "grpc"`, descriptions: "Seconds between server keep-alive pings to each compute member; also the transport keepalive idle time." / "Seconds of inbound silence or write stall before a compute member is evicted; also the transport keepalive ack timeout."
- Move the two bullet rows from `config/cluster.md` to `config/grpc.md`.
- `content/grpc.md` KEEPALIVE section: rewrite to describe (a) the server ping every interval, (b) eviction on `timeout` of inbound silence **or** on one write stalled for `timeout`, (c) processor/criteria/function responses and acks counting as liveness, (d) the transport keepalive with the same two values and the 5s enforcement floor, (e) that a compute node must serialise its own stream writes. Delete the sentence claiming the variables are applied "at gRPC server construction time" and replace with "Both variables are applied to the gRPC server at construction; a value that is not a positive integer is a startup error."
- `docs/ARCHITECTURE.md:1557-1559`: same wording as the registry descriptions.

- [ ] **Step 4: Run**

Run: `go test ./internal/grpc/... ./app/... ./cmd/cyoda/help/... 2>&1 | tail -20`
Expected: PASS including `TestRootConfigVars_MatchDefaults`, `TestConfigAll_Complete`, `TestConfig_EnvVarCoverage`.

- [ ] **Step 5: Commit**

```bash
git add internal/grpc app cmd/cyoda/help docs/ARCHITECTURE.md
git commit -m "fix(grpc): CYODA_KEEPALIVE_* actually reach the server, and drive transport keepalive too

The two variables were parsed and dropped; the help topic said otherwise.
grpc-go keepalive is configured from the same values, with a permissive
enforcement floor so external compute nodes are never GOAWAY'd for pinging.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task A6: Real-network tests — frozen member, black-holed connection

**Files:**
- Test: `internal/grpc/frozen_member_test.go` (new)
- Reuses: `startRecoveryTestServer` (`recovery_test.go:449`) — extend it to accept a `KeepAliveConfig` (add a `startRecoveryTestServerWithKeepAlive(t, healthFlag, ka)` variant and have the old one call it with 10s/30s).

**Interfaces:** consumes A1–A5. Produces nothing.

- [ ] **Step 1: Write the tests** (these are the acceptance tests; they fail on the pre-A1 code, which is the point — run them once against `git stash`-free `release/v0.8.4` only if you want to prove it; otherwise proceed)

```go
package grpc

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
	cyodapb "github.com/cyoda-platform/cyoda-go/api/grpc/cyoda"
)

// A member that joins, then stops reading while the server keeps writing,
// fills the HTTP/2 write window (pinned to 64 KiB on the client) and is
// evicted within the keep-alive timeout; the dispatcher waiting on it is
// released by its own deadline and no goroutine is left wedged.
func TestFrozenMember_IsEvictedAndDispatchersAreReleased(t *testing.T) {
	ka := KeepAliveConfig{Interval: 100 * time.Millisecond, Timeout: 500 * time.Millisecond}
	ts := startRecoveryTestServerWithKeepAlive(t, nil, ka)

	conn, err := googlegrpc.NewClient(ts.addr,
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
		googlegrpc.WithInitialWindowSize(65535),
		googlegrpc.WithInitialConnWindowSize(65535))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := cyodapb.NewCloudEventsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := client.StartStreaming(ctx)
	if err != nil {
		t.Fatal(err)
	}
	join, _ := NewCloudEvent(CalculationMemberJoinEvent, map[string]any{"id": "j", "tags": []string{"frozen"}, "joinedLegalEntityId": string(ts.uc.Tenant.ID)})
	if err := stream.Send(join); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil { // the greet
		t.Fatalf("greet: %v", err)
	}
	// From here the client never calls Recv again: frozen.

	var member *Member
	for i := 0; i < 200 && member == nil; i++ {
		if ms := ts.registry.List(); len(ms) == 1 {
			member = ms[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	if member == nil {
		t.Fatal("member not registered")
	}

	// Push >128 KiB through Send so the writer wedges in the write window.
	big := make([]byte, 256*1024)
	for i := range big {
		big[i] = 'x'
	}
	payload := map[string]any{"requestId": "big", "payload": string(big)}
	ce, _ := NewCloudEvent(EntityProcessorCalculationRequest, payload)
	var released atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer scancel()
			_ = member.Send(sctx, ce)
			released.Add(1)
		}()
	}

	select {
	case <-member.Evicted():
	case <-time.After(5 * time.Second):
		t.Fatal("frozen member was not evicted within the keep-alive timeout")
	}
	wg.Wait()
	if released.Load() != 5 {
		t.Fatalf("%d of 5 senders released", released.Load())
	}
	select {
	case <-member.WriterDone():
	case <-time.After(5 * time.Second):
		t.Fatal("writer still wedged after eviction: handler return did not unblock the raw send")
	}
}

// A connection whose bytes stop flowing in both directions (a pausable TCP
// proxy) is torn down by the transport keepalive within Time+Timeout and the
// member is unregistered, even though nothing at the application level ever
// errors.
func TestBlackholedConnection_IsTornDownByTransportKeepalive(t *testing.T) {
	ka := KeepAliveConfig{Interval: 1 * time.Second, Timeout: 1 * time.Second}
	ts := startRecoveryTestServerWithKeepAlive(t, nil, ka)
	proxy := newPausableProxy(t, ts.addr)

	conn, err := googlegrpc.NewClient(proxy.addr, googlegrpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := cyodapb.NewCloudEventsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := client.StartStreaming(ctx)
	if err != nil {
		t.Fatal(err)
	}
	join, _ := NewCloudEvent(CalculationMemberJoinEvent, map[string]any{"id": "j", "tags": []string{"bh"}, "joinedLegalEntityId": string(ts.uc.Tenant.ID)})
	_ = stream.Send(join)
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("greet: %v", err)
	}
	proxy.pause() // both directions stop; TCP stays open.

	deadline := time.Now().Add(10 * time.Second) // Time+Timeout is 2s; allow slack
	for len(ts.registry.List()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("black-holed member still registered")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The application keep-alive fires on the same clock, so the member being
	// gone does not by itself prove the transport noticed. GracefulStop waits
	// for every connection to close: with transport keepalive the dead one is
	// torn down and Stop returns; without it, Stop would wait on a connection
	// that never drains (grpc-go's default is a two-hour keepalive).
	stopped := make(chan struct{})
	go func() { ts.srv.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("GracefulStop hung: the black-holed connection was never closed by transport keepalive")
	}
}

// pausableProxy forwards bytes between a client and target until pause() is
// called, after which it silently drops everything while keeping both TCP
// connections open.
type pausableProxy struct {
	addr   string
	paused atomic.Bool
}

func newPausableProxy(t *testing.T, target string) *pausableProxy {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &pausableProxy{addr: lis.Addr().String()}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			pump := func(dst, src net.Conn) {
				buf := make([]byte, 32*1024)
				for {
					n, err := src.Read(buf)
					if n > 0 && !p.paused.Load() {
						if _, werr := dst.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}
			go pump(up, c)
			go pump(c, up)
		}
	}()
	return p
}

func (p *pausableProxy) pause() { p.paused.Store(true) }
```

Extend `recoveryTestServer` with `addr string`, `registry *MemberRegistry` and `srv *Server`, and add `startRecoveryTestServerWithKeepAlive(t, healthFlag, ka KeepAliveConfig)`; the existing `startRecoveryTestServer` calls it with `KeepAliveConfig{Interval: 10 * time.Second, Timeout: 30 * time.Second}`. The `t.Cleanup(srv.GracefulStop)` already registered is idempotent, so the second test's explicit stop is safe. Drop the `io` import if unused.

- [ ] **Step 2: Run**

Run: `go test ./internal/grpc/ -run 'TestFrozenMember|TestBlackholed' -v 2>&1 | tail -20`
Expected: PASS within ~15s. If the first test does not wedge the writer (eviction never fires and senders return nil immediately), the payload is under the window: raise it to 1 MiB.

- [ ] **Step 3: Race**

Run: `go test -race ./internal/grpc/ -run 'TestFrozenMember|TestBlackholed' 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/grpc/frozen_member_test.go internal/grpc/recovery_test.go
git commit -m "test(grpc): a frozen member is evicted and its dispatchers released; a black-holed connection is torn down

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task A7: Exit check — one raw send on the member stream

**Files:**
- Test: `internal/grpc/single_writer_exit_check_test.go` (new)

- [ ] **Step 1: Write the test**

```go
package grpc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Exactly one call to the member stream's Send exists in this package: the
// closure StartStreaming hands to Register, which only the member's writer
// invokes. Any other raw write on the member stream would race the writer and
// corrupt HTTP/2 framing. Scoped to the member stream: entity.go and search.go
// write their own per-request server streams from a single goroutine.
func TestSingleWriter_OnlyOneRawSendOnTheMemberStream(t *testing.T) {
	fset := token.NewFileSet()
	total := 0
	for _, file := range []string{"streaming.go", "members.go", "dispatch.go"} {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Send" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "stream" {
				total++
				t.Logf("raw stream.Send at %s", fset.Position(call.Pos()))
			}
			return true
		})
	}
	if total != 1 {
		t.Fatalf("found %d raw stream.Send calls on the member stream; exactly one (the writer's closure) is allowed", total)
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/grpc/ -run TestSingleWriter -v` → PASS with one logged position inside `StartStreaming`.

- [ ] **Step 3: Commit**

```bash
git add internal/grpc/single_writer_exit_check_test.go
git commit -m "test(grpc): pin the single-writer invariant on the member stream

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task A8: Reference compute client serialises its stream writes

**Files:**
- Modify: `cmd/compute-test-client/dispatch.go` (`dispatcher` gets `sendMu sync.Mutex`; a `send(stream, ce)` helper; `run` and `keepAliveLoop` use it)
- Test: `cmd/compute-test-client/dispatch_send_test.go` (new)

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
)

type overlapStream struct {
	grpc.BidiStreamingClient[cepb.CloudEvent, cepb.CloudEvent]
	inFlight atomic.Int32
	overlaps atomic.Int32
}

func (s *overlapStream) Send(*cepb.CloudEvent) error {
	if s.inFlight.Add(1) > 1 {
		s.overlaps.Add(1)
	}
	defer s.inFlight.Add(-1)
	for i := 0; i < 1000; i++ { // widen the window
	}
	return nil
}
func (s *overlapStream) Context() context.Context { return context.Background() }
func (s *overlapStream) Header() (metadata.MD, error) { return nil, nil }

func TestDispatcher_SendIsSerialised(t *testing.T) {
	d := &dispatcher{}
	s := &overlapStream{}
	ce, _ := newCloudEvent(ceTypeKeepAlive, map[string]any{"success": true})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = d.send(s, ce)
			}
		}()
	}
	wg.Wait()
	if n := s.overlaps.Load(); n != 0 {
		t.Fatalf("%d overlapping sends on one stream", n)
	}
}
```

- [ ] **Step 2: Run** — `go test ./cmd/compute-test-client/ -run TestDispatcher_SendIsSerialised` → compile error (`d.send` undefined).

- [ ] **Step 3: Implement** — add to `dispatcher`:

```go
	// sendMu serialises every write to the stream: the request loop and the
	// keep-alive ticker both send, and grpc-go forbids concurrent SendMsg on
	// one stream. Compute-node implementations must do the same.
	sendMu sync.Mutex

// send is the only place this client writes to its stream.
func (d *dispatcher) send(stream grpc.BidiStreamingClient[cepb.CloudEvent, cepb.CloudEvent], ce *cepb.CloudEvent) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	return stream.Send(ce)
}
```

Replace the four `stream.Send(...)` calls in `run` and `keepAliveLoop` with `d.send(stream, ...)`. (The join send in the connect path runs before the goroutines exist; route it through `d.send` too for uniformity.)

- [ ] **Step 4: Run** — `go test -race ./cmd/compute-test-client/...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/compute-test-client/
git commit -m "fix(compute-test-client): one writer per stream in the reference client too

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task A9: Stream A verification

- [ ] **Step 1:** `make test` → green. If a parity suite or an unrelated package fails, check the failure at the merge-base before treating it as yours.
- [ ] **Step 2:** `go vet ./internal/grpc/... ./app/... ./cmd/...` → clean.
- [ ] **Step 3:** Grep exit checks:
  - `grep -rn "sendMu" internal/grpc/` → no hits.
  - `grep -rn "SetKeepAliveConfig\|DefaultKeepAliveInterval\|registry.Get(memberID)" internal/grpc/` → no hits outside tests of `Get` itself.
- [ ] **Step 4:** No commit needed unless something was fixed.

---

## Stream B — HTTP door

### Task B1: `Recovery` re-raises `http.ErrAbortHandler`; proxy hang-up does not latch

**Files:**
- Modify: `internal/api/middleware/recovery.go:27-55`
- Test: `internal/api/middleware/recovery_test.go` (append), `internal/cluster/proxy/http_test.go` (append)

- [ ] **Step 1: Write the failing tests**

`recovery_test.go`:

```go
// net/http uses http.ErrAbortHandler as a sentinel: a handler (notably
// httputil.ReverseProxy when the client hangs up mid-body) panics with it to
// abort the response silently. Recovery must re-raise it, not treat it as a
// defect: no log, no 500, no health latch.
func TestRecoveryMiddlewareReRaisesErrAbortHandler(t *testing.T) {
	healthFlag := &atomic.Bool{}
	healthFlag.Store(true)
	handler := middleware.Recovery(healthFlag)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	srv := httptest.NewServer(handler) // a real server: net/http swallows the sentinel
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/x")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected the connection to be aborted, got status %d", resp.StatusCode)
	}
	if !healthFlag.Load() {
		t.Fatal("ErrAbortHandler must not latch the health flag")
	}
}
```

`internal/cluster/proxy/http_test.go` (append; reuse `newFakeRegistry`, `mustNewSigner`, and the token-minting helper the existing `TestHTTPProxy_TokenForOtherNode_Proxies` uses):

```go
// An upstream that hangs up mid-body makes ReverseProxy panic with
// http.ErrAbortHandler. Under Recovery that must stay a silent abort: the
// node's health flag is untouched.
func TestHTTPProxy_UpstreamHangupMidBody_DoesNotLatchHealth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // closes the upstream connection short
	}))
	defer upstream.Close()

	signer := mustNewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	reg := newFakeRegistry(
		contract.NodeInfo{NodeID: "node-1", Addr: "http://localhost:9999", Alive: true},
		contract.NodeInfo{NodeID: "node-2", Addr: upstream.URL, Alive: true},
	)
	tok, err := signer.Issue("node-2", "tx-hangup", time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	healthFlag := &atomic.Bool{}
	healthFlag.Store(true)
	h := middleware.Recovery(healthFlag)(proxy.HTTPRouting(signer, reg, "node-1", 5*time.Second, true)(localHandler()))
	srv := httptest.NewServer(h) // a real server, so the re-raised sentinel reaches net/http
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/test", nil)
	req.Header.Set(proxy.TxTokenHeader, tok)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if !healthFlag.Load() {
		t.Fatal("a proxied client hang-up latched the node unhealthy")
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/api/middleware/ -run ReRaises; go test ./internal/cluster/proxy/ -run Hangup` → the middleware test FAILS (a 500 is returned, flag false).

- [ ] **Step 3: Implement** — at the top of the deferred `recover` in `recovery.go`:

```go
			if rec := recover(); rec != nil {
				// net/http's own abort sentinel: a handler (ReverseProxy on a
				// client hang-up mid-body, for one) panics with it to end the
				// response silently. Re-raise so the server handles it as
				// designed; it is not a defect and must not latch the node.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
```

- [ ] **Step 4: Run** both tests → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/middleware/ internal/cluster/proxy/
git commit -m "fix(api): Recovery re-raises http.ErrAbortHandler — a client hang-up is not a panic

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task B2: Recovery outermost; admin server recovery

**Files:**
- Modify: `app/app.go:782-802` (chain order; `HealthFlag()` accessor)
- Create: `cmd/cyoda/adminserver.go` (`newAdminHandler`)
- Modify: `cmd/cyoda/run.go:103-111`
- Test: `cmd/cyoda/adminserver_test.go` (new)

**Interfaces:**
- Produces: `func (a *App) HealthFlag() *atomic.Bool`; `func newAdminHandler(readiness func() error, metricsBearer string, healthFlag *atomic.Bool) http.Handler`.

- [ ] **Step 1: Write the failing test**

`cmd/cyoda/adminserver_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A panic on the admin surface is contained exactly like one on the API
// surface: 500 with a ticket, and the node latches unhealthy.
func TestAdminHandler_PanicIsContainedAndLatches(t *testing.T) {
	flag := &atomic.Bool{}
	flag.Store(true)
	h := newAdminHandler(func() error { panic("probe exploded") }, "", flag)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if flag.Load() {
		t.Fatal("admin panic must latch the health flag")
	}
	// /livez keeps answering (the node is drained, not restarted).
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/livez = %d after a recovered panic, want 200", w.Code)
	}
}
```

- [ ] **Step 2: Run** — `go test ./cmd/cyoda/ -run TestAdminHandler` → compile error.

- [ ] **Step 3: Implement**

`cmd/cyoda/adminserver.go`:

```go
package main

import (
	"net/http"
	"sync/atomic"

	"github.com/cyoda-platform/cyoda-go/internal/admin"
	"github.com/cyoda-platform/cyoda-go/internal/api/middleware"
	"github.com/cyoda-platform/cyoda-go/internal/observability"
)

// newAdminHandler assembles the admin surface (/livez, /readyz, /metrics)
// under the same panic recovery the API surface has. One policy per door: a
// panic in a probe or a metrics scrape is contained by ticket and latches the
// node unhealthy like any other. /livez still answers, so the node is
// drained, not restarted.
func newAdminHandler(readiness func() error, metricsBearer string, healthFlag *atomic.Bool) http.Handler {
	h := admin.NewHandler(admin.Options{
		Readiness:          readiness,
		MetricsBearerToken: metricsBearer,
		MetricsHandler:     observability.MetricsHandler(),
	})
	return middleware.Recovery(healthFlag)(h)
}
```

`run.go`: `Handler: newAdminHandler(a.ReadinessCheck, cfg.Admin.MetricsBearerToken, a.HealthFlag())`. Drop the now-unused `admin` and `observability` imports from `run.go`.

`app/app.go`: add

```go
// HealthFlag is the process-wide flag every panic-recovery site latches and
// /readyz reads. Exposed so the admin server, built in cmd/cyoda, can wrap
// its own handler in the same Recovery.
func (a *App) HealthFlag() *atomic.Bool { return a.healthFlag }
```

and reorder the chain at `app.go:782-802` to: proxy (cluster only) → CORS → Recovery, replacing the three comments:

```go
	// Cluster routing sits directly over the mux: a request carrying a
	// transaction token for another node is forwarded before auth runs here
	// (auth is applied on the owning node).
	if cfg.Cluster.Enabled {
		a.handler = proxy.HTTPRouting(a.tokenSigner, a.nodeRegistry, cfg.Cluster.NodeID, cfg.Cluster.ProxyTimeout, cfg.Cluster.DispatchAllowLoopback)(a.handler)
	}

	// CORS sits outside cluster routing so preflights short-circuit at the
	// receiving node and never get proxied, and outside outerMux so /help,
	// discovery, and the API surface share one policy. See
	// docs/superpowers/specs/2026-05-01-issue-196-cors-design.md.
	corsPolicy := middleware.NewCORSPolicy(cfg.CORS.Enabled, cfg.CORS.Wildcard, cfg.CORS.AllowedOrigins)
	a.handler = middleware.CORS(corsPolicy)(a.handler)

	// Recovery is the outermost layer, so nothing — CORS, cluster routing,
	// or any route added later — sits outside panic containment. CORS writes
	// its headers before calling the next handler, so a recovered 500 keeps
	// them. Recovery re-raises http.ErrAbortHandler, which is how the reverse
	// proxy reports a client hang-up, so proxied disconnects stay silent.
	a.handler = middleware.Recovery(a.healthFlag)(a.handler)
```

- [ ] **Step 4: Run** — `go test ./cmd/cyoda/ ./app/ ./internal/api/... 2>&1 | tail -20` → PASS (`app_readiness_test.go` drives `/health` through `Handler()` and must still see the latch).

- [ ] **Step 5: Commit**

```bash
git add app/app.go cmd/cyoda/adminserver.go cmd/cyoda/adminserver_test.go cmd/cyoda/run.go
git commit -m "fix(api): panic recovery is the outermost layer on both HTTP servers

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task B3: HTTP timeouts — config, server construction, tests, help

**Files:**
- Modify: `app/config.go` (`HTTPConfig`, `Config.HTTP`, `DefaultConfig`, `ValidateHTTP`, `Validate`)
- Create: `cmd/cyoda/httpserver.go`
- Modify: `cmd/cyoda/run.go:83-84,103-111` (use `newHTTPServer`)
- Modify: `cmd/cyoda/help/config_registry.go:32-42` (four rows), `cmd/cyoda/help/content/config.md` (server list), `app/config_registry_binding_test.go` (four rows), `docs/ARCHITECTURE.md:1446` area (four rows)
- Test: `cmd/cyoda/httpserver_test.go` (new), `app/config_test.go` (append), `app/config_validate_test.go` (append)

**Interfaces:**
- Produces:
  ```go
  type HTTPConfig struct{ ReadHeaderTimeout, ReadTimeout, WriteTimeout, IdleTimeout time.Duration } // app
  func ValidateHTTP(c HTTPConfig) error
  func newHTTPServer(addr string, handler http.Handler, t app.HTTPConfig) *http.Server              // cmd/cyoda
  ```

- [ ] **Step 1: Write the failing tests**

`app/config_test.go` (append; follow the file's existing `t.Setenv` style):

```go
func TestDefaultConfig_HTTPTimeouts(t *testing.T) {
	cfg := app.DefaultConfig()
	want := app.HTTPConfig{ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 0, IdleTimeout: 120 * time.Second}
	if cfg.HTTP != want {
		t.Fatalf("HTTP defaults = %+v, want %+v", cfg.HTTP, want)
	}
	t.Setenv("CYODA_HTTP_READ_HEADER_TIMEOUT", "3s")
	t.Setenv("CYODA_HTTP_READ_TIMEOUT", "1m")
	t.Setenv("CYODA_HTTP_WRITE_TIMEOUT", "45s")
	t.Setenv("CYODA_HTTP_IDLE_TIMEOUT", "30s")
	cfg = app.DefaultConfig()
	want = app.HTTPConfig{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: time.Minute, WriteTimeout: 45 * time.Second, IdleTimeout: 30 * time.Second}
	if cfg.HTTP != want {
		t.Fatalf("HTTP from env = %+v, want %+v", cfg.HTTP, want)
	}
}
```

`app/config_validate_test.go` (append):

```go
func TestValidateHTTP_RejectsNegative(t *testing.T) {
	if err := app.ValidateHTTP(app.HTTPConfig{ReadTimeout: -1}); err == nil {
		t.Fatal("negative timeout accepted")
	}
	if err := app.ValidateHTTP(app.HTTPConfig{}); err != nil {
		t.Fatalf("all-zero (disabled) rejected: %v", err)
	}
}
```

`cmd/cyoda/httpserver_test.go`:

```go
package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/app"
)

func serve(t *testing.T, h http.Handler, cfg app.HTTPConfig) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newHTTPServer(lis.Addr().String(), h, cfg)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return lis.Addr().String()
}

func TestNewHTTPServer_CarriesTheFourTimeouts(t *testing.T) {
	cfg := app.HTTPConfig{ReadHeaderTimeout: 1 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second}
	srv := newHTTPServer(":0", http.NotFoundHandler(), cfg)
	if srv.ReadHeaderTimeout != cfg.ReadHeaderTimeout || srv.ReadTimeout != cfg.ReadTimeout ||
		srv.WriteTimeout != cfg.WriteTimeout || srv.IdleTimeout != cfg.IdleTimeout {
		t.Fatalf("server timeouts %v/%v/%v/%v do not match config", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
}

// A client that never finishes its headers is cut off with no response.
func TestNewHTTPServer_SlowHeaderClientIsCutOff(t *testing.T) {
	addr := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
		app.HTTPConfig{ReadHeaderTimeout: 100 * time.Millisecond, ReadTimeout: time.Second, IdleTimeout: time.Second})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n")) // no terminating blank line
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c.Read(make([]byte, 64))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read n=%d err=%v; want 0 bytes and EOF (connection closed, no reply)", n, err)
	}
}

// A client that never finishes its body makes the handler's read fail with a
// timeout, and the request context is cancelled at that instant.
func TestNewHTTPServer_SlowBodyClientIsCutOff(t *testing.T) {
	sawErr := make(chan error, 1)
	addr := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		sawErr <- err
		w.WriteHeader(http.StatusBadRequest)
	}), app.HTTPConfig{ReadHeaderTimeout: time.Second, ReadTimeout: 150 * time.Millisecond, IdleTimeout: time.Second})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\npartial"))
	select {
	case err := <-sawErr:
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("handler read err = %v, want a timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never saw the body read fail")
	}
}

// Once the body has been read, ReadTimeout does not cancel a handler that
// runs longer than it: the server imposes no time budget on work.
func TestNewHTTPServer_ReadTimeoutDoesNotCancelARunningHandler(t *testing.T) {
	addr := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		time.Sleep(300 * time.Millisecond) // 3x ReadTimeout
		if r.Context().Err() != nil {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(http.StatusOK)
	}), app.HTTPConfig{ReadHeaderTimeout: time.Second, ReadTimeout: 100 * time.Millisecond, IdleTimeout: time.Second})
	resp, err := http.Post("http://"+addr+"/", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; a drained request must not be cancelled by ReadTimeout", resp.StatusCode)
	}
}

// An idle keep-alive connection is closed after IdleTimeout.
func TestNewHTTPServer_IdleConnectionIsClosed(t *testing.T) {
	addr := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
		app.HTTPConfig{ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, IdleTimeout: 100 * time.Millisecond})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	if _, err := http.ReadResponse(bufio.NewReader(c), nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := c.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("idle connection still open: n=%d err=%v", n, err)
	}
}
```

- [ ] **Step 2: Run** — `go test ./app/ -run 'HTTPTimeouts|ValidateHTTP'; go test ./cmd/cyoda/ -run TestNewHTTPServer` → compile errors.

- [ ] **Step 3: Implement**

`app/config.go`: add to `Config` (next to `HTTPPort`) `HTTP HTTPConfig`, the type:

```go
// HTTPConfig holds the receive-side timeouts applied to both the API server
// and the admin server. ReadHeaderTimeout, ReadTimeout and IdleTimeout bound
// how a request is received and how long an idle keep-alive connection is
// kept; none of them limits how long a handler runs (Go clears the read
// deadline once the body is drained). WriteTimeout does limit handler
// execution and ships disabled: the server imposes no time budget on work.
// Zero disables a timeout.
type HTTPConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}
```

In `DefaultConfig()`:

```go
		HTTP: HTTPConfig{
			ReadHeaderTimeout: envDuration("CYODA_HTTP_READ_HEADER_TIMEOUT", 10*time.Second),
			ReadTimeout:       envDuration("CYODA_HTTP_READ_TIMEOUT", 5*time.Minute),
			WriteTimeout:      envDuration("CYODA_HTTP_WRITE_TIMEOUT", 0),
			IdleTimeout:       envDuration("CYODA_HTTP_IDLE_TIMEOUT", 120*time.Second),
		},
```

```go
// ValidateHTTP rejects a negative timeout; zero means disabled.
func ValidateHTTP(c HTTPConfig) error {
	for name, d := range map[string]time.Duration{
		"CYODA_HTTP_READ_HEADER_TIMEOUT": c.ReadHeaderTimeout,
		"CYODA_HTTP_READ_TIMEOUT":        c.ReadTimeout,
		"CYODA_HTTP_WRITE_TIMEOUT":       c.WriteTimeout,
		"CYODA_HTTP_IDLE_TIMEOUT":        c.IdleTimeout,
	} {
		if d < 0 {
			return fmt.Errorf("%s must not be negative, got %s", name, d)
		}
	}
	return nil
}
```

Add to `Validate()`. `cmd/cyoda/httpserver.go`:

```go
package main

import (
	"net/http"

	"github.com/cyoda-platform/cyoda-go/app"
)

// newHTTPServer is the one place an http.Server is built for this binary, so
// the API server and the admin server carry the same receive-side timeouts.
func newHTTPServer(addr string, handler http.Handler, t app.HTTPConfig) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: t.ReadHeaderTimeout,
		ReadTimeout:       t.ReadTimeout,
		WriteTimeout:      t.WriteTimeout,
		IdleTimeout:       t.IdleTimeout,
	}
}
```

`run.go`: `httpServer := newHTTPServer(httpAddr, a.Handler(), cfg.HTTP)` and `adminServer := newHTTPServer(adminAddr, newAdminHandler(...), cfg.HTTP)`.

Registry rows (`Topic: "server"`, `Type: "duration"`):
- `CYODA_HTTP_READ_HEADER_TIMEOUT` default `10s` — "Time allowed to receive a request's headers on the API and admin servers. 0 disables."
- `CYODA_HTTP_READ_TIMEOUT` default `5m` — "Time allowed to receive a whole request, body included. Does not limit handler execution. 0 disables."
- `CYODA_HTTP_WRITE_TIMEOUT` default `0s` — "Time from the end of the request headers to the end of the response. Limits handler execution, so it ships disabled; set only if you want the server to cut off long-running requests."
- `CYODA_HTTP_IDLE_TIMEOUT` default `120s` — "How long an idle keep-alive connection is held open between requests. 0 disables."

Add the four to `config_registry_binding_test.go`'s map with `renderDuration(c.HTTP.X)`. Add the same four bullets to `config.md`'s server list and rows to the ARCHITECTURE env table.

- [ ] **Step 4: Run** — `go test ./app/... ./cmd/... 2>&1 | tail -20` → PASS including the registry/coverage tests.

- [ ] **Step 5: Commit**

```bash
git add app/config.go app/config_test.go app/config_validate_test.go app/config_registry_binding_test.go cmd/cyoda/httpserver.go cmd/cyoda/httpserver_test.go cmd/cyoda/run.go cmd/cyoda/help docs/ARCHITECTURE.md
git commit -m "feat(api): receive-side timeouts on both HTTP servers, configurable, write timeout off by policy

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task B4: `/health` semantics documented; stale probe references corrected

**Files:**
- Modify: `internal/api/health.go` (doc comment), `cmd/cyoda/help/content/run.md:272,297-299`, `cli/serve.md:33-51`, `cli/health.md:25-52`, `docs/ARCHITECTURE.md:374-382,1310-1314`, `docs/PRD.md:686`, `docs/FEATURES.md:106`

- [ ] **Step 1:** Read each site. Write, in each, this content adapted to the surrounding style:

> `GET /health` mirrors the node's readiness flag: `200 {"status":"UP"}` while healthy, `503 {"status":"DOWN"}` after any recovered panic (API, gRPC, admin, or a background loop doing engine work). The flag latches: nothing re-arms it, because the node's state after a panic is unverified. Read the ticket in the log, then replace the node. Deployment probes are `/livez` (unconditional) and `/readyz` (same flag) on the admin listener; `/health` is for humans and simple scripts.

PRD and FEATURES: replace "readiness probe" wording for `/health` with "health summary; readiness is `/readyz` on the admin listener".

- [ ] **Step 2:** `go test ./cmd/cyoda/help/... 2>&1 | tail -5` → PASS (help content tests parse the markdown tree).

- [ ] **Step 3: Commit**

```bash
git add internal/api/health.go cmd/cyoda/help/content docs/ARCHITECTURE.md docs/PRD.md docs/FEATURES.md
git commit -m "docs(api): /health keeps its latch — say so, and stop calling it the readiness probe

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

## Stream C — pool saturation metrics

### Task C1: Postgres plugin registers pool instruments

**Files:**
- Create: `plugins/postgres/metrics.go`
- Modify: `plugins/postgres/plugin.go:32-55` (register after `ensureSchema`), `plugins/postgres/store_factory.go` (`unregisterMetrics func()` field; `Close`)
- Modify: `plugins/postgres/go.mod` (`go get go.opentelemetry.io/otel@v1.44.0 go.opentelemetry.io/otel/metric@v1.44.0`; test dep `go.opentelemetry.io/otel/sdk/metric@v1.44.0`), then `go mod tidy` in the plugin
- Test: `plugins/postgres/metrics_test.go` (new; uses `newTestPool(t)` from `migrate_test.go:13`)

**Interfaces:**
- Produces: `func registerPoolMetrics(meter metric.Meter, pool *pgxpool.Pool) (unregister func(), err error)`; `const meterName = "github.com/cyoda-platform/cyoda-go/plugins/postgres"`.

- [ ] **Step 1: Write the failing test**

```go
package postgres

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The callback reports the pool's current state under backend="postgres",
// and unregistering stops it.
func TestRegisterPoolMetrics_ReportsPoolStat(t *testing.T) {
	pool := newTestPool(t)
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	unregister, err := registerPoolMetrics(mp.Meter(meterName), pool)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(context.Background()) // one acquired connection
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			found[m.Name] = true
			if m.Name == "cyoda.storage.pool.connections" {
				g := m.Data.(metricdata.Gauge[int64])
				var acquired int64 = -1
				for _, dp := range g.DataPoints {
					state, _ := dp.Attributes.Value(attribute.Key("state"))
					backend, _ := dp.Attributes.Value(attribute.Key("backend"))
					if backend.AsString() != "postgres" {
						t.Fatalf("data point without backend=postgres: %v", dp.Attributes)
					}
					if state.AsString() == "acquired" {
						acquired = dp.Value
					}
				}
				if acquired < 1 {
					t.Fatalf("acquired connections = %d, want >= 1", acquired)
				}
			}
		}
	}
	for _, want := range []string{
		"cyoda.storage.pool.connections", "cyoda.storage.pool.max_connections",
		"cyoda.storage.pool.acquires", "cyoda.storage.pool.empty_acquires",
		"cyoda.storage.pool.canceled_acquires", "cyoda.storage.pool.acquire_duration",
		"cyoda.storage.pool.empty_acquire_wait",
	} {
		if !found[want] {
			t.Errorf("instrument %s not reported", want)
		}
	}

	unregister()
	rm = metricdata.ResourceMetrics{}
	_ = reader.Collect(context.Background(), &rm)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "cyoda.storage.pool.connections" && len(m.Data.(metricdata.Gauge[int64]).DataPoints) > 0 {
				t.Fatal("callback still reporting after unregister")
			}
		}
	}
}
```

- [ ] **Step 2: Run** — `cd plugins/postgres && go test ./ -run TestRegisterPoolMetrics` → compile error.

- [ ] **Step 3: Implement**

`plugins/postgres/metrics.go`:

```go
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meterName is the instrumentation scope for this plugin's instruments.
const meterName = "github.com/cyoda-platform/cyoda-go/plugins/postgres"

// registerPoolMetrics exports pgxpool.Stat on every scrape as observable
// instruments. Pool saturation is the dominant outage mode of this design;
// empty_acquire_wait (time callers spent waiting because the pool was
// empty) is the signal to alarm on. One pool per process is assumed: two
// factories would both observe backend="postgres" and the last observation
// per cycle would win. The returned func unregisters the callback and must
// run before the pool is closed.
func registerPoolMetrics(meter metric.Meter, pool *pgxpool.Pool) (func(), error) {
	connections, err := meter.Int64ObservableGauge("cyoda.storage.pool.connections",
		metric.WithDescription("Pool connections by state"))
	if err != nil {
		return nil, fmt.Errorf("instrument connections: %w", err)
	}
	maxConns, err := meter.Int64ObservableGauge("cyoda.storage.pool.max_connections",
		metric.WithDescription("Configured maximum pool size"))
	if err != nil {
		return nil, fmt.Errorf("instrument max_connections: %w", err)
	}
	acquires, err := meter.Int64ObservableCounter("cyoda.storage.pool.acquires",
		metric.WithDescription("Successful connection acquires"))
	if err != nil {
		return nil, fmt.Errorf("instrument acquires: %w", err)
	}
	emptyAcquires, err := meter.Int64ObservableCounter("cyoda.storage.pool.empty_acquires",
		metric.WithDescription("Acquires that found the pool empty and had to wait"))
	if err != nil {
		return nil, fmt.Errorf("instrument empty_acquires: %w", err)
	}
	canceled, err := meter.Int64ObservableCounter("cyoda.storage.pool.canceled_acquires",
		metric.WithDescription("Acquires cancelled by their context before a connection was available"))
	if err != nil {
		return nil, fmt.Errorf("instrument canceled_acquires: %w", err)
	}
	acquireDuration, err := meter.Float64ObservableCounter("cyoda.storage.pool.acquire_duration",
		metric.WithUnit("s"), metric.WithDescription("Cumulative time spent in acquire, all acquires"))
	if err != nil {
		return nil, fmt.Errorf("instrument acquire_duration: %w", err)
	}
	emptyWait, err := meter.Float64ObservableCounter("cyoda.storage.pool.empty_acquire_wait",
		metric.WithUnit("s"), metric.WithDescription("Cumulative time callers waited because the pool was empty"))
	if err != nil {
		return nil, fmt.Errorf("instrument empty_acquire_wait: %w", err)
	}

	backend := attribute.String("backend", "postgres")
	acquired := metric.WithAttributes(backend, attribute.String("state", "acquired"))
	idle := metric.WithAttributes(backend, attribute.String("state", "idle"))
	constructing := metric.WithAttributes(backend, attribute.String("state", "constructing"))
	plain := metric.WithAttributes(backend)

	reg, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		st := pool.Stat()
		o.ObserveInt64(connections, int64(st.AcquiredConns()), acquired)
		o.ObserveInt64(connections, int64(st.IdleConns()), idle)
		o.ObserveInt64(connections, int64(st.ConstructingConns()), constructing)
		o.ObserveInt64(maxConns, int64(st.MaxConns()), plain)
		o.ObserveInt64(acquires, st.AcquireCount(), plain)
		o.ObserveInt64(emptyAcquires, st.EmptyAcquireCount(), plain)
		o.ObserveInt64(canceled, st.CanceledAcquireCount(), plain)
		o.ObserveFloat64(acquireDuration, st.AcquireDuration().Seconds(), plain)
		o.ObserveFloat64(emptyWait, st.EmptyAcquireWaitTime().Seconds(), plain)
		return nil
	}, connections, maxConns, acquires, emptyAcquires, canceled, acquireDuration, emptyWait)
	if err != nil {
		return nil, fmt.Errorf("register pool metrics callback: %w", err)
	}
	return func() { _ = reg.Unregister() }, nil
}
```

`plugin.go` `NewFactory`, after `ensureSchema` succeeds:

```go
	factory := newStoreFactory(pool, cfg)
	factory.initTransactionManager(&defaultUUIDGenerator{})
	unregister, err := registerPoolMetrics(otel.Meter(meterName), pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: %w", err)
	}
	factory.unregisterMetrics = unregister
	return factory, nil
```

`store_factory.go`: field `unregisterMetrics func()`; `Close`:

```go
func (f *StoreFactory) Close() error {
	if f.unregisterMetrics != nil {
		f.unregisterMetrics()
	}
	f.pool.Close()
	return nil
}
```

Run `cd plugins/postgres && go get go.opentelemetry.io/otel@v1.44.0 go.opentelemetry.io/otel/metric@v1.44.0 go.opentelemetry.io/otel/sdk/metric@v1.44.0 && go mod tidy`. Confirm the root `go.mod`/`go.sum` did not change (the plugin is its own module; `go.work` resolves it).

- [ ] **Step 4: Run** — `cd plugins/postgres && go test ./ -run 'TestRegisterPoolMetrics|TestPlugin' 2>&1 | tail` → PASS (Docker needed for the pool).

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/
git commit -m "feat(postgres): pool saturation is visible — pgxpool.Stat exported as OTel instruments

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task C2: E2E — the series appear on the metrics endpoint

**Files:**
- Modify: `internal/e2e/e2e_test.go` (`TestMain`: `observability.Init` before `app.New`)
- Test: `internal/e2e/pool_metrics_test.go` (new)
- Modify: `cmd/cyoda/help/content/telemetry.md:85-89` area (list the seven instruments), `docs/ARCHITECTURE.md:1652` area (one sentence)

- [ ] **Step 1: Write the failing test**

```go
package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/observability"
)

// Pool statistics are exported on the metrics endpoint with the rendered
// Prometheus names, labelled backend="postgres".
func TestMetrics_PostgresPoolSeriesAreExported(t *testing.T) {
	srv := httptest.NewServer(observability.MetricsHandler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	for _, want := range []string{
		`cyoda_storage_pool_connections{backend="postgres",state="acquired"}`,
		`cyoda_storage_pool_connections{backend="postgres",state="idle"}`,
		`cyoda_storage_pool_max_connections{backend="postgres"} 5`,
		`cyoda_storage_pool_acquires_total{backend="postgres"}`,
		`cyoda_storage_pool_empty_acquires_total{backend="postgres"}`,
		`cyoda_storage_pool_canceled_acquires_total{backend="postgres"}`,
		`cyoda_storage_pool_acquire_duration_seconds_total{backend="postgres"}`,
		`cyoda_storage_pool_empty_acquire_wait_seconds_total{backend="postgres"}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
}
```

(`max_connections` is 5 because `TestMain` sets `CYODA_POSTGRES_MAX_CONNS=5`. Label order in the exposition is alphabetical.)

- [ ] **Step 2: Run** — `go test ./internal/e2e/ -run TestMetrics_PostgresPool 2>&1 | tail` → FAIL (empty registry: `Init` never ran).

- [ ] **Step 3: Implement** — in `TestMain` immediately before `testApp = app.New(cfg)`:

```go
	// Same order as cmd/cyoda/main.go: the metrics pipeline exists before the
	// storage plugin registers its instruments.
	otelShutdown, err := observability.Init(ctx, "cyoda-e2e", "e2e", false)
	if err != nil {
		log.Fatalf("observability init: %v", err)
	}
	defer otelShutdown(ctx)
```

Docs: add the seven instruments to `telemetry.md`'s instrument list (same bullet style as `cyoda.tx.*`), noting the `backend` label and that they are Postgres-only and always on; one sentence in ARCHITECTURE §11.

- [ ] **Step 4: Run** — `go test ./internal/e2e/ -run 'TestMetrics_PostgresPool|TestHealth' 2>&1 | tail` → PASS. Then `go test ./internal/e2e/... 2>&1 | tail -5` → PASS (the whole suite still runs with the meter provider live).

- [ ] **Step 5: Commit**

```bash
git add internal/e2e/e2e_test.go internal/e2e/pool_metrics_test.go cmd/cyoda/help/content/telemetry.md docs/ARCHITECTURE.md
git commit -m "test(e2e): pool series are visible on the metrics endpoint

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

## Stream D — closing

### Task D1: CHANGELOG, failure-mode playbook and ledger, cloud-parity

**Files:**
- Modify: `CHANGELOG.md` `[Unreleased]` `### Added` (line ~863) and `### Fixed` (line ~1198)
- Modify: `docs/analysis/failure-modes/2026-06-29-operational-failure-mode-analysis-playbook.md:61` (F8), `:63` (F10, mention write-progress eviction)
- Modify: `docs/analysis/failure-modes/2026-06-29-operational-failure-mode-analysis.md` ledger rows at lines ~399, 416, 417, 422, 429, 430, 431
- Create: `docs/cloud-parity/grpc-keepalive-and-member-eviction.md`; add its row to `docs/cloud-parity/README.md`

- [ ] **Step 1: CHANGELOG** — `### Added`:

> - **HTTP receive-side timeouts, configurable.** `CYODA_HTTP_READ_HEADER_TIMEOUT` (`10s`), `CYODA_HTTP_READ_TIMEOUT` (`5m`), `CYODA_HTTP_IDLE_TIMEOUT` (`120s`) on both the API and admin servers. A request whose headers or body are not received within those bounds is cut off; handler execution is not limited. `CYODA_HTTP_WRITE_TIMEOUT` exists and ships disabled (`0`): the server imposes no time budget on work. A client that takes more than five minutes to deliver a request body is now cut off. `cyoda help config server`.
> - **Postgres pool saturation metrics.** `cyoda_storage_pool_connections{state}`, `cyoda_storage_pool_max_connections`, `cyoda_storage_pool_acquires_total`, `cyoda_storage_pool_empty_acquires_total`, `cyoda_storage_pool_canceled_acquires_total`, `cyoda_storage_pool_acquire_duration_seconds_total`, `cyoda_storage_pool_empty_acquire_wait_seconds_total`, all labelled `backend="postgres"`, always on at `/metrics`. `cyoda help telemetry`.

`### Fixed`:

> - **A frozen compute node is evicted within the keep-alive timeout and never wedges a dispatcher.** Each member's stream now has exactly one writer goroutine draining an outbox; dispatchers hand it events under their own deadline and are released by that deadline (`503 DISPATCH_TIMEOUT`, "member not draining") or by the member's eviction (`503 COMPUTE_MEMBER_DISCONNECTED`). A member is evicted after `CYODA_KEEPALIVE_TIMEOUT` of inbound silence **or** when one write has stalled that long, so a node that keeps pinging while its application is stuck is caught too. grpc-go transport keepalive is configured from the same two variables, with a permissive enforcement floor (5s) so external compute nodes are never GOAWAY'd for pinging.
> - **`CYODA_KEEPALIVE_INTERVAL` and `CYODA_KEEPALIVE_TIMEOUT` were parsed and ignored.** They now reach the gRPC server; a non-positive value is a startup error. Processor, criteria and function responses count as liveness, as the help topic said.
> - **Panics in the member stream's keep-alive loop, receive goroutine and writer are contained** with a ticket and evict the member; they do not latch the node unhealthy (no engine work runs there).
> - **Compute-member routing tags could go stale under connect/disconnect flap:** tag publication is versioned and an older snapshot never overwrites a newer one.
> - **A member disconnecting between being chosen and the request being tracked** now fails fast with `COMPUTE_MEMBER_DISCONNECTED` instead of waiting out the dispatch timeout.
> - **Panic recovery is the outermost HTTP layer**, covering the CORS and cluster-routing middleware, and the admin server (`/livez`, `/readyz`, `/metrics`) is covered too. `Recovery` re-raises `http.ErrAbortHandler`, so a client hanging up on a proxied response is no longer logged as a panic — and, now that the proxy sits inside recovery, does not latch the node.
> - **The reference compute client** (`cmd/compute-test-client`) serialises its own stream writes; compute-node implementations must do the same.

- [ ] **Step 2: Playbook F8** → "`ReadHeaderTimeout` (10s), `ReadTimeout` (5m) and `IdleTimeout` (120s) are set on both servers from `CYODA_HTTP_*`; `WriteTimeout` ships `0` and there is deliberately no per-request deadline — the server imposes no time budget on work (search bounding contract). Pool acquire is bounded separately (F1). | `cmd/cyoda/httpserver.go`; `app/config.go` (`HTTPConfig`) | **Closed.** Regressed if a server is built outside `newHTTPServer` or a timeout is set to `0`". F10: replace "serialised by that member's `sendMu`" with "written by that member's single writer goroutine; a write stalled longer than the keep-alive timeout evicts the member".

- [ ] **Step 3: Ledger rows** — mark `grpc-compute-2`, `grpc-compute-4`, `grpc-compute-5`, `grpc-compute-6`, `api-boundary-http-no-timeouts-1`, `api-boundary-recovery-coverage-2` as `✅ closed` with a short "how" in the last column; `api-boundary-health-1` as `✅ decided — latch kept, documented; probes are /livez and /readyz`.

- [ ] **Step 4: Cloud-parity doc** `docs/cloud-parity/grpc-keepalive-and-member-eviction.md`, sections: *Contract* (server pings every `CYODA_KEEPALIVE_INTERVAL`; eviction after `CYODA_KEEPALIVE_TIMEOUT` of inbound silence or of one stalled write; responses and acks count as liveness; the greet is always the first event; transport keepalive: server PING after interval idle, connection closed if unacked within timeout; clients may ping as often as every 5s with or without an active stream), *What a compute node must do* (serialise its own writes; answer or ping within the timeout; read continuously), *Status codes the member sees* (the §6 table), *Cloud alignment* (what Cloud's gRPC server must mirror). README row.

- [ ] **Step 5:** `go test ./cmd/cyoda/help/... 2>&1 | tail -3` → PASS. Commit:

```bash
git add CHANGELOG.md docs/
git commit -m "docs: server-boundary resilience — changelog, failure-mode ledger, cloud-parity contract

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0157hdW8WkcnyEpfLeqZ4EwF"
```

---

### Task D2: Verification, review gates, PR

- [ ] **Step 1:** `make preflight` → Docker OK.
- [ ] **Step 2:** `make test-full` → green (root + three plugins + E2E). Paste the summary lines.
- [ ] **Step 3:** `make race` → clean. `go vet ./...` → clean; also `cd plugins/postgres && go vet ./...`.
- [ ] **Step 4:** Exit-check greps (all must return nothing):
  - `grep -rn "sendMu" internal/grpc/`
  - `grep -rn "SetKeepAliveConfig\|DefaultKeepAliveInterval" --include='*.go' .`
  - `grep -rn "&http.Server{" cmd/cyoda/ | grep -v httpserver.go`
  - `grep -rn "#473\|#358\|#361" --include='*.go' --include='*.md' internal app cmd docs/cloud-parity cmd/cyoda/help/content`
- [ ] **Step 5:** `superpowers:requesting-code-review` with a fresh-context subagent over the whole branch diff against `release/v0.8.4`; fix findings via red/green; then `antigravity-bundle-security-developer:cc-skill-security-review`.
- [ ] **Step 6:** `superpowers:finishing-a-development-branch`; PR against `release/v0.8.4`, title `fix(api/grpc): server-boundary resilience — panic containment, single-writer streams, timeouts, pool visibility`, body covering the spec's decision table in plain language, `Closes #473`, ending with
  `🤖 Generated with [Claude Code](https://claude.com/claude-code)` and the session link.
