---
topic: errors.BAD_REQUEST
title: "BAD_REQUEST — malformed or invalid request"
stability: stable
see_also:
  - errors
  - errors.VALIDATION_FAILED
---

# errors.BAD_REQUEST

## NAME

BAD_REQUEST — the request body, query parameter, or header is malformed or structurally invalid.

## SYNOPSIS

HTTP: `400` `Bad Request`. Retryable: `no`.

## DESCRIPTION

Fired when the server cannot parse or structurally process the incoming request. Common triggers include invalid JSON, missing required fields, unsupported format specifiers, or mutually exclusive parameters being set simultaneously.

Not retryable. The same request produces the same error.

## SEE ALSO

- errors
- errors.VALIDATION_FAILED
