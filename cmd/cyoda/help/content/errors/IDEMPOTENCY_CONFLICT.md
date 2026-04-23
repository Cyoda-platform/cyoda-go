---
topic: errors.IDEMPOTENCY_CONFLICT
title: "IDEMPOTENCY_CONFLICT — duplicate request with conflicting payload"
stability: stable
see_also:
  - errors
  - errors.CONFLICT
  - errors.TX_CONFLICT
---

# errors.IDEMPOTENCY_CONFLICT

## NAME

IDEMPOTENCY_CONFLICT — a request with the same idempotency key was already received but its payload differs from the original.

## SYNOPSIS

HTTP: `409` `Conflict`. Retryable: `no`.

## DESCRIPTION

The server has already processed (or is currently processing) a request with the same idempotency key, but the new request's body or parameters differ from the first one. Idempotency keys protect against duplicate submissions of identical requests; they do not allow changing the request after the fact.

Use a fresh idempotency key for a new operation. Do not reuse an existing key with a modified payload.

## SEE ALSO

- errors
- errors.CONFLICT
- errors.TX_CONFLICT
