# OpenAPI Conformance Audit — 2026-04-29

Per #21 design Section 3. One row per operationId. Disposition values:
`match`, `fix-spec`, `fix-server`, `fix-both`, `out-of-scope`. Empty
disposition means TBD — populated by the per-domain commits (Tasks 3.1
through 10.1).

**Totals:** 81 operations declared in `api/openapi.yaml`. 22 are excluded
from codegen by `api/config.yaml` `exclude-tags` (`Stream Data`, `SQL-Schema`)
and marked out-of-scope. 59 are in scope for #21.

**Inputs:**
- Spec: `api/openapi.yaml`
- Validator's record-mode output: `internal/e2e/_openapi-conformance-report.md` (gitignored — regenerate via `go test ./internal/e2e/... -count=1`)
- Validator wired in commit 95e3589 (Task 1.9), hardened in commit 444103c.

---

## Entity Management

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| create | POST | /entity/{format}/{entityName}/{modelVersion} | `internal/domain/entity/handler.go:268` | `type:array + $ref EntityTransactionResponse` (malformed — array with sibling $ref) | `[]any{map{transactionId,entityIds}}` — array wrapping one object | fix-spec | |
| createCollection | POST | /entity/{format} | `internal/domain/entity/handler.go:551` | `type:array + $ref EntityTransactionResponse` (malformed — array with sibling $ref) | `[]any{map{transactionId,entityIds}}` — array wrapping one object | fix-spec | |
| deleteEntities | DELETE | /entity/{entityName}/{modelVersion} | `internal/domain/entity/handler.go:488` | `$ref StreamDeleteResult` — object with `entityModelClassId`, `deleteResult`, optional `ids` | `[]map{deleteResult,entityModelClassId}` — array not matching spec object | fix-server | |
| deleteSingleEntity | DELETE | /entity/{entityId} | `internal/domain/entity/handler.go:447` | `$ref SingleDeleteResult` — object with `id`, `modelKey`, `transactionId` | `map{id,modelKey,transactionId}` | | |
| getAllEntities | GET | /entity/{entityName}/{modelVersion} | `internal/domain/entity/handler.go:508` | `application/x-ndjson; type:array + $ref JsonNode` | `[]map{type,data,meta}` via `application/json` WriteJSON (wrong content-type) | fix-server | |
| getEntityChangesMetadata | GET | /entity/{entityId}/changes | `internal/domain/entity/handler.go:465` | `type:array + $ref EntityChangeMeta` (malformed — array with sibling $ref) | `[]map{changeType,timeOfChange,user,...}` — array | fix-spec | |
| getEntityStatistics | GET | /entity/stats | `internal/domain/entity/handler.go:360` | `type:array + $ref ModelStatsDto` (malformed — array with sibling $ref) | `[]genapi.ModelStatsDto` | fix-spec | |
| getEntityStatisticsByState | GET | /entity/stats/states | `internal/domain/entity/handler.go:380` | `type:array + $ref ModelStateStatsDto` (malformed — array with sibling $ref) | `[]genapi.ModelStateStatsDto` | fix-spec | |
| getEntityStatisticsByStateForModel | GET | /entity/stats/states/{entityName}/{modelVersion} | `internal/domain/entity/handler.go:406` | `type:array + $ref ModelStateStatsDto` (malformed — array with sibling $ref) | `[]genapi.ModelStateStatsDto` | fix-spec | |
| getEntityStatisticsForModel | GET | /entity/stats/{entityName}/{modelVersion} | `internal/domain/entity/handler.go:431` | `$ref ModelStatsDto` — single object | `genapi.ModelStatsDto` | | |
| getOneEntity | GET | /entity/{entityId} | `internal/domain/entity/handler.go:326` | `type:object` (loose — no named schema) | `map{type,data,meta}` | | |
| updateCollection | PUT | /entity/{format} | `internal/domain/entity/handler.go:604` | `type:array + $ref EntityTransactionResponse` (malformed — array with sibling $ref) | `[]any{map{transactionId,entityIds}}` — array wrapping one object | fix-spec | |
| updateSingle | PUT | /entity/{format}/{entityId}/{transition} | `internal/domain/entity/handler.go:704` | `$ref EntityTransactionResponse` — object with `transactionId`, `entityIds[]object` | `map{transactionId,entityIds}` where entityIds is `[]string` (string vs object mismatch) | fix-both | |
| updateSingleWithLoopback | PUT | /entity/{format}/{entityId} | `internal/domain/entity/handler.go:671` | `$ref EntityTransactionResponse` — object with `transactionId`, `entityIds[]object` | `map{transactionId,entityIds}` where entityIds is `[]string` (string vs object mismatch) | fix-both | |

