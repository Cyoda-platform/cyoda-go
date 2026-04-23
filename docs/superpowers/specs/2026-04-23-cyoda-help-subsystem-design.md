# cyoda help subsystem — design (v0.6.1 Phase 1)

**Target release:** v0.6.1
**Tracks issue:** #80 (Ship a topic-structured `cyoda help` surface)
**Scope:** Phase 1 — tooling infrastructure + reference-first content (`cli`, `config`, `errors`), CLI and REST consumption, release-asset CI. Remaining 10 top-level topic frames ship as experimental stubs.
**Bundled fix:** `--version` flag (handler missing from the binary; ldflag wiring and vars already exist).

## Goal

Embed a topic-organised help system in the `cyoda` binary. Four consumption paths from a single source:

1. `cyoda help <topic>` on the CLI (terminal-friendly text by default)
2. `GET /api/help[/<path>]` on any running server (JSON)
3. `cyoda_help_<version>.{tar.gz,json}` as release assets (static, version-scoped)
4. `cyoda --help` delegates to the help subsystem — single source of truth, no drift

v0.6.1 ships the engine + authoritative content for three reference-first topics. Remaining concept topics accrue in subsequent patches; every new topic is a standalone markdown file with no Go code change required.

## Non-goals

- Full authoring of all 13 topic trees (phased over v0.6.2+)
- Enterprise/Cassandra topic content (OSS-only build; architecture supports overlay — see §Forward compatibility)
- cyoda-docs website integration (consumes the release asset separately, cross-repo)
- Shell completion for topic names (v0.6.2+)
- Localisation/i18n (English only; out of scope indefinitely)
- Interactive paging (`| less` idiom suffices)

## Architecture

### Canonical path forms

The same topic has four string representations. One rule: **dots for identifiers, slashes for filesystem, spaces for CLI argv.**

| Form | Example | Used in |
|---|---|---|
| Filesystem | `cli/serve.md` | `cmd/cyoda/help/content/**` — where authors edit |
| Front-matter `topic:` | `cli.serve` | YAML header of each `.md` |
| JSON `topic` field | `cli.serve` | `cyoda help --format json`, `/api/help`, release JSON asset |
| REST path parameter | `cli.serve` | `GET {ContextPath}/help/cli.serve` (dots on the URL; no slash segments) |
| CLI argv | `cli serve` | `cyoda help cli serve` |
| Go `Topic.Path` | `[]string{"cli","serve"}` | internal representation; converters in both directions |

Content test #7 asserts the filesystem→front-matter mapping: `cli/serve.md` must declare `topic: cli.serve`, reject mismatches at `Tree.Load()` time.

### Source layout

Authoring source is markdown with YAML front-matter, organised as a hierarchical directory tree under `cmd/cyoda/help/content/`. Top-level parent topics live as sibling `.md` files alongside their child directories.

```
cmd/cyoda/help/content/
├── cli.md                  # "cli" (stable)
├── cli/
│   ├── serve.md            # "cli.serve"
│   ├── init.md             # "cli.init"
│   ├── migrate.md          # "cli.migrate"
│   ├── health.md           # "cli.health"
│   └── help.md             # "cli.help" (meta-topic: includes stability contract)
├── config.md               # "config" (stable)
├── config/
│   ├── database.md
│   ├── auth.md
│   ├── grpc.md
│   └── schema.md
├── errors.md               # "errors" (stable)
├── errors/
│   ├── MODEL_NOT_FOUND.md
│   ├── POLYMORPHIC_SLOT.md
│   └── ...                 # one per ErrCode* in internal/common/error_codes.go
├── crud.md                 # stub (experimental)
├── search.md               # stub
├── analytics.md            # stub
├── models.md               # stub
├── workflows.md            # stub
├── run.md                  # stub
├── helm.md                 # stub
├── telemetry.md            # stub
├── openapi.md              # stub
├── grpc.md                 # stub
└── quickstart.md           # stub
```

