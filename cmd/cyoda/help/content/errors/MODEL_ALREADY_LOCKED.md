---
topic: errors.MODEL_ALREADY_LOCKED
title: "MODEL_ALREADY_LOCKED — model is already in the LOCKED state"
stability: stable
see_also:
  - errors
  - errors.MODEL_NOT_FOUND
  - errors.MODEL_NOT_LOCKED
  - errors.CONFLICT
---

# errors.MODEL_ALREADY_LOCKED

## NAME

MODEL_ALREADY_LOCKED — an admin operation requested the model be in the `UNLOCKED` state, but the model is already `LOCKED`.

## SYNOPSIS

HTTP: `409` `Conflict`. Retryable: `no`.

## DESCRIPTION

Returned when a lock-transition request (e.g. `POST /model/{name}/{version}/lock`) is issued against a model that is already `LOCKED`. The relock is rejected because the caller's expected pre-state (`UNLOCKED`) does not match the actual state (`LOCKED`).

Not retryable. To proceed, either accept the existing lock or unlock the model first via `POST /model/{name}/{version}/unlock`.

## SEE ALSO

- errors
- errors.MODEL_NOT_FOUND
- errors.MODEL_NOT_LOCKED
- errors.CONFLICT
