---
topic: errors.EPOCH_MISMATCH
title: "EPOCH_MISMATCH — shard ownership epoch has changed"
stability: stable
see_also:
  - errors
  - errors.CONFLICT
  - errors.TX_CONFLICT
---

# errors.EPOCH_MISMATCH

## NAME

EPOCH_MISMATCH — a node attempted to write to a shard it no longer owns because the cluster epoch advanced.

## SYNOPSIS

HTTP: `409` `Conflict`. Retryable: `yes`.

## DESCRIPTION

Shard ownership is tracked by an epoch counter that increments whenever the cluster re-partitions. A write is rejected with this error when the writing node's cached epoch is stale — i.e., another node has since taken ownership of the shard. This prevents split-brain writes.

Retry the request; the client will be re-routed to the current shard owner. If the error recurs frequently, review cluster stability and the frequency of re-partitioning events.

## SEE ALSO

- errors
- errors.CONFLICT
- errors.TX_CONFLICT