---

## Edge Message

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| deleteMessage | DELETE | /message/{messageId} | `internal/domain/messaging/handler.go:190` | `$ref EntityTransactionResponse` — object with `transactionId`, `entityIds[]object` | `map{entityIds:[]string}` (no transactionId; entityIds is []string not []object) | fix-both | |
| deleteMessages | DELETE | /message | `internal/domain/messaging/handler.go:222` | `type:string` (loose — spec is wrong; example shows array) | `[]map{entityIds,success}` — array | fix-spec | |
| getMessage | GET | /message/{messageId} | `internal/domain/messaging/handler.go:115` | `$ref EdgeMessageDto` — object with `header`, `metaData`, `content` | `map{header,metaData,content}` — matches shape; Content-Type mismatch on 404 errors (application/problem+json vs ErrorResponse) | fix-server | |
| newMessage | POST | /message/new/{subject} | `internal/domain/messaging/handler.go:32` | `type:string` (loose — spec is wrong; example shows array) | `[]map{entityIds,success}` — array | fix-spec | |

---

## Entity Audit

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| getStateMachineFinishedEvent | GET | /audit/entity/{entityId}/workflow/{transactionId}/finished | `internal/domain/audit/handler.go:231` | `object{state,stopReason,success}` (required: state, stopReason, success) | `map{auditEventType,eventType,severity,utcTime,entityId,details,data,...}` — missing `stopReason`, `success` at top level; extra fields present | fix-server | |
| searchEntityAuditEvents | GET | /audit/entity/{entityId} | `internal/domain/audit/handler.go:26` | `$ref EntityAuditEventsResponseDto` — paginated object with `items[]` (discriminated by eventType) | `map{items,pagination}` — outer shape matches; items mixin of EntityChange and StateMachine events; StateMachine events use non-spec eventType values (TRANSITION_MAKE vs TRANSITION_MADE) and null data field violations | fix-server | |

---

## Entity Model

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| deleteEntityModel | DELETE | /model/{entityName}/{modelVersion} | `internal/domain/model/handler.go:142` | `$ref EntityModelActionResultDto` — object with `success`, `modelKey`, `message`, `modelId` | `genapi.EntityModelActionResultDto` struct | | |
| exportMetadata | GET | /model/export/{converter}/{entityName}/{modelVersion} | `internal/domain/model/handler.go:103` | `type:string` (loose — spec says string; content is actually JSON object) | raw JSON bytes written directly to response writer | fix-spec | |
| getAvailableEntityModels | GET | /model/ | `internal/domain/model/handler.go:168` | `type:array + $ref EntityModelDto` (malformed — array with sibling $ref) | `[]genapi.EntityModelDto` | fix-spec | |
| importEntityModel | POST | /model/import/{dataFormat}/{converter}/{entityName}/{modelVersion} | `internal/domain/model/handler.go:78` | `type:string, format:uuid` — bare UUID string | `result.ModelID` (UUID value) | | |
| lockEntityModel | PUT | /model/{entityName}/{modelVersion}/lock | `internal/domain/model/handler.go:116` | `$ref EntityModelActionResultDto` | `genapi.EntityModelActionResultDto` struct | | |
| setEntityModelChangeLevel | POST | /model/{entityName}/{modelVersion}/changeLevel/{changeLevel} | `internal/domain/model/handler.go:155` | `$ref EntityModelActionResultDto` | `genapi.EntityModelActionResultDto` struct | | |
| unlockEntityModel | PUT | /model/{entityName}/{modelVersion}/unlock | `internal/domain/model/handler.go:129` | `$ref EntityModelActionResultDto` | `genapi.EntityModelActionResultDto` struct | | |
| validateEntityModel | POST | /model/validate/{entityName}/{modelVersion} | `internal/domain/model/handler.go:191` | `$ref EntityModelActionResultDto` | `genapi.EntityModelActionResultDto` struct | | |

