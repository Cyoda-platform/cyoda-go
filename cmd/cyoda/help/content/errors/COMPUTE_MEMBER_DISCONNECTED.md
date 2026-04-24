---
topic: errors.COMPUTE_MEMBER_DISCONNECTED
title: "COMPUTE_MEMBER_DISCONNECTED — compute member dropped from the cluster"
stability: stable
see_also:
  - errors
  - errors.CLUSTER_NODE_NOT_REGISTERED
  - errors.NO_COMPUTE_MEMBER_FOR_TAG
---

# errors.COMPUTE_MEMBER_DISCONNECTED

## NAME

COMPUTE_MEMBER_DISCONNECTED — a compute member that was holding a workflow or processor assignment has disconnected.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `yes`.

## DESCRIPTION

The compute member responsible for executing a processor or workflow step disconnected before completing the operation. The task may or may not have been executed.

Retryable. The cluster re-routes to an available member. Callers must be idempotent or use an idempotency key when retrying. Persistent failures indicate insufficient compute capacity for the required tags.

## SEE ALSO

- errors
- errors.CLUSTER_NODE_NOT_REGISTERED
- errors.NO_COMPUTE_MEMBER_FOR_TAG
