# Server-boundary resilience: panic containment, stream write discipline, timeouts, saturation visibility

Issue: #473 (supersedes #358, #361). Milestone v0.8.4 (PATCH; see the
version-policy section of the issue). Design approved by Paul 2026-09-07;
independently reviewed by two fresh-context reviewers the same day, findings
folded in below.

## 0. Terms

- **HTTP door** — the main API server (`CYODA_HTTP_PORT`) and the admin
  server (`CYODA_ADMIN_PORT`, serving `/livez`, `/readyz`, `/metrics`).
- **gRPC door** — the server compute nodes connect to.
- **Compute node / member** — an external process that opens one long-lived
  bidirectional gRPC stream (`StartStreaming`) and executes processors,
  criteria and functions the engine dispatches to it. Server-side it is a
  `*Member` in the `MemberRegistry`.
- **Dispatch** — the engine sending a callout request down a member's stream
  and waiting for the matching response (`dispatchCalloutToMember`).
- **Application keep-alive** — the CloudEvent ping the server sends each
  member every `CYODA_KEEPALIVE_INTERVAL` seconds and the eviction the server
  performs when a member has shown no activity for `CYODA_KEEPALIVE_TIMEOUT`
  seconds. Distinct from the **transport keepalive**, which is grpc-go's
  HTTP/2 PING mechanism on the underlying connection.
- **Health flag** — the process-wide `atomic.Bool` that starts true and is
  latched false by the first recovered panic. `/readyz` and `/health` read
  it; `/livez` does not.
- **Evict** — the server unilaterally ending a member's stream and failing
  its in-flight dispatches with a retryable `COMPUTE_MEMBER_DISCONNECTED`.
- **Writer** — the one goroutine per member that is the only caller of the
  raw stream send.

## 1. What is already done (not redone here)

Re-verified at `53fca77`:

- Unary and stream panic-recovery interceptors, chained first
  (`internal/grpc/recovery.go`, `server.go`). Landed by #471.
- The greet is sent through `Member.Send`; the receive loop does not echo
  keep-alives. No raw `stream.Send` on the member stream exists outside the
  `sendFn` closure.
- Pending-request entries are cleared on every dispatch exit
  (`defer member.AbandonRequest`, `dispatch.go`). grpc-compute-4 is closed;
  the failure-mode ledger row is stale.
- The async-search goroutine has a recover (`internal/domain/search`).
- Every Postgres pool acquire is bounded by `CYODA_POSTGRES_ACQUIRE_TIMEOUT`
  (`plugins/postgres/ceilings.go`), so "a request blocks on Acquire with no
  deadline" no longer holds.

