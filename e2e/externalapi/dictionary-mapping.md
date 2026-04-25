# External API Scenario Dictionary — cyoda-go mapping

Triage of all 100 scenarios in `e2e/externalapi/scenarios/` against the
current cyoda-go implementation. (The plan's estimate of ~85 was based on
an earlier draft; the authoritative count from `grep -cE "^\s*- id:"` is 100.)

Status vocabulary:

- `covered_by:<fn>` — already exists as a parity `Run*`.
- `new:<fn>` — implemented as part of tranche 1 (this PR).
- `pending:tranche-N` — planned for a later tranche; not implemented.
- `internal_only_skip` — tests platform internals not reachable via
  HTTPDriver (gRPC-only endpoint, internal facade call, or RSocket-only
  transport with no REST equivalent in this file).
- `shape_only_skip` — shape-only assertion better expressed as JSON
  Schema check than scenario run.
- `gap_on_our_side` — endpoint or capability missing in cyoda-go
  today; scenario cannot run. See `notes`.

---

## 01-model-lifecycle.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| model-lifecycle/01-register-model-from-sample | new:RunExternalAPI_01_01_RegisterModel | tranche 1 |
| model-lifecycle/02-upsert-model-extends-schema | new:RunExternalAPI_01_02_UpsertExtendsSchema | tranche 1 |
| model-lifecycle/03-upsert-model-with-incompatible-type | new:RunExternalAPI_01_03_UpsertIncompatibleType | tranche 1 |
| model-lifecycle/04-reregister-same-schema | new:RunExternalAPI_01_04_ReregisterIdempotent | tranche 1 |
| model-lifecycle/05-lock-model | new:RunExternalAPI_01_05_LockModel | tranche 1 |
| model-lifecycle/06-unlock-model | new:RunExternalAPI_01_06_UnlockModel | tranche 1 |
| model-lifecycle/07-lock-twice-is-rejected | gap_on_our_side (#128) | tranche 1 implemented and skipped under tranche-2's discover-and-compare rubric: cyoda-go emits the generic `CONFLICT` code while the dictionary requires `MODEL_ALREADY_LOCKED`. Test body and `LockModelRaw` helper remain in place — flipping the `t.Skip` is the close-the-issue checklist item. |
| model-lifecycle/08-delete-model | new:RunExternalAPI_01_08_DeleteModel | tranche 1 |
| model-lifecycle/09-list-models-empty | new:RunExternalAPI_01_09_ListModelsEmpty | tranche 1 |
| model-lifecycle/10-list-models-non-empty | new:RunExternalAPI_01_10_ListModelsNonEmpty | tranche 1 |
| model-lifecycle/11-export-metadata-as-json-schema | new:RunExternalAPI_01_11_ExportMetadataViews | tranche 1 |
| model-lifecycle/12-parse-nobel-laureates-sample | new:RunExternalAPI_01_12_NobelLaureatesSample | tranche 1 |
| model-lifecycle/13-parse-lei-data-sample | new:RunExternalAPI_01_13_LEISample | tranche 1 |

---

## 02-change-level-governance.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| change-level/01-set-structural | new:RunExternalAPI_02_01_SetChangeLevelStructural | tranche 2 — happy path |
| change-level/02-structural-null-field-does-not-grow-changelog | new:RunExternalAPI_02_02_StructuralNullFieldNoChangelog | tranche 2 — null-array regression |
| change-level/03-type-widening-int-to-float-incompatible | gap_on_our_side (#129) | tranche 2 negative path classified `worse`: cyoda-go emits generic `BAD_REQUEST`; dictionary requires `FoundIncompatibleTypeWitEntityModelException`. Detail string carries the type-mismatch info but the error code is generic. v0.7.0 fix tracked in #129. Test body in place; flipping `t.Skip` is the close-the-issue checklist item. |
| change-level/04-type-narrowing-float-to-int-compatible | new:RunExternalAPI_02_04_TypeNarrowingFloatToInt | tranche 2 — int-into-float accepted |
| change-level/05-updated-schema-on-unlocked-then-lock-and-save | new:RunExternalAPI_02_05_UpdatedSchemaThenLockAndSave | tranche 2 — schema-extend-then-lock |
| change-level/06-multinode-type-level-with-all-fields-model | new:RunExternalAPI_02_06_MultinodeTypeLevelAllFields | tranche 2 — N=10 bounded (dictionary specifies 100; parity smoke does not need load testing) |
| change-level/07-structural-concurrent-extend-30-versions | new:RunExternalAPI_02_07_ConcurrentExtendVersions | tranche 2 — N=5 bounded (dictionary specifies 30; parity smoke does not need load testing) |

---

## 03-entity-ingestion-single.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| ingest-single/01-success-path | new:RunExternalAPI_03_01_CreateEntitySuccess | tranche 1 |
| ingest-single/02-import-list-of-objects-in-one-call | new:RunExternalAPI_03_02_ListOfObjects | tranche 1 |
| ingest-single/03-all-fields-model-round-trip | new:RunExternalAPI_03_03_AllFieldsRoundTrip | tranche 1 |
| ingest-single/04-save-family-rich-nested-array | new:RunExternalAPI_03_04_FamilyNested | tranche 1 |
| ingest-single/05-grpc-create-entity | internal_only_skip | endpoint block has `grpc:` only — no `rest:` line |
| ingest-single/06-grpc-multiple-entities-single-endpoint-warning | internal_only_skip | endpoint block has `grpc:` only — no `rest:` line |

---

## 04-entity-ingestion-collection.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| ingest-collection/01-family-and-pets-single-transaction | new:RunExternalAPI_04_01_FamilyAndPets | tranche 1 |
| ingest-collection/02-update-collection-age-increment | new:RunExternalAPI_04_02_UpdateCollectionAge | tranche 1 |
| ingest-collection/03-grpc-create-multiple-by-collection-rpc | internal_only_skip | endpoint block has `grpc:` only — no `rest:` line |
| ingest-collection/04-parsing-spec-transaction-window | new:RunExternalAPI_04_04_TransactionWindow | tranche 1 |

---

## 05-entity-update.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| update/01-nested-array-append-and-modify | new:RunExternalAPI_05_01_NestedArrayAppendAndModify | tranche 2 — uses UpdateEntity (UPDATE transition) per YAML's `update_entity_transition` |
| update/02-nested-array-shrink-and-modify-top-level | new:RunExternalAPI_05_02_NestedArrayShrinkAndModify | tranche 2 |
| update/03-remove-object-and-array-keep-one-field | new:RunExternalAPI_05_03_RemoveObjectAndArrayKeepOneField | tranche 2 — note: cyoda-go sets removed fields to null (does not drop) |
| update/04-populate-minimal-into-full | new:RunExternalAPI_05_04_PopulateMinimalIntoFull | tranche 2 — adapted for isolated-tenant: model seeded with full schema upfront. The YAML implies cross-scenario shared model state (01-04 share a model), which doesn't hold per-tenant. Worth proposing upstream as an explicit `preconditions:` block. |
| update/05-loopback-absent-transition | new:RunExternalAPI_05_05_LoopbackAbsentTransition | tranche 2 — uses UpdateEntityData (loopback path) |
| update/06-unchanged-payload-still-transitions | new:RunExternalAPI_05_06_UnchangedPayloadStillTransitions | tranche 2 — verifies no error on identical-payload PUT; deeper assertion (audit-event count growing by 1) is tranche-3 audit work |

---

## 06-entity-delete.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| delete/01-single-by-id | new:RunExternalAPI_06_01_DeleteSingle | tranche 1 |
| delete/02-all-by-model-version | new:RunExternalAPI_06_02_DeleteByModel | tranche 1 |
| delete/03-by-condition-jsonpath-equals | gap_on_our_side | The OpenAPI generator emits `DeleteEntitiesJSONRequestBody = AbstractConditionDto` (`api/generated.go:DeleteEntitiesJSONRequestBody`), but `internal/domain/entity/handler.go:DeleteEntities` does not read the body — it only consults `DeleteEntitiesParams` (`transactionSize`/`pointInTime`/`verbose`) and calls `DeleteAllEntities(name, version)`. Implementing this means parsing the existing `AbstractConditionDto` typedef, extending the service with a condition-aware delete path, and propagating to the storage SPI. |
| delete/04-by-condition-not-null | gap_on_our_side | same as 06/03 — handler ignores the existing `AbstractConditionDto` body type |
| delete/05-by-condition-at-point-in-time-too-many-entities | gap_on_our_side | same as 06/03 + `entitySearchLimit` enforcement on condition+pointInTime deletes is missing |
| delete/06-all-by-model-at-point-in-time | new:RunExternalAPI_06_06_DeleteAtPointInTime (skipped pending #124) | tranche 1 — test body in place; t.Skip until #124 ships in v0.7.0. `Handler.DeleteEntities` ignores `params.PointInTime`; storage SPI has no `DeleteAllAsAt`. Cross-repo fix (SPI tag + plugin impls + handler wiring) tracked in #124. |

---

## 07-point-in-time-and-changelog.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| pit/01-get-single-entity-at-point-in-time | new:RunExternalAPI_07_01_GetEntityAtPointInTime | tranche 2 — three timestamps, three states |
| pit/02-get-single-entity-by-transaction-id | new:RunExternalAPI_07_02_GetEntityByTransactionID (skipped) | tranche 2 — t.Skip pending parity-client surface addition (no transactionId-scoped GET helper today). Same gap as 12/05. |
| pit/03-entity-change-history-full | new:RunExternalAPI_07_03_ChangeHistoryFull | tranche 2 — note: cyoda-go uses `"CREATED"` / `"UPDATED"` enum values where the YAML spec uses `"CREATE"` / `"UPDATE"`. `different_naming_same_level`. Test asserts on cyoda-go's emission. |
| pit/04-entity-change-history-point-in-time | new:RunExternalAPI_07_04_ChangeHistoryAtPointInTime (skipped) | tranche 2 — t.Skip pending parity-client surface addition (no pointInTime-scoped change history helper today) |
| pit/05-change-history-non-existent-entity | new:RunExternalAPI_07_05_ChangeHistoryNonExistent | tranche 2 — string-based 404 detection (stopgap until GetEntityChangesRaw, added in Phase 5 of tranche 2, is wired in). NB: also surfaced and fixed a real parity bug in postgres plugin's `GetVersionHistory` (was returning empty slice instead of `spi.ErrNotFound` for unknown entities). |

---

## 08-workflow-import-export.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| wf-import/01-simple-automated-transition | pending:tranche-3 | POST /entity/{name}/{v}/workflow/import round-trip; issue #120 |
| wf-import/02-defaults-applied-and-returned | pending:tranche-3 | processor/transition defaults on export; issue #120 |
| wf-import/03-advanced-criteria-and-processors | pending:tranche-3 | group criterion + scheduled processor; issue #120 |
| wf-import/04-strategy-replace | pending:tranche-3 | importMode=REPLACE drops prior workflows; issue #120 |
| wf-import/05-strategy-activate | pending:tranche-3 | importMode=ACTIVATE deactivates prior + activates new; issue #120 |
| wf-import/06-strategy-merge | pending:tranche-3 | importMode=MERGE updates in place + adds new; issue #120 |

---

## 09-workflow-externalization.yaml

All ext/ scenarios use REST entity-create endpoints (`POST /entity/JSON/{name}/{v}`) but
require a gRPC calculation member connected via `CloudEventsService.startStreaming`.
The entity-facing HTTP endpoint is present; the precondition of an active gRPC streaming
client makes these untestable with HTTPDriver alone. They are marked `pending:tranche-3`
rather than `internal_only_skip` because the primary action (`create_entity`) is REST.

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| ext/01-sync-processor-success | pending:tranche-3 | REST entry; requires gRPC calc member for SYNC mode; issue #120 |
| ext/02-sync-processor-exception-rolls-back | pending:tranche-3 | SYNC processor exception rollback; issue #120 |
| ext/03-async-same-tx-exception-rolls-back | pending:tranche-3 | ASYNC_SAME_TX exception rollback; issue #120 |
| ext/04-async-new-tx-exception-keeps-initial-save | pending:tranche-3 | ASYNC_NEW_TX exception — initial save survives; issue #120 |
| ext/05-sync-error-flag-rolls-back | pending:tranche-3 | SYNC success=false flag rollback; issue #120 |
| ext/06-async-same-tx-error-flag-rolls-back | pending:tranche-3 | ASYNC_SAME_TX error flag rollback; issue #120 |
| ext/07-async-new-tx-error-flag-keeps-initial-save | pending:tranche-3 | ASYNC_NEW_TX error flag — initial save survives; issue #120 |
| ext/08-no-external-registered-fails | pending:tranche-3 | save fails when no calc member registered; issue #120 |
| ext/09-external-disconnect-succeeds-on-retry | pending:tranche-3 | retry on second member after first disconnects; issue #120 |
| ext/10-external-timeout-failover | pending:tranche-3 | slow member times out, fast member responds; issue #120 |
| ext/11-processing-node-disconnects-mid-request | pending:tranche-3 | node disconnect mid-request, retry on other node; issue #120 |
| ext/12-externalized-criterion-skips-call-when-not-matched | pending:tranche-3 | upstream filter short-circuits external call; issue #120 |

---

## 10-concurrency-and-multinode.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| multi/01-create-and-delete-through-load-balancer | pending:tranche-3 | full lifecycle via load balancer; issue #120 |
| multi/02-readback-reaches-all-replicas | pending:tranche-3 | write on node A visible from node B; issue #120 |
| multi/03-parallel-updates-to-same-entity | pending:tranche-3 | concurrent disjoint-field updates serialise; issue #120 |

---

## 11-edge-message.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| edge-msg/01-save-single | pending:tranche-3 | POST /edge-message + GET /edge-message/{id}; issue #120 |
| edge-msg/02-delete-single | pending:tranche-3 | DELETE /edge-message/{id} drops payload blob; issue #120 |
| edge-msg/03-delete-collection | pending:tranche-3 | DELETE /edge-message with id list; issue #120 |

---

## 12-negative-validation.yaml

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| neg/01-create-entity-on-unlocked-model | new:RunExternalAPI_12_01_CreateEntityOnUnlockedModel | tranche 2 — `equiv_or_better`: cyoda-go emits `MODEL_NOT_LOCKED` @409 (specific); dictionary's `EntityModelWrongStateException` is more generic. Tightened assertion uses cyoda-go's code; propose upstream tightening. |
| neg/02-create-entity-with-incompatible-type | gap_on_our_side (#129) | tranche 2 negative path classified `worse`: cyoda-go emits generic `BAD_REQUEST`; dictionary requires `FoundIncompatibleTypeWitEntityModelException`. Same code path / same #129 as 02/03. |
| neg/03-set-change-level-invalid-enum | gap_on_our_side (#130) | tranche 2 negative path classified `worse`: cyoda-go emits generic `BAD_REQUEST`; dictionary requires "Invalid enum value" detail. Test body in place; flipping `t.Skip` is the close-the-issue checklist item. |
| neg/04-get-single-entity-at-time-before-creation | new:RunExternalAPI_12_04_GetEntityAtTimeBeforeCreation (skipped) | tranche 2 — t.Skip pending GetEntityAtRaw on Driver (no negative-path Raw variant for pointInTime GET today) |
| neg/05-get-single-entity-with-bogus-transaction-id | new:RunExternalAPI_12_05_GetEntityWithBogusTransactionID (skipped) | tranche 2 — t.Skip pending parity-client transactionId-scoped GET (same gap as 07/02) |
| neg/06-get-changes-for-missing-entity | new:RunExternalAPI_12_06_GetChangesForMissingEntity | tranche 2 — `equiv_or_better`: cyoda-go emits `ENTITY_NOT_FOUND` @404; matches dictionary's `EntityNotFoundException` semantically. Tightened assertion. |
| neg/07-condition-delete-at-pit-too-many-matches | gap_on_our_side (#124) | tranche 2 — t.Skip pending #124. Whole delete-by-condition surface is a v0.7.0 server-side gap (handler ignores condition body and pointInTime). |
| neg/08-update-with-unknown-transition | new:RunExternalAPI_12_08_UpdateUnknownTransition | tranche 2 — `equiv_or_better` after wiring `TRANSITION_NOT_FOUND` into the engine-failure code path (review C1 fix). cyoda-go emits `TRANSITION_NOT_FOUND` @400 — matches dictionary's `(IllegalTransition\|TransitionNotFound)` semantically. |
| neg/09-get-model-after-delete | new:RunExternalAPI_12_09_GetModelAfterDelete | tranche 2 — `different_naming_same_level`: cyoda-go has no per-model GET endpoint; test verifies via `ListModels` and confirms absence. Semantically equivalent to per-model 404; reconcile in tranche-5 cloud smoke if cyoda-go ever adds a per-model GET. |
| neg/10-import-workflow-on-unknown-model | gap_on_our_side (#131) | tranche 2 negative path classified `worse`: cyoda-go returns HTTP 200 `{"success":true}` instead of 404 for workflow imports on non-existent models. Real bug — dictionary requires HTTP 404 with `(ModelNotFound\|EntityModelNotFound)`. Test body in place; flipping `t.Skip` is the close-the-issue checklist item. |

---

## 13-numeric-types.yaml

Note: `numeric/03` and `numeric/05` carry `internal_only: true` in the YAML — they
require `EntityModelFacade.upsert()` with a custom `ParsingSpec(intScope=BYTE,
decimalScope=FLOAT)`, which is not reachable through any REST or gRPC endpoint.
`numeric/02` is a cross-reference to neg/02 with no independent steps, so it is
`shape_only_skip`. `numeric/05ext` is the REST-reachable external equivalent of
`numeric/05` and is `pending:tranche-4`.

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| numeric/01-compatible-int-lands-in-double-field | pending:tranche-4 | integer value accepted for DOUBLE-locked field; issue #121 |
| numeric/02-incompatible-decimal-after-int-cross-ref | shape_only_skip | cross-reference to neg/02; no independent steps |
| numeric/03-parsing-spec-intScope-byte | internal_only_skip | requires `EntityModelFacade.upsert()` with custom `ParsingSpec`; not reachable via REST or gRPC |
| numeric/04-default-intScope-integer-external | pending:tranche-4 | default intScope=INTEGER over REST; issue #121 |
| numeric/05-polymorphic-field-after-merge | internal_only_skip | requires `EntityModelFacade.upsert()` with custom `ParsingSpec`; not reachable via REST or gRPC |
| numeric/05ext-polymorphic-field-after-merge-external | pending:tranche-4 | REST-reachable polymorphism with default scopes; issue #121 |
| numeric/06-double-at-max-boundary-round-trip | pending:tranche-4 | DOUBLE boundary round-trip; issue #121 |
| numeric/07-big-decimal-high-precision-round-trip | pending:tranche-4 | BIG_DECIMAL 20+18 digit round-trip; issue #121 |
| numeric/08-unbound-decimal-arbitrary-precision | pending:tranche-4 | UNBOUND_DECIMAL >18 fractional digits; issue #121 |
| numeric/09-big-integer-38-digits | pending:tranche-4 | BIG_INTEGER 38-digit round-trip; issue #121 |
| numeric/10-unbound-integer-40-digits | pending:tranche-4 | UNBOUND_INTEGER 40-digit round-trip; issue #121 |
| numeric/11-search-condition-integer-against-double-field | pending:tranche-4 | search with INTEGER value against DOUBLE field; issue #121 |

---

## 14-polymorphism.yaml

Note: `poly/02` carries `internal_only: true` in the YAML — it tests the internal
TreeNode save/reconstruct API, not the REST surface. `poly/05` uses an RSocket
`treeNode.getData` transport as its primary assertion path but also includes a REST
direct-search fallback step; it is `pending:tranche-4` (the REST step is exercisable).
`poly/07` carries `shape_only: true` in the YAML.

| source_id | cyoda_go_status | notes |
|-----------|-----------------|-------|
| poly/01-mixed-object-or-string-at-same-path | pending:tranche-4 | polymorphic search via async+direct REST paths; issue #121 |
| poly/02-tree-node-mixed-children-round-trip | internal_only_skip | `internal_only: true` in YAML; requires internal TreeNode save/reconstruct API |
| poly/03-polymorphic-value-array-in-all-fields-model | pending:tranche-4 | PolymorphicValue variants round-trip via REST; issue #121 |
| poly/04-polymorphic-timestamp-array-in-all-fields-model | pending:tranche-4 | PolymorphicTimestamp variants round-trip via REST; issue #121 |
| poly/05-trino-search-on-polymorphic-scalar | pending:tranche-4 | REST direct-search step exercisable; RSocket step skipped; issue #121 |
| poly/06-reject-condition-with-wrong-scalar-type | pending:tranche-4 | 400 on wrong-type condition value; issue #121 |
| poly/07-error-body-shape-for-invalid-polymorphic-types | shape_only_skip | `shape_only: true` in YAML; shape contract verified by JSON Schema, not scenario run |

---

## 00-endpoints.yaml

`00-endpoints.yaml` is an endpoint reference catalogue, not a scenario file. It lists
the REST and gRPC surface of the External API (URLs, HTTP methods, gRPC service names
and request/response types) without defining any `id:`-keyed scenario sequences. It has
no rows to triage and does not belong in the per-file tables above.

---

## Reverse section — parity entries not yet in upstream dictionary

The following `Run*` functions are registered in `e2e/parity/registry.go`'s `allTests`
slice and cover behaviour the upstream `e2e/externalapi/scenarios/` dictionary does not
yet describe. They are candidates for future cyoda-cloud dictionary contributions.

All twelve entries listed in the plan were verified present in `allTests`:

| parity name | topic |
|-------------|-------|
| `NumericClassification18DigitDecimal` | 18-digit decimal classified as BIG_DECIMAL |
| `NumericClassification20DigitDecimal` | 20-digit decimal classified correctly |
| `NumericClassificationLargeInteger` | large integer classification boundary |
| `NumericClassificationIntegerSchemaAcceptsInteger` | integer schema accepts integer value |
| `NumericClassificationIntegerSchemaRejectsDecimal` | integer schema rejects decimal value |
| `SchemaExtensionsSequentialFoldAcrossRequests` | sequential schema fold across multiple import requests |
| `SchemaExtensionCrossBackendByteIdentity` | cross-backend byte-identity of stored schema |
| `SchemaExtensionAtomicRejection` | schema extension atomically rejected on invalid input |
| `SchemaExtensionConcurrentConvergence` | concurrent schema extensions converge to same result |
| `SchemaExtensionSavepointOnLockFoldEquivalence` | savepoint-on-lock fold is equivalent to sequential fold |
| `SchemaExtensionLocalCacheInvalidationOnCommit` | local cache invalidated when schema commit lands |
| `SchemaExtensionByteIdentityProperty` | byte-level identity of schema roundtrip |
