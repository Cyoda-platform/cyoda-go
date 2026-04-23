---
topic: errors.FORBIDDEN
title: "FORBIDDEN — caller lacks required role or permission"
stability: stable
see_also:
  - errors
  - errors.UNAUTHORIZED
---

# errors.FORBIDDEN

## NAME

FORBIDDEN — the authenticated caller does not have the role or permission required to perform the operation.

## SYNOPSIS

HTTP: `403` `Forbidden`. Retryable: `no`.

## DESCRIPTION

The request was authenticated successfully but the caller's JWT claims do not include the role required by the endpoint (for example, `admin` is required for administrative operations). Tenant mismatch — where the caller's tenant does not match the resource — also produces this error.

Obtain a token with the appropriate role before retrying. Do not retry with the same token.

## SEE ALSO

- errors
- errors.UNAUTHORIZED