## 2. Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | **No per-request context deadline** on HTTP handlers, and `WriteTimeout` ships **disabled (0)**. The issue's Fix list asked for the deadline; it is declined, not forgotten. | #475 settled that the server imposes no time budget; dispatch `responseTimeoutMs` is uncapped by design. The acquire wait is already bounded. Approved by Paul. |
| D2 | `ReadHeaderTimeout`, `ReadTimeout`, `IdleTimeout` ship **enabled** on both HTTP servers. | They bound how a request is *received*, not how long its work runs. Once the body is drained Go clears the read deadline, so `ReadTimeout` never cancels a running handler (pinned by test). |
| D3 | The four HTTP timeouts are **configurable**, one set of env vars applied to **both** servers. | One knob set; no heterogeneous landscape between the two servers. |
| D4 | Transport keepalive derives from the **existing** `CYODA_KEEPALIVE_INTERVAL` / `CYODA_KEEPALIVE_TIMEOUT`, which are also wired for real for the first time. `EnforcementPolicy` is fixed at `MinTime: 5s, PermitWithoutStream: true`. `MaxConnectionIdle`/`MaxConnectionAge` stay infinite. | Two knobs govern both layers. grpc-go's default enforcement (`MinTime: 5m`) already GOAWAYs clients pinging more often than 5 minutes, so 5s only loosens what external compute nodes see. A connection age would GOAWAY healthy members on a timer and fail in-flight dispatches. |
| D5 | `Member` gets an **unbuffered outbox and one writer goroutine**; `sendMu` is deleted; `Send` takes a context and each queued item carries its caller's context. | Single-writer discipline by construction. `Send` returning nil means the writer holds the event now, so staleness is bounded to one event. A full HTTP/2 write window wedges only the writer, never a dispatcher or the keep-alive loop. |
| D6 | **Write progress is a liveness signal.** A member is evicted when it has shown no inbound activity for the keep-alive timeout **or** when one write has been in flight for longer than the keep-alive timeout. | A member whose application is stuck but whose own keep-alive goroutine keeps pinging (the in-repo client does exactly this) never goes stale on inbound activity alone. "Has not accepted a single event in `timeout`" is a property of the member, not a test hook. |
| D7 | Eviction is a **per-member closed channel**; the receive goroutine, the writer, the keep-alive loop and blocked senders all select on it. Returning from the stream handler is what makes grpc-go cancel the stream and unblock a send stuck in the write window. | grpc-go's `SendMsg` cannot be cancelled by a derived context. On handler return `finishStream` runs `s.cancel()` first (`internal/transport/http2_server.go`), and `writeQuota.get` selects on the stream's done channel, so the blocked send returns. Implementation property of grpc-go v1.82, not a documented guarantee; cited here so the dependency is visible. |
| D8 | A panic in the keep-alive loop or the receive goroutine is recovered with the interceptors' ticket-and-log envelope and **evicts the member, without latching** the health flag. | The tx-lifecycle spec §1.4 criterion: latch when the recovered code was doing engine or store work on the application's behalf; do not latch for code that holds no transaction and self-heals. Both goroutines only tick, enqueue or `Recv`; the member reconnects fresh. The main loop's response handlers still run under the stream interceptor and latch, unchanged. |
| D9 | Tag-change publication carries a **version**, advanced **only on a successful publish**; an older snapshot never overwrites a newer one. | grpc-compute-5. Advancing before success would let a failed publish of the newest version drop every older one and leave routing stale until the next change. |
| D10 | `TrackRequest` fails once the member is **evicted or closed**, both checked under the pending-map lock; `Send`/`TrySend` test eviction before enqueueing. | grpc-compute-6, and the keep-alive-eviction → deferred-`Unregister` window. |
| D11 | `/health` **keeps the latch**. Documentation states the semantics. | A behaviour change would need a probe audit; #485 owns the further drain decision. |
| D12 | Pool statistics are exported by the **Postgres plugin registering observable instruments on the global OTel meter**. No SPI or core change. One pool per process is the assumption. | `observability.Init` runs before the plugin factory in `main`; the global meter replays instruments and callbacks on the first `SetMeterProvider`, so the order also works when a test initialises later. The acquire-timeout precedent avoided an SPI change for the same reason. |
| D13 | `Recovery` becomes the **outermost** layer of the main handler chain and wraps the admin mux. `Recovery` **re-raises `http.ErrAbortHandler`**. | CORS writes its headers before calling `next`, so a recovered 500 keeps them. `httputil.ReverseProxy` panics with `ErrAbortHandler` when the client hangs up mid-body; `net/http` handles that sentinel silently, and without the re-raise every proxied client disconnect would latch the node unready. Latent defect in `Recovery` today; fixed on its own. A panic in a `/metrics` scrape or `/livez` latches like any other: one policy per door, stated in the docs. |
| D14 | Processor, criteria and function responses **refresh liveness**. | The gRPC help topic already says they do. |
| D15 | `Register` takes the greet event as a **value** and returns `*Member`. | Greet-first ordering without inverting control; removes the six `registry.Get(memberID)` re-lookups in the stream handler, each a small member-death race. |
| D16 | The in-repo compute client (`cmd/compute-test-client`) gets the same single-writer discipline. | It calls the raw stream send from two goroutines today; it is the reference client. |
| D17 | Out of scope: a cap on `responseTimeoutMs` (grpc-compute-3). | Not in this issue's fold-in list; needs its own value and error-reporting decision. |

