# Changelog

All notable changes to Cyoda-Go are documented here. The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) conventions and [Semantic Versioning](https://semver.org/) — pre-1.0, so minor bumps may include breaking changes (see [README — Versioning](./README.md#versioning)).

## [Unreleased]

## [0.7.0] — 2026-05-01

### ⚠️ Breaking changes (wire format)

The OpenAPI spec at `api/openapi.yaml` has been reconciled with the actual server wire format across all 81 declared operations. Clients generated from the pre-0.7.0 spec will be incorrect for the endpoints listed below — regenerate clients against `v0.7.0`'s `api/openapi.yaml` (or fetch via `cyoda help openapi yaml`).

**Server response shape changes:**

- **`GET /message/{messageId}` (`getMessage`)** — `content` field is now embedded JSON, not a JSON-encoded string. Wire was `"content": "{\"x\":1}"`; now `"content": {"x":1}`. Clients that did `JSON.parse(content)` must consume `content` directly.
- **Stub error code (account/IAM/OIDC/OAuth-keys ops)** — `errorCode` value in 501 responses changed from `"BAD_REQUEST"` to `"NOT_IMPLEMENTED"`. Pairs correctly with the HTTP status now.
- **`getStateMachineFinishedEvent`** — response now includes `microsTime` field (additive; non-breaking unless client strict-rejects unknown fields).

**Spec declaration changes (server unchanged but client codegen will differ):**

- All 4xx/5xx responses on entity ops, workflow export/import, and shared `components.responses.*` fragments now declare `Content-Type: application/problem+json` (RFC 9457). Server has always emitted this; spec was wrong.
- `getEntityChangesMetadata.changeType` enum corrected from `[CREATE, UPDATE, DELETE]` to `[CREATED, UPDATED, DELETED]`.
- `EntityTransactionResponse.entityIds` declared as `array<string>` (UUIDs), not `array<object>`.
- `getOneEntity` response declares the `Envelope` named schema `{type, data, meta}` instead of loose `type:object`.
- 7 malformed `type:array + sibling $ref` sites in the spec corrected to well-formed `type:array, items:{ $ref:... }` (`create`, `createCollection`, `updateCollection`, `getEntityChangesMetadata`, 3 statistics variants, `getAvailableEntityModels`).
- `messaging.deleteMessage` declares `MessageDeleteResponse` (`{entityIds: array<string>}`) instead of `EntityTransactionResponse` (no `transactionId` was ever emitted by the server).
- `messaging.deleteMessages` and `newMessage` declare `array<EntityTransactionResponse>` (was `type:string`, which never matched the server).
- 22 IAM/OAuth/OIDC/account stub endpoints declare `501 Not Implemented` per the design's deferred-implementation policy. Real implementation is tracked in #194. Clients generated from the pre-0.7.0 spec for these endpoints will be wrong.
- `basicAuth` security scheme declared (was referenced but never declared).

### Added

- **OpenAPI runtime conformance validator** (`internal/e2e/openapivalidator/`) — every E2E response is matched against the spec via `kin-openapi`. Drift fails the build. Documented in [ADR 0001](./docs/adr/0001-openapi-server-spec-conformance.md).
- **2 previously-undocumented customer endpoints declared in the spec:**
  - `getEntityTransitions` (GET `/entity/{entityId}/transitions`)
  - `fetchEntityTransitions` (GET `/platform-api/entity/fetch/transitions`)
- **7 new named schemas** in `components/schemas/`: `Envelope`, `EdgeMessagePayload`, `MessageDeleteResponse`, `MessageDeleteBatchResponse`, `TransitionNameList`, `WorkflowImportSuccessDto`, `AuditEvent` (oneOf+discriminator union for state-machine + entity-change + system audit events).
- **4 shared response fragments** in `components/responses/`: `Unauthorized`, `Forbidden`, `InternalServerError`, `NotImplemented` — referenced from every operation's per-status declarations.

### Fixed

- `messaging.GetMessage` content field — JSON-in-string defect (the original [#21](https://github.com/Cyoda-platform/cyoda-go/issues/21) confirmed defect for messaging).
- `messaging.NewMessage` — dead-code branch in `json.Compact` fallback removed; replaced with explicit invariant-broken 500.
- `audit.GetStateMachineFinishedEvent` — missing `microsTime` field added.
- `search.cancelAsyncSearch` 400 path — uses `WriteError` (proper Content-Type) instead of raw `WriteJSON`.
- `account` stub handlers — error code corrected to `NOT_IMPLEMENTED`.
- Pre-existing tag-list test stale entry (`CQL Execution Statistics` removed).
- Root `go.mod` / `go.sum` tidied after dependabot PR #180 bumped sqlite plugin deps without propagating to root (was breaking `Release smoke` and `per-module-hygiene` jobs on `main`).

### Process / Documentation

- ADR 0001 added: chose runtime validation via `kin-openapi` over compile-time strict typing (oapi-codegen strict-server, ogen, goa all evaluated).
- Conformance audit table at `docs/superpowers/audits/2026-04-29-openapi-conformance-audit.md` — one row per operationId, dispositioned with commit SHA. Carried forward as the starting point for future external-spec reconciliation work.
- Issue [#194](https://github.com/Cyoda-platform/cyoda-go/issues/194) filed for the 22 stub-implemented IAM/OAuth/OIDC/account endpoints (out of scope for #21 per the A+C policy).

### Versioning policy

`v0.6.x` is no longer maintained. No back-port branch exists. Consumers needing 0.6.x stability should pin to `v0.6.3`. If a concrete need emerges, open an issue and we'll consider branching `release/v0.6.x` from the `v0.6.3` tag.

---

## [0.6.3] — 2026-04-28 and earlier

For releases prior to 0.7.0, see the [Releases page](https://github.com/Cyoda-platform/cyoda-go/releases) and the git history. This is the first release with a maintained CHANGELOG.
