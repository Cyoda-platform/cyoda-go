---
topic: errors.COMPUTE_MEMBER_DISCONNECTED
title: "COMPUTE_MEMBER_DISCONNECTED — compute member dropped from the cluster"
stability: stable
see_also:
  - errors
  - errors.CLUSTER_NODE_NOT_REGISTERED
  - errors.NO_COMPUTE_MEMBER_FOR_TAG
  - errors.DISPATCH_TIMEOUT
  - grpc
---

# errors.COMPUTE_MEMBER_DISCONNECTED

## NAME

COMPUTE_MEMBER_DISCONNECTED — a compute member that was holding a workflow or processor assignment has disconnected.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `yes`.

## DESCRIPTION

The compute member responsible for executing a processor or workflow step disconnected before completing the operation. The task may or may not have been executed.

The server also raises this code when it evicts a member itself — after `CYODA_KEEPALIVE_TIMEOUT` seconds of inbound silence, or when one write to the member has stalled that long — and every callout in flight on that member fails with this code. It is also returned when a member leaves in the instant between being chosen for a callout and the request being handed to it: the caller gets this code immediately rather than waiting out `errors.DISPATCH_TIMEOUT`.

Retryable. The cluster re-routes to an available member. Callers must be idempotent or use an idempotency key when retrying. Persistent failures indicate insufficient compute capacity for the required tags.

## SEE ALSO

- errors
- errors.CLUSTER_NODE_NOT_REGISTERED
- errors.NO_COMPUTE_MEMBER_FOR_TAG
- errors.DISPATCH_TIMEOUT
- grpc