## 3. gRPC door

### 3.1 Member outbox and writer (D5, D6, D7, D10)

```go
type outboxItem struct {
    ce  *cepb.CloudEvent
    ctx context.Context   // caller's; the writer skips an item whose ctx is done
}

type Member struct {
    ID, TenantID, Tags, ConnectedAt          // unchanged
    send           SendFunc                  // raw stream write; called ONLY by writeLoop
    outbox         chan outboxItem           // unbuffered
    evicted        chan struct{}             // closed once by Evict
    evictOnce      sync.Once
    evictErr       error                     // valid only after evicted is closed
    writeStartedAt atomic.Int64              // unix nanos; 0 when no write in flight
    lastSeen / lastSeenMu                    // unchanged
    pendingReqs / pendingMu                  // unchanged
    closed         bool                      // under pendingMu; set by Evict and FailAllPending
}
```

- `Send(ctx, ce) error` — non-blocking pre-check of `evicted` and `ctx.Done()`,
  then `select { outbox <- item: nil; <-evicted: ErrMemberEvicted; <-ctx.Done(): ctx.Err() }`.
- `TrySend(ce) bool` — non-blocking enqueue with `context.Background()`;
  false when the writer is busy or the member is evicted.
- `Evict(err error)` — once: sets `closed` under `pendingMu`, stores `err`,
  closes `evicted`. A second call is a no-op; the first error wins.
- `Evicted() <-chan struct{}`; `EvictErr() error` (call only after `Evicted()` fired).
- `WriteInFlightSince() time.Time` — zero when idle.
- `writeLoop()` — started by `Register`:
  `for { select { item := <-outbox: if item.ctx.Err() != nil { continue }; writeStartedAt = now; err := m.send(item.ce); writeStartedAt = 0; if err != nil { m.Evict(status.Error(codes.Unavailable, "send failed: "+err.Error())); return }; <-evicted: return } }`.
  A send blocked in the write window returns once the stream handler returns (D7).
- `sendMu` is deleted. `ErrMemberEvicted` is a package sentinel.
- `TrackRequest(requestID) (chan *ProcessingResponse, error)` returns
  `ErrMemberEvicted` when `closed`. `FailAllPending` sets `closed` before
  swapping the map (unchanged otherwise).

**Greet ordering (D15).** `Register(memberID string, tenantID, tags, send SendFunc, greet *cepb.CloudEvent) *Member`.
The handler mints the ID (`uuid.NewString()`), builds the greet, and calls
`Register`, which starts the writer with the greet as its first item **before**
publishing the member into the map. No dispatch can precede the greet.
`greet` may be nil (test fixtures). `Unregister` calls
`Evict(status.Error(codes.Unavailable, "unregistered"))` so the writer exits.

### 3.2 Stream handler (`StartStreaming`)

1. Auth, join parsing, tenant check — unchanged.
2. `member := s.registry.Register(...)`; `defer s.registry.Unregister(member.ID)`.
3. Start `keepAliveLoop(ctx, member)`.
4. Start **one** long-lived receive goroutine:
   `for { msg, err := stream.Recv(); if err != nil { member.Evict(err); return }; select { recvCh <- msg; <-member.Evicted(): return } }`,
   wrapped in `recover` → ticket-and-log (no latch, D8) → `member.Evict(thatStatusErr)`.
5. Main loop: `select { <-member.Evicted(): return member.EvictErr(); msg := <-recvCh: ... }`.
   Response handlers additionally call `member.UpdateLastSeen()` (D14).

The per-iteration `Recv` goroutine, `timeoutCh` and every `registry.Get(memberID)`
re-lookup are removed. When a peer resets the stream, the writer and the
receive goroutine race to `Evict`; whichever wins sets the returned status.
The status the departing client sees is therefore not deterministic, and
the table in §6 says so.

