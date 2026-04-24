---
topic: workflows
title: "workflows — state machine definitions"
stability: stable
see_also:
  - models
  - crud
  - grpc
  - errors.TRANSITION_NOT_FOUND
  - errors.WORKFLOW_NOT_FOUND
  - errors.WORKFLOW_FAILED
  - errors.NO_COMPUTE_MEMBER_FOR_TAG
  - errors.COMPUTE_MEMBER_DISCONNECTED
---

# workflows

## NAME

workflows — workflow state machine definitions: states, transitions, processors, and criteria.

## SYNOPSIS

```
POST  /api/model/{entityName}/{modelVersion}/workflow/import
GET   /api/model/{entityName}/{modelVersion}/workflow/export
```

Context path prefix is `CYODA_CONTEXT_PATH` (default `/api`). All endpoints require `Authorization: Bearer <token>` except when `CYODA_IAM_MODE=mock`.

## DESCRIPTION

A workflow definition is a named finite state machine attached to an entity model. Workflows are stored per model reference `(entityName, modelVersion)`. A model may have multiple workflow definitions; the engine selects the matching one per entity using the workflow-level `criterion` field evaluated at entity creation time. When no `criterion` matches, the engine uses the default built-in workflow.

The engine executes automatically after every entity write. It sets the initial state, evaluates automated transitions (cascade), and invokes processors on each transition. Manual transitions are triggered by the client via `PUT /entity/{format}/{entityId}/{transition}`.

The engine enforces a per-state visit limit of 10 by default (configurable via `WithMaxStateVisits`) and an absolute cascade depth limit of 100 to prevent infinite loops. Static cycle detection runs at import time.

## WORKFLOW SCHEMA

**WorkflowDefinition** (element of the `workflows` array in import):

```json
{
  "version": "1",
  "name": "prize-lifecycle",
  "desc": "State machine for Nobel Prize entities",
  "initialState": "NEW",
  "active": true,
  "criterion": null,
  "states": {
    "NEW": {
      "transitions": [
        {
          "name": "APPROVE",
          "next": "APPROVED",
          "manual": true,
          "disabled": false,
          "criterion": null,
          "processors": [
            {
              "type": "EXTERNAL",
              "name": "notify-approval",
              "executionMode": "SYNC",
              "config": {
                "attachEntity": true,
                "calculationNodesTags": "approval-service",
                "responseTimeoutMs": 30000,
                "retryPolicy": "",
                "context": ""
              }
            }
          ]
        },
        {
          "name": "AUTO_VALIDATE",
          "next": "VALIDATED",
          "manual": false,
          "disabled": false,
          "criterion": {
            "type": "simple",
            "jsonPath": "$.year",
            "operatorType": "EQUALS",
            "value": "2024"
          },
          "processors": []
        }
      ]
    },
    "APPROVED": {
      "transitions": []
    },
    "VALIDATED": {
      "transitions": []
    }
  }
}
```

**WorkflowDefinition fields:**

- `version` — string — schema version tag (informational; not interpreted by the engine)
- `name` — string — unique within the model; the primary key for MERGE mode
- `desc` — string — optional description
- `initialState` — string — state assigned when the entity is first created; must exist in `states`
- `active` — boolean — when `false`, the engine skips this workflow during selection
- `criterion` — `Condition` JSON or `null` — evaluated against the entity at creation to select this workflow; `null` matches all entities
- `states` — object — map of state name → `StateDefinition`

**StateDefinition:**

- `transitions` — array of `TransitionDefinition` — may be empty

## TRANSITIONS

**TransitionDefinition fields:**

- `name` — string — transition name; used by the client in `PUT /entity/{format}/{entityId}/{name}` and in engine cascade
- `next` — string — target state; must exist in `states`
- `manual` — boolean — `true` means the transition requires an explicit client request; `false` means the engine evaluates it automatically in cascade
- `disabled` — boolean — when `true`, the engine skips this transition entirely
- `criterion` — `Condition` JSON or `null` — evaluated before executing the transition; `null` means always matches; the same Condition DSL as search (see `search` topic)
- `processors` — array of `ProcessorDefinition` — invoked sequentially on this transition

## PROCESSORS

**ProcessorDefinition fields:**

- `type` — string — processor type; `"EXTERNAL"` dispatches to a calculation node via gRPC
- `name` — string — logical processor name
- `executionMode` — string — `"SYNC"` (engine waits for the processor response before continuing) or `"ASYNC"` (fire-and-forget)
- `config` — `ProcessorConfig`

**ProcessorConfig fields:**

- `attachEntity` — boolean — when `true`, the full entity payload is sent to the processor
- `calculationNodesTags` — string — comma-separated tags for routing to registered calculation nodes; the engine selects a node that declares all required tags; returns `errors.NO_COMPUTE_MEMBER_FOR_TAG` if no node matches
- `responseTimeoutMs` — int64 — timeout in milliseconds for `SYNC` processor response; `0` means use node default
- `retryPolicy` — string — retry policy name (plugin/platform-defined); empty means no retry
- `context` — string — arbitrary string forwarded to the processor as context metadata

## CRITERIA

