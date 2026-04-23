---
topic: errors.ENTITY_NOT_FOUND
title: "ENTITY_NOT_FOUND — entity does not exist"
stability: stable
see_also:
  - errors
  - errors.MODEL_NOT_FOUND
---

# errors.ENTITY_NOT_FOUND

## NAME

ENTITY_NOT_FOUND — the requested entity does not exist or is not accessible to the caller.

## SYNOPSIS

HTTP: `404` `Not Found`. Retryable: `no`.

## DESCRIPTION

No entity with the given ID exists in the tenant's data store, or the entity existed at a point-in-time that precedes the requested snapshot. Also returned for audit log lookups when the specified event or message cannot be found.

Verify the entity ID and tenant context. Point-in-time lookups require a timestamp within the entity's history.

## SEE ALSO

- errors
- errors.MODEL_NOT_FOUND