### 3.3 Keep-alive loop

Wrapped in `recover` → ticket-and-log → `member.Evict(thatStatusErr)`. Per
tick:

```
if since(member.LastSeen()) > timeout            → Evict(DeadlineExceeded "keep-alive timeout")
if w := member.WriteInFlightSince(); !w.IsZero() && since(w) > timeout
                                                 → Evict(DeadlineExceeded "member not draining")
else member.TrySend(ping); false → debug log "writer busy, ping skipped"
```

The loop exits on `ctx.Done()` or `member.Evicted()`. It never blocks on the
stream.

### 3.4 Dispatch (`dispatchCalloutToMember`)

```
timeout := resolve(timeoutMs)                            // unchanged rule
callCtx, cancel := context.WithTimeout(ctx, timeout); defer cancel()
ch, err := member.TrackRequest(requestID)                // D10
  ErrMemberEvicted        → 503 COMPUTE_MEMBER_DISCONNECTED (retryable)
defer member.AbandonRequest(requestID)
err = member.Send(callCtx, ce)
  ErrMemberEvicted        → 503 COMPUTE_MEMBER_DISCONNECTED (retryable)
  callCtx deadline        → 503 DISPATCH_TIMEOUT, message "member not draining" (retryable)
  parent ctx cancelled    → ctx.Err()
select { resp := <-ch: unchanged;
         <-callCtx.Done(): parent cancelled ? ctx.Err() : 503 DISPATCH_TIMEOUT "no response" }
```

One deadline bounds enqueue plus wait. `time.After` is replaced by the
context. `context.WithTimeout` inherits the parent's error, and the parent is
checked first, so cancellation is never misreported as a timeout.

### 3.5 Tag-change versioning (D9)

`TagChangeFunc` becomes `func(map[string][]string) error` (the gossip
callback already has an error to return). `MemberRegistry` gains
`tagsVersion uint64` (incremented under `mu` in `Register`/`Unregister`),
`publishMu sync.Mutex`, `publishedVersion uint64`. `notifyChange` snapshots
`(tags, version)` under one `RLock`, then in its goroutine:

```
publishMu.Lock(); defer publishMu.Unlock()
if version <= publishedVersion { return }
if err := fn(tags); err != nil { log; return }     // version NOT advanced
publishedVersion = version
```

The existing recover stays and likewise does not advance the version.
Running `fn` under `publishMu` is safe: `r.mu` is not held, `UpdateTags`
takes only gossip's own lock, `UpdateNode(0)` does not wait, and nothing
calls back into the registry.

### 3.6 Transport keepalive and config wiring (D4)

`NewServer` gains a `KeepAliveConfig{Interval, Timeout time.Duration}`
parameter, sets the service's keep-alive fields from it, and adds:

```go
keepalive.KeepaliveParams(keepalive.ServerParameters{Time: Interval, Timeout: Timeout})
keepalive.EnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: true})
```

`app.New` converts `cfg.GRPC.KeepAliveInterval/Timeout` (seconds, `int`) to
durations. `Config.Validate` rejects values `<= 0` (startup error, like the
other validators). `SetKeepAliveConfig` and the package-level
`DefaultKeepAliveInterval/Timeout` are retired; tests construct the service
with the config. grpc-go clamps `Time` below 1s to 1s; tests that use
millisecond intervals for the application keep-alive are unaffected because
the two layers are separate parameters.

Worst-case detection: black-holed or SIGSTOPped peer, transport
`Time + Timeout` (40s at defaults); frozen application that still pings,
`Timeout + Interval` via the write-progress rule (40s); silent application,
`Timeout + Interval` via `LastSeen` (40s).

### 3.7 Exit check

