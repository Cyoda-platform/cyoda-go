---
topic: errors.TRANSACTION_NODE_UNAVAILABLE
title: "TRANSACTION_NODE_UNAVAILABLE — the node owning the transaction is unreachable"
stability: stable
see_also:
  - errors
  - errors.TRANSACTION_EXPIRED
  - errors.TRANSACTION_NOT_FOUND
  - errors.CLUSTER_NODE_NOT_REGISTERED
---

# errors.TRANSACTION_NODE_UNAVAILABLE

## NAME

TRANSACTION_NODE_UNAVAILABLE — the cluster node that owns the open transaction is not alive or reachable.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `yes`.

## DESCRIPTION

Transaction state is pinned to the node that opened it. If that node crashes or becomes unreachable while the transaction is in progress, subsequent requests using the transaction token are rejected with this error because the proxy cannot forward them to the owner.

The transaction is likely lost. Retry by opening a new transaction. Implement health checks and reconnect logic if your client must tolerate node failures mid-transaction.

## SEE ALSO

- errors
- errors.TRANSACTION_EXPIRED
- errors.TRANSACTION_NOT_FOUND
- errors.CLUSTER_NODE_NOT_REGISTERED
