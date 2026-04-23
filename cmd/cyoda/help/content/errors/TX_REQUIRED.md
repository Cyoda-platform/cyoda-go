---
topic: errors.TX_REQUIRED
title: "TX_REQUIRED — operation must be performed inside a transaction"
stability: stable
see_also:
  - errors
  - errors.TX_CONFLICT
  - errors.TX_COORDINATOR_NOT_CONFIGURED
---

# errors.TX_REQUIRED

## NAME

TX_REQUIRED — the requested operation can only be performed within an open transaction but no transaction context was provided.

## SYNOPSIS

HTTP: `400` `Bad Request`. Retryable: `no`.

## DESCRIPTION

Certain write operations that require atomic multi-step coordination mandate a transaction context. When such an operation is called without a `transactionId` header or query parameter, this error is returned.

Open a transaction first using the transaction management API, then pass the resulting transaction ID with the request.

## SEE ALSO

- errors
- errors.TX_CONFLICT
- errors.TX_COORDINATOR_NOT_CONFIGURED
