---
topic: errors.WORKFLOW_FAILED
title: "WORKFLOW_FAILED — workflow processor returned an error"
stability: stable
see_also:
  - errors
  - errors.WORKFLOW_NOT_FOUND
  - errors.TRANSITION_NOT_FOUND
---

# errors.WORKFLOW_FAILED

## NAME

WORKFLOW_FAILED — a workflow processor or guard condition returned a failure during entity state transition.

## SYNOPSIS

HTTP: `400` `Bad Request`. Retryable: `no`.

## DESCRIPTION

During an entity create or transition operation the associated workflow processors (pre-processors, post-processors) or guard conditions ran but one of them signalled failure. The failure message from the processor is included in the error detail.

Inspect the processor's error message in the response detail. The failure originates from application logic in the processor — fix the data, the processor implementation, or the workflow configuration. Do not retry unless the underlying condition that caused the failure has changed.

## SEE ALSO

- errors
- errors.WORKFLOW_NOT_FOUND
- errors.TRANSITION_NOT_FOUND