---

## Entity Model, Workflow

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| exportEntityModelWorkflow | GET | /model/{entityName}/{modelVersion}/workflow/export | `internal/domain/workflow/handler.go:122` | `$ref WorkflowExportResponseDto` | `map{entityName,modelVersion,workflows}` — shape matches; 404 Content-Type is application/problem+json (spec expects ErrorResponseDto with application/json) | fix-server | |
| importEntityModelWorkflow | POST | /model/{entityName}/{modelVersion}/workflow/import | `internal/domain/workflow/handler.go:33` | `content:{}` (no body — 200 with empty body) | `map{success:true}` — non-empty body when spec declares empty | fix-server | |

---

## Search

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| cancelAsyncSearch | PUT | /search/async/{jobId}/cancel | `internal/domain/search/handler.go:338` | `$ref CancelAsyncSearchDto` | `map{isCancelled,cancelled,currentSearchJobStatus}` — shape TBD against schema | | |
| getAsyncSearchResults | GET | /search/async/{jobId} | `internal/domain/search/handler.go:254` | `$ref PagedEntityResultsDto` | `map{content,page{number,size,totalElements,totalPages}}` | | |
| getAsyncSearchStatus | GET | /search/async/{jobId}/status | `internal/domain/search/handler.go:239` | `$ref AsyncSearchStatusDto` | `map{searchJobStatus,createTime,entitiesCount,calculationTimeMillis,expirationDate,...}` | | |
| searchEntities | POST | /search/direct/{entityName}/{modelVersion} | `internal/domain/search/handler.go:103` | `application/x-ndjson; type:array items:$ref EntityResultDto` | ndjson stream of `map{type,data,meta}` | | |
| submitAsyncSearchJob | POST | /search/async/{entityName}/{modelVersion} | `internal/domain/search/handler.go:186` | `type:string, format:uuid` — bare UUID | `jobID` string value | | |

---

## OAuth, Keys

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| deleteJwtKeyPair | DELETE | /oauth/keys/keypair/{keyId} | `internal/domain/account/handler.go:93` | `content:{}` (empty body); `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| deleteTrustedKey | DELETE | /oauth/keys/trusted/{keyId} | `internal/domain/account/handler.go:113` | `content:{}` (empty body); `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| getCurrentJwtKeyPair | GET | /oauth/keys/keypair/current | `internal/domain/account/handler.go:89` | `$ref JwtKeyPairResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| invalidateJwtKeyPair | POST | /oauth/keys/keypair/{keyId}/invalidate | `internal/domain/account/handler.go:97` | `content:{}` (empty body); `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| invalidateTrustedKey | POST | /oauth/keys/trusted/{keyId}/invalidate | `internal/domain/account/handler.go:117` | `content:{}` (empty body); `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| issueJwtKeyPair | POST | /oauth/keys/keypair | `internal/domain/account/handler.go:85` | `$ref JwtKeyPairResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| listTrustedKeys | GET | /oauth/keys/trusted | `internal/domain/account/handler.go:105` | `type:array items:$ref TrustedKeyResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| reactivateJwtKeyPair | POST | /oauth/keys/keypair/{keyId}/reactivate | `internal/domain/account/handler.go:101` | `$ref JwtKeyPairResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| reactivateTrustedKey | POST | /oauth/keys/trusted/{keyId}/reactivate | `internal/domain/account/handler.go:121` | `$ref TrustedKeyResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| registerTrustedKey | POST | /oauth/keys/trusted | `internal/domain/account/handler.go:109` | `$ref TrustedKeyResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |

---

