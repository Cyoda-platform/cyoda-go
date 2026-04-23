---
topic: errors.SEARCH_RESULT_LIMIT
title: "SEARCH_RESULT_LIMIT — search result set exceeds the allowed limit"
stability: stable
see_also:
  - errors
  - errors.SEARCH_JOB_NOT_FOUND
  - errors.SEARCH_SHARD_TIMEOUT
---

# errors.SEARCH_RESULT_LIMIT

## NAME

SEARCH_RESULT_LIMIT — the search query matched more results than the server-enforced maximum page or result set size.

## SYNOPSIS

HTTP: `400` `Bad Request`. Retryable: `no`.

## DESCRIPTION

The server imposes an upper bound on the number of results returned per page and per job to protect cluster resources. When a request exceeds this limit — either by requesting too large a page size or by the matched result count exceeding the cap — this error is returned.

Reduce the `pageSize` parameter or apply more selective filter conditions to narrow the result set. Use cursor-based pagination if you need to iterate over large result sets.

## SEE ALSO

- errors
- errors.SEARCH_JOB_NOT_FOUND
- errors.SEARCH_SHARD_TIMEOUT
