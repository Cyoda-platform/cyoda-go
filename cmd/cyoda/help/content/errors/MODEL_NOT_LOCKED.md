---
topic: errors.MODEL_NOT_LOCKED
title: "MODEL_NOT_LOCKED — model must be locked before writing entities"
stability: stable
see_also:
  - errors
  - errors.MODEL_NOT_FOUND
  - errors.CONFLICT
---

# errors.MODEL_NOT_LOCKED

## NAME

MODEL_NOT_LOCKED — the entity model exists but is not in a locked state, which is required before entities of that type can be created or updated.

## SYNOPSIS

HTTP: `409` `Conflict`. Retryable: `no`.

## DESCRIPTION

Entity creation and bulk write operations require the model to be in the `LOCKED` lifecycle state. Models in `DRAFT` or unlocked-for-editing state reject writes to prevent schema changes from affecting in-flight data.

Not retryable. Entity writes require the model to be in `LOCKED` state.

## SEE ALSO

- errors
- errors.MODEL_NOT_FOUND
- errors.CONFLICT
