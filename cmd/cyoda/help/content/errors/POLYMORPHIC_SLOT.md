---
topic: errors.POLYMORPHIC_SLOT
title: "POLYMORPHIC_SLOT — payload does not satisfy polymorphic slot constraint"
stability: stable
see_also:
  - errors
  - errors.BAD_REQUEST
  - errors.VALIDATION_FAILED
---

# errors.POLYMORPHIC_SLOT

## NAME

POLYMORPHIC_SLOT — the entity payload does not satisfy the model's polymorphic slot definition.

## SYNOPSIS

HTTP: `400` `Bad Request`. Retryable: `no`.

## DESCRIPTION

A model can define polymorphic slots — fields whose schema varies based on a discriminator value. This error fires when the payload's discriminator selects a variant whose schema the provided data fails to match, or when the discriminator value itself is unrecognised.

Review the model's polymorphic slot definitions and ensure the payload's discriminator field matches one of the registered variants. Fix the payload before retrying.

## SEE ALSO

- errors
- errors.BAD_REQUEST
- errors.VALIDATION_FAILED
