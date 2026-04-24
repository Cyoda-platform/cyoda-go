---
topic: errors.TX_COORDINATOR_NOT_CONFIGURED
title: "TX_COORDINATOR_NOT_CONFIGURED — distributed transaction coordinator is not enabled"
stability: stable
see_also:
  - errors
  - errors.TX_REQUIRED
  - errors.TX_NO_STATE
---

# errors.TX_COORDINATOR_NOT_CONFIGURED

## NAME

TX_COORDINATOR_NOT_CONFIGURED — the request requires a distributed transaction coordinator but none is configured on this node.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `no`.

## DESCRIPTION

Certain operations that span multiple storage shards require a distributed transaction coordinator. This error is returned when such an operation is attempted on a node where the coordinator component is disabled or misconfigured.

Not retryable on this node. Distributed transaction operations require the coordinator to be enabled via the relevant `CYODA_TX_*` environment variables, or must be routed to a node where the coordinator is enabled.

## SEE ALSO

- errors
- errors.TX_REQUIRED
- errors.TX_NO_STATE
