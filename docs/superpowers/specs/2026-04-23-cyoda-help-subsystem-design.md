# cyoda help subsystem — design (v0.6.1 Phase 1)

**Target release:** v0.6.1
**Tracks issue:** #80 (Ship a topic-structured `cyoda help` surface)
**Scope:** Phase 1 — tooling infrastructure + reference-first content (`cli`, `config`, `errors`). All other topic frames ship as experimental stubs.
**Bundled fix:** `--version` flag (hallucinated in docs, missing from binary).

## Goal

Embed a topic-organised help system in the `cyoda` binary. Three output formats (`text`, `markdown`, `json`) from one source. The binary becomes the authoritative reference for flags, env vars, metrics, error codes, and endpoints — versioned with the binary itself rather than drifting on an external docs site.

v0.6.1 ships the engine plus authoritative content for three reference-first topics. Remaining concept topics accrue in subsequent patches; every new topic is a standalone markdown file with no Go code change required.

## Non-goals

- Full authoring of all 13 topic trees (phased over v0.6.2+)
- Enterprise/Cassandra topic content (OSS-only build)
- Cross-repo docs-site integration (cyoda-docs imports the help.tar.gz asset separately)
- Interactive paging (`| less` idiom is sufficient)

## Architecture

### Source layout

Authoring source is markdown with YAML front-matter, organised as a hierarchical directory tree under `cmd/cyoda/help/content/`. Filesystem path maps 1:1 to topic path — `cli/serve.md` is the `cli serve` topic. Top-level parent topics live as sibling `.md` files alongside their child directories (e.g. `cli.md` beside `cli/`).

```
cmd/cyoda/help/content/
├── cli.md                  # "cli"
├── cli/
│   ├── serve.md            # "cli serve"
│   ├── init.md             # "cli init"
│   ├── migrate.md          # "cli migrate"
│   └── help.md             # "cli help" (meta)
├── config.md
├── config/
│   ├── database.md
│   ├── auth.md
│   ├── grpc.md
│   └── schema.md
├── errors.md
├── errors/
│   ├── MODEL_NOT_FOUND.md
│   ├── POLYMORPHIC_SLOT.md
│   └── ... (one per ErrCode* in internal/common/error_codes.go)
├── crud.md                 # stub
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
topic: cli                    # required: dotted path, must match filesystem location
title: "cyoda CLI — subcommand reference"
stability: stable             # required: stable | evolving | experimental
see_also:                     # optional: list of topic paths
  - config
  - run
version_added: "0.6.1"        # optional
---
```

Body follows the man-page template from issue #80 (NAME / SYNOPSIS / DESCRIPTION / OPTIONS / EXAMPLES / SEE ALSO) using H2 headings. Front-matter `see_also` is authoritative for navigation; the body's `SEE ALSO` section is its human-readable presentation.

### Go types

```go
// cmd/cyoda/help/help.go
package help

type Topic struct {
    Path      []string    // ["cli", "serve"]
    Title     string
    Stability string      // stable | evolving | experimental
    SeeAlso   []string    // dotted paths
    Body      []byte      // markdown body, front-matter stripped
    Children  []*Topic    // from directory walk
}

type Tree struct {
    Root *Topic           // synthetic root; Children are top-level topics
}

// Package-level, initialised once from go:embed content/ at program start.
var DefaultTree = loadEmbedded()
```

### Rendering layer

Three renderers under `cmd/cyoda/help/renderer/`. All take a `*Topic` and produce bytes. Shared tokenizer (~150 LOC) consumed by the text renderer; JSON and markdown renderers need no parsing.

**`json.go`** — marshals `TopicDescriptor`:

