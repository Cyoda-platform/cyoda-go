# OpenAPI server-spec conformance — design

**Issue:** [#21](https://github.com/Cyoda-platform/cyoda-go/issues/21) (originally migrated from `cyoda-light-go#202`; was superseded by #192, then #192 was closed won't-do and #21 reopened with the runtime-validation scope captured here)
**Branch:** `issue-21-openapi-conformance` → `main`
**ADR:** [0001 OpenAPI server-spec conformance approach](../../adr/0001-openapi-server-spec-conformance.md) (Accepted 2026-04-29 — runtime validation via `kin-openapi` at the E2E test boundary; defer compile-time strict typing)
**Related:** [#193](https://github.com/Cyoda-platform/cyoda-go/issues/193) — feature work for arbitrary EdgeMessage payload content types (out of scope for this PR)
**Status:** Design approved 2026-04-29

## 1. Scope, framing, non-goals

### Scope (single PR landing on `main`)

- **Audit table** at `docs/superpowers/audits/2026-04-29-openapi-conformance-audit.md` — every operationId paired with handler function, current spec response shape, actual server response shape, disposition.
- **Spec fixes** in `api/openapi.yaml`:
  - Named schemas for every response.
  - All 6 sites with `type: array` + sibling `$ref` corrected to well-formed `type: array, items: { $ref: ... }`.
  - Loose `type: object` blocks replaced with `$ref` to a named schema or `additionalProperties: true` + `description: polymorphic by intent`.
  - `basicAuth` declared in `components.securitySchemes` (referenced at `api/openapi.yaml:4587` but never declared — uncovered during the ogen spike that informed ADR 0001).
  - Per-operation error blocks declared, including the shared 401/403/500 from middleware via `components.responses` `$ref`s.
  - `messaging.GetMessage.content` declared as polymorphic JSON (`EdgeMessagePayload`), not `type: string`.
- **Handler defect fixes**:
  - `internal/domain/messaging/handler.go:183` — `string(payloadBytes)` → `json.RawMessage(payloadBytes)`.
  - Other shape defects discovered during audit — fixed inline per Gate 6 unless they require design input.
- **Runtime validator** at the E2E test layer — collect-and-report mode, single end-of-suite failure with full mismatch list.
- **E2E coverage closure** — minimal happy-path test for every operationId currently uncovered.
- **Derived-artefact updates** — `e2e/parity/client/types.go`, parity scenarios, `cmd/cyoda/help/content/openapi.md` narrative.

### Non-goals

- **Compile-time strict typing of response shapes** (deferred per ADR 0001).
- **External reconciliation with `docs/cyoda/openapi.yml`** (separate future milestone). The audit table lives past this PR and becomes the starting point for that work.
- **Cassandra plugin SPI changes** (out of scope; surfaces via parity registry's next dep update).
- **5xx envelope standardization beyond what CLAUDE.md already mandates** (ticket UUID + generic message). The validator enforces the declared shape; we don't redesign.
- **Arbitrary EdgeMessage payload content types** (filed as #193). This PR documents the current JSON-only limitation in the spec (`contentType` description: "informational; does not affect storage or retrieval format — payload is always treated as a JSON value; clients needing non-JSON content stringify it (e.g. base64 for binary). See #193 for proper content-type support.").

## 2. Validator architecture

### Library

`github.com/getkin/kin-openapi/openapi3filter` (the validation subpackage of `kin-openapi v0.137.0`, already a direct dep). Exposes `ValidateResponse(ctx, ValidateResponseInput{...})` which checks a `*http.Response` against the matched route's response schema for the actual status code.

### Hook point

Wrap the `http.Handler` returned by `app.New(...)` before it's passed to `httptest.NewServer` in `internal/e2e/e2e_test.go`'s `TestMain`. The wrapper is a small `http.Handler` middleware that:

1. Constructs a `httptest.ResponseRecorder` proxy around the real `http.ResponseWriter`.
2. Calls the wrapped handler with the proxy.
3. Routes the captured request through `kin-openapi`'s router (built once in `TestMain` from the embedded spec via `genapi.GetSwagger()`) to find the matched operation.
4. Calls `openapi3filter.ValidateResponse` with the captured response.
5. If validation fails, appends the diff (operation, path, status, JSON path, expected, actual) to a process-level collector via a mutex-guarded `append`.
6. Forwards the captured response bytes to the real `ResponseWriter`.

Single insertion point, zero changes to test code.

### Collector + report

The collector lives in `internal/e2e/openapivalidator/`:

```go
type Mismatch struct {
    Operation string
    Method    string
    Path      string
    Status    int
    JSONPath  string
    Reason    string
    TestName  string  // from t.Name() at request time, captured via context
}

var collector struct {
    mu  sync.Mutex
    out []Mismatch
}
```

After `m.Run()` returns in `TestMain`, the collector's contents are written to a markdown file (path: `internal/e2e/_openapi-conformance-report.md`, gitignored). If `len(collector.out) > 0`, `TestMain` calls `os.Exit(1)` with a stderr summary listing the first 20 mismatches and pointing to the full report file.

### Test-name capture

Each `httptest`-issued request gets the current test name attached via a context key set by a thin helper in `helpers_test.go`. Existing test code can opt in by switching from `http.NewRequest(...)` to `e2e.NewRequest(t, ...)` — but the validator works without this (test name just shows as "unknown"). Helper migration happens organically, not as a blocking change.

### Coverage gap reporting

The same end-of-suite hook prints any operationId that was *never* exercised during the run — surfaces dead spots in E2E coverage. This list informs the per-domain commits (Section 8) — every uncovered op needs a happy-path test before merge.

## 3. Audit table — format and process

### Location

`docs/superpowers/audits/2026-04-29-openapi-conformance-audit.md` (new directory). Checked in. Lives past the PR; the future external-reconciliation milestone consumes it.

### Format

One row per operationId. Columns:

| operationId | method | path | handler | spec response (today) | server response (today) | disposition | resolved-by-commit |
|---|---|---|---|---|---|---|---|

Where:
- **handler** = `internal/domain/<domain>/handler.go:<line>` linking to the function.
- **spec response (today)** = brief summary of what `api/openapi.yaml` claims for the 200/primary success status.
- **server response (today)** = what the handler actually writes.
- **disposition** = one of `match` / `fix-spec` / `fix-server` / `fix-both`. Default policy: server is source of truth, so most `fix-spec`; `fix-server` only when the server is genuinely emitting wrong wire data (e.g. JSON-in-string, missing JSON tags producing PascalCase).
- **resolved-by-commit** = filled in as commits land — short SHA or commit subject.

### Process

1. Initial pass during the audit-foundation commit fills in `operationId`, `method`, `path`, `handler`, `spec response (today)`, `server response (today)` for all 81 ops. Disposition starts empty.
2. Per-domain commits (Section 8) fill in disposition + resolved-by-commit as defects are fixed.
3. By the final commit, every row's disposition is non-empty.
4. PR description links the audit table; reviewers spot-check rows.

### Generation

The initial pass is partly mechanical: `operationId` / `method` / `path` come from parsing `api/openapi.yaml`; `handler` comes from `oapi-codegen`'s `ServerInterface` method names + grep. A small one-shot Go script in `tools/audit-skeleton/` (not retained beyond this PR) emits the skeleton table; the human or implementing agent fills in the response-shape columns by reading each handler.

### Future use

When the external-reconciliation milestone opens, the table is the starting point — extended with two more columns (`Cloud spec response` / `Cloud disposition`) and walked again with Cloud's spec as the second axis.

## 4. Spec changes (`api/openapi.yaml`)

### Schema additions

New named components in `components/schemas/`:

- `Envelope` = `{type: string, data: object (polymorphic), meta: object}` — for `getOneEntity`.
- `EnvelopeList` = `array<Envelope>` — for `getAllEntities`.
- `EntityChangeMetadata`, `EntityStatistics` (+ 3 by-state/by-model variants), `WorkflowTransition`, `TransitionsList`, `AuditEvent` (3-variant `oneOf` with `discriminator.propertyName: type`), `SearchSnapshot`, `SearchJobStatus`, `SearchJobResults`, `ModelExportResponse`, `ModelImportResponse`, `AccountInfo`, `SubscriptionList`, `TechnicalUser`, `OidcProvider`, `JwtKeyPair`, `EdgeMessage`, `EdgeMessagePayload`.

The exact list emerges during the audit pass; the design commits to the set, the audit row for each op confirms which named schema applies.

### Schema fixes

- All 6 sites with `type: array` + sibling `$ref` (the original #21 anti-pattern) become well-formed `type: array, items: { $ref: ... }`.
- Loose `type: object` blocks replaced with `$ref` to a named schema (or `additionalProperties: true` for polymorphic-by-intent fields with a `description: polymorphic by intent; user-supplied content` marker).
- `basicAuth` declared in `components.securitySchemes`.
- `messaging.GetMessage.content` declared as `EdgeMessagePayload` (polymorphic), not `type: string`.

### Per-operation error declarations

Every operation gets:

- A success block per actually-emitted status (`200`/`201`/`204`).
- Per-operation 4xx blocks for each error code the *handler* emits, sourced by reading `common.WriteError` call sites in `internal/domain/**/handler.go`.
- A **shared 5xx fragment** referenced by every operation: `default: $ref: '#/components/responses/InternalServerError'` where the response component is `ProblemDetail` with the ticket-UUID shape per CLAUDE.md.
- A **shared 401 fragment** referenced by every operation under `bearerAuth`: `401: $ref: '#/components/responses/Unauthorized'`.
- A **shared 403 fragment** for tenant-isolation enforcement: `403: $ref: '#/components/responses/Forbidden'`.

Shared fragments live in `components.responses` — declared once, `$ref`'d from each operation. Avoids 81 copies of the same 5xx block.

### Polymorphic markers

Fields that intentionally accept any JSON (entity `data`, edge message payload) get `description: polymorphic by intent; user-supplied content` and remain `additionalProperties: true`.

### Tag exclusions

`Stream Data`, `CQL Execution Statistics`, `SQL-Schema` stay excluded in `api/config.yaml` — out of scope for cyoda-go regardless of this PR.

### Validation against the audit

The validator (Section 2) catches drift between these declarations and what the server actually emits — that's the runtime guard. The spec-tightening commits write the schemas; the per-domain commits fix any mismatches the validator surfaces.

## 5. Handler defect fixes

### Confirmed defect — `messaging.GetMessage.content` JSON-in-string

`internal/domain/messaging/handler.go:183` returns `content: string(payloadBytes)`. Wire today is `{"content": "{\"actual\":\"json\"}", ...}`. Wire after fix is `{"content": {actual: "json"}, ...}`.

Change:
```go
// before
"content": string(payloadBytes),
// after
"content": json.RawMessage(payloadBytes),
```

Spec change in lockstep — `EdgeMessagePayload` becomes the field's schema (polymorphic) instead of `type: string`. The constraint named in #21 stays: when `contentType` is genuinely binary (`application/octet-stream`), base64 string with `format: byte` remains correct; the rule applies only when the bytes are JSON. **Today's reality** (per the spec's `contentType` description and the workaround documented in #193) is that `contentType` is informational — clients stringify non-JSON content. The polymorphic `EdgeMessagePayload` accommodates that workaround.

Test pinning: a new E2E test posts a message with a JSON payload, calls `GetMessage`, asserts the `content` field is parseable as JSON without a second `json.Unmarshal` (i.e. it's already JSON, not a string). The validator's `EdgeMessagePayload` schema then prevents future regression on the wire shape.

### Audit-discovered defects

Other shape defects surface during the audit pass. Per Gate 6, each gets fixed inline via TDD (red test → green fix) in the same domain commit, unless the fix:

- requires structural change beyond the wire shape (stop-and-ask),
- requires a design decision (stop-and-ask), or
- would balloon a single domain commit beyond reviewability (split into a focused commit, but still inside this PR — no follow-up issue).

### Likely candidates (to be confirmed during audit, not committed to as scope yet)

- Any `WriteJSON(w, x, status)` site where the Go value's field tags don't match the spec (the `EntityEnvelope`-via-`map[string]any` pattern is the one we know about; others may exist).
- Any handler emitting an undeclared status code (will surface as a validator mismatch — "status N not declared for operation X").
- Any handler emitting a `ProblemDetail.code` value not declared in the operation's 4xx blocks.

### Fix-vs-defer decision rule (Gate 6 surface)

- Mechanical (one handler, one wire-shape change, test pins it) → fix in this PR.
- Structural (e.g. service-layer type needs reshaping to fix the wire) → stop and surface.

## 6. E2E coverage closure

Every operationId gets at least a happy-path E2E test before merge. Process:

1. **First validator run (audit foundation commit) prints the uncovered list.** This is the work backlog for the per-domain commits.
2. **Per-domain commits add minimal happy-path tests** for each uncovered op. "Minimal" means one positive-path call, no edge-case coverage; just enough to exercise the wire shape so the validator's automatic guard inherits coverage.
3. **The final cutover commit's validator run shows zero uncovered ops.** That's the Section 2 acceptance signal.

### Test-writing scope per uncovered op

- For ops with simple prerequisites (e.g. `getEntityStatistics` — no setup needed), a single test function: setup minimal state, call the endpoint, assert 200, validator pins the shape.
- For ops with complex prerequisites (e.g. async search jobs requiring a full search-and-poll cycle), reuse existing test helpers if they exist; otherwise add a focused helper in `helpers_test.go`. If the helper itself becomes a significant lift (>~50 lines), surface as a Gate-6 stop-and-ask — it might mean the op needs proper feature work, not a shape probe.
- For ops we genuinely can't test (e.g. multi-node cluster state we don't model in E2E), document the gap in the audit table's notes column and stop-and-ask. Don't fake coverage.

### Estimated work

Unknown until first validator run produces the uncovered list. Audit foundation commit will tell us the size; the design commits to closing whatever it surfaces but acknowledges scope visibility is delayed until the audit runs.

## 7. Derived-artefact updates

Three artefacts update in lockstep with the per-domain commits, not in a separate batch.

### `e2e/parity/client/types.go`

Hand-rolled mirror types for the wire format (sometimes drift from server reality, per the M3 design doc's "approved deviation"). After the spec fixes land, these types update to reflect the corrected shapes. Likely shrinks via re-export from `genapi.*` where the generated type is a clean fit; otherwise keeps hand-rolled types with corrected fields.

### `e2e/parity/registry.go` and parity scenarios in `e2e/parity/*.go`

Pinpoint each scenario whose assertion implicitly relied on a now-fixed shape (e.g. JSON-in-string `content`, missing envelope wrapper, malformed `EntityTransactionResponse` array). Update assertions to consume the corrected wire format. The Cassandra plugin (out of scope) consumes this registry via Go module dep and surfaces any breakage on its next dep update — file an issue in `cyoda-go-cassandra` only if a backwards-incompatible interface shift surfaces.

### `cmd/cyoda/help/content/openapi.md`

Narrative content that may reference corrected fields. Audited and updated where the narrative would be misleading after the fix. The `cyoda help openapi {json,yaml,tags}` action outputs auto-emit from the embedded spec via `genapi.GetSwagger()` — no code change needed there.

### Lockstep rule

Every commit that changes a response schema in `api/openapi.yaml` also updates the corresponding handler (if any handler-side fix is needed), parity test (if affected), and narrative (if affected) in the same commit. No "schema first, fix tests next commit" — that violates Gate 5 (would leave intermediate commits with failing tests).

## 8. Commit topology

Foundation-then-domains. ~10-11 commits.

### Foundation

1. **Validator + collector + end-of-suite report.** Adds `internal/e2e/openapivalidator/` package with collector, `Mismatch` type, and the wrapping middleware. Wires into `internal/e2e/e2e_test.go`'s `TestMain`. No spec or handler changes yet. Build green; existing E2E tests pass; validator runs against the current (drifted) spec and produces the first list of mismatches printed at end-of-suite. Test that pins the validator itself (small unit test feeding a known-mismatching response, asserting it's collected) is also part of this commit.
2. **Audit table skeleton.** Adds `docs/superpowers/audits/2026-04-29-openapi-conformance-audit.md` with all 81 ops listed, columns `operationId`/`method`/`path`/`handler` filled in via the one-shot `tools/audit-skeleton/` script. Response-shape columns filled in by reading each handler. Disposition column empty; resolved-by-commit column empty. The validator output from commit 1 informs which rows need fix-spec / fix-server / fix-both.

### Per-domain commits

(One per domain, each: spec changes + handler fixes + new E2E coverage + parity updates + audit table rows updated.)

3. **account / IAM** (10 ops; mostly simple GETs)
4. **messaging** (5 ops; includes the JSON-in-string fix and #193's documentation marker)
5. **audit** (4 ops; includes the `AuditEvent` discriminator-union schema)
6. **search** (6 ops)
7. **model** (12 ops; export/import; possibly XML schemas need careful handling)
8. **workflow** (8 ops)
9. **entity** (14 ops; includes the original #21 confirmed defects — POST array, GET envelope)
10. **dispatch / health** (4 ops; trivial)

### Final cleanup

11. **Derived artefacts + final consistency check.** `cmd/cyoda/help/content/openapi.md` narrative pass; final `e2e/parity` consistency check; verify ADR 0001 is unchanged (no decision drift during execution); close out any audit-table rows still empty (everything `match` if no fix was needed); confirm validator end-of-suite report is empty.

### Verification cadence

After each commit: `go build ./... && go test -short ./...`. Before merge: `make test-all && go test -race ./...` (CLAUDE.md gates).

### Order rationale

Account/IAM first because it's mostly trivial GETs — proves the per-domain commit pattern works against simple cases before hitting harder ones. Audit and search later because the discriminator-union and `oneOf` shapes are the riskiest. Entity last because it's the largest and contains the original #21 defects — by then the pattern is settled.

## 9. Risk register

Risks ordered by likelihood × impact, each with a mitigation:

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| `kin-openapi/openapi3filter` doesn't catch a class of mismatch we care about (e.g. silently passes a missing required field, doesn't validate `oneOf` discriminator dispatch correctly) | Medium | High | Validator must catch all four named #21 defects as fixtures (POST array, GET envelope, JSON-in-string, basicAuth) BEFORE being trusted as a guard. A small unit test under `internal/e2e/openapivalidator/` feeds each defect's wire shape against its operation's spec and asserts the collector records the expected mismatch. If any fixture fails to surface, investigate the validator before continuing the audit |
| Operations not exercisable in E2E (e.g. require multi-node cluster state we don't model) leave coverage holes | Medium | Medium | Stop-and-ask at the per-domain commit. Document the gap in the audit table's notes column. Don't fake coverage; either expand E2E infrastructure or accept the documented gap |
| Audit pass surfaces a handler defect that requires service-layer changes to fix (Gate 6 stop-and-ask) | Medium | Medium | Each per-domain commit can stop on its own surface. If the fix balloons, surface the choice: either land a bounded fix in this PR or split off as a separate issue with explicit scope boundary |
| Cyoda Cloud reference spec (`docs/cyoda/openapi.yml`) shows the same defect as cyoda-go, so "fix the spec" doesn't have a clear right answer (server-vs-Cloud-vs-cyoda-go-spec triangle) | Low | Medium | Default: server is source of truth (per #21 body). Note the Cloud divergence in the audit table's notes column. The future external-reconciliation milestone resolves it; this PR doesn't |
| Validator runtime cost in E2E suite is non-trivial | Low | Low | E2E suite already takes ~30s; validator adds tens of microseconds per response. Negligible. If wrong, wrap behind a build tag or env var |
| Per-op error-status declarations explode the spec size (81 ops × 4-6 error blocks each) | Medium | Low | Use shared `responses` components — `default`, `Unauthorized`, `Forbidden`, `BadRequest` defined once in `components.responses`, `$ref`'d from each operation. Adds ~3 lines per operation, not 30 |
| `e2e/parity` registry changes break the Cassandra plugin on its next dep update | Low | Medium | Out of scope per #21. If parity test fails for Cassandra, file an issue in `cyoda-go-cassandra`. Wire-shape changes should not affect the SPI surface; parity test assertion changes might |
| Per-domain commits drift in style — each one writes the audit table entries differently, error declarations differently | Medium | Low | Foundation commit 2 establishes the audit-table convention; the first per-domain commit (account/IAM) sets the implementation pattern. Subsequent commits follow it |
| `cmd/cyoda/help/openapi*.go` artefacts consumed by cyoda-docs change shape, breaking docs build | Medium | Low | cyoda-docs takes a snapshot of the help output; the docs repo will need a sympathetic update. Cross-repo coordination noted in PR description |
| 5xx envelope misuse — handler returns an unsanitized error message in the body, leaking internals | Low | High (security) | Existing `common.Internal(...)` already structures the 5xx envelope correctly. Validator catches shape drift but not message content. Reviewer scans new handler code for `common.Internal(msg, err)` calls and verifies `msg` is generic. Per CLAUDE.md no-leak rule |

### Stop-and-ask triggers (Gate 6 surface points)

- Validator misses a fixture (showstopper — investigate before continuing)
- Audit surfaces a handler defect requiring service-layer rework
- An operation has no clean E2E coverage path
- A spec defect is shared between cyoda-go and Cloud, so "match the server" doesn't resolve which form is canonical

In each case: stop, surface the choice, do not silently pick.
