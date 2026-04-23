---
topic: errors
title: "cyoda error reference"
stability: stable
see_also:
  - openapi
---

# errors

## NAME

errors — error model and code catalogue.

## SYNOPSIS

REST responses use RFC 9457 Problem Details:

```json
{
  "type": "about:blank",
  "title": "Not Found",
  "status": 404,
  "detail": "ENTITY_NOT_FOUND: entity id=abc not found",
  "instance": "/api/v1/entities/abc",
  "properties": {
    "errorCode": "ENTITY_NOT_FOUND",
    "retryable": false
  }
}
```

gRPC responses carry `code`, `message`, and `retryable` inside the error sub-object of the CloudEvent response payload, following the standard gRPC error model.

## DESCRIPTION

Every error response from the Cyoda REST API carries a structured `errorCode` in the `properties` object. Use this code — not the HTTP status alone — for programmatic error handling, as multiple codes may share the same HTTP status.

The `retryable` property is present and `true` only when the operation is safe to retry as-is (e.g., transient cluster conditions). When absent or `false`, fix the request or system state before retrying.

5xx responses include a `ticket` UUID for server-side log correlation. Share this value when reporting issues.

See subtopics for each code:

- `errors.BAD_REQUEST`
- `errors.CLUSTER_NODE_NOT_REGISTERED`
- `errors.COMPUTE_MEMBER_DISCONNECTED`
- `errors.CONFLICT`
- `errors.DISPATCH_FORWARD_FAILED`
- `errors.DISPATCH_TIMEOUT`
- `errors.ENTITY_NOT_FOUND`
- `errors.EPOCH_MISMATCH`
- `errors.FORBIDDEN`
- `errors.HELP_TOPIC_NOT_FOUND`
- `errors.IDEMPOTENCY_CONFLICT`
- `errors.MODEL_NOT_FOUND`
- `errors.MODEL_NOT_LOCKED`
- `errors.NO_COMPUTE_MEMBER_FOR_TAG`
- `errors.NOT_IMPLEMENTED`
- `errors.POLYMORPHIC_SLOT`
- `errors.SEARCH_JOB_ALREADY_TERMINAL`
- `errors.SEARCH_JOB_NOT_FOUND`
- `errors.SEARCH_RESULT_LIMIT`
- `errors.SEARCH_SHARD_TIMEOUT`
- `errors.SERVER_ERROR`
- `errors.TRANSACTION_EXPIRED`
- `errors.TRANSACTION_NODE_UNAVAILABLE`
- `errors.TRANSACTION_NOT_FOUND`
- `errors.TRANSITION_NOT_FOUND`
- `errors.TX_CONFLICT`
- `errors.TX_COORDINATOR_NOT_CONFIGURED`
- `errors.TX_NO_STATE`
- `errors.TX_REQUIRED`
- `errors.UNAUTHORIZED`
- `errors.VALIDATION_FAILED`
- `errors.WORKFLOW_FAILED`
- `errors.WORKFLOW_NOT_FOUND`

## SEE ALSO

- openapi
