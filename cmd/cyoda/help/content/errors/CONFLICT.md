---
topic: errors.CONFLICT
title: "CONFLICT — optimistic concurrency conflict"
stability: stable
see_also:
  - errors
  - errors.TX_CONFLICT
  - errors.IDEMPOTENCY_CONFLICT
  - errors.EPOCH_MISMATCH
---

# errors.CONFLICT

## NAME

CONFLICT — an optimistic concurrency check failed because the entity was modified concurrently.

## SYNOPSIS

HTTP: `409` `Conflict`. Retryable: `yes`.

## DESCRIPTION

The server detected that the entity was modified by another writer between the time it was read and the time the current write was committed. Normal outcome under concurrent load.

Retryable. The full read-modify-write cycle must be repeated using the current entity state; replaying the original write without re-fetching produces stale data.

## SEE ALSO

- errors
- errors.TX_CONFLICT
- errors.IDEMPOTENCY_CONFLICT
- errors.EPOCH_MISMATCH
