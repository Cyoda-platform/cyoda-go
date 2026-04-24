---
topic: errors.UNAUTHORIZED
title: "UNAUTHORIZED — authentication required or token invalid"
stability: stable
see_also:
  - errors
  - errors.FORBIDDEN
---

# errors.UNAUTHORIZED

## NAME

UNAUTHORIZED — the request does not include valid authentication credentials or the provided token failed verification.

## SYNOPSIS

HTTP: `401` `Unauthorized`. Retryable: `no`.

## DESCRIPTION

Returned when the `Authorization` header is missing, the bearer token is expired, the token signature is invalid, or the token was issued by an untrusted issuer. Also returned when a request reaches a protected route with no identity context established by the auth middleware.

Not retryable with the same token. A fresh `Authorization: Bearer <token>` header is required.

## SEE ALSO

- errors
- errors.FORBIDDEN
