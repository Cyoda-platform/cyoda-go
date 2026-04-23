---
topic: errors.TRANSITION_NOT_FOUND
title: "TRANSITION_NOT_FOUND — workflow transition does not exist"
stability: stable
see_also:
  - errors
  - errors.WORKFLOW_NOT_FOUND
  - errors.WORKFLOW_FAILED
---

# errors.TRANSITION_NOT_FOUND

## NAME

TRANSITION_NOT_FOUND — the requested workflow transition is not defined for the entity's current state.

## SYNOPSIS

HTTP: `404` `Not Found`. Retryable: `no`.

## DESCRIPTION

Entity workflow state machines define explicit transitions between states. This error fires when a transition is triggered that does not exist in the model's workflow definition for the entity's current state. It can also occur when the transition name is misspelled or when the entity is in a terminal state that allows no further transitions.

Review the workflow definition for the entity model to determine which transitions are valid from the current state. Correct the transition name or verify the entity's current state before retrying.

## SEE ALSO

- errors
- errors.WORKFLOW_NOT_FOUND
- errors.WORKFLOW_FAILED
