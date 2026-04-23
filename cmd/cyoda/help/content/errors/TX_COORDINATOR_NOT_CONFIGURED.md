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

Enable the transaction coordinator via the relevant `CYODA_TX_*` environment variables, or route distributed transaction operations to a node that has the coordinator enabled.

## SEE ALSO

- errors
- errors.TX_REQUIRED
- errors.TX_NO_STATE
