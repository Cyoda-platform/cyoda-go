---
topic: errors.DISPATCH_FORWARD_FAILED
title: "DISPATCH_FORWARD_FAILED — inter-node dispatch forwarding error"
stability: stable
see_also:
  - errors
  - errors.DISPATCH_TIMEOUT
  - errors.COMPUTE_MEMBER_DISCONNECTED
---

# errors.DISPATCH_FORWARD_FAILED

## NAME

DISPATCH_FORWARD_FAILED — forwarding a processor or criteria dispatch request to a peer node failed.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `yes`.

## DESCRIPTION

The cluster dispatcher attempted to forward a processor invocation or criteria evaluation to a peer node but the HTTP call to that peer failed (network error, peer crash, or connection refused). The operation has not been executed on the target node.

Retryable. Persistent failures indicate inter-node network or peer node health issues.

## SEE ALSO

- errors
- errors.DISPATCH_TIMEOUT
- errors.COMPUTE_MEMBER_DISCONNECTED