Content is embedded via `go:embed content/` at build time.

### Front-matter schema

```yaml
---
topic: cli                    # required: dotted path; must match filesystem location
title: "cyoda CLI — subcommand reference"
stability: stable             # required: stable | evolving | experimental
see_also:                     # optional: list of topic paths (dotted)
  - config
  - run
version_added: "0.6.1"        # optional
see_also_replace: false       # optional, default false; Enterprise-overlay only:
                              # when true, this topic's see_also REPLACES rather
                              # than unions with the OSS counterpart. Opt-in to
                              # advertised divergence.
---
```

Body follows the man-page template (NAME / SYNOPSIS / DESCRIPTION / OPTIONS / EXAMPLES / SEE ALSO) using H2 headings.

**Front-matter `see_also` is authoritative** for navigation and for the JSON output. The body's `SEE ALSO` section is advisory prose only — the markdown renderer strips it from the body and re-emits the authoritative list.

### Supported markdown subset

Authoring is constrained to a pinned subset so the custom text tokenizer (~200 LOC) stays small and predictable. The content linter test (test #10) rejects anything outside this list:

- ATX headings: `# H1`, `## H2`, `### H3`
- Paragraphs (blank-line separated)
- Single-level bullet lists starting with `-` or `*`
- Fenced code blocks (``` ` ``` opening and closing fences; language hint ignored by text renderer)
- Inline: `**bold**`, `*italic*`, `` `code` ``, `[text](url)` — exactly these four, no nesting
- Horizontal rules: `---` on its own line

**Not supported:** tables, nested lists, blockquotes, footnotes, admonitions, HTML, images, setext headings, reference-style links, inline HTML. Content using any of these is a build-time failure (see test #10).

This bound is explicit and enforced. If a future topic legitimately needs tables or nested lists, that's a spec-change event: either extend the tokenizer (with tests) or adopt `goldmark` — one decision, one time.

### Go types

```go
// cmd/cyoda/help/help.go
package help

type Topic struct {
    Path      []string    // ["cli", "serve"]
    Title     string
    Stability string      // stable | evolving | experimental
    SeeAlso   []string    // dotted paths
    Body      []byte      // markdown body, front-matter stripped, SEE ALSO stripped
    Children  []*Topic    // from directory walk
}

type Tree struct {
    Root *Topic           // synthetic root; Children are top-level topics
}

// Package-level, initialised once from go:embed content/ at program start.
var DefaultTree = loadEmbedded()

// Loader supports overlay for Enterprise builds (see §Forward compatibility).
func Load(roots ...fs.FS) (*Tree, error)
```

`fs.FS` input lets tests inject a synthetic tree and lets Enterprise add an overlay without modifying the OSS loader.

### Rendering layer

Three renderers under `cmd/cyoda/help/renderer/`. All take a `*Topic` and produce bytes.

**`json.go`** — marshals `TopicDescriptor`:

```go
type TopicDescriptor struct {
    Topic     string    `json:"topic"`       // "cli.serve"
    Path      []string  `json:"path"`
    Title     string    `json:"title"`
    Synopsis  string    `json:"synopsis"`    // first paragraph of DESCRIPTION
    Body      string    `json:"body"`        // full markdown, SEE ALSO re-emitted
    Sections  []Section `json:"sections"`    // parsed H2 sections
    SeeAlso   []string  `json:"see_also"`
    Stability string    `json:"stability"`
    Children  []string  `json:"children,omitempty"`
}

type Section struct {
    Name string `json:"name"` // "SYNOPSIS", "DESCRIPTION", etc.
    Body string `json:"body"`
}
```

Full-tree output wraps these:

```go
type HelpPayload struct {
    Schema  int                `json:"schema"`   // monotonic version, starts at 1
    Version string             `json:"version"`  // binary version (ldflag-injected)
    Topics  []TopicDescriptor  `json:"topics"`
}
```

**`schema` versioning contract**: additive changes to `TopicDescriptor`/`Section` keep `schema: 1`. Breaking changes (field removal, semantic change) bump to `schema: 2` and document migration. Consumers key on `schema` before parsing.

**`markdown.go`** — pass-through. Front-matter stripped, body-level `SEE ALSO` stripped, authoritative `SEE ALSO` re-emitted from front-matter at the end.

**`text.go`** — ANSI-ified for the pinned markdown subset:

- H1/H2/H3 → ANSI bold, blank-line padding
- `**bold**` / `*italic*` → ANSI codes
- `` `code` `` → dim/inverted
- Fenced code blocks → 2-space-indented + dim
- Bullet lists → `  • item`
- Links `[text](url)` → `text (url)` plain
- Horizontal rules → dim line of `─`
- TTY detection via `golang.org/x/term` — drops all ANSI when stdout is piped

**Output policy exception:** `cmd/cyoda/help/renderer/` writes user-facing output to injected `io.Writer`s via `fmt.Fprint*`. This is a documented carve-out from the `log/slog`-exclusive rule (`.claude/rules/logging.md`) — that rule governs **operational logging**, not CLI stdout. Comment to that effect at the top of `text.go`.

`--format=auto` (the default) resolves to `text` on TTY stdout, `markdown` when piped. It does **not** apply to JSON — `--format=json` is explicit and always produces JSON.

### CLI command

```
cyoda help                             # tree summary (all top-level topics, grouped by stability)
cyoda help <topic>                     # renders a top-level topic
cyoda help <topic> <sub>               # drilldown
cyoda help <topic> <sub> <sub2>        # depth-3
cyoda help --format=markdown <...>
cyoda help --format=json               # no topic → full tree
cyoda help --format=json <topic>       # single descriptor
```

**`cyoda --help` / `cyoda -h`** is rewired to invoke `cyoda help cli` internally. No separate printHelp.

**`cyoda --version` / `cyoda -v`** prints the existing ldflag-injected `version`, `commit`, `buildDate` vars in a single parse-friendly line, then exits 0:

```
cyoda version 0.6.1 (commit abc1234, built 2026-04-23T14:06:14Z)
```

For `go run` without ldflags: `cyoda version dev (commit unknown, built unknown)`.

**`printHelp()` deletion.** `cmd/cyoda/main.go:258-393` (the current `printHelp` function) is removed in this change. Its content migrates into `cli/*.md` and `config/*.md` topic bodies. The parity test (test #11) asserts every `CYODA_*` env var referenced in the codebase appears in at least one `config/**/*.md` file — enforces the content migration didn't lose anything.

**Tree summary output** (`cyoda help` with no args, text format) groups by stability:

```
cyoda help — topic reference

Stable
  cli              operate the binary (subcommands, conventions)
  config           configuration model, env vars, precedence
  errors           error catalogue and RFC 7807 shape

Experimental — content pending
  analytics        Trino SQL surface
  crud             entity CRUD over REST
  grpc             gRPC surface, CloudEvents envelope
  helm             chart values reference
  models           entity model overview
  openapi          REST surface overview
  quickstart       install, bootstrap, first entity
  run              deployment shapes
  search           query modes and predicates
  telemetry        observability interface
  workflows        state-machine model

Run 'cyoda help <topic>' for details.
```

Sort: alphabetical within each stability group. Stability groups in order: stable, evolving, experimental.

**Stub body convention.** Experimental-stub topic bodies use a minimal two-line form, not a full templated frame:

```markdown
# crud

**Content pending in v0.6.1.** See the cyoda-go README for links to current external documentation while this topic is authored.
```

No external URL hardcoded in stubs — stubs ship on every release and an outdated link becomes a broken link. The README is the authoritative external-links directory and is updated alongside releases via Gate 4. No NAME/SYNOPSIS/DESCRIPTION sections either — user sees immediately that this is a placeholder, not polished-but-thin documentation.

**Error behavior:**

- Unknown topic: exit 2, message `cyoda help: no such topic: "widgetry". Run 'cyoda help' to list available topics.`
- Unknown subtopic under valid parent: exit 2, message `cyoda help: topic "config" has no subtopic "widgetry". Available: database, auth, grpc, schema. Run 'cyoda help config' for an overview.`
- Unknown `--format`: exit 2 via argument parser.

### REST API

Help is accessible over HTTP on any running cyoda server. Zero auth (read-only, public by construction — the binary ships the content openly).

**Mount point: `{ContextPath}/help`**. The operator-configurable `CYODA_CONTEXT_PATH` (default `/api`, see `app/config.go:ContextPath`) prefixes this endpoint like every other API route, so an operator who sets `CYODA_CONTEXT_PATH=/v1/api` gets `/v1/api/help`. Help is an API resource — it honors the API prefix.

| Method | Path | Response |
|---|---|---|
| `GET` | `{ContextPath}/help` | `200 application/json` — full `HelpPayload` (same shape as release JSON asset) |
| `GET` | `{ContextPath}/help/{topic}` | `200 application/json` — single `TopicDescriptor` |

`{topic}` uses the dotted form: `{ContextPath}/help/cli.serve`. No slash segments.

**Path validation.** `{topic}` is regex-validated against `^[A-Za-z0-9._-]+$` at handler entry. Non-matches return `400` (RFC 7807), not `404` — the distinction matters: malformed input vs. well-formed-but-unknown topic.

**Error responses (both RFC 7807 Problem Details):**

- Unknown topic → `404`:
  ```json
  { "type": "about:blank",
    "title": "Not Found",
    "status": 404,
    "detail": "no such help topic: widgetry",
    "code": "HELP_TOPIC_NOT_FOUND" }
  ```
- Malformed topic path → `400`:
  ```json
  { "type": "about:blank",
    "title": "Bad Request",
    "status": 400,
    "detail": "invalid topic path: contains disallowed characters",
    "code": "BAD_REQUEST" }
  ```

**Bundled errors-catalog additions:** this change adds `ErrCodeHelpTopicNotFound = "HELP_TOPIC_NOT_FOUND"` to `internal/common/error_codes.go` AND `cmd/cyoda/help/content/errors/HELP_TOPIC_NOT_FOUND.md` in the same PR. Test #12 (ErrCode parity) gates this — the code and its doc must ship together.

**CORS:** default GET-from-anywhere acceptable for help content. Nothing sensitive. Header: `Access-Control-Allow-Origin: *` on help endpoints only.

**OpenAPI:** the endpoints are added to the generated OpenAPI spec so they appear in any future #81 release asset.

**Handler location:** `internal/api/help.go` + `RegisterHelpRoutes(mux http.Handler, tree *help.Tree)` — matching the existing flat layout of `internal/api/` (e.g. `health.go`, `admin.go`, `scalar.go`). No new subdirectory. ~60 LOC including path validation + test file.

### Forward compatibility for Enterprise builds

The loader supports **overlay merging** at load time:

```go
// OSS build
tree, _ := help.Load(ossContent)

// Enterprise build (cyoda-go-cassandra embeds extra content)
tree, _ := help.Load(ossContent, enterpriseContent)
```

**Collision semantics for a topic path that exists in both inputs:**

- `Body`, `Title`, `Stability`, `version_added` — later argument wins (Enterprise replaces).
- `SeeAlso` — **union**, OSS order preserved, later-argument's entries appended, deduplicated. Prevents the OSS cross-topic navigation graph from being silently lost when Enterprise authors forget to copy-forward `see_also` entries. Enterprise authors can opt into replacement by setting `see_also_replace: true` in front-matter (deliberately verbose, advertises the divergence).
- `Children` — merged by path; collision rules recurse.

**Unit test `TestLoad_OverlayMerge`** constructs two synthetic `fstest.MapFS` trees:

- One with `topic-a.md` (see_also: `[x, y]`) + `topic-c.md`
- One with `topic-a.md` (see_also: `[z]`, different body) + `topic-b.md`

Asserts: merged tree has `topic-a`, `topic-b`, `topic-c`; `topic-a.Body` is from the second argument; `topic-a.SeeAlso == [x, y, z]` (union, dedup-aware). Second sub-test passes `see_also_replace: true` front-matter and asserts `topic-a.SeeAlso == [z]`.

v0.6.1 OSS build calls `help.Load(ossContent)` with a single arg. Enterprise wiring lands with the cyoda-go-cassandra integration, not in this release.

### Release asset contract

Two artifacts per `v*` tag, attached to the GitHub Release:

| Asset | URL | Content |
|---|---|---|
| `cyoda_help_<version>.tar.gz` | `https://github.com/Cyoda-platform/cyoda-go/releases/download/v<version>/cyoda_help_<version>.tar.gz` | `cmd/cyoda/help/content/` tree, verbatim |
| `cyoda_help_<version>.json` | `https://github.com/Cyoda-platform/cyoda-go/releases/download/v<version>/cyoda_help_<version>.json` | `HelpPayload` JSON |

**Generation is a goreleaser `before.hooks:` entry** so the assets appear inside `dist/` after `--clean` has run, and before `release.extra_files` globs them:

```yaml
# .goreleaser.yaml
before:
  hooks:
    - go mod tidy
    - bash -c 'mkdir -p dist && tar -czf "dist/cyoda_help_${VERSION#v}.tar.gz" -C cmd/cyoda/help content/'
    - bash -c 'go run ./cmd/cyoda help --format json > "dist/cyoda_help_${VERSION#v}.json"'
```

Goreleaser populates `$VERSION` (the current tag) automatically in `before.hooks`. Using `go run ./cmd/cyoda help --format json` as the JSON generator means the release asset is byte-identical to what the installed binary emits — same code, no separate tool.

**`.goreleaser.yaml` addition** in the top-level `release:` block:

```yaml
release:
  extra_files:
    - glob: ./dist/cyoda_help_*.tar.gz
    - glob: ./dist/cyoda_help_*.json
```

**Checksum coverage.** Goreleaser's `checksum:` config at `.goreleaser.yaml:97-99` hashes `archives:` artifacts only, not `release.extra_files`. Extended via an `after:` hook, which runs once goreleaser has written `SHA256SUMS` but before the release is published:

```yaml
# .goreleaser.yaml
after:
  hooks:
    - bash -c 'cd dist && sha256sum cyoda_help_*.tar.gz cyoda_help_*.json >> SHA256SUMS'
```

No signature exists on `SHA256SUMS` today (only `docker_signs:` at `.goreleaser.yaml:138`), so append-in-place is safe. If that changes in the future the hook moves to run before the signing step.

**Naming convention `cyoda_<artifact>_<version>.<ext>`** sets the pattern for #81 (openapi) and #82 (proto) to follow.

**Snapshot smoke (`release-smoke.yml`)** asserts the two assets appear in `dist/`:

```yaml
- name: Assert help assets generated
  run: |
    test -f "dist/cyoda_help_0.0.0.tar.gz"
    test -f "dist/cyoda_help_0.0.0.json"
    jq . "dist/cyoda_help_0.0.0.json" > /dev/null
```

## Testing

### Renderer tests (`cmd/cyoda/help/renderer/*_test.go`)

1. **Tree-walk symmetry.** Table-driven: for every topic in the tree, render all 3 formats. Assert:
   - JSON output parses; `schema: 1`
   - Markdown output starts with H1 matching front-matter `title`
   - Text output contains the title string (ANSI-stripped)
   - JSON `see_also` matches front-matter `see_also` verbatim
   - JSON `TopicDescriptor` has all required fields populated
2. **Tokenizer unit tests.** Table-driven markdown fragments + expected text output. Covers every supported subset element.
3. **JSON schema pin.** Golden-file test for one stable topic. Catches unintended struct changes.
4. **CLI dispatch.** `TestHelpCommand_UnknownTopic_ErrorAndExit2`, `TestHelpCommand_DepthTraversal`, `TestHelpCommand_FormatFlag`, `TestHelpCommand_NoArgsShowsGroupedTree`, `TestVersionFlag`.
5. **Front-matter parser.** Unit tests for malformed YAML — missing `topic`, invalid `stability`. Fail at `Tree.Load()` time, not invocation time.

### Content tests (`cmd/cyoda/help/help_test.go`)

6. **Valid front-matter everywhere.** Walk `content/`, parse each file, assert required fields.
7. **Topic path matches filesystem path.** `cli/serve.md` must declare `topic: cli.serve`.
8. **See-also targets exist.** Every `see_also` entry resolves to a topic in the tree.
9. **All 13 top-level frames present.** Hardcoded list — catches accidental deletion.
10. **Markdown-subset linter.** Reject any file using disallowed markdown constructs (tables, nested lists, HTML, reference-style links, etc.). Enforces tokenizer's pinned scope.
11. **`CYODA_*` env vars covered in `config/**/*.md`.** Scope: `cmd app plugins internal` — NOT just `cmd app plugins`. Many operational vars (`CYODA_CLUSTER_ENABLED`, `CYODA_MODEL_CACHE_LEASE`, `CYODA_SEARCH_*`, `CYODA_TX_*`, `CYODA_DISPATCH_*`, cluster-member IDs, compute-node creds) live in `internal/`. Exclude `_test.go` files to keep test-fixture vars out of the doc surface. An explicit allow-list excludes known test-only prefixes: `CYODA_TEST_*`, `CYODA_MARKER`, `CYODA_DEBUG`. Extracted set must be a subset of vars documented under `config/**/*.md`; each missing var is a test failure.

    Implementation sketch:
    ```go
    //go:build !short
    func TestConfig_EnvVarCoverage(t *testing.T) {
        referenced := extractEnvVars(t, "cmd", "app", "plugins", "internal") // excludes _test.go + allow-list
        documented := extractEnvVars(t, "cmd/cyoda/help/content/config")
        for v := range referenced {
            if _, ok := documented[v]; !ok {
                t.Errorf("CYODA_* var %q referenced in source but not documented in config/**/*.md", v)
            }
        }
    }
    ```

11b. **`printHelp()` content-migration parity.** `printHelp()` contains more than env vars — subcommand descriptions, `_FILE` secret-pattern documentation, quick-start examples, Docker/compose wrappers. A must-appear phrase list asserts post-migration coverage:

    ```go
    var mustAppearSomewhereUnderCLIOrConfig = []string{
        "_FILE",                 // secret-from-file pattern
        "--force",               // cyoda init flag
        "--timeout",             // cyoda health flag
        "CYODA_PROFILES",        // env-var profile loader (app/profiles.go)
        "mock",                  // mock-IAM default warning
        "docker",                // run-docker.sh reference
    }
    ```

    Trivially extended when future migrations surface gaps. Purpose: close the confidence gap that test #11 (env-vars-only) leaves open.
12. **`ErrCode*` parity with `errors/*.md`.** Extract `grep -oE 'ErrCode[A-Z][A-Za-z]+\s*=\s*"([A-Z_]+)"' internal/common/error_codes.go` → set of codes. Compare against `ls cmd/cyoda/help/content/errors/*.md` stripped of `.md` extension. Missing code = test failure. This is the C-level defense against error-catalog drift.

### Overlay test (`cmd/cyoda/help/help_test.go`)

13. **`TestLoad_OverlayMerge`.** Construct two `fstest.MapFS` trees; assert merged tree has both topics and that the second argument's content wins on path collision.

### REST API tests (`internal/api/help_test.go`)

14. **`TestGetFullTree`.** `httptest.Server` serves `{ContextPath}/help`; assert `HelpPayload` JSON + `schema: 1`.
15. **`TestGetSingleTopic`.** `{ContextPath}/help/cli.serve` returns one descriptor.
16. **`TestGetUnknownTopic_404_RFC7807`.** `{ContextPath}/help/widgetry` returns 404 with correct problem-details shape and `HELP_TOPIC_NOT_FOUND` code.
17. **`TestMalformedTopicPath_400`.** `{ContextPath}/help/foo%20bar` (or any input containing chars outside `[A-Za-z0-9._-]`) returns 400 with RFC 7807 body and `BAD_REQUEST` code. Distinct from 404 — validates input hygiene.
18. **`TestCORSHeadersPresent`.** GET response includes `Access-Control-Allow-Origin: *`.
19. **`TestRespectsContextPath`.** Run handler with `CYODA_CONTEXT_PATH=/v1/api`; assert full tree is reachable at `/v1/api/help` and `/api/help` returns 404.

### No integration/e2e tests beyond the REST unit tests

Help is a local, deterministic subcommand + a stateless HTTP handler. No DB, no tenant scope, no async. Unit + CLI-dispatch + handler coverage is sufficient.

### Gate 5 verification

- `go test ./cmd/cyoda/... ./internal/api/handlers/help/...` green
- `go vet ./...` clean
- `go test -race ./...` one-shot before PR
- Manual smoke: `cyoda help`, `cyoda --help` (equals `cyoda help cli`), `cyoda help errors POLYMORPHIC_SLOT`, `cyoda help --format json | jq .`, `cyoda --version`
- Manual REST smoke: `curl http://localhost:8080/api/help | jq '.topics[0]'`, `curl http://localhost:8080/api/help/cli.serve`

## Content scope for v0.6.1

| Topic | Stability | Source of truth |
|---|---|---|
| `cli` + subcommands (`serve`, `init`, `migrate`, `health`, `help`) | stable | Hand-written, cross-checked by tests #11 (env vars) and post-migration from deleted `printHelp()` content |
| `config` + topic groups (`database`, `auth`, `grpc`, `schema`) | stable | Hand-written, covers every `CYODA_*` var enforced by test #11 |
| `errors` + every `ErrCode*` | stable | Hand-written, one subtopic per code, enforced by test #12 |
| `crud`, `search`, `analytics`, `models`, `workflows`, `run`, `helm`, `telemetry`, `openapi`, `grpc`, `quickstart` | experimental | Two-line stub bodies per the stub convention |

Estimated content: ~3500 lines of markdown across ~40 files.

## File structure

```
cmd/cyoda/
├── main.go                                # -printHelp, +--version handler, +--help→help cli, +help subcmd wiring
└── help/
    ├── help.go                            # go:embed + Tree + Topic types + Load(fs.FS...)
    ├── help_test.go                       # content-level + overlay tests (6-13)
    ├── command.go                         # subcommand + --format handling + tree-summary renderer
    ├── command_test.go                    # CLI dispatch tests (4)
    ├── content/                           # AUTHORING SOURCE
    │   └── ... (markdown tree)
    └── renderer/
        ├── tokenizer.go                   # ~200-line markdown subset tokenizer
        ├── tokenizer_test.go
        ├── text.go                        # + fmt-exception comment at top
        ├── text_test.go
        ├── markdown.go                    # + see-also strip+re-emit
        ├── json.go                        # + HelpPayload wrapper with schema version
        └── json_test.go

internal/
├── api/
│   ├── help.go                            # RegisterHelpRoutes(mux, tree) — flat, matches existing handlers
│   └── help_test.go                       # REST tests (14-19)
└── common/
    └── error_codes.go                     # +ErrCodeHelpTopicNotFound = "HELP_TOPIC_NOT_FOUND"

cmd/cyoda/help/content/errors/
└── HELP_TOPIC_NOT_FOUND.md                # bundled with the ErrCode* addition; test #12 enforces
```

Outside `cmd/cyoda/help/` and `internal/`:

- `cmd/cyoda/main.go` — delete `printHelp`, add `--version` handler, rewire `--help/-h` to `help cli`, add `help` dispatch arm
- `.goreleaser.yaml` — `release.extra_files` glob (ldflags untouched, already inject `buildDate`)
- `.github/workflows/release.yml` — Build-help-assets pre-release step + SHA256SUMS extension step
- `.github/workflows/release-smoke.yml` — assert help assets generated
- `README.md` — one-paragraph pointer at `cyoda help` and `/api/help`; Gate 4
- `CONTRIBUTING.md` — topic-tree stability contract (additions free; renames/removals require deprecation window); Gate 4
- `api/openapi.yaml` or equivalent source — `/api/help` endpoints for #81 future asset

## Estimated LOC

- Go code: ~1400 (renderer ~400, loader + overlay ~250, CLI command ~200, REST handler ~100, tests ~450)
- Markdown content: ~3500 across ~40 files
- Config (goreleaser + workflows + error codes): ~40

## Acceptance (maps to issue #80 criteria + bundled scope)

- [ ] `cyoda help` lists all 13 top-level topics grouped by stability with one-line synopses
- [ ] `cyoda help <topic>` renders the templated structure for each topic
- [ ] `cyoda help <topic> <subtopic>` works for every drilldown currently defined
- [ ] All three formats (`text`, `markdown`, `json`) produce equivalent content from a single source
- [ ] `cyoda help --format json` (no topic) emits `HelpPayload` with `schema: 1` and all descriptors
- [ ] Per-topic `stability` marker is present in all formats
- [ ] `GET {ContextPath}/help` returns `HelpPayload` with `schema: 1`; `GET {ContextPath}/help/{topic}` returns one descriptor; 404 on unknown, 400 on malformed path, both RFC 7807
- [ ] `CYODA_CONTEXT_PATH=/v1/api` correctly relocates the help endpoint to `/v1/api/help` (test #19)
- [ ] Release CI attaches `cyoda_help_<version>.tar.gz` and `cyoda_help_<version>.json` via goreleaser `before.hooks:`; both included in `SHA256SUMS` via `after.hooks:`
- [ ] Topic-tree stability contract documented in `CONTRIBUTING.md` and in the `cli.help` topic body
- [ ] `cyoda --version` prints ldflag-injected version + commit + build date
- [ ] `cyoda --help` renders `cyoda help cli` — no separate `printHelp()` maintained
- [ ] Markdown-subset linter rejects disallowed constructs (test #10)
- [ ] `CYODA_*` env var coverage test (test #11) — scope `cmd app plugins internal` minus `_test.go` minus allow-listed test-only vars — passes
- [ ] `printHelp()` content-migration parity (test #11b) — must-appear phrase list asserts subcommand descriptions, `_FILE`, `--force`, profile loader, etc. all survived the migration
- [ ] `ErrCode*` parity test (test #12) passes — including the newly-added `HELP_TOPIC_NOT_FOUND`
- [ ] Overlay-merge loader test (test #13) passes — both union-`see_also` and explicit `see_also_replace: true` paths covered

## Out of scope for v0.6.1 (tracked for later)

- Authoritative content for the 10 experimental-stub topics — each is a standalone future change, co-authored with cyoda-docs where applicable
- Enterprise build overlay for Cassandra-tier deltas — architecture ready via `Load(fs.FS...)`; activation lands with cyoda-go-cassandra
- cyoda-docs website integration with the release asset
- Shell completion for topic names (`cyoda help <TAB>`) — v0.6.2+
- OpenAPI release asset (#81), gRPC proto release asset (#82) — sibling patterns, separate tickets

## Decommission when done

No decommission — this is a new subsystem that will live for the lifetime of the project. One resurrected piece: the help subsystem formally replaces `cmd/cyoda/main.go:printHelp()`, which is deleted as part of this change.
