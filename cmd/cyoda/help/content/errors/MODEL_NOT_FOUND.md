---
topic: errors.MODEL_NOT_FOUND
title: "MODEL_NOT_FOUND — entity model does not exist"
stability: stable
see_also:
  - errors
  - errors.ENTITY_NOT_FOUND
  - errors.MODEL_NOT_LOCKED
---

# errors.MODEL_NOT_FOUND

## NAME

MODEL_NOT_FOUND — the referenced entity model (schema) does not exist.

## SYNOPSIS

HTTP: `404` `Not Found`. Retryable: `no`.

## DESCRIPTION

The entity type or model name specified in the request does not exist in the tenant's model registry. Occurs when creating entities with an unknown type, importing data that references a missing model, or performing model lifecycle transitions on a model ID that does not exist.

Not retryable. Entity creation requires the model to exist and be registered in the tenant's registry.

## SEE ALSO

- errors
- errors.ENTITY_NOT_FOUND
- errors.MODEL_NOT_LOCKED
