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

The underlying storage detected a serialization failure (e.g., PostgreSQL error 40001 or 40P01) and aborted the transaction. Normal occurrence under concurrent write load when using serializable or repeatable-read isolation.

Retryable. The entire transaction — including any data read inside it — must be restarted from the beginning.

## SEE ALSO

- errors
- errors.CONFLICT
- errors.EPOCH_MISMATCH
- errors.IDEMPOTENCY_CONFLICT
