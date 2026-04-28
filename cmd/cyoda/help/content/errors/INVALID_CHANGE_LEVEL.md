---
topic: errors.INVALID_CHANGE_LEVEL
title: "INVALID_CHANGE_LEVEL — set-change-level supplied value is not a known ChangeLevel"
stability: stable
see_also:
  - errors
  - errors.BAD_REQUEST
  - models
---

# errors.INVALID_CHANGE_LEVEL

## NAME

INVALID_CHANGE_LEVEL — `POST /model/{name}/{version}/changeLevel/{changeLevel}` was called with a value that is not one of the four accepted `ChangeLevel` enum members.

## SYNOPSIS

HTTP: `400` `Bad Request`. Retryable: `no`.

## DESCRIPTION

The `changeLevel` path segment must be exactly one of: `ARRAY_LENGTH`, `ARRAY_ELEMENTS`, `TYPE`, or `STRUCTURAL`. Comparison is case-sensitive. Any other value (including the empty string, lower-cased variants, or typos) yields this error.

The problem-detail body carries the following properties so callers can branch on the precondition without scraping the message string:

- `entityName` — the model's name from the URL path.
- `entityVersion` — the model's integer version from the URL path.
- `suppliedValue` — the offending `changeLevel` string as supplied by the caller.
- `validValues` — the canonical accepted values, in the hierarchy order documented in `models` (most-restrictive to most-permissive): `ARRAY_LENGTH`, `ARRAY_ELEMENTS`, `TYPE`, `STRUCTURAL`.

Not retryable: the request will keep failing until the caller sends a valid value. Re-issue the request with one of `validValues`.

## SEE ALSO

- errors
- errors.BAD_REQUEST
- models
