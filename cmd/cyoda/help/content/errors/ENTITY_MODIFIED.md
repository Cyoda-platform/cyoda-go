---
topic: errors.ENTITY_MODIFIED
title: "ENTITY_MODIFIED — If-Match precondition failed; entity changed since last read"
stability: stable
see_also:
  - errors
  - errors.CONFLICT
  - errors.IDEMPOTENCY_CONFLICT
  - errors.EPOCH_MISMATCH
---

# errors.ENTITY_MODIFIED

## NAME

ENTITY_MODIFIED — an `If-Match`-guarded entity update was rejected because the supplied transaction-ID no longer matches the entity's current version.

## SYNOPSIS

HTTP: `412` `Precondition Failed`. Retryable: `no`.

## DESCRIPTION

When an entity update request carries an `If-Match` header, the server requires the supplied transaction ID to equal the entity's current `meta.transactionId`. A mismatch means another writer has updated the entity since the caller's last read. The optimistic-concurrency guard rejects the update rather than silently overwrite.

The `entityId` property in the problem-detail body identifies the conflicting entity.

Not retryable in the protocol sense — replaying the same payload with the same `If-Match` value will fail again.

## RECOVERY

1. **Re-read the entity:** `GET /api/entity/{entityId}`. The response envelope's `meta.transactionId` is the entity's current version.
2. **Reconcile your change against the current state.** Whatever the concurrent writer changed is now the baseline; merge or override it intentionally rather than blindly replaying your previous payload.
3. **Re-submit the update with the fresh `If-Match`:**

   ```
   curl -X PUT \
     -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -H "If-Match: <meta.transactionId from step 1>" \
     -d '<reconciled payload>' \
     "http://localhost:8080/api/entity/JSON/{entityId}[/{transition}]"
   ```

A second `412 ENTITY_MODIFIED` on retry means another writer raced you again. Either accept the loss (drop your change), back off and retry the read-reconcile-write loop with jitter, or escalate to a coarser locking strategy (lock the model, or coordinate writers out of band) — naive looping will livelock under contention.

Omitting the `If-Match` header on the next update bypasses the precondition entirely and produces a last-writer-wins update; do this only when the desired semantics genuinely tolerate clobbering concurrent changes.

## SEE ALSO

- errors
- errors.CONFLICT
- errors.IDEMPOTENCY_CONFLICT
- errors.EPOCH_MISMATCH
