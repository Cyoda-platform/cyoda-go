---
topic: errors.DISPATCH_TIMEOUT
title: "DISPATCH_TIMEOUT — dispatch to compute member timed out"
stability: stable
see_also:
  - errors
  - errors.DISPATCH_FORWARD_FAILED
  - errors.NO_COMPUTE_MEMBER_FOR_TAG
---

# errors.DISPATCH_TIMEOUT

## NAME

DISPATCH_TIMEOUT — the dispatcher waited longer than the configured timeout for a compute member to accept and complete a task.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `yes`.

## DESCRIPTION

A workflow processor or criteria evaluation was dispatched to a compute member but the response did not arrive within the dispatch timeout window. The underlying task may or may not have completed on the remote node.

Retry the request. If timeouts recur, check compute member load, network latency, and the `CYODA_DISPATCH_TIMEOUT` configuration value.

## SEE ALSO

- errors
- errors.DISPATCH_FORWARD_FAILED
- errors.NO_COMPUTE_MEMBER_FOR_TAG
