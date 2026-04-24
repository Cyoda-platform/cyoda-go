---
topic: errors.SEARCH_SHARD_TIMEOUT
title: "SEARCH_SHARD_TIMEOUT — a search shard did not respond in time"
stability: stable
see_also:
  - errors
  - errors.SEARCH_JOB_NOT_FOUND
  - errors.DISPATCH_TIMEOUT
---

# errors.SEARCH_SHARD_TIMEOUT

## NAME

SEARCH_SHARD_TIMEOUT — one or more search shards did not respond within the configured timeout, causing the search job to fail.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `yes`.

## DESCRIPTION

Distributed search fans out to multiple shards in parallel. If any shard does not return results before the search timeout expires, the job is marked failed and this error is returned. Occurs under high load, during partial cluster degradation, or with expensive queries.

Retryable. Frequent timeouts indicate that the query scope, result limit, or cluster configuration requires adjustment.

## SEE ALSO

- errors
- errors.SEARCH_JOB_NOT_FOUND
- errors.DISPATCH_TIMEOUT
