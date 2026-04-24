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

The request was routed to or requires a specific cluster node that is not present in the registry. Occurs during startup, rolling restarts, or after a node failure before the gossip layer has converged.

Retryable. Gossip convergence determines when the node becomes available. Persistence beyond the expected convergence window indicates a node health or cluster membership issue.

## SEE ALSO

- errors
- errors.COMPUTE_MEMBER_DISCONNECTED
