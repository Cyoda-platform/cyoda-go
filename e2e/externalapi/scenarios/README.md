# External API Scenario Dictionary

Language-agnostic test-suite description derived from the Kotlin integration
tests under `integration-tests/src/test` and
`tree-node/tree-node-integration-tests/src/test`. Purpose: allow an external
client to drive a running Cyoda environment over REST/gRPC and assert that
entity saving, retrieval and associated model mutations behave as the
internal tests assert.

Scope:

- Entity ingestion (single, list, multi-model collection).
- Entity update (transition, loopback, no-change transition, nested JSON).
- Model lifecycle (register, lock/unlock, change-level, delete, list).
- Workflow import/export (REPLACE, ACTIVATE, MERGE) and FSM semantics.
- Externalized processors/criteria (sync/async, retry, crossover timeouts).
- Point-in-time reads, change-log metadata, transaction-scoped reads.
- Concurrency and multi-node ingestion.
- Edge message save/delete.
- Negative-validation cases (unlocked model, incompatible types, not-found,
  validation errors).
- Numeric representation edge cases (DOUBLE boundaries, BIG_DECIMAL/UNBOUND_DECIMAL
  precision, BIG_INTEGER/UNBOUND_INTEGER, ParsingSpec scope narrowing,
  polymorphic fields).
- Polymorphism (mixed scalar/object at same path, heterogeneous TreeNode
  siblings, sealed-hierarchy arrays, Trino queries on polymorphic scalars,
  rejection of ill-typed conditions).

Out of scope (explicitly excluded): authentication, tenancy, statistics-only
read paths, Trino/search-only read paths, in-process performance tests, any
test with no external REST/gRPC surface.

## Files

- `00-endpoints.yaml`           — canonical REST/gRPC endpoint catalogue.
- `01-model-lifecycle.yaml`     — create/upsert/lock/unlock/delete/list.
- `02-change-level-governance.yaml` — changeLevel transitions and compatibility.
- `03-entity-ingestion-single.yaml` — create single entity / all-fields model.
- `04-entity-ingestion-collection.yaml` — create across multiple models.
- `05-entity-update.yaml`       — transition/loopback/no-change/nested updates.
- `06-entity-delete.yaml`       — delete single / by-model / by-condition.
- `07-point-in-time-and-changelog.yaml` — pointInTime, transactionId, changes.
- `08-workflow-import-export.yaml` — workflow import modes + FSM round-trip.
- `09-workflow-externalization.yaml` — externalized processors/criteria.
- `10-concurrency-and-multinode.yaml` — concurrent saves, multi-node client.
- `11-edge-message.yaml`        — edge-message save/delete, blob payload.
- `12-negative-validation.yaml` — error paths for ingestion/model mutations.
- `13-numeric-types.yaml`       — numeric landing (DOUBLE/BIG_DECIMAL/UNBOUND_DECIMAL/BIG_INTEGER),
                                  `ParsingSpec` scope narrowing, polymorphic fields,
                                  cross-type search compatibility.
- `14-polymorphism.yaml`        — mixed scalar/object at one path, heterogeneous
                                  TreeNode siblings, PolymorphicValue/Timestamp
                                  sealed-hierarchy arrays, Trino search on
                                  polymorphic scalars, invalid-type rejections.

## Scenario schema

Each YAML file contains a top-level `scenarios:` list. A single scenario has
the following keys:

```yaml
id: <group-slug>/<NN>-<scenario-slug>     # stable identifier
name: <human title>
source_test: <relative-path>#<test-method>
description: <one or two sentence summary>
preconditions:                             # optional, ordered
  - <textual precondition>
data:                                      # payloads and model refs
  model:
    name: <string>
    version: <int>
    change_level: <ARRAY_LENGTH|ARRAY_ELEMENTS|TYPE|STRUCTURAL|null>
  sample_json: |                           # JSON used to register the model
    { ... }
  payload_json: |                          # JSON used to create/update entity
    { ... }
workflow:                                  # optional; null for default workflow
  import_mode: <REPLACE|ACTIVATE|MERGE>
  definition_json: |
    { "workflows": [ ... ] }
steps:                                     # ordered; each step is an op
  - action: <see below>
    endpoint:
      rest: <METHOD> <path>
      grpc: <service.rpc | n/a>
    body_ref: <data.sample_json | data.payload_json | inline>
    capture: <symbolic name, e.g. entityId, transactionId>
assertions:                                # ordered, post-conditions
  - type: <entity_count|entity_equals_json|state_equals|transaction_status
           |changelog_size|model_change_log_size|http_status|error_class>
    ...
negative: <true|false>                     # optional; default false
expected_error:                            # required when negative: true
  http_status: <int>
  class_or_message_pattern: <string>
```

## Canonical action vocabulary

- `create_model_from_sample`     — register a new model from sample JSON.
- `update_model_from_sample`     — upsert/merge model from sample JSON.
- `export_model`                 — fetch model schema view.
- `delete_model`                 — delete the model.
- `lock_model` / `unlock_model`  — toggle lock state.
- `set_change_level`             — adjust allowed mutations.
- `list_models`                  — list all models for the tenant.
- `import_workflow`              — import one or more workflows for a model.
- `export_workflow`              — fetch current workflow definitions.
- `create_entity`                — create a single entity of a given model.
- `create_entities_collection`   — create across models in one call.
- `update_entity_loopback`       — update with self-transition.
- `update_entity_transition`     — update and advance workflow transition.
- `update_entities_collection`   — update many in one call.
- `get_entity` / `get_all_entities`
- `get_entity_changes_metadata`  — fetch change history.
- `delete_entity`                — delete single by id.
- `delete_entities_by_model`     — delete all for (name, version).
- `delete_entities_by_condition` — delete by query, optionally at pointInTime.
- `send_edge_message`            — save EdgeMessage with blob payload.
- `delete_edge_message` / `delete_edge_messages_collection`.
- `join_calculation_member`      — externalizer gRPC bidi join.
- `send_calculation_response`    — respond to processor/criterion request.

## Conventions

- `{name}` and `{version}` are path variables on the model & entity endpoints.
- `{format}` on entity endpoints is `JSON` in all tests.
- `{changeLevel}` is one of `ARRAY_LENGTH | ARRAY_ELEMENTS | TYPE | STRUCTURAL`.
- `{transition}` is the workflow transition name (`UPDATE` is the
  loopback-compatible default). Absence of `{transition}` selects loopback.
- Authentication headers are assumed present but ignored by this dictionary.
- `pointInTime` is an ISO-8601 offset timestamp (`OffsetDateTime`).
- All negative scenarios assert HTTP 4xx/5xx with an error body containing
  the documented class or message pattern.

## Readback equivalence helper

Several scenarios use `entity_equals_json`. The assertion passes when:

1. Fetching the entity (`GET /entity/JSON/{id}` or gRPC `entityRead`).
2. Reconstructing the JSON tree from the stored entity.
3. Comparing against the expected JSON with `assertThatJson(...).isEqualTo(...)`
   semantics (order-insensitive for objects, order-sensitive for arrays).