```go
type TopicDescriptor struct {
    Topic     string    `json:"topic"`       // "cli.serve"
    Path      []string  `json:"path"`
    Title     string    `json:"title"`
    Synopsis  string    `json:"synopsis"`    // first paragraph of DESCRIPTION
    Body      string    `json:"body"`        // full markdown
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

`cyoda help --format json` with no topic emits `{"version": "<binary-version>", "topics": [<descriptor>...]}` — full tree, depth-first. Single-topic query emits one descriptor.

**`markdown.go`** — pass-through. Front-matter stripped, body emitted verbatim. `SEE ALSO` section appended if not already present in the body.

**`text.go`** — ANSI-ified minimal markdown:

- H1/H2/H3 → ANSI bold, blank-line padding
- `**bold**` / `*italic*` → ANSI codes
- `` `code` `` → dim/inverted
- Fenced code blocks → 2-space-indented + dim
- Bullet lists → `  • item`
- Links `[text](url)` → `text (url)` plain
- No tables (topics use bullet lists per template)
- TTY detection via `golang.org/x/term` — drops all ANSI when stdout is piped

### CLI command

```
cyoda help                             # tree summary (all top-level topics)
cyoda help <topic>                     # renders a top-level topic
cyoda help <topic> <sub>               # drilldown
cyoda help <topic> <sub> <sub2>        # depth-3
cyoda help --format=markdown <...>
cyoda help --format=json               # no topic → full tree
cyoda help --format=json <topic>       # single descriptor
cyoda --version                        # ldflag-injected version
```

`--format` defaults to `text` when stdout is a TTY, `markdown` when piped. Explicit flag wins.

Unknown topic → exit 2 with an error message listing valid children of the nearest existing parent.

### `--version` flag

Separately-scoped change, bundled into the same release because it's adjacent and trivial.

- ldflag-injected vars in `cmd/cyoda/main.go`:
  ```go
  var (
      version = "dev"
      commit  = "unknown"
      date    = "unknown"
  )
  ```
- `.goreleaser.yaml` `builds[].ldflags`: `-X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}}`
- Output: `cyoda version 0.6.1 (commit abc1234, built 2026-04-23T14:06:14Z)` — single line, parse-friendly

### Release asset contract

Two artifacts per `v*` tag, attached to the GitHub Release via goreleaser's `release.extra_files`:

| Asset | URL | Content |
|---|---|---|
| `cyoda_help_<version>.tar.gz` | `https://github.com/Cyoda-platform/cyoda-go/releases/download/v<version>/cyoda_help_<version>.tar.gz` | Verbatim tarball of `cmd/cyoda/help/content/` |
| `cyoda_help_<version>.json` | `https://github.com/Cyoda-platform/cyoda-go/releases/download/v<version>/cyoda_help_<version>.json` | Full-tree JSON descriptor (same as `cyoda help --format json`) |

Generated in a pre-release step before goreleaser runs, moved to `dist/`, referenced in `.goreleaser.yaml`'s `release.extra_files` glob.

Naming convention `cyoda_<artifact>_<version>.<ext>` sets the pattern for #81 (openapi) and #82 (proto) to follow.

### Forward compatibility for Enterprise builds

Not in v0.6.1 scope, but the architecture accommodates:

- `go:embed content_oss/` in the OSS build vs `go:embed content_enterprise/` under `//go:build enterprise`
- OR: same `content/` directory plus an Enterprise-only overlay directory merged at tree-load time

No code change required in v0.6.1 — the loader just uses `content/` today. When the Enterprise build lands, the loader gains the overlay logic.

## Testing

### Renderer tests (`cmd/cyoda/help/renderer/*_test.go`)

1. **Tree-walk symmetry.** Table-driven: for every topic in the tree, render all 3 formats. Assert:
   - JSON output parses as valid JSON
   - Markdown output starts with H1 matching front-matter `title`
   - Text output contains the title string (ANSI-stripped)
   - JSON `see_also` matches front-matter `see_also` verbatim
2. **Tokenizer unit tests.** Table-driven markdown fragments + expected text output. Covers headings, lists, code fences, bold/italic, links, TTY vs non-TTY.
3. **JSON schema pin.** Golden-file test for one stable topic. Catches unintended struct changes.
4. **CLI dispatch.** `TestHelpCommand_UnknownTopic_ErrorAndExit2`, `TestHelpCommand_DepthTraversal`, `TestHelpCommand_FormatFlag`.
5. **Front-matter parser.** Unit tests for malformed YAML — missing `topic`, invalid `stability`. Fail at `Tree.Load()` time, not invocation time.

### Content tests (`cmd/cyoda/help/help_test.go`)

6. **Valid front-matter everywhere.** Walk `content/`, parse each file, assert required fields.
7. **Topic path matches filesystem path.** `cli/serve.md` must declare `topic: cli.serve`.
8. **See-also targets exist.** Every `see_also` entry resolves to a topic in the tree.
9. **All 13 top-level frames present.** Hardcoded list — catches accidental deletion.

