---
topic: errors.ENTITY_MODIFIED
title: "ENTITY_MODIFIED — If-Match precondition failed; entity changed since last read"
stability: stable
see_also:
  - errors
  - errors.CONFLICT
  - errors.IDEMPOTENCY_CONFLICT
---

# errors.ENTITY_MODIFIED

## NAME

ENTITY_MODIFIED — an `If-Match`-guarded entity update was rejected because the supplied transaction-ID no longer matches the entity's current version.

## SYNOPSIS

HTTP: `412` `Precondition Failed`. Retryable: `no`.

## DESCRIPTION

When an entity update request carries an `If-Match` header, the server requires the supplied transaction ID to equal the entity's current version. A mismatch means another writer has updated the entity since the caller's last read. The optimistic-concurrency guard rejects the update rather than silently overwrite.

The `entityId` property in the problem-detail body identifies the conflicting entity.

Not retryable in the protocol sense — replaying the same payload with the same `If-Match` value will fail again. The caller must re-read the entity to obtain a current transaction ID, reconcile the desired change against the new state, and re-submit.

## SEE ALSO

- errors
- errors.CONFLICT
- errors.IDEMPOTENCY_CONFLICT
