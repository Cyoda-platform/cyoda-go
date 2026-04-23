---
topic: errors.SERVER_ERROR
title: "SERVER_ERROR — unexpected internal server error"
stability: stable
see_also:
  - errors
---

# errors.SERVER_ERROR

## NAME

SERVER_ERROR — an unexpected error occurred on the server that prevented the request from being fulfilled.

## SYNOPSIS

HTTP: `500` `Internal Server Error`. Retryable: `yes` (with caution).

## DESCRIPTION

The server encountered an unclassified internal error. The response body contains a `ticket` UUID that correlates with the server-side error log. No internal details are exposed in the response.

Record the `ticket` value from the response for support escalation. Simple reads may be retried safely; writes should be treated as potentially applied and verified before retrying to avoid duplicates.

## SEE ALSO

- errors