## OAuth, OIDC Providers

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| deleteOidcProvider | DELETE | /oauth/oidc/providers/{id} | `internal/domain/account/handler.go:137` | `content:{}` (empty body); `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| invalidateOidcProvider | POST | /oauth/oidc/providers/{id}/invalidate | `internal/domain/account/handler.go:145` | `content:{}` (empty body); `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| listOidcProviders | GET | /oauth/oidc/providers | `internal/domain/account/handler.go:125` | `type:array items:$ref OidcProviderResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| reactivateOidcProvider | POST | /oauth/oidc/providers/{id}/reactivate | `internal/domain/account/handler.go:149` | `$ref OidcProviderResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| registerOidcProvider | POST | /oauth/oidc/providers | `internal/domain/account/handler.go:129` | `$ref OidcProviderResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| reloadOidcProviders | POST | /oauth/oidc/providers/reload | `internal/domain/account/handler.go:133` | `content:{}` (empty body); `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| updateOidcProvider | PATCH | /oauth/oidc/providers/{id} | `internal/domain/account/handler.go:141` | `$ref OidcProviderResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |

---

## User, Account

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| accountGet | GET | /account | `internal/domain/account/handler.go:27` | `$ref UserAccountInfoResponseDto`; `401` added; `UserRoleDto.desc` made optional (server has no role desc) | `map{userAccountInfo{userId,userName,legalEntity,roles[{id}],currentSubscription}}` — matches spec after desc fix | match | TBD |
| accountSubscriptionsGet | GET | /account/subscriptions | `internal/domain/account/handler.go:61` | `$ref SubscriptionsResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |

---

## User, Machine

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| createTechnicalUser | POST | /clients | `internal/domain/account/handler.go:69` | `$ref TechnicalUserCredentialsDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| deleteTechnicalUser | DELETE | /clients/{clientId} | `internal/domain/account/handler.go:73` | `$ref DeleteTechnicalUser200ResponseDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| getTechnicalUserToken | POST | /oauth/token | real impl in `internal/auth/token.go` (account handler stub bypassed) | `$ref TokenResponseDto`; 200/400/401/403/500 declared — matches server for `client_credentials`; note: `issued_token_type` enum only has `access_token` but server returns `jwt` for token-exchange (fix-spec minor) | real impl via `auth/token.go`; `client_credentials` wire matches spec; `token-exchange` `issued_token_type` enum drift noted | fix-spec (minor: issued_token_type enum) | TBD |
| listTechnicalUsers | GET | /clients | `internal/domain/account/handler.go:65` | `type:array items:$ref TechnicalUserDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD |
| resetTechnicalUserSecret | PUT | /clients/{clientId}/secret | `internal/domain/account/handler.go:77` | `$ref TechnicalUserCredentialsDto`; `501` added | `stub → 501` | out-of-scope-not-implemented (#194) | TBD | |

---

## Excluded: Stream Data (13 ops)

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| delete | DELETE | /stream-data/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| deleteStreamDataConfig | DELETE | /stream-data/config/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| exportAll | GET | /stream-data/export/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| exportAll_1 | GET | /stream-data/export/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| exportByIds | POST | /stream-data/export/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| getAllConfigs | GET | /stream-data/config/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| getIndexs | GET | /stream-data/index/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| getQueryPlan | GET | /stream-data/query-plan/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| getStreamData | POST | /stream-data/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| getStreamDataConfig | GET | /stream-data/config/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| importContainer | POST | /stream-data/import/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| saveStreamDataConfig | POST | /stream-data/config/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| updateStreamDataConfig | PUT | /stream-data/config/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |

---

## Excluded: SQL-Schema (9 ops)

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|
| deleteSchema | DELETE | /sql/schema/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| deleteSchemaByName | DELETE | /sql/schema/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| genTables | POST | /sql/schema/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| getSchema | GET | /sql/schema/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| getSchemaByName | GET | /sql/schema/ | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| getSchemas | GET | /sql/schema/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| putSchema | PUT | /sql/schema/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| saveSchema | POST | /sql/schema/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
| updateTables | PUT | /sql/schema/... | out-of-scope | n/a (excluded by api/config.yaml) | n/a (excluded by api/config.yaml) | out-of-scope | |
