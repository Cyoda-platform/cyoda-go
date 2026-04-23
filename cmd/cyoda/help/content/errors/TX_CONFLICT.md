---
topic: errors.TX_CONFLICT
title: "TX_CONFLICT — transaction serialization conflict"
stability: stable
see_also:
  - errors
  - errors.CONFLICT
  - errors.EPOCH_MISMATCH
  - errors.IDEMPOTENCY_CONFLICT
---

# errors.TX_CONFLICT

## NAME

TX_CONFLICT — the transaction was aborted because it conflicted with a concurrent transaction at the storage level.

## SYNOPSIS

HTTP: `409` `Conflict`. Retryable: `yes`.

## DESCRIPTION

The underlying storage detected a serialization failure (e.g., PostgreSQL error 40001 or 40P01) and aborted the transaction. This is a normal occurrence under concurrent write load when using serializable or repeatable-read isolation.

Retry the entire transaction from the beginning, including re-reading any data read inside the transaction. Implement exponential backoff to reduce conflict probability under sustained load.

## SEE ALSO

- errors
- errors.CONFLICT
- errors.EPOCH_MISMATCH
- errors.IDEMPOTENCY_CONFLICT
