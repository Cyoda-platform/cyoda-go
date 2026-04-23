---
topic: errors.TRANSACTION_NOT_FOUND
title: "TRANSACTION_NOT_FOUND — transaction ID does not exist"
stability: stable
see_also:
  - errors
  - errors.TRANSACTION_EXPIRED
  - errors.TRANSACTION_NODE_UNAVAILABLE
---

# errors.TRANSACTION_NOT_FOUND

## NAME

TRANSACTION_NOT_FOUND — no transaction with the given ID exists on this node.

## SYNOPSIS

HTTP: `404` `Not Found`. Retryable: `no`.

## DESCRIPTION

The transaction ID supplied in the request does not correspond to an active transaction. The transaction may have been committed, rolled back, expired, or may never have existed. This error can also occur when a request is mis-routed to a node that never opened the transaction.

Verify the transaction ID. If the transaction was expected to be active, check whether it was committed or rolled back by a prior request.

## SEE ALSO

- errors
- errors.TRANSACTION_EXPIRED
- errors.TRANSACTION_NODE_UNAVAILABLE
