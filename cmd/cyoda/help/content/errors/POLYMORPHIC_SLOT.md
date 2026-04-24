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

Not retryable. The model's polymorphic slot definition determines valid discriminator values and their corresponding schemas.

## SEE ALSO

- errors
- errors.BAD_REQUEST
- errors.VALIDATION_FAILED
