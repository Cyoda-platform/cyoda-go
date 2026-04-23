---
topic: errors.NOT_IMPLEMENTED
title: "NOT_IMPLEMENTED — endpoint is not yet implemented"
stability: stable
see_also:
  - errors
---

# errors.NOT_IMPLEMENTED

## NAME

NOT_IMPLEMENTED — the requested endpoint or operation exists in the API contract but has not yet been implemented in this version.

## SYNOPSIS

HTTP: `501` `Not Implemented`. Retryable: `no`.

## DESCRIPTION

The route is defined and accepted by the server but the handler returns this error because the feature is pending implementation. This is distinct from a `404` — the endpoint exists, it simply has no functional implementation yet.

Check the release notes or roadmap for when the feature is expected. Do not retry; the response will be the same until a new version is deployed.

## SEE ALSO

- errors
