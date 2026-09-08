# gRPC compute-member keep-alive and eviction — Cloud twin-alignment spec

This document is the contract Cyoda Cloud implements to stay aligned with
cyoda-go's compute-member gRPC stream (`StartStreaming`). cyoda-go is the
authoritative implementation.

## Contract

- The server pings every member once per `CYODA_KEEPALIVE_INTERVAL` seconds
  while the stream is open.
- A member is evicted after `CYODA_KEEPALIVE_TIMEOUT` seconds of inbound
  silence, **or** after one queued write to that member has been in flight
  longer than `CYODA_KEEPALIVE_TIMEOUT` — whichever comes first. The second
  condition is what catches a member whose own keep-alive goroutine keeps
  pinging while the application behind it is stuck: inbound activity alone
  is not sufficient evidence of liveness.
- The join is what registers the member and seeds its last-seen time. Once
  registered, exactly five inbound message kinds refresh last-seen: a
  keep-alive ping, an ack response, and a processor, criteria or function
  response. An event type the server does not recognize is logged and does
  **not** refresh last-seen — it is not evidence of liveness.
- The greet event is always the first event a member receives on the stream
  — sent as part of registering the member, before the member is published
  and can be handed a dispatch, so a dispatch racing registration can never
  arrive ahead of the greet.
- Transport keepalive rides the same two variables: the server sends an
  HTTP/2 PING after `CYODA_KEEPALIVE_INTERVAL` of connection idleness and
  closes the connection if it goes unacknowledged for
  `CYODA_KEEPALIVE_TIMEOUT`. The enforcement policy is permissive
  (`MinTime: 5s`, `PermitWithoutStream: true`): a client may ping as often
  as every 5 seconds, with or without an active stream, and is never
  `GOAWAY`'d for it. Connection idle/age limits are infinite — a healthy
  member is never torn down on a timer.

## What a compute node must do

- **Serialise its own writes to the stream.** Only one goroutine may call
  the raw stream send at a time. cyoda-go's server enforces this
  server-side with one writer goroutine per member draining an outbox;
  a compute-node client must enforce the same discipline on its own side,
  since gRPC stream sends are not safe for concurrent use from multiple
  goroutines.
- **Answer, or ping, within the keep-alive timeout.** A response to an
  outstanding request and a keep-alive ping both count as liveness; a
  compute node that goes silent for `CYODA_KEEPALIVE_TIMEOUT` is evicted
  regardless of which kind of silence it is.
- **Read continuously.** The receive side must keep calling `Recv` for the
  life of the stream; a compute node that stops reading looks the same to
  the server as one that has stopped responding.

## Status codes the member sees

Exit conditions on the member's `StartStreaming` stream:

| Cause | Status |
|-------|--------|
| No `ROLE_M2M` / no user context | `PermissionDenied` / `Unauthenticated` (unchanged) |
| No inbound activity for the keep-alive timeout | `DeadlineExceeded` "keep-alive timeout" (unchanged) |
| One write in flight longer than the keep-alive timeout | `DeadlineExceeded` "member not draining" |
| Writer's raw send failed | `Unavailable` "send failed: …" |
| Panic in the writer goroutine (raw send) | `Internal` "SERVER_ERROR: internal error [ticket: …]" (the member is evicted with it; no latch) |
| Panic in keep-alive loop or receive goroutine | `Internal` "SERVER_ERROR: internal error [ticket: …]" (interceptor envelope; no latch) |
| Client closed or reset the stream | the `Recv` error, or `Unavailable` "send failed" if the writer noticed first |

A dispatch routed to a member that is gone, or that goes stale mid-dispatch,
surfaces to the HTTP/gRPC caller as `503 COMPUTE_MEMBER_DISCONNECTED` or
`503 DISPATCH_TIMEOUT`, both retryable — unchanged by this contract, and
covered by the existing dispatch-outcome documentation.

## Cloud alignment

For Cloud's gRPC server to stay aligned:

1. Compute-member liveness must be tracked the same way: the join
   registers the member and seeds last-seen; a keep-alive ping, an ack
   response, or a processor/criteria/function response refreshes it; an
   unrecognized event type does not. Eviction fires on either inbound
   silence or a stalled outbound write reaching the timeout — not on
   inbound silence alone.
2. Writes to one member's stream must be serialised through a single
   writer so that a slow or stuck member wedges only its own writer, never
   a request-handling goroutine or the keep-alive loop.
3. The greet must be the first event a member sees, ordered strictly before
   the member becomes eligible for dispatch.
4. Transport-level keepalive tolerances (ping interval, ack timeout,
   enforcement floor) must not be tighter than what cyoda-go allows, so a
   compute node written against one server is not rejected by the other.
5. A panic while servicing one member's stream must not take down the
   node's health for every other member or every other tenant.
