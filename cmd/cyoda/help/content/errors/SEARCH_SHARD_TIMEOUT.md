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

Distributed search fans out to multiple shards in parallel. If any shard does not return results before the search timeout expires, the job is marked failed and this error is returned. This can occur under high load, during partial cluster degradation, or with very expensive queries.

Retry the search request. If timeouts are frequent, consider narrowing the query, reducing the result limit, or increasing the search timeout via cluster configuration.

## SEE ALSO

- errors
- errors.SEARCH_JOB_NOT_FOUND
- errors.DISPATCH_TIMEOUT
