---
topic: errors.WORKFLOW_NOT_FOUND
title: "WORKFLOW_NOT_FOUND — workflow definition does not exist"
stability: stable
see_also:
  - errors
  - errors.TRANSITION_NOT_FOUND
  - errors.WORKFLOW_FAILED
---

# errors.WORKFLOW_NOT_FOUND

## NAME

WORKFLOW_NOT_FOUND — the workflow definition referenced by the entity model or the request does not exist.

## SYNOPSIS

HTTP: `404` `Not Found`. Retryable: `no`.

## DESCRIPTION

Entity models reference a workflow by name to govern state transitions. This error is returned when the named workflow cannot be found in the tenant's workflow registry, typically during entity type registration or when a model references a workflow that was deleted.

Register the workflow before associating it with an entity model. Verify the workflow name and tenant context.

## SEE ALSO

- errors
- errors.TRANSITION_NOT_FOUND
- errors.WORKFLOW_FAILED