Criteria on workflows and transitions use the same `Condition` DSL as search. All four condition types are supported: `simple`, `lifecycle`, `group`, `array`. Criteria are evaluated in-memory against the entity's JSON payload and lifecycle metadata.

`simple` criteria match entity data fields via JSONPath. `lifecycle` criteria match `state`, `creationDate`, or `previousTransition` from entity metadata.

A `null` criterion on a workflow means the workflow matches any entity. A `null` criterion on a transition means the transition always fires (automated) or is always available (manual). When multiple automated transitions are eligible, the engine selects the first one whose criterion matches.

## IMPORT REQUEST

**POST /api/model/{entityName}/{modelVersion}/workflow/import**

- `entityName` (path): string
- `modelVersion` (path): int32

Request body (`application/json`):

```json
{
  "importMode": "MERGE",
  "workflows": [
    { ...WorkflowDefinition... }
  ]
}
```

- `importMode` — `"MERGE"` (default): incoming workflows overwrite existing ones by name; existing workflows not in the import are preserved. `"REPLACE"`: all existing workflows are discarded; only the incoming set is stored. `"ACTIVATE"`: incoming workflows replace same-named existing ones and are set `active=true`; existing workflows not in the import set are set `active=false`.
- `workflows` — array of `WorkflowDefinition`; all imported workflows are set `active=true` regardless of the `active` field in the body

Static validation runs before saving: definite infinite loops (cycles reachable only via automated transitions) cause `400 VALIDATION_FAILED`.

Response: `200 OK`, `application/json`:

```json
{"success": true}
```

## EXPORT RESPONSE

**GET /api/model/{entityName}/{modelVersion}/workflow/export**

Response: `200 OK`, `application/json`:

```json
{
  "entityName": "nobel-prize",
  "modelVersion": 1,
  "workflows": [
    { ...WorkflowDefinition... }
  ]
}
```

Returns `404 WORKFLOW_NOT_FOUND` when no workflows have been imported for the model.

## ENGINE EXECUTION

The workflow engine runs synchronously within the entity write transaction. The execution sequence for a CREATE:

1. Load workflow definitions for the model.
2. Evaluate each workflow's `criterion` against the entity; select the first match. If none match, use the built-in default workflow.
3. Set `entity.Meta.State = workflow.initialState`.
4. If a named transition was requested (by the client), execute it: evaluate `criterion`, invoke processors, set `entity.Meta.State = transition.next`.
5. Cascade: repeatedly scan the current state's transitions; for each automated (`manual=false`) non-disabled transition, evaluate `criterion`; if it matches, invoke processors and advance the state. Stop when no automated transition matches or the state has no automated transitions.
6. The engine records `StateMachineEvent` entries to the audit log under the entity's `transactionId`.

Per-state visit limit (default 10) and total cascade depth limit (100) are enforced to prevent infinite loops.

## ERRORS

- `errors.TRANSITION_NOT_FOUND` — `404` — named transition does not exist in the current state's workflow
- `errors.WORKFLOW_NOT_FOUND` — `404` — no workflows found for the model (export endpoint)
- `errors.WORKFLOW_FAILED` — workflow engine encountered an unrecoverable error during execution
- `errors.NO_COMPUTE_MEMBER_FOR_TAG` — no registered calculation node matches the required `calculationNodesTags`
- `errors.COMPUTE_MEMBER_DISCONNECTED` — a calculation node disconnected during processor dispatch
- `errors.VALIDATION_FAILED` — `400` — static cycle detection failed during workflow import

## EXAMPLES

**Import a workflow:**

```
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "importMode": "MERGE",
    "workflows": [
      {
        "version": "1",
        "name": "prize-lifecycle",
        "initialState": "NEW",
        "active": true,
        "states": {
          "NEW": {
            "transitions": [
              {
                "name": "APPROVE",
                "next": "APPROVED",
                "manual": true,
                "processors": []
              }
            ]
          },
          "APPROVED": {
            "transitions": []
          }
        }
      }
    ]
  }' \
  "http://localhost:8080/api/model/nobel-prize/1/workflow/import"
```

**Export workflows:**

```
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/model/nobel-prize/1/workflow/export"
```

**Trigger a manual transition:**

```
curl -s -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"category":"physics","year":"2024"}' \
  "http://localhost:8080/api/entity/JSON/74807f00-ed0d-11ee-a357-ae468cd3ed16/APPROVE"
```

**Replace all workflows:**

```
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "importMode": "REPLACE",
    "workflows": [
      {
        "version": "1",
        "name": "simple-wf",
        "initialState": "OPEN",
        "active": true,
        "states": {
          "OPEN": { "transitions": [] }
        }
      }
    ]
  }' \
  "http://localhost:8080/api/model/nobel-prize/1/workflow/import"
```

## SEE ALSO

- models
- crud
- grpc
- errors.TRANSITION_NOT_FOUND
- errors.WORKFLOW_NOT_FOUND
- errors.WORKFLOW_FAILED
- errors.NO_COMPUTE_MEMBER_FOR_TAG
- errors.COMPUTE_MEMBER_DISCONNECTED
