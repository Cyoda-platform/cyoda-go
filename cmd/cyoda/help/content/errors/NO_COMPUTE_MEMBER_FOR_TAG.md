---
topic: errors.NO_COMPUTE_MEMBER_FOR_TAG
title: "NO_COMPUTE_MEMBER_FOR_TAG — no compute member registered for the required tag"
stability: stable
see_also:
  - errors
  - errors.COMPUTE_MEMBER_DISCONNECTED
  - errors.DISPATCH_TIMEOUT
---

# errors.NO_COMPUTE_MEMBER_FOR_TAG

## NAME

NO_COMPUTE_MEMBER_FOR_TAG — the dispatcher found no live cluster node advertising the compute tag required by the workflow or processor.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `yes`.

## DESCRIPTION

Workflow processors are dispatched to nodes that advertise matching compute tags. When no node with the required tag is alive in the cluster within the configured wait timeout (`CYODA_DISPATCH_WAIT_TIMEOUT`), the operation is rejected with this error.

Retryable after compute capacity is restored. At least one live node advertising the required tag is required for dispatch to succeed.

## SEE ALSO

- errors
- errors.COMPUTE_MEMBER_DISCONNECTED
- errors.DISPATCH_TIMEOUT
