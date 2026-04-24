---
topic: errors.HELP_TOPIC_NOT_FOUND
title: "HELP_TOPIC_NOT_FOUND — help topic not found"
stability: stable
see_also:
  - errors
---

# errors.HELP_TOPIC_NOT_FOUND

## NAME

HELP_TOPIC_NOT_FOUND — requested help topic does not exist.

## SYNOPSIS

HTTP: `404 Not Found`. Retryable: no.

## DESCRIPTION

Returned by `GET {ContextPath}/help/{topic}` when `{topic}` is well-formed (matches `[A-Za-z0-9._-]+`) but does not resolve to any topic in the tree. Clients should `GET {ContextPath}/help` to discover available topic paths.

## SEE ALSO

- errors
