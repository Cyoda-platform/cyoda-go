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

Workflow processors are dispatched to nodes that advertise matching compute tags. When no node with the required tag is alive in the cluster within the configured wait timeout, the operation is rejected with this error.

Ensure at least one compute node with the required tag is running. Check `CYODA_COMPUTE_TAGS` on your node configuration and verify the node is registered in the cluster. Retry after compute capacity is restored.

## SEE ALSO

- errors
- errors.COMPUTE_MEMBER_DISCONNECTED
- errors.DISPATCH_TIMEOUT
