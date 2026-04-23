---
topic: errors.CLUSTER_NODE_NOT_REGISTERED
title: "CLUSTER_NODE_NOT_REGISTERED — target node is not in the cluster registry"
stability: stable
see_also:
  - errors
  - errors.COMPUTE_MEMBER_DISCONNECTED
---

# errors.CLUSTER_NODE_NOT_REGISTERED

## NAME

CLUSTER_NODE_NOT_REGISTERED — the target cluster node has not registered itself with the gossip registry.

## SYNOPSIS

HTTP: `503` `Service Unavailable`. Retryable: `yes`.

## DESCRIPTION

The request was routed to or requires a specific cluster node that is not present in the registry. This typically occurs during startup, rolling restarts, or after a node failure before the gossip layer has converged.

Retry with exponential backoff. If the error persists beyond the cluster's expected convergence window, inspect node health and cluster membership configuration.

## SEE ALSO

- errors
- errors.COMPUTE_MEMBER_DISCONNECTED
