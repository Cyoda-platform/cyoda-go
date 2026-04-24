---
topic: errors.DISPATCH_TIMEOUT
title: "DISPATCH_TIMEOUT — dispatch to compute member timed out"
stability: stable
see_also:
  - errors
  - errors.DISPATCH_FORWARD_FAILED
  - errors.NO_COMPUTE_MEMBER_FOR_TAG
---

# errors.DISPATCH_TIMEOUT

## NAME

DISPATCH_TIMEOUT — the dispatcher waited longer than the configured timeout for a compute member to accept and complete a task.

## SYNOPSIS

HTTP: `503` `Service Unavailable`.

## DESCRIPTION

A workflow processor or criteria evaluation was dispatched to a compute member but the response did not arrive within the dispatch timeout window. The underlying task may or may not have completed on the remote node.

Retryable. Completion on the remote node is not guaranteed; retries must be idempotent or carry an idempotency key.

If timeouts recur, check compute member load and network latency. The relevant configuration variables are `CYODA_DISPATCH_WAIT_TIMEOUT` (how long the dispatcher polls gossip for a compute member with matching tags; default `5s`) and `CYODA_DISPATCH_FORWARD_TIMEOUT` (HTTP timeout for the cross-node forwarding call; default `30s`).

## SEE ALSO

- errors
- errors.DISPATCH_FORWARD_FAILED
- errors.NO_COMPUTE_MEMBER_FOR_TAG
