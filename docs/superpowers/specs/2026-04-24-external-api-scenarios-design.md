# External API Scenario Suite — Tranche 1 Design

- **Issue:** [#118](https://github.com/Cyoda-platform/cyoda-go/issues/118) (tranche 1 of 5; #119–#122 follow)
- **Date:** 2026-04-24
- **Target branch:** `release/v0.6.3`
- **Source dictionary:** `/Users/paul/dev/cyoda/.ai/plans/external-api-scenarios/` (cyoda-cloud repo)

## 1. Purpose

cyoda-cloud maintains a language-agnostic External API Scenario Dictionary
(~85 scenarios in 15 YAML files) describing REST/gRPC behaviour the platform
must satisfy. This tranche lays the foundation for running that dictionary
against cyoda-go across all storage plugins (memory, sqlite, postgres; the
cassandra plugin picks it up through the parity registry on its next dep
bump) and, on demand, against a remote cyoda-cloud instance.

The goal is to replace ad-hoc Kotlin-class-name regex matching in
cyoda-cloud's test suite with a normalised, cross-language error contract
and a shared scenario vocabulary.

## 2. Scope

### 2.1 In scope (tranche 1)

- Copy all 15 YAML scenarios verbatim into `e2e/externalapi/scenarios/`
  as reference documentation.
- Introduce the `HTTPDriver` abstraction at `e2e/externalapi/driver/`
  with `NewInProcess` and `NewRemote` constructors.
- Introduce the expected-error contract at `e2e/externalapi/errorcontract/`
  (Go struct + matcher + JSON Schema companion).
- Triage all 85 scenarios in `e2e/externalapi/dictionary-mapping.md`.
- Implement `Run*` parity tests for the four low-overlap files:
  - `01-model-lifecycle.yaml` (13 scenarios)
  - `03-entity-ingestion-single.yaml`
  - `04-entity-ingestion-collection.yaml`
  - `06-entity-delete.yaml`
- Add three missing client helpers to `e2e/parity/client/http.go`
  (`CreateEntitiesCollection`, `DeleteEntitiesByModel`,
  `DeleteEntitiesByCondition`) — Gate 6 fix, TDD'd.
- Validate remote-mode driver path against an in-process `httptest.Server`.

### 2.2 Out of scope

- YAML files `02`, `05`, `07`, `08`–`14` — tranches 2–4 (#119–#121).
- Live cyoda-cloud remote smoke — tranche 5 (#122, optional).
- `internal_only: true` and `shape_only: true` scenarios — recorded in
  the mapping doc, not implemented as tests.
- Server-side changes to the error wire format. The server keeps emitting
  RFC 9457 Problem Details unchanged.

## 3. Architecture

### 3.1 Directory layout

```
e2e/
├── externalapi/
│   ├── scenarios/                 # verbatim YAML copies (reference only)
│   │   ├── 00-endpoints.yaml
│   │   ├── 01-model-lifecycle.yaml
│   │   └── …through 14-polymorphism.yaml
│   ├── driver/                    # HTTPDriver abstraction
│   │   ├── driver.go
│   │   ├── driver_test.go
│   │   └── remote_smoke_test.go   # deliverable §6 validation
│   ├── errorcontract/             # expected-error contract
│   │   ├── contract.go
│   │   ├── contract_test.go
│   │   └── schema.json
│   └── dictionary-mapping.md      # 85-scenario triage
├── parity/
│   ├── externalapi/               # Go tests that drive the HTTPDriver
│   │   ├── model_lifecycle_test.go
│   │   ├── entity_ingestion_single_test.go
│   │   ├── entity_ingestion_collection_test.go
│   │   └── entity_delete_test.go
│   ├── client/                    # existing — gains 3 new helpers
│   └── registry.go                # `allTests` gains ExternalAPI_* entries
└── docs/
    └── test-scenarios/
        └── external-api-scenarios.md   # pointer/overview doc
```

### 3.2 Data flow

```
Go Run* function
  └─ constructs HTTPDriver via NewInProcess(t, fixture) OR NewRemote(t, baseURL, jwt)
       └─ HTTPDriver delegates to e2e/parity/client.Client
            └─ issues HTTP requests with Authorization: Bearer <jwt>
                 └─ cyoda-go HTTP stack (internal/api, internal/domain/*)
                      └─ RFC 9457 responses
       ← Captures returned as typed values (uuid.UUID, etc.)
  ← Errors asserted via errorcontract.Match(t, status, body, expected)
```

## 4. Components

### 4.1 `e2e/externalapi/driver` — `Driver`

```go
type Driver struct {
    t      *testing.T
    client *parityclient.Client
    // tenant identity for observability/assertions, not for multi-tenant tests
    tenantID string
}

// NewInProcess mints a fresh tenant via fixture.NewTenant(t) and wires
// up a client against the fixture's BaseURL. Used by parity Run* tests.
func NewInProcess(t *testing.T, fixture parity.BackendFixture) *Driver

// NewRemote wires up a client against an arbitrary base URL using
// a pre-minted JWT. Used for remote-mode smoke tests and, later,
// live cyoda-cloud smoke (tranche 5).
func NewRemote(t *testing.T, baseURL, jwtToken string) *Driver
```

**Typed helpers (tranche-1 vocabulary):**

| Method | Returns | YAML action |
|--------|---------|-------------|
| `CreateModelFromSample(name, version, sample string)` | `error` | `create_model_from_sample` |
| `UpdateModelFromSample(name, version, sample string)` | `error` | `update_model_from_sample` |
| `ExportModel(converter, name, version)` | `(json.RawMessage, error)` | `export_model` |
| `LockModel(name, version)` | `error` | `lock_model` |
| `UnlockModel(name, version)` | `error` | `unlock_model` |
| `DeleteModel(name, version)` | `error` | `delete_model` |
| `CreateEntity(name, version, body)` | `(uuid.UUID, error)` | `create_entity` |
| `CreateEntitiesCollection(items []CollectionItem)` | `([]uuid.UUID, error)` | `create_entities_collection` |
| `UpdateEntitiesCollection(items []UpdateItem)` | `error` | `update_entities_collection` |
| `DeleteEntity(id uuid.UUID)` | `error` | `delete_entity` |
| `DeleteEntitiesByModel(name, version)` | `error` | `delete_entities_by_model` |
| `DeleteEntitiesByCondition(name, version, condition string)` | `error` | `delete_entities_by_condition` |

Raw-byte companions (`…Raw` variants) return `(int, []byte, error)` for
negative-path tests that assert on the error body.

All helpers delegate to the existing `e2e/parity/client.Client`. The driver
is a thin wrapper that (a) makes in-process vs remote construction explicit,
(b) names methods to match the dictionary vocabulary, and (c) surfaces only
the subset relevant to external callers (no backend handles, no fixture
internals).

### 4.2 `e2e/externalapi/errorcontract` — expected-error contract

```go
type ExpectedError struct {
    HTTPStatus int
    ErrorCode  string        // empty = don't assert
    Fields     []ErrorField  // nil = don't assert
}

type ErrorField struct {
    Path          string
    Value         any
    EntityName    string
    EntityVersion int
}

// Match parses an RFC 9457 Problem Details response body, lifts
//   properties.errorCode  -> ErrorCode
//   properties.fields     -> Fields
// and asserts against `want`. Zero-value want fields are skipped.
func Match(t *testing.T, httpStatus int, body []byte, want ExpectedError)
```

**Why test-match (not wire-format):** cyoda-go already emits RFC 9457-
compliant Problem Details; rewriting the wire format would be a breaking
change for no test benefit. Cyoda-cloud is required to produce output that
maps cleanly into `ExpectedError` — however its wire shape is structured.
The `schema.json` companion at `e2e/externalapi/errorcontract/schema.json`
documents the *normalised* struct for cross-language consumers, not the
wire.

### 4.3 `e2e/parity/externalapi` — parity Run* tests

One file per tranche-1 YAML. Each scenario becomes one exported `Run*`
function. Example signature:

```go
func RunExternalAPI_01_01_CreateModelFromSample(t *testing.T, fixture parity.BackendFixture) {
    d := driver.NewInProcess(t, fixture)
    if err := d.CreateModelFromSample("family", 1, sampleFamilyJSON); err != nil {
        t.Fatalf("create model: %v", err)
    }
    // …assertions per YAML scenario…
}
```

All `Run*` functions register in `e2e/parity/registry.go` via the existing
`allTests` slice:

```go
allTests = append(allTests,
    parity.NamedTest{Name: "ExternalAPI_01_01_CreateModelFromSample",
                     Fn: externalapi.RunExternalAPI_01_01_CreateModelFromSample},
    // …
)
```

Naming convention: `ExternalAPI_<file>_<scenario>_<description>` where
`<file>_<scenario>` is the `source_id` from `dictionary-mapping.md`
with the `/` separator rewritten as `_` (Go identifiers can't contain
slashes). So `source_id` `01/03` → `RunExternalAPI_01_03_<description>`.
This cross-references cleanly and lets the cassandra plugin pick the
tests up verbatim.

### 4.4 `e2e/parity/client` — three new helpers

Added alongside the existing 30 methods:

```go
func (c *Client) CreateEntitiesCollection(t *testing.T, items []CollectionItem) ([]uuid.UUID, error)
func (c *Client) DeleteEntitiesByModel(t *testing.T, name string, version int) error
func (c *Client) DeleteEntitiesByCondition(t *testing.T, name string, version int, condition string) error
```

Each driven red/green; unit tests in `e2e/parity/client/http_test.go` use an
`httptest.Server` with a handler that asserts method + path + body shape.

### 4.5 `e2e/externalapi/dictionary-mapping.md`

A table covering all 85 scenarios:

| Column | Values |
|--------|--------|
| `source_id` | e.g., `01/01`, `numeric/07` |
| `cyoda_go_status` | `covered_by:<Run-fn>` / `new:<proposed-Run-fn>` / `internal_only_skip` / `shape_only_skip` / `gap_on_our_side` |
| `notes` | free text |

Plus a reverse section: parity entries we already have that aren't yet in
the upstream dictionary (e.g., `NumericClassification*`) — to be proposed
back upstream.

### 4.6 `e2e/externalapi/driver/remote_smoke_test.go`

One test: `TestDriverRemoteModeSmoke`. Spins up an in-process
`httptest.Server` via the memory fixture, mints a tenant JWT, hands
`(baseURL, jwtToken)` to `NewRemote`, then replays a concrete scenario
end-to-end (create model → lock → create entity → delete entity, i.e.
the spine of `01/01` + `03/01` + `06/01`). Ensures the remote path
touches no fixture internals.

## 5. Error handling

- **Positive path:** client helpers return `error`; tests `t.Fatalf` on
  non-nil. Existing `e2e/parity/client` already does 409-retry with
  `retryable` detection; nothing new needed.
- **Negative path:** tests use `*Raw` helpers to capture status code +
  body, then call `errorcontract.Match(t, status, body, want)`.
- **Security by default (Gate 3):** the matcher never logs JWT tokens or
  request bodies verbatim; it asserts against fields and reports field
  diffs only.

## 6. Testing strategy

- **TDD red/green** for every new `Run*` function and every new client
  helper. Each scenario YAML entry drives one failing test first.
- **Driver unit tests** (`e2e/externalapi/driver/driver_test.go`) cover
  the dispatching layer against an `httptest.Server` — independent of any
  storage backend.
- **Errorcontract unit tests** cover parsing edge cases: RFC 9457 bodies
  with/without `properties.errorCode`, with/without `properties.fields`,
  malformed bodies.
- **Parity registry runs** exercise the full path through memory, sqlite,
  and postgres; cassandra inherits on its next dep bump.
- **Race detector** one-shot before PR (`go test -race ./...`) per
  `.claude/rules/race-testing.md`.

## 7. Acceptance criteria

From issue #118:

- `go test ./e2e/parity/... -v` green across memory, sqlite, postgres.
- `make test-all` green (includes plugin submodules).
- `dictionary-mapping.md` triaged for all 85 scenarios.
- `HTTPDriver` remote mode validated against at least one scenario
  pointed at an in-process `httptest.Server` (no live cyoda-cloud).

## 8. Open questions / deferred

None blocking. Cross-tenant and multi-driver scenarios are a tranche-3
concern (10-concurrency-and-multinode.yaml) and are explicitly out of
scope here.

## 9. Workflow

Per `CLAUDE.md` feature workflow and memory rules:

1. Worktree on `feat/issue-118-external-api-tranche1` off `release/v0.6.3` ✓ done
2. Brainstorming ✓ done
3. This design doc
4. `superpowers:writing-plans` → executable plan
5. `superpowers:subagent-driven-development` → TDD implementation
6. `superpowers:verification-before-completion`
7. `superpowers:requesting-code-review`
8. `antigravity-bundle-security-developer:cc-skill-security-review`
9. PR targeting `release/v0.6.3` with `Closes #118` in body
