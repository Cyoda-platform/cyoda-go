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

Entity creation and bulk write operations require the model to be in the `LOCKED` lifecycle state. If the model is still in `DRAFT` or has been unlocked for editing, writes against it are rejected to prevent schema changes from affecting in-flight data.

Lock the model via the model lifecycle API before writing entities. If the model is intentionally being edited, complete the edit and lock it again.

## SEE ALSO

- errors
- errors.MODEL_NOT_FOUND
- errors.CONFLICT
