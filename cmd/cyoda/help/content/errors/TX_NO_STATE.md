---
topic: errors.TX_NO_STATE
title: "TX_NO_STATE — transaction has no state record"
stability: stable
see_also:
  - errors
  - errors.TX_REQUIRED
  - errors.TX_COORDINATOR_NOT_CONFIGURED
  - errors.TRANSACTION_NOT_FOUND
---

# errors.TX_NO_STATE

## NAME

TX_NO_STATE — the transaction coordinator cannot find a state record for the given transaction ID.

## SYNOPSIS

HTTP: `404` `Not Found`. Retryable: `no`.

## DESCRIPTION

The two-phase commit coordinator tracks per-transaction state (prepared, committed, aborted). This error is returned when a commit or abort instruction references a transaction for which no state record exists, usually because the transaction was never prepared or was already cleaned up.

Do not retry. Verify the transaction lifecycle: ensure `prepare` was called before `commit` or `abort`. If the state was cleaned up prematurely, review the coordinator's state retention configuration.

## SEE ALSO

- errors
- errors.TX_REQUIRED
- errors.TX_COORDINATOR_NOT_CONFIGURED
- errors.TRANSACTION_NOT_FOUND
