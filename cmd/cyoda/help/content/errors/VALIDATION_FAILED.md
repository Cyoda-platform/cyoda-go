---
topic: errors.VALIDATION_FAILED
title: "VALIDATION_FAILED — payload fails model schema validation"
stability: stable
see_also:
  - errors
  - errors.BAD_REQUEST
  - errors.POLYMORPHIC_SLOT
---

# errors.VALIDATION_FAILED

## NAME

VALIDATION_FAILED — the request payload is structurally valid JSON but fails the model's schema or workflow validation rules.

## SYNOPSIS

HTTP: `422` `Unprocessable Entity`. Retryable: `no`.

## DESCRIPTION

Unlike `BAD_REQUEST` (which covers parse failures), this error is returned when the payload is parseable but violates the registered model schema — for example, a required field is missing, a value is out of the allowed range, or a workflow guard condition is not satisfied. The error detail includes the specific validation failure.

Correct the payload according to the model's schema constraints. Review the model definition for required fields, type constraints, and workflow guard conditions.

## SEE ALSO

- errors
- errors.BAD_REQUEST
- errors.POLYMORPHIC_SLOT