### No integration/e2e tests

Help is a pure local subcommand — no network, storage, or state. Unit + CLI-dispatch coverage is sufficient.

### Gate 5 verification

- `go test ./cmd/cyoda/...` green
- `go vet ./...` clean
- `go test -race ./...` one-shot before PR
- Manual smoke: `cyoda help`, `cyoda help cli`, `cyoda help errors POLYMORPHIC_SLOT`, `cyoda help --format json | jq .`, each piped to `/dev/null` to confirm no ANSI leakage

## Content scope for v0.6.1

| Topic | Stability | Source of truth |
|---|---|---|
| `cli` + subcommands | stable | Mirrors `printHelp()` in `cmd/cyoda/main.go`; one subtopic per existing subcommand |
| `config` + topic groups | stable | Derived from env-var constants + `parseConfig` per plugin; subtopics `database`, `auth`, `grpc`, `schema` |
| `errors` + all codes | stable | One subtopic per `ErrCode*` in `internal/common/error_codes.go` — name, trigger, retryable, operator action |
| `crud`, `search`, `analytics`, `models`, `workflows`, `run`, `helm`, `telemetry`, `openapi`, `grpc`, `quickstart` | experimental | Minimal NAME/SYNOPSIS with "content pending — see cyoda-docs" pointer |

Estimated content: ~3500 lines of markdown across ~40 files.

## File structure

```
cmd/cyoda/
├── main.go                                # +--version, +ldflag vars, +help wiring
└── help/
    ├── help.go                            # go:embed + Tree + Topic types
    ├── help_test.go                       # content-level tests (6-9)
    ├── command.go                         # cobra subcommand + --format handling
    ├── command_test.go                    # CLI dispatch tests (4)
    ├── content/                           # AUTHORING SOURCE
    │   └── ... (markdown tree, see above)
    └── renderer/
        ├── tokenizer.go
        ├── tokenizer_test.go
        ├── text.go
        ├── text_test.go
        ├── markdown.go
        ├── json.go
        └── json_test.go
```

Outside `cmd/cyoda/help/`:

- `cmd/cyoda/main.go` — +ldflag vars, +`--version`, wire `help` subcommand
- `.goreleaser.yaml` — +ldflags in `builds[]`, +`release.extra_files` glob
- `.github/workflows/release.yml` — +Build-help-assets pre-release step
- `.github/workflows/release-smoke.yml` — assert help assets exist in snapshot `dist/`
- `README.md` — one-paragraph pointer at `cyoda help`

## Estimated LOC

- Go code: ~1200 (renderer ~400, loader ~200, command ~150, tests ~450)
- Markdown content: ~3500 across ~40 files
- Config (goreleaser + workflows): ~30

## Acceptance (maps to issue #80 criteria)

- [ ] `cyoda help` lists all 13 top-level topics with one-line synopses
- [ ] `cyoda help <topic>` renders the templated structure for each topic
- [ ] `cyoda help <topic> <subtopic>` works for every drilldown currently defined
- [ ] All three formats produce equivalent content from a single source
- [ ] `cyoda help --format json` (no topic) emits the full topic-tree descriptor
- [ ] Per-topic stability marker is present in all formats
- [ ] Release CI attaches `cyoda_help_<version>.tar.gz` and `cyoda_help_<version>.json`
- [ ] OSS build contains no confidential content (architecture supports future Enterprise extension without overlap)
- [ ] Topic-tree stability contract documented (additions free; renames/removals require deprecation window) — lives in `cli.help` topic body
- [ ] `cyoda --version` prints ldflag-injected version + commit + build date

## Out of scope for v0.6.1 (tracked for later)

- Authoritative content for the 10 experimental-stub topics — each is a standalone future change
- Enterprise build overlay for Cassandra-tier deltas — architecture ready, loader change needed when cyoda-go-cassandra lands
- cyoda-docs import of the help release assets — separate cross-repo coordination
- #81 OpenAPI release asset (sibling pattern, separate ticket)
- #82 gRPC proto release asset (sibling pattern, separate ticket)

## Decommission when done

No decommission — this is a new subsystem that will live for the lifetime of the project.