A `go/ast` test in `internal/grpc` parses `streaming.go`, `members.go` and
`dispatch.go` and asserts that a `Send` selector on the member stream
parameter occurs exactly once, inside the `sendFn` closure. The invariant is
scoped to the **member** stream; `entity.go` and `search.go` write their own
per-request server streams from a single goroutine and are out of scope.

### 3.8 Reference client (D16)

`cmd/compute-test-client/dispatch.go` serialises its two sending goroutines
(request loop and keep-alive ticker) behind one mutex around `stream.Send`.

## 4. HTTP door

### 4.1 Timeouts (D1–D3)

`app.Config` gains `HTTP HTTPConfig{ReadHeaderTimeout, ReadTimeout, WriteTimeout, IdleTimeout time.Duration}`:

| Env var | Default | Meaning |
|---------|---------|---------|
| `CYODA_HTTP_READ_HEADER_TIMEOUT` | `10s` | Time allowed to receive the request headers. |
| `CYODA_HTTP_READ_TIMEOUT` | `5m` | Time allowed to receive the whole request, body included. |
| `CYODA_HTTP_WRITE_TIMEOUT` | `0` (disabled) | Time from end of headers to end of response. Bounds handler execution; off by policy (#475). |
| `CYODA_HTTP_IDLE_TIMEOUT` | `2m` (120 seconds) | Keep-alive connection idle time between requests. |

`0` disables `ReadTimeout` and `WriteTimeout` outright. For
`ReadHeaderTimeout` and `IdleTimeout`, `0` instead means "use `ReadTimeout`"
— Go's own `net/http.Server` fallback — so those two are off only when
`ReadTimeout` is also `0`. Validation rejects negatives.

`ReadTimeout` default rationale: request bodies are capped at 10 MiB by the
entity, search and grouped-stats handlers, so `5m` admits any client
sustaining ~280 kbit/s and cuts off only a body that is effectively not
arriving. `60s` would have cut off a max-size upload below ~1.4 Mbit/s, a
request that completes today. `IdleTimeout` (120s) exceeds the cluster
proxy's and forwarder's outbound `IdleConnTimeout` (90s), the correct
direction. On the admin server `ReadTimeout` is inert (no body-reading
handlers); sharing the values keeps one knob set.

Both servers are built by one function, `newHTTPServer(addr, handler, cfg.HTTP) *http.Server`
in `cmd/cyoda`, replacing the two inline literals in `run.go`.

What a client observes: a slow-header client's connection is **closed with
no response** (Go's server treats the header-read timeout as a common net
read error and does not reply). A slow-body client's handler read returns a
timeout error and the request context is cancelled at that instant; the
entity handler reads the body first, so it answers `400 BAD_REQUEST`. Once a
body has been fully read, the server clears the read deadline, so a handler
running longer than `ReadTimeout` is not affected (pinned by test).
`internal/e2e` uses `httptest.Server`'s own `http.Server`, so no E2E path
sees these timeouts; the tests live in `cmd/cyoda` deliberately.

### 4.2 Recovery coverage (D13)

1. `middleware.Recovery` re-raises `http.ErrAbortHandler` (`if rec == http.ErrAbortHandler { panic(rec) }`)
   before anything else. Own red/green step.
2. In `app.New` the order becomes inner mux → cluster proxy (cluster only)
   → CORS → `Recovery` (outermost). No helper with fake-injectable layers;
   the comments at the three sites are rewritten to state the order and why.
3. `run.go` wraps the admin handler in `middleware.Recovery(a.HealthFlag())`;
   `App` gains a `HealthFlag()` accessor. No import cycle: `internal/admin`
   imports no internal package, and `middleware` imports only
   `internal/common` and `internal/contract`.

### 4.3 `/health` (D11)

No code change. `internal/api/health.go`'s doc comment,
`cmd/cyoda/help/content/run.md`, `cli/serve.md`, `cli/health.md`,
`docs/ARCHITECTURE.md` state: `/health` mirrors the readiness flag; a
recovered panic anywhere (API, gRPC, admin, background loops that do engine
work) latches it until the node is replaced; deployment probes are `/livez`
and `/readyz` on the admin server. `docs/PRD.md` and `docs/FEATURES.md` stop
calling `/health` the readiness probe.

## 5. Pool saturation metrics (D12)

`plugins/postgres/metrics.go`: `registerPoolMetrics(pool *pgxpool.Pool) (unregister func(), err error)`
creates observable instruments on
`otel.Meter("github.com/cyoda-platform/cyoda-go/plugins/postgres")` and one
`RegisterCallback` reading `pool.Stat()`. Registered in `NewFactory` **after**
`ensureSchema` (a migration failure closes the pool; a callback must never
observe a closed pool); `StoreFactory.Close` unregisters before closing the
pool.

| OTel instrument | Kind | Source (`pgxpool.Stat`) | Prometheus name as rendered |
|-----------------|------|-------------------------|-----------------------------|
| `cyoda.storage.pool.connections` `{state=acquired\|idle\|constructing}` | Int64ObservableGauge | `AcquiredConns`, `IdleConns`, `ConstructingConns` | `cyoda_storage_pool_connections` |
| `cyoda.storage.pool.max_connections` | Int64ObservableGauge | `MaxConns` | `cyoda_storage_pool_max_connections` |
| `cyoda.storage.pool.acquires` | Int64ObservableCounter | `AcquireCount` | `cyoda_storage_pool_acquires_total` |
| `cyoda.storage.pool.empty_acquires` | Int64ObservableCounter | `EmptyAcquireCount` | `cyoda_storage_pool_empty_acquires_total` |
| `cyoda.storage.pool.canceled_acquires` | Int64ObservableCounter | `CanceledAcquireCount` | `cyoda_storage_pool_canceled_acquires_total` |
| `cyoda.storage.pool.acquire_duration` (unit `s`) | Float64ObservableCounter | `AcquireDuration` | `cyoda_storage_pool_acquire_duration_seconds_total` |
| `cyoda.storage.pool.empty_acquire_wait` (unit `s`) | Float64ObservableCounter | `EmptyAcquireWaitTime` | `cyoda_storage_pool_empty_acquire_wait_seconds_total` |

All carry `backend="postgres"`. `empty_acquire_wait` is the saturation
signal the issue asks for: cumulative time callers waited because the pool
was empty. `acquire_duration` sums every acquire, instant ones included.
The rendered names follow the exporter's `UnderscoreEscapingWithSuffixes`
strategy (`_total` on monotonic sums, `_seconds` from unit `s`).

The plugin's `go.mod` promotes `go.opentelemetry.io/otel` and `otel/metric`
to direct dependencies. The global meter replays instruments and callbacks
only on the **first** `SetMeterProvider`; a test that resets and
re-initialises observability does not re-attach plugin instruments.
`internal/e2e`'s `TestMain` calls `observability.Init` before `app.New`, the
same order as `main`, so the scrape sees live series.

## 6. Error / status table

gRPC `StartStreaming` (the member stream), per exit:

| Cause | Status |
|-------|--------|
| No `ROLE_M2M` / no user context | `PermissionDenied` / `Unauthenticated` (unchanged) |
| No inbound activity for the keep-alive timeout | `DeadlineExceeded` "keep-alive timeout" (unchanged) |
| One write in flight longer than the keep-alive timeout | `DeadlineExceeded` "member not draining" |
| Writer's raw send failed | `Unavailable` "send failed: …" |
| Panic in keep-alive loop or receive goroutine | `Internal` "SERVER_ERROR: internal error [ticket: …]" (interceptor envelope; no latch) |
| Client closed or reset the stream | the `Recv` error, or `Unavailable` "send failed" if the writer noticed first |

Dispatch outcomes surfaced to the HTTP/gRPC caller of the transition:

| Cause | HTTP | Code | Retryable |
|-------|------|------|-----------|
| Member evicted/unregistered before track or during enqueue | 503 | `COMPUTE_MEMBER_DISCONNECTED` | yes |
| Member disconnected while waiting | 503 | `COMPUTE_MEMBER_DISCONNECTED` | yes (unchanged) |
| Enqueue exceeded `responseTimeoutMs` ("member not draining") | 503 | `DISPATCH_TIMEOUT` | yes |
| Wait exceeded `responseTimeoutMs` ("no response") | 503 | `DISPATCH_TIMEOUT` | yes (unchanged) |
| Caller context cancelled | ctx error (unchanged) | | |

HTTP servers: slow-header → connection closed, no response; slow-body →
`400 BAD_REQUEST` on the entity route; panic anywhere in the chain, admin
included → `500 SERVER_ERROR` with ticket, health flag latched; client
hang-up during a proxied response → nothing written, nothing latched. No new
error codes.

## 7. Coverage matrix

| Scenario | Unit | Running-backend e2e | Parity | gRPC real-TCP |
|----------|------|---------------------|--------|---------------|
| Writer wedged on a never-returning send: evicted within keep-alive timeout by the write-progress rule; concurrent dispatchers return `DISPATCH_TIMEOUT` "member not draining" by their own deadline; none wedge | `internal/grpc` (blocking `SendFunc`) | — | — | ✓ client dialled with `WithInitialWindowSize`/`WithInitialConnWindowSize` 64 KiB that stops reading; one dispatch carrying a ~256 KiB entity fills the window |
| Member that keeps pinging inbound but never reads → evicted within timeout | `internal/grpc` | — | — | ✓ (same fixture, client keeps pinging) |
| Transport keepalive tears down a black-holed connection within `Time + Timeout`: member unregistered and `GracefulStop` returns (it waits for every connection to close, so a lingering dead one would hang it) | — | — | — | ✓ pausable in-test TCP proxy between client and server |
| Enforcement tolerates a client pinging every 5s (no GOAWAY): the enforcement-policy constants (`MinTime: 5s`, `PermitWithoutStream: true`) are reviewed, not tested — a discriminating test needs ~20s of idle real-network time, so the test was deleted rather than kept flaky/slow | — | — | — | — |
| `Send` honours ctx deadline while the writer is busy; a sender that gives up is released and nothing it queued is written (the writer's own ctx check on a received item is reviewed, not tested: with an unbuffered handoff it is not separately observable) | `internal/grpc` | — | — | — |
| Greet is the first event on the wire even with a dispatch racing `Register` | `internal/grpc` (`overlapDetectingStream` reuse) | — | — | — |
| `-race`: dispatch + keep-alive + greet, exactly one goroutine calls the raw send | `internal/grpc` | — | — | — |
| Writer panic (raw send panics): process survives, member evicted, ticket in status, flag **not** latched | `internal/grpc` | — | — | — |
| Keep-alive loop panic: same recover pattern as the writer and receive goroutine; a zero keep-alive interval panics `time.NewTicker` inside the loop, giving a reachable trigger without a test hook | `internal/grpc` (`TestStreaming_KeepAliveLoopPanic_IsContained`) | — | — | — |
| Receive goroutine panic: same | `internal/grpc` | — | — | ✓ |
| Clean client disconnect unregisters promptly (existing regression test kept) | `internal/grpc` | — | — | — |
| Member unregistered between lookup and track → immediate `DISCONNECTED`; after `Evict` but before `Unregister` → same | `internal/grpc` | — | — | — |
| Double `Evict` keeps the first error | `internal/grpc` | — | — | — |
| Writer, receive and keep-alive goroutines exit after handler return (done-channel per goroutine) | `internal/grpc` | — | — | — |
| Older tag snapshot never overwrites newer; failed publish does not advance the version | `internal/grpc` | — | — | — |
| Responses refresh `LastSeen` | `internal/grpc` | — | — | — |
| Server options carry the configured keepalive values | `internal/grpc` | — | — | — |
| `CYODA_KEEPALIVE_*` reach the server (binding test + wiring pin); `<= 0` rejected | `app` | — | — | — |
| Raw member-stream `Send` exit check (`go/ast`) | `internal/grpc` | — | — | — |
| Reference client: one goroutine at a time calls the raw send (`-race`) | `cmd/compute-test-client` | — | — | — |
| HTTP: `newHTTPServer` field values; slow-header → EOF with no bytes; slow-body → handler read times out; idle conn closed; a handler running past `ReadTimeout` after draining the body is not cancelled | `cmd/cyoda` (`net.Listen` + `srv.Serve`, 50–100 ms timeouts) | — | — | — |
| HTTP env binding for all four; negatives rejected; `WriteTimeout` default 0 | `app`, `cmd/cyoda/help` | — | — | — |
| `Recovery` re-raises `ErrAbortHandler`; ordinary panic still 500 + latch | `internal/api/middleware` | — | — | — |
| Proxy under `Recovery` with an upstream that hangs up mid-body: nothing latched | `internal/cluster/proxy` | — | — | — |
| Admin server panic → 500 + latch | `cmd/cyoda` | — | — | — |
| Pool metrics: callback reports pool state; unregister stops it | `plugins/postgres` (`sdk/metric` `ManualReader`) | `internal/e2e` scrape via `observability.MetricsHandler()` asserts the seven rendered names with `backend="postgres"` | — | — |
| `/health` and `/readyz` latch semantics (existing tests) | existing | existing | — | — |

Parity column is empty by design: nothing here is backend-agnostic
behaviour; the metrics are Postgres-plugin-specific and the rest is transport.
Concurrency tests live in isolated package tests, never in the parity suite.

## 8. Gate 4 documentation touch-set

- `app/config.go` (`HTTPConfig`, defaults, validation), `app/config_registry_binding_test.go`, `app/config_test.go`.
- `cmd/cyoda/help/config_registry.go`: four `server` rows; the two keep-alive rows move from `Topic: "cluster"` to `Topic: "grpc"`.
- `cmd/cyoda/help/content/config.md` (server list), `config/grpc.md` (keep-alive rows in, transport layer described), `config/cluster.md` (rows out), `grpc.md` KEEPALIVE section corrected, `run.md` / `cli/serve.md` / `cli/health.md` health wording.
- `README.md` has no env table; no change. `docs/ARCHITECTURE.md`: env table rows, health section, member write path description.
- `CHANGELOG.md` `[Unreleased]` → `### Added` (timeouts, metrics), `### Fixed` (keep-alive vars, eviction, races, recovery coverage, `ErrAbortHandler`, reference client). No `### Breaking`. The `ReadTimeout` entry states that a body not arriving within 5 minutes is now cut off.
- `docs/analysis/failure-modes/…-playbook.md` F8 → closed state recording "no per-request deadline by decision (#475)"; ledger rows grpc-compute-2/4/5/6, `api-boundary-http-no-timeouts-1`, `api-boundary-recovery-coverage-2`, `api-boundary-health-1` → closed/decided.
- `docs/cloud-parity/grpc-keepalive-and-member-eviction.md` + README row: the wire-visible contract (server pings every interval; eviction on inbound silence or on write stall; transport keepalive tolerances; greet-first ordering; compute-node authors must serialise their own stream writes).
- `docs/PRD.md`, `docs/FEATURES.md`: `/health` wording.
- Helm: `extraEnv` covers the new vars; no chart change. `COMPATIBILITY.md`: no pin or tag change; no update.

## 9. Compatibility

No wire-API change. Transport keepalive enforcement is looser than grpc-go's
default. `WriteTimeout` defaults to 0; the per-request deadline the issue
listed is declined by D1 with approval. `/health` behaviour unchanged. The
`NewServer`, `Register` and `TagChangeFunc` signatures change (internal API
only). v0.8.4 stays a PATCH.
