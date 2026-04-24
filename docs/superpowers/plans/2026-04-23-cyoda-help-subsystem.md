# cyoda help subsystem — implementation plan (v0.6.1 Phase 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a topic-organised `cyoda help` subsystem (CLI + REST + release assets) as v0.6.1, plus a `--version` flag fix.

**Architecture:** Markdown topic tree under `cmd/cyoda/help/content/` embedded via `go:embed`. Three renderers (text/markdown/json) driven by a ~200-LOC tokenizer for a pinned markdown subset. CLI subcommand `cyoda help <topic>…`; REST endpoint `{ContextPath}/help[/{topic}]`. Release assets `cyoda_help_<version>.{tar.gz,json}` generated in goreleaser hooks.

**Tech Stack:** Go 1.26, `go:embed`, `gopkg.in/yaml.v3`, `golang.org/x/term`, `encoding/json`, `net/http` (no markdown library — custom tokenizer for a bounded subset).

**Spec reference:** `docs/superpowers/specs/2026-04-23-cyoda-help-subsystem-design.md`

---

## File structure

### New files

```
cmd/cyoda/help/
├── help.go                                # Tree, Topic, TopicDescriptor, HelpPayload types; Load(fs.FS...)
├── help_test.go                           # Tree load, overlay, content tests #6-13
├── command.go                             # cobra/stdlib help subcommand + summary
├── command_test.go                        # CLI dispatch tests #4
├── content/                               # AUTHORING SOURCE — go:embed root
│   ├── cli.md
│   ├── cli/{serve,init,migrate,health,help}.md
│   ├── config.md
│   ├── config/{database,auth,grpc,schema}.md
│   ├── errors.md
│   ├── errors/*.md                        # 33 files (one per ErrCode*)
│   ├── crud.md, search.md, analytics.md, models.md, workflows.md,
│   ├── run.md, helm.md, telemetry.md, openapi.md, grpc.md, quickstart.md
│   └── (stubs — 2-line "Content pending" bodies)
└── renderer/
    ├── tokenizer.go                       # ~200-line subset tokenizer
    ├── tokenizer_test.go
    ├── text.go                            # ANSI renderer, TTY detection
    ├── text_test.go
    ├── markdown.go                        # pass-through + see_also re-emit
    ├── markdown_test.go
    ├── json.go                            # TopicDescriptor/HelpPayload marshaller
    └── json_test.go

internal/api/
├── help.go                                # RegisterHelpRoutes(mux, tree)
└── help_test.go                           # REST tests #14-19
```

### Modified files

| File | Changes |
|---|---|
| `cmd/cyoda/main.go` | delete `printHelp()`; add `--version` handler; rewire `--help/-h` → `help cli`; add `help` dispatch |
| `internal/common/error_codes.go` | `+ErrCodeHelpTopicNotFound = "HELP_TOPIC_NOT_FOUND"` |
| `.goreleaser.yaml` | `+before.hooks` (generate help assets); `+release.extra_files` glob; `+after.hooks` (SHA256SUMS extension) |
| `.github/workflows/release-smoke.yml` | `+assert help assets generated in dist/` |
| `README.md` | one paragraph: `cyoda help` + `/api/help` as canonical reference |
| `CONTRIBUTING.md` | topic-tree stability contract |

---

## Tasks

Tasks 1-11 build the engine with fstest.MapFS fixtures. Tasks 12-16 author real content. Task 17 migrates away from `printHelp`. Tasks 18-19 add REST. Tasks 20-23 finalize release and docs. Task 24 is Gate 5.

---

### Task 1: Topic type + front-matter parsing

**Files:**
- Create: `cmd/cyoda/help/help.go`
- Create: `cmd/cyoda/help/help_test.go`

- [ ] **Step 1.1: Write failing test for front-matter parsing**

In `cmd/cyoda/help/help_test.go`:

```go
package help

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseFrontMatter_ValidMinimal(t *testing.T) {
	src := []byte(`---
topic: cli
title: "cyoda CLI — subcommand reference"
stability: stable
---

# cli

NAME section follows here.
`)
	fm, body, err := parseFrontMatter(src)
	if err != nil {
		t.Fatalf("parseFrontMatter: %v", err)
	}
	if fm.Topic != "cli" {
		t.Errorf("topic = %q, want %q", fm.Topic, "cli")
	}
	if fm.Stability != "stable" {
		t.Errorf("stability = %q, want %q", fm.Stability, "stable")
	}
	if !strings.HasPrefix(string(body), "# cli") {
		t.Errorf("body must start with '# cli'; got %q", body[:min(20, len(body))])
	}
}

func TestParseFrontMatter_RejectsMissingTopic(t *testing.T) {
	src := []byte(`---
title: "missing topic"
stability: stable
---

body
`)
	_, _, err := parseFrontMatter(src)
	if err == nil {
		t.Fatal("parseFrontMatter must reject missing topic field")
	}
	if !strings.Contains(err.Error(), "topic") {
		t.Errorf("error must mention 'topic': %v", err)
	}
}

func TestParseFrontMatter_RejectsInvalidStability(t *testing.T) {
	src := []byte(`---
topic: cli
title: x
stability: bogus
---

body
`)
	_, _, err := parseFrontMatter(src)
	if err == nil {
		t.Fatal("parseFrontMatter must reject unknown stability")
	}
}

func TestParseFrontMatter_ParsesSeeAlso(t *testing.T) {
	src := []byte(`---
topic: cli
title: x
stability: stable
see_also:
  - config
  - run
---
`)
	fm, _, err := parseFrontMatter(src)
	if err != nil {
		t.Fatalf("parseFrontMatter: %v", err)
	}
	want := []string{"config", "run"}
	if !reflect.DeepEqual(fm.SeeAlso, want) {
		t.Errorf("see_also = %v, want %v", fm.SeeAlso, want)
	}
}
```

- [ ] **Step 1.2: Run failing test**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/v0.6.1-help
go test ./cmd/cyoda/help/ -run TestParseFrontMatter -v
```
Expected: FAIL — `parseFrontMatter` undefined.

- [ ] **Step 1.3: Implement `parseFrontMatter`**

In `cmd/cyoda/help/help.go`:

```go
// Package help embeds and renders the cyoda help topic tree.
package help

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// FrontMatter is the YAML header on every help topic source file.
type FrontMatter struct {
	Topic           string   `yaml:"topic"`
	Title           string   `yaml:"title"`
	Stability       string   `yaml:"stability"`
	SeeAlso         []string `yaml:"see_also,omitempty"`
	VersionAdded    string   `yaml:"version_added,omitempty"`
	SeeAlsoReplace  bool     `yaml:"see_also_replace,omitempty"`
}

var frontMatterDelim = []byte("---\n")

// parseFrontMatter extracts the YAML front-matter from a markdown source
// and returns the parsed header, the body (front-matter stripped), and
// any error. Malformed front-matter or missing required fields are
// errors — this fails at tree-load time, not at query time.
func parseFrontMatter(src []byte) (*FrontMatter, []byte, error) {
	if !bytes.HasPrefix(src, frontMatterDelim) {
		return nil, nil, fmt.Errorf("front-matter missing: file must start with '---\\n'")
	}
	rest := src[len(frontMatterDelim):]
	end := bytes.Index(rest, frontMatterDelim)
	if end < 0 {
		return nil, nil, fmt.Errorf("front-matter unterminated: no closing '---' found")
	}
	header := rest[:end]
	body := rest[end+len(frontMatterDelim):]

	fm := &FrontMatter{}
	if err := yaml.Unmarshal(header, fm); err != nil {
		return nil, nil, fmt.Errorf("front-matter YAML: %w", err)
	}
	if fm.Topic == "" {
		return nil, nil, fmt.Errorf("front-matter: required field 'topic' is empty")
	}
	if fm.Title == "" {
		return nil, nil, fmt.Errorf("front-matter: required field 'title' is empty")
	}
	switch fm.Stability {
	case "stable", "evolving", "experimental":
		// ok
	default:
		return nil, nil, fmt.Errorf("front-matter: stability must be stable|evolving|experimental, got %q", fm.Stability)
	}
	return fm, body, nil
}
```

- [ ] **Step 1.4: Add dependency**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/v0.6.1-help
go get gopkg.in/yaml.v3
go mod tidy
```

- [ ] **Step 1.5: Run tests, verify pass**

```bash
go test ./cmd/cyoda/help/ -run TestParseFrontMatter -v
```
Expected: all 4 subtests PASS.

- [ ] **Step 1.6: Commit**

```bash
git add cmd/cyoda/help/help.go cmd/cyoda/help/help_test.go go.mod go.sum
git commit -m "feat(help): front-matter parser with required-field validation

FrontMatter type covers the full schema (topic, title, stability,
see_also, version_added, see_also_replace). parseFrontMatter fails
loudly at tree-load time — missing required fields, invalid
stability enum values, and malformed YAML all produce actionable
errors. Body bytes returned untouched for downstream rendering.

Part of issue #80 Phase 1."
```

---

### Task 2: Topic + Tree types + single-FS Load

**Files:**
- Modify: `cmd/cyoda/help/help.go`
- Modify: `cmd/cyoda/help/help_test.go`

- [ ] **Step 2.1: Write failing test for Load with MapFS**

Append to `cmd/cyoda/help/help_test.go`:

```go
import (
	"io/fs"
	"testing/fstest"
)

func TestLoad_SingleFS(t *testing.T) {
	fsys := fstest.MapFS{
		"content/cli.md": &fstest.MapFile{Data: []byte(`---
topic: cli
title: "cli reference"
stability: stable
---

# cli

Body.
`)},
		"content/cli/serve.md": &fstest.MapFile{Data: []byte(`---
topic: cli.serve
title: "cli serve"
stability: stable
---

# serve

Serve body.
`)},
	}
	tree, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cli := tree.Find([]string{"cli"})
	if cli == nil {
		t.Fatal("cli topic missing")
	}
	if cli.Title != "cli reference" {
		t.Errorf("cli.Title = %q", cli.Title)
	}
	if len(cli.Children) != 1 {
		t.Fatalf("cli.Children = %d, want 1", len(cli.Children))
	}
	serve := tree.Find([]string{"cli", "serve"})
	if serve == nil {
		t.Fatal("cli.serve missing")
	}
	if serve.Title != "cli serve" {
		t.Errorf("serve.Title = %q", serve.Title)
	}
}

func TestLoad_PathMismatch_IsError(t *testing.T) {
	fsys := fstest.MapFS{
		"content/cli.md": &fstest.MapFile{Data: []byte(`---
topic: wrong
title: x
stability: stable
---
body
`)},
	}
	_, err := Load(fsys)
	if err == nil {
		t.Fatal("Load must error when front-matter topic doesn't match filesystem path")
	}
}

func TestLoad_MissingFrontMatter_IsError(t *testing.T) {
	fsys := fstest.MapFS{
		"content/cli.md": &fstest.MapFile{Data: []byte("no front-matter here")},
	}
	_, err := Load(fsys)
	if err == nil {
		t.Fatal("Load must error on missing front-matter")
	}
}

var _ fs.FS // keep import
```

- [ ] **Step 2.2: Run failing test**

```bash
go test ./cmd/cyoda/help/ -run TestLoad -v
```
Expected: FAIL — `Topic`, `Tree`, `Load`, `tree.Find` undefined.

- [ ] **Step 2.3: Implement types + Load**

Append to `cmd/cyoda/help/help.go`:

```go
import (
	"io/fs"
	"sort"
	"strings"
)

// Topic is a node in the help tree.
type Topic struct {
	Path      []string // ["cli", "serve"]
	Title     string
	Stability string // stable | evolving | experimental
	SeeAlso   []string
	Body      []byte // markdown body, front-matter stripped
	Children  []*Topic
}

// DottedPath returns the canonical dotted identifier, e.g. "cli.serve".
func (t *Topic) DottedPath() string { return strings.Join(t.Path, ".") }

// Tree holds the synthetic root and provides lookup.
type Tree struct{ Root *Topic }

// Find returns the topic at path, or nil if not present.
func (t *Tree) Find(path []string) *Topic {
	cur := t.Root
	for _, seg := range path {
		var next *Topic
		for _, c := range cur.Children {
			if len(c.Path) > 0 && c.Path[len(c.Path)-1] == seg {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

// Load reads one or more `fs.FS` roots and merges them into a single
// Tree. The first argument is the base (typically the OSS embed); each
// subsequent argument is an overlay. On topic-path collision across
// overlays, later-argument values replace earlier ones for body/title/
// stability; see_also unions unless the later entry sets
// see_also_replace: true.
//
// All roots are expected to contain a top-level directory called
// "content/" — the markdown tree lives there.
func Load(roots ...fs.FS) (*Tree, error) {
	tree := &Tree{Root: &Topic{}}
	for i, root := range roots {
		if err := loadInto(tree, root); err != nil {
			return nil, fmt.Errorf("root %d: %w", i, err)
		}
	}
	sortTree(tree.Root)
	return tree, nil
}

// loadInto walks `content/` of a single root and inserts each topic
// into the tree. Overlay semantics are applied on collision.
func loadInto(tree *Tree, root fs.FS) error {
	return fs.WalkDir(root, "content", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		raw, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		fm, body, err := parseFrontMatter(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		// Derive canonical path from filesystem.
		rel := strings.TrimPrefix(p, "content/")
		rel = strings.TrimSuffix(rel, ".md")
		segs := strings.Split(rel, "/")
		dotted := strings.Join(segs, ".")
		if fm.Topic != dotted {
			return fmt.Errorf("%s: front-matter topic %q does not match filesystem path %q", p, fm.Topic, dotted)
		}
		insertOrMerge(tree.Root, segs, &Topic{
			Path:      segs,
			Title:     fm.Title,
			Stability: fm.Stability,
			SeeAlso:   fm.SeeAlso,
			Body:      body,
		}, fm.SeeAlsoReplace)
		return nil
	})
}

// insertOrMerge places `topic` under the root at `path`. If a topic
// already exists at that path, fields are replaced (Enterprise wins)
// except SeeAlso which is unioned unless replace==true.
func insertOrMerge(root *Topic, path []string, topic *Topic, replace bool) {
	cur := root
	for i, seg := range path {
		var found *Topic
		for _, c := range cur.Children {
			if len(c.Path) > 0 && c.Path[len(c.Path)-1] == seg {
				found = c
				break
			}
		}
		if found == nil {
			// Build intermediate placeholder if needed (not the final target).
			newNode := &Topic{Path: append([]string(nil), path[:i+1]...)}
			cur.Children = append(cur.Children, newNode)
			found = newNode
		}
		if i == len(path)-1 {
			// Replace body/title/stability; merge see_also.
			found.Title = topic.Title
			found.Stability = topic.Stability
			found.Body = topic.Body
			if replace {
				found.SeeAlso = topic.SeeAlso
			} else {
				found.SeeAlso = unionSeeAlso(found.SeeAlso, topic.SeeAlso)
			}
		}
		cur = found
	}
}

func unionSeeAlso(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range a {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range b {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func sortTree(t *Topic) {
	sort.Slice(t.Children, func(i, j int) bool {
		return t.Children[i].Path[len(t.Children[i].Path)-1] <
			t.Children[j].Path[len(t.Children[j].Path)-1]
	})
	for _, c := range t.Children {
		sortTree(c)
	}
}
```

- [ ] **Step 2.4: Run tests**

```bash
go test ./cmd/cyoda/help/ -run TestLoad -v
```
Expected: all 3 subtests PASS.

- [ ] **Step 2.5: Commit**

```bash
git add cmd/cyoda/help/help.go cmd/cyoda/help/help_test.go
git commit -m "feat(help): Tree, Topic, Load(fs.FS...) with single-FS support

Load walks content/ under each root, derives canonical dotted paths
from filesystem layout, asserts front-matter topic:field matches.
Tree.Find(path) looks up nodes by []string segment list.

Overlay semantics sketched in doc comments; exercised by test in
Task 3."
```

---

### Task 3: Load overlay merge (Test #13)

**Files:**
- Modify: `cmd/cyoda/help/help_test.go`

- [ ] **Step 3.1: Write failing test**

Append to `cmd/cyoda/help/help_test.go`:

```go
func TestLoad_OverlayMerge_UnionSeeAlso(t *testing.T) {
	oss := fstest.MapFS{
		"content/topic-a.md": &fstest.MapFile{Data: []byte(`---
topic: topic-a
title: oss-a
stability: stable
see_also: [x, y]
---

oss body
`)},
		"content/topic-c.md": &fstest.MapFile{Data: []byte(`---
topic: topic-c
title: oss-c
stability: stable
---

oss c
`)},
	}
	ent := fstest.MapFS{
		"content/topic-a.md": &fstest.MapFile{Data: []byte(`---
topic: topic-a
title: ent-a
stability: stable
see_also: [z]
---

ent body
`)},
		"content/topic-b.md": &fstest.MapFile{Data: []byte(`---
topic: topic-b
title: ent-b
stability: stable
---

ent b
`)},
	}
	tree, err := Load(oss, ent)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, name := range []string{"topic-a", "topic-b", "topic-c"} {
		if tree.Find([]string{name}) == nil {
			t.Errorf("topic %q missing from merged tree", name)
		}
	}
	a := tree.Find([]string{"topic-a"})
	if a.Title != "ent-a" {
		t.Errorf("topic-a.Title = %q, want %q (Enterprise wins)", a.Title, "ent-a")
	}
	if string(a.Body) != "ent body\n" {
		t.Errorf("topic-a.Body = %q, want ent body", a.Body)
	}
	wantSeeAlso := []string{"x", "y", "z"}
	if !reflect.DeepEqual(a.SeeAlso, wantSeeAlso) {
		t.Errorf("topic-a.SeeAlso = %v, want %v (union)", a.SeeAlso, wantSeeAlso)
	}
}

func TestLoad_OverlayMerge_ReplaceSeeAlso(t *testing.T) {
	oss := fstest.MapFS{
		"content/topic-a.md": &fstest.MapFile{Data: []byte(`---
topic: topic-a
title: oss-a
stability: stable
see_also: [x, y]
---
body
`)},
	}
	ent := fstest.MapFS{
		"content/topic-a.md": &fstest.MapFile{Data: []byte(`---
topic: topic-a
title: ent-a
stability: stable
see_also_replace: true
see_also: [z]
---
body
`)},
	}
	tree, err := Load(oss, ent)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := tree.Find([]string{"topic-a"})
	want := []string{"z"}
	if !reflect.DeepEqual(a.SeeAlso, want) {
		t.Errorf("topic-a.SeeAlso = %v, want %v (replace)", a.SeeAlso, want)
	}
}
```

- [ ] **Step 3.2: Run tests, verify pass**

```bash
go test ./cmd/cyoda/help/ -run TestLoad_OverlayMerge -v
```
Expected: PASS (Task 2's implementation already covers overlay semantics).

- [ ] **Step 3.3: Commit**

```bash
git add cmd/cyoda/help/help_test.go
git commit -m "test(help): overlay merge — union see_also + explicit replace path

Exercises the two collision modes: default union (Enterprise appends
to OSS) and explicit replacement via see_also_replace: true in
Enterprise front-matter. Proves the forward-compat seam referenced
in the spec §Forward compatibility."
```

---

### Task 4: Markdown subset tokenizer

**Files:**
- Create: `cmd/cyoda/help/renderer/tokenizer.go`
- Create: `cmd/cyoda/help/renderer/tokenizer_test.go`

The tokenizer turns supported markdown into a slice of `Token` values that both the text renderer and the linter consume. Supported subset: H1/H2/H3, paragraphs, single-level `-`/`*` bullets, fenced code blocks, `**bold**`, `*italic*`, `` `code` ``, `[text](url)`, horizontal rule `---`.

- [ ] **Step 4.1: Write failing test**

In `cmd/cyoda/help/renderer/tokenizer_test.go`:

```go
package renderer

import (
	"reflect"
	"testing"
)

func TestTokenize_Headings(t *testing.T) {
	tokens := Tokenize([]byte("# H1\n\n## H2\n\n### H3\n"))
	want := []Token{
		{Kind: KindHeading, Level: 1, Text: "H1"},
		{Kind: KindHeading, Level: 2, Text: "H2"},
		{Kind: KindHeading, Level: 3, Text: "H3"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestTokenize_Paragraph(t *testing.T) {
	tokens := Tokenize([]byte("This is a paragraph.\n\nAnother paragraph.\n"))
	want := []Token{
		{Kind: KindParagraph, Text: "This is a paragraph."},
		{Kind: KindParagraph, Text: "Another paragraph."},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestTokenize_Bullets(t *testing.T) {
	tokens := Tokenize([]byte("- one\n- two\n* three\n"))
	want := []Token{
		{Kind: KindBullet, Text: "one"},
		{Kind: KindBullet, Text: "two"},
		{Kind: KindBullet, Text: "three"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestTokenize_FencedCode(t *testing.T) {
	tokens := Tokenize([]byte("```go\nfunc main() {}\n```\n"))
	want := []Token{
		{Kind: KindCodeBlock, Text: "func main() {}"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

func TestTokenize_HorizontalRule(t *testing.T) {
	tokens := Tokenize([]byte("before\n\n---\n\nafter\n"))
	want := []Token{
		{Kind: KindParagraph, Text: "before"},
		{Kind: KindRule},
		{Kind: KindParagraph, Text: "after"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %+v, want %+v", tokens, want)
	}
}

// TestFindUnsupported returns disallowed-syntax offsets for the linter.
func TestFindUnsupported_TableDetected(t *testing.T) {
	issues := FindUnsupported([]byte("| col |\n|-----|\n| val |\n"))
	if len(issues) == 0 {
		t.Fatal("pipe table must be flagged")
	}
}

func TestFindUnsupported_NestedListDetected(t *testing.T) {
	issues := FindUnsupported([]byte("- a\n  - nested\n"))
	if len(issues) == 0 {
		t.Fatal("nested list must be flagged")
	}
}

func TestFindUnsupported_HTMLBlockDetected(t *testing.T) {
	issues := FindUnsupported([]byte("<div>x</div>\n"))
	if len(issues) == 0 {
		t.Fatal("HTML block must be flagged")
	}
}

func TestFindUnsupported_CleanContentHasNoIssues(t *testing.T) {
	src := []byte("# Title\n\nParagraph text with **bold** and `code`.\n\n- bullet one\n- bullet two\n\n```\nfenced code\n```\n")
	issues := FindUnsupported(src)
	if len(issues) != 0 {
		t.Errorf("clean content flagged: %+v", issues)
	}
}
```

- [ ] **Step 4.2: Run failing test**

```bash
go test ./cmd/cyoda/help/renderer/ -v
```
Expected: FAIL — `Token`, `Tokenize`, `FindUnsupported` undefined.

- [ ] **Step 4.3: Implement tokenizer**

In `cmd/cyoda/help/renderer/tokenizer.go`:

```go
// Package renderer parses and renders the cyoda help markdown subset.
// The supported subset is intentionally tight so the tokenizer stays
// small. See /docs/superpowers/specs/2026-04-23-cyoda-help-subsystem-design.md
// §Supported markdown subset.
//
// CLI output from this package uses fmt.Fprint to injected writers.
// This is NOT operational logging — the project's log/slog-exclusive
// rule does not apply here.
package renderer

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// TokenKind enumerates the supported block-level shapes.
type TokenKind int

const (
	KindParagraph TokenKind = iota + 1
	KindHeading
	KindBullet
	KindCodeBlock
	KindRule
)

type Token struct {
	Kind  TokenKind
	Level int    // heading level 1-3 only
	Text  string // flattened content (paragraph/heading/bullet) or code body
}

// Tokenize parses the supported subset. Unsupported constructs are
// silently ignored by the tokenizer; FindUnsupported is the linter that
// catches them at content-test time.
func Tokenize(src []byte) []Token {
	var out []Token
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var paraBuf []string
	flushPara := func() {
		if len(paraBuf) > 0 {
			out = append(out, Token{Kind: KindParagraph, Text: strings.TrimSpace(strings.Join(paraBuf, " "))})
			paraBuf = nil
		}
	}
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		// Horizontal rule.
		if trimmed == "---" {
			flushPara()
			out = append(out, Token{Kind: KindRule})
			continue
		}

		// Blank line ends paragraphs/bullets.
		if trimmed == "" {
			flushPara()
			continue
		}

		// Headings.
		if strings.HasPrefix(trimmed, "### ") {
			flushPara()
			out = append(out, Token{Kind: KindHeading, Level: 3, Text: strings.TrimSpace(trimmed[4:])})
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			flushPara()
			out = append(out, Token{Kind: KindHeading, Level: 2, Text: strings.TrimSpace(trimmed[3:])})
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			flushPara()
			out = append(out, Token{Kind: KindHeading, Level: 1, Text: strings.TrimSpace(trimmed[2:])})
			continue
		}

		// Bullets.
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flushPara()
			out = append(out, Token{Kind: KindBullet, Text: strings.TrimSpace(trimmed[2:])})
			continue
		}

		// Fenced code block.
		if strings.HasPrefix(trimmed, "```") {
			flushPara()
			var body []string
			for sc.Scan() {
				inner := sc.Text()
				if strings.HasPrefix(strings.TrimSpace(inner), "```") {
					break
				}
				body = append(body, inner)
			}
			out = append(out, Token{Kind: KindCodeBlock, Text: strings.Join(body, "\n")})
			continue
		}

		// Fallthrough: paragraph continuation.
		paraBuf = append(paraBuf, trimmed)
	}
	flushPara()
	return out
}

// Issue describes a disallowed-markdown site.
type Issue struct {
	Line int
	Kind string
	Text string
}

func (i Issue) String() string { return fmt.Sprintf("line %d: %s (%q)", i.Line, i.Kind, i.Text) }

// FindUnsupported scans for disallowed markdown constructs. Returns
// non-empty issues when any are present.
func FindUnsupported(src []byte) []Issue {
	var issues []Issue
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNum := 0
	inFenced := false
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFenced = !inFenced
			continue
		}
		if inFenced {
			continue
		}
		// Pipe table rows.
		if strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
			issues = append(issues, Issue{Line: lineNum, Kind: "pipe table", Text: line})
			continue
		}
		// HTML blocks.
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && len(trimmed) > 2 {
			issues = append(issues, Issue{Line: lineNum, Kind: "html block", Text: line})
			continue
		}
		// Nested list (indented bullet).
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			t := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
				issues = append(issues, Issue{Line: lineNum, Kind: "nested list", Text: line})
				continue
			}
		}
		// Blockquote.
		if strings.HasPrefix(trimmed, "> ") {
			issues = append(issues, Issue{Line: lineNum, Kind: "blockquote", Text: line})
			continue
		}
	}
	return issues
}
```

- [ ] **Step 4.4: Run tests, verify pass**

```bash
go test ./cmd/cyoda/help/renderer/ -v
```
Expected: all 9 subtests PASS.

- [ ] **Step 4.5: Commit**

```bash
git add cmd/cyoda/help/renderer/
git commit -m "feat(help/renderer): markdown subset tokenizer + linter

Tokenizer covers H1-H3, paragraphs, bullets, fenced code, horizontal
rule (the pinned subset per spec §Supported markdown subset). Inline
markers (**bold**, *italic*, \`code\`, [text](url)) render as literal
text in Token.Text; the text renderer is responsible for ANSI'ing
them.

FindUnsupported catches the linter cases: pipe tables, HTML blocks,
nested bullets, blockquotes. Used by content test #10."
```

---

### Task 5: Text renderer

**Files:**
- Create: `cmd/cyoda/help/renderer/text.go`
- Create: `cmd/cyoda/help/renderer/text_test.go`

- [ ] **Step 5.1: Write failing test**

In `cmd/cyoda/help/renderer/text_test.go`:

```go
package renderer

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderText_HeadingBoldWhenTTY(t *testing.T) {
	tokens := []Token{{Kind: KindHeading, Level: 1, Text: "cli"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, true) // isTTY=true
	out := buf.String()
	if !strings.Contains(out, "\x1b[1m") {
		t.Errorf("H1 must emit bold ANSI on TTY; got %q", out)
	}
	if !strings.Contains(out, "cli") {
		t.Errorf("H1 text missing from output: %q", out)
	}
}

func TestRenderText_NoANSIWhenNotTTY(t *testing.T) {
	tokens := []Token{{Kind: KindHeading, Level: 1, Text: "cli"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false) // isTTY=false
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("non-TTY output must NOT contain ANSI; got %q", out)
	}
	if !strings.Contains(out, "cli") {
		t.Errorf("H1 text missing: %q", out)
	}
}

func TestRenderText_Bullets(t *testing.T) {
	tokens := []Token{
		{Kind: KindBullet, Text: "one"},
		{Kind: KindBullet, Text: "two"},
	}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if !strings.Contains(out, "  • one") || !strings.Contains(out, "  • two") {
		t.Errorf("bullets should render as '  • ...'; got %q", out)
	}
}

func TestRenderText_InlineBoldStripsMarkers(t *testing.T) {
	tokens := []Token{{Kind: KindParagraph, Text: "hello **world** done"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if !strings.Contains(out, "world") {
		t.Errorf("bold text missing: %q", out)
	}
	if strings.Contains(out, "**") {
		t.Errorf("asterisks must be stripped in non-TTY text: %q", out)
	}
}

func TestRenderText_InlineBoldEmitsANSIOnTTY(t *testing.T) {
	tokens := []Token{{Kind: KindParagraph, Text: "hello **world** done"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, true)
	out := buf.String()
	if !strings.Contains(out, "\x1b[1m") {
		t.Errorf("TTY output must emit bold ANSI; got %q", out)
	}
}

func TestRenderText_CodeBlockIndented(t *testing.T) {
	tokens := []Token{{Kind: KindCodeBlock, Text: "line1\nline2"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if !strings.Contains(out, "  line1") || !strings.Contains(out, "  line2") {
		t.Errorf("code block must be 2-space-indented: %q", out)
	}
}

func TestRenderText_LinksFlattenToTextAndURL(t *testing.T) {
	tokens := []Token{{Kind: KindParagraph, Text: "see [docs](https://x/y)"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if !strings.Contains(out, "docs (https://x/y)") {
		t.Errorf("link should render as 'text (url)'; got %q", out)
	}
}
```

- [ ] **Step 5.2: Run failing test**

```bash
go test ./cmd/cyoda/help/renderer/ -run TestRenderText -v
```
Expected: FAIL — `RenderText` undefined.

- [ ] **Step 5.3: Implement text renderer**

In `cmd/cyoda/help/renderer/text.go`:

```go
package renderer

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// ANSI escape sequences used when isTTY is true. Stripped when false.
const (
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiReset  = "\x1b[0m"
)

// Pre-compiled inline regexes. Order matters — match code spans before
// bold so backtick-delimited ** isn't bolded.
var (
	reCode   = regexp.MustCompile("`([^`]+)`")
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*]+)\*`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// RenderText writes tokens to w as ANSI-colourised (when isTTY) or
// plain-text (when !isTTY) output.
func RenderText(w io.Writer, tokens []Token, isTTY bool) {
	for i, tok := range tokens {
		if i > 0 {
			fmt.Fprint(w, "\n")
		}
		switch tok.Kind {
		case KindHeading:
			text := applyInline(tok.Text, isTTY)
			if isTTY {
				fmt.Fprintf(w, "%s%s%s\n", ansiBold, text, ansiReset)
			} else {
				fmt.Fprintf(w, "%s\n", text)
			}
		case KindParagraph:
			fmt.Fprintln(w, applyInline(tok.Text, isTTY))
		case KindBullet:
			fmt.Fprintf(w, "  • %s\n", applyInline(tok.Text, isTTY))
		case KindCodeBlock:
			for _, line := range strings.Split(tok.Text, "\n") {
				if isTTY {
					fmt.Fprintf(w, "%s  %s%s\n", ansiDim, line, ansiReset)
				} else {
					fmt.Fprintf(w, "  %s\n", line)
				}
			}
		case KindRule:
			if isTTY {
				fmt.Fprintf(w, "%s──────────────%s\n", ansiDim, ansiReset)
			} else {
				fmt.Fprintf(w, "──────────────\n")
			}
		}
	}
}

// applyInline walks the inline markers (**bold**, *italic*, `code`,
// [text](url)) and either emits ANSI codes (TTY) or strips the markers
// (non-TTY). Code spans are replaced first so ** inside backticks isn't
// mistakenly bolded.
func applyInline(s string, isTTY bool) string {
	// `code`
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		if isTTY {
			return ansiDim + inner + ansiReset
		}
		return inner
	})
	// **bold**
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		if isTTY {
			return ansiBold + inner + ansiReset
		}
		return inner
	})
	// *italic*
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		if isTTY {
			return ansiBold + inner + ansiReset // no separate italic code; reuse bold
		}
		return inner
	})
	// [text](url) → "text (url)"
	s = reLink.ReplaceAllString(s, "$1 ($2)")
	return s
}
```

- [ ] **Step 5.4: Run tests**

```bash
go test ./cmd/cyoda/help/renderer/ -run TestRenderText -v
```
Expected: all 7 subtests PASS.

- [ ] **Step 5.5: Commit**

```bash
git add cmd/cyoda/help/renderer/text.go cmd/cyoda/help/renderer/text_test.go
git commit -m "feat(help/renderer): ANSI text renderer with TTY awareness

Renders the tokenizer output to an io.Writer. ANSI codes when isTTY
is true (bold headings, dim code blocks); marker stripping when
false. Inline regexes handle code spans, bold, italic, links in the
right order (code first to avoid ** inside backticks being bolded).

The isTTY flag is the caller's responsibility (golang.org/x/term
probe happens at CLI entry, not here)."
```

---

### Task 6: Markdown renderer (pass-through + see_also re-emit)

**Files:**
- Create: `cmd/cyoda/help/renderer/markdown.go`
- Create: `cmd/cyoda/help/renderer/markdown_test.go`

- [ ] **Step 6.1: Write failing test**

In `cmd/cyoda/help/renderer/markdown_test.go`:

```go
package renderer

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderMarkdown_BodyPassthrough(t *testing.T) {
	body := []byte("# Title\n\nBody.\n")
	var buf bytes.Buffer
	RenderMarkdown(&buf, body, nil)
	if !strings.Contains(buf.String(), "# Title") {
		t.Errorf("body content missing: %q", buf.String())
	}
}

func TestRenderMarkdown_StripsBodyseeAlsoAndReemitsFromFrontMatter(t *testing.T) {
	body := []byte(`# Title

Body here.

## SEE ALSO

- old-a
- old-b
`)
	seeAlso := []string{"new-a", "new-b"}
	var buf bytes.Buffer
	RenderMarkdown(&buf, body, seeAlso)
	out := buf.String()
	if strings.Contains(out, "old-a") {
		t.Errorf("body SEE ALSO should be stripped: %q", out)
	}
	if !strings.Contains(out, "new-a") || !strings.Contains(out, "new-b") {
		t.Errorf("authoritative see_also should be re-emitted: %q", out)
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"),
		"- new-b") && !strings.Contains(out, "- new-a\n- new-b") {
		t.Errorf("re-emitted list malformed: %q", out)
	}
}

func TestRenderMarkdown_NoSeeAlsoHeadingIfFrontMatterEmpty(t *testing.T) {
	body := []byte("# Title\n\nBody.\n")
	var buf bytes.Buffer
	RenderMarkdown(&buf, body, nil)
	if strings.Contains(buf.String(), "SEE ALSO") {
		t.Errorf("SEE ALSO section must not appear with empty front-matter: %q", buf.String())
	}
}
```

- [ ] **Step 6.2: Run failing test**

```bash
go test ./cmd/cyoda/help/renderer/ -run TestRenderMarkdown -v
```
Expected: FAIL — `RenderMarkdown` undefined.

- [ ] **Step 6.3: Implement**

In `cmd/cyoda/help/renderer/markdown.go`:

```go
package renderer

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// RenderMarkdown writes the topic's body to w, stripping any body-level
// SEE ALSO section (case-sensitive H2 "## SEE ALSO" through the next
// blank-line-bounded block or end-of-file), and appending the
// authoritative see_also from front-matter as a fresh SEE ALSO section
// when non-empty.
func RenderMarkdown(w io.Writer, body []byte, seeAlso []string) {
	stripped := stripSeeAlsoSection(body)
	_, _ = w.Write(stripped)
	// Ensure single trailing newline before the SEE ALSO we append.
	if !bytes.HasSuffix(stripped, []byte("\n")) {
		fmt.Fprintln(w)
	}
	if len(seeAlso) == 0 {
		return
	}
	fmt.Fprintln(w, "\n## SEE ALSO")
	fmt.Fprintln(w)
	for _, s := range seeAlso {
		fmt.Fprintf(w, "- %s\n", s)
	}
}

// stripSeeAlsoSection returns src with any "## SEE ALSO" section
// removed (H2 through the next H2, H1, or EOF).
func stripSeeAlsoSection(src []byte) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	skipping := false
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if !skipping && trimmed == "## SEE ALSO" {
			skipping = true
			continue
		}
		if skipping && (strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ")) {
			skipping = false
			// don't consume this line; fall through to write it
		}
		if skipping {
			continue
		}
		fmt.Fprintln(&out, line)
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}
```

- [ ] **Step 6.4: Run tests**

```bash
go test ./cmd/cyoda/help/renderer/ -run TestRenderMarkdown -v
```
Expected: all 3 subtests PASS.

- [ ] **Step 6.5: Commit**

```bash
git add cmd/cyoda/help/renderer/markdown.go cmd/cyoda/help/renderer/markdown_test.go
git commit -m "feat(help/renderer): markdown pass-through with see_also authority

Body is emitted as-is, any body-level SEE ALSO section is stripped,
and the authoritative see_also from front-matter is appended as a
fresh ## SEE ALSO section. If front-matter see_also is empty, no
SEE ALSO heading appears at all (prevents an empty section)."
```

---

### Task 7: JSON renderer with HelpPayload schema

**Files:**
- Create: `cmd/cyoda/help/renderer/json.go`
- Create: `cmd/cyoda/help/renderer/json_test.go`

- [ ] **Step 7.1: Write failing test**

In `cmd/cyoda/help/renderer/json_test.go`:

```go
package renderer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderTopicDescriptor_Roundtrip(t *testing.T) {
	d := TopicDescriptor{
		Topic:     "cli.serve",
		Path:      []string{"cli", "serve"},
		Title:     "cli serve",
		Synopsis:  "Serve the cyoda API.",
		Body:      "# serve\n\nbody",
		Sections:  []Section{{Name: "SYNOPSIS", Body: "cyoda serve"}},
		SeeAlso:   []string{"config.database"},
		Stability: "stable",
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back TopicDescriptor
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Topic != d.Topic || back.Stability != d.Stability {
		t.Errorf("roundtrip mismatch: %+v", back)
	}
}

func TestHelpPayload_SchemaVersionField(t *testing.T) {
	p := HelpPayload{Schema: 1, Version: "0.6.1", Topics: nil}
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), `"schema":1`) {
		t.Errorf("payload missing schema version field: %s", b)
	}
}

func TestExtractSynopsis_FirstParagraphOfDescription(t *testing.T) {
	body := []byte(`# serve

## NAME

cli.serve — serve the HTTP API

## SYNOPSIS

cyoda serve [--flags]

## DESCRIPTION

This is the first paragraph.

This is the second paragraph.
`)
	syn := extractSynopsis(body)
	if syn != "This is the first paragraph." {
		t.Errorf("got %q", syn)
	}
}

func TestExtractSections_ByH2Heading(t *testing.T) {
	body := []byte(`# serve

## SYNOPSIS

cyoda serve

## DESCRIPTION

body text

## EXAMPLES

example
`)
	secs := extractSections(body)
	if len(secs) != 3 {
		t.Fatalf("got %d sections, want 3: %+v", len(secs), secs)
	}
	if secs[0].Name != "SYNOPSIS" || secs[1].Name != "DESCRIPTION" || secs[2].Name != "EXAMPLES" {
		t.Errorf("section names: %+v", secs)
	}
}
```

- [ ] **Step 7.2: Run failing test**

```bash
go test ./cmd/cyoda/help/renderer/ -run "TestRender(Topic|Help|Extract)|TestHelpPayload" -v
```
Expected: FAIL.

- [ ] **Step 7.3: Implement**

In `cmd/cyoda/help/renderer/json.go`:

```go
package renderer

import (
	"bufio"
	"bytes"
	"strings"
)

// TopicDescriptor is the stable JSON shape consumed by release assets,
// the REST API, and external tooling. Field additions are allowed
// without schema bump; field removal or semantic change bumps the
// HelpPayload.Schema integer.
type TopicDescriptor struct {
	Topic     string    `json:"topic"`
	Path      []string  `json:"path"`
	Title     string    `json:"title"`
	Synopsis  string    `json:"synopsis"`
	Body      string    `json:"body"`
	Sections  []Section `json:"sections"`
	SeeAlso   []string  `json:"see_also"`
	Stability string    `json:"stability"`
	Children  []string  `json:"children,omitempty"`
}

type Section struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// HelpPayload wraps the full-tree response of /api/help and the release
// JSON asset. Schema integer is the additive-only versioning key —
// consumers check it before parsing.
type HelpPayload struct {
	Schema  int               `json:"schema"`
	Version string            `json:"version"`
	Topics  []TopicDescriptor `json:"topics"`
}

// extractSynopsis returns the first paragraph under the DESCRIPTION
// H2 section. If absent, falls back to the first paragraph anywhere
// after the H1.
func extractSynopsis(body []byte) string {
	secs := extractSections(body)
	for _, s := range secs {
		if s.Name == "DESCRIPTION" {
			return firstParagraph(s.Body)
		}
	}
	return firstParagraph(string(body))
}

func firstParagraph(s string) string {
	for _, p := range strings.Split(strings.TrimSpace(s), "\n\n") {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return trimmed
	}
	return ""
}

// extractSections splits body into H2-delimited sections. The section
// Name is the H2 text as-is; Body is everything between this H2 and the
// next H2 or end-of-file. H1 is ignored.
func extractSections(body []byte) []Section {
	var out []Section
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var cur *Section
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			if cur != nil {
				cur.Body = strings.TrimSpace(cur.Body)
				out = append(out, *cur)
			}
			cur = &Section{Name: strings.TrimSpace(line[3:])}
			continue
		}
		if cur != nil {
			cur.Body += line + "\n"
		}
	}
	if cur != nil {
		cur.Body = strings.TrimSpace(cur.Body)
		out = append(out, *cur)
	}
	return out
}
```

- [ ] **Step 7.4: Run tests**

```bash
go test ./cmd/cyoda/help/renderer/ -run "TestRender(Topic|Help|Extract)|TestHelpPayload" -v
```
Expected: all 4 subtests PASS.

- [ ] **Step 7.5: Commit**

```bash
git add cmd/cyoda/help/renderer/json.go cmd/cyoda/help/renderer/json_test.go
git commit -m "feat(help/renderer): JSON renderer types and extractors

TopicDescriptor carries path, title, synopsis, body, parsed H2
sections, see_also, stability, and children. HelpPayload wraps the
tree response with a schema: 1 field for additive-only versioning.

extractSynopsis prefers the first paragraph of the DESCRIPTION H2
section; extractSections does the H2-split."
```

---

### Task 8: Topic→TopicDescriptor conversion + tree walk

**Files:**
- Modify: `cmd/cyoda/help/help.go`
- Modify: `cmd/cyoda/help/help_test.go`

- [ ] **Step 8.1: Write failing test**

Append to `cmd/cyoda/help/help_test.go`:

```go
func TestTopicDescriptor_FromTopic(t *testing.T) {
	topic := &Topic{
		Path:      []string{"cli", "serve"},
		Title:     "cli serve",
		Stability: "stable",
		SeeAlso:   []string{"config"},
		Body:      []byte("# serve\n\n## DESCRIPTION\n\nServe the HTTP API.\n"),
	}
	d := topic.Descriptor()
	if d.Topic != "cli.serve" {
		t.Errorf("Topic = %q", d.Topic)
	}
	if d.Synopsis != "Serve the HTTP API." {
		t.Errorf("Synopsis = %q", d.Synopsis)
	}
}

func TestTree_WalkDescriptors_DepthFirst(t *testing.T) {
	fsys := fstest.MapFS{
		"content/a.md": &fstest.MapFile{Data: []byte(`---
topic: a
title: a
stability: stable
---

## DESCRIPTION

aa
`)},
		"content/a/b.md": &fstest.MapFile{Data: []byte(`---
topic: a.b
title: ab
stability: stable
---

## DESCRIPTION

ab
`)},
	}
	tree, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := tree.WalkDescriptors()
	if len(got) != 2 {
		t.Fatalf("got %d descriptors, want 2: %+v", len(got), got)
	}
	if got[0].Topic != "a" || got[1].Topic != "a.b" {
		t.Errorf("depth-first order wrong: %+v", got)
	}
	// Parent descriptor lists children.
	if len(got[0].Children) != 1 || got[0].Children[0] != "a.b" {
		t.Errorf("parent Children = %+v, want [a.b]", got[0].Children)
	}
}
```

- [ ] **Step 8.2: Run failing test**

```bash
go test ./cmd/cyoda/help/ -run "TestTopicDescriptor|TestTree_WalkDescriptors" -v
```
Expected: FAIL — `topic.Descriptor`, `tree.WalkDescriptors` undefined.

- [ ] **Step 8.3: Implement**

Append to `cmd/cyoda/help/help.go`:

```go
import (
	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help/renderer"
)

// Descriptor builds a renderer.TopicDescriptor for this topic.
func (t *Topic) Descriptor() renderer.TopicDescriptor {
	desc := renderer.TopicDescriptor{
		Topic:     t.DottedPath(),
		Path:      append([]string(nil), t.Path...),
		Title:     t.Title,
		Synopsis:  renderer.ExtractSynopsis(t.Body),
		Body:      string(t.Body),
		Sections:  renderer.ExtractSections(t.Body),
		SeeAlso:   append([]string(nil), t.SeeAlso...),
		Stability: t.Stability,
	}
	for _, c := range t.Children {
		desc.Children = append(desc.Children, c.DottedPath())
	}
	return desc
}

// WalkDescriptors returns every topic's descriptor, depth-first,
// parents before children. The synthetic root is not included.
func (t *Tree) WalkDescriptors() []renderer.TopicDescriptor {
	var out []renderer.TopicDescriptor
	var visit func(*Topic)
	visit = func(n *Topic) {
		if len(n.Path) > 0 {
			out = append(out, n.Descriptor())
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	visit(t.Root)
	return out
}
```

Also export `extractSynopsis` and `extractSections` in `renderer/json.go` — rename to `ExtractSynopsis` and `ExtractSections`:

```bash
# In renderer/json.go, rename (lowercase → uppercase first letter):
#   extractSynopsis  → ExtractSynopsis
#   extractSections  → ExtractSections
# Update renderer/json_test.go accordingly.
```

- [ ] **Step 8.4: Run tests**

```bash
go test ./cmd/cyoda/help/... -v
```
Expected: all tests in help/ and help/renderer/ PASS.

- [ ] **Step 8.5: Commit**

```bash
git add cmd/cyoda/help/
git commit -m "feat(help): Topic.Descriptor() + Tree.WalkDescriptors()

Converts Topic nodes into the renderer.TopicDescriptor shape. Walk
is depth-first, parents before children, synthetic root excluded.
Parent descriptors list child dotted-paths via the Children field."
```

---

### Task 9: `help` subcommand — dispatch + format flag

**Files:**
- Create: `cmd/cyoda/help/command.go`
- Create: `cmd/cyoda/help/command_test.go`

- [ ] **Step 9.1: Write failing test**

In `cmd/cyoda/help/command_test.go`:

```go
package help

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

func testTree(t *testing.T) *Tree {
	fsys := fstest.MapFS{
		"content/cli.md": &fstest.MapFile{Data: []byte(`---
topic: cli
title: cyoda CLI
stability: stable
---

# cli

## DESCRIPTION

Operate the binary.
`)},
		"content/cli/serve.md": &fstest.MapFile{Data: []byte(`---
topic: cli.serve
title: cli serve
stability: stable
---

# serve

## DESCRIPTION

Serve API.
`)},
		"content/config.md": &fstest.MapFile{Data: []byte(`---
topic: config
title: config
stability: stable
---

# config

Body.
`)},
	}
	tree, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return tree
}

func TestRunHelp_NoArgs_ShowsTopics(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{}, &out, "0.6.1", false)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, "cli") || !strings.Contains(s, "config") {
		t.Errorf("top-level summary missing topics: %q", s)
	}
}

func TestRunHelp_TopicLookup(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"cli"}, &out, "0.6.1", false)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "Operate the binary.") {
		t.Errorf("cli body missing: %q", out.String())
	}
}

func TestRunHelp_Subtopic(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"cli", "serve"}, &out, "0.6.1", false)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "Serve API.") {
		t.Errorf("cli serve body missing: %q", out.String())
	}
}

func TestRunHelp_UnknownTopic_Exit2(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"widgetry"}, &out, "0.6.1", false)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "widgetry") {
		t.Errorf("error should name the topic: %q", out.String())
	}
}

func TestRunHelp_FormatJSON(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"--format=json"}, &out, "0.6.1", false)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, `"schema":1`) || !strings.Contains(s, `"version":"0.6.1"`) {
		t.Errorf("json full-tree output malformed: %q", s)
	}
}

func TestRunHelp_FormatJSONSingleTopic(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"--format=json", "cli"}, &out, "0.6.1", false)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, `"topic":"cli"`) {
		t.Errorf("single-topic json malformed: %q", s)
	}
	// Single topic should NOT include the HelpPayload wrapper fields.
	if strings.Contains(s, `"topics":[`) {
		t.Errorf("single-topic output should not include wrapper: %q", s)
	}
}
```

- [ ] **Step 9.2: Run failing test**

```bash
go test ./cmd/cyoda/help/ -run TestRunHelp -v
```
Expected: FAIL — `RunHelp` undefined.

- [ ] **Step 9.3: Implement**

In `cmd/cyoda/help/command.go`:

```go
// Package help — CLI dispatch for the `cyoda help` subcommand.
//
// CLI output uses fmt.Fprint to injected writers — this is user-facing
// output, not operational logging. The log/slog rule applies to
// slog-ingested diagnostic events, not stdout.
package help

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help/renderer"
)

// RunHelp dispatches `cyoda help [args...]`. Returns the intended exit
// code: 0 on success, 2 on unknown topic / bad args.
//
//   tree       — the resolved topic tree
//   args       — positional and --format args after "help"
//   out        — stdout of the CLI
//   version    — binary version string for HelpPayload.Version
//   isTTY      — whether out is a TTY (governs text vs markdown default)
func RunHelp(tree *Tree, args []string, out io.Writer, version string, isTTY bool) int {
	format := "auto"
	var positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "--format=") {
			format = strings.TrimPrefix(a, "--format=")
			continue
		}
		if a == "--format" {
			// space-form not supported; treat as positional for error clarity
			fmt.Fprintln(out, "cyoda help: --format requires = value (e.g. --format=json)")
			return 2
		}
		positional = append(positional, a)
	}

	// No positional: render tree summary (text format only; json returns
	// the full payload).
	if len(positional) == 0 {
		if format == "json" {
			return writeFullTreeJSON(tree, out, version)
		}
		return writeTreeSummary(tree, out, isTTY)
	}

	// Topic lookup.
	topic := tree.Find(positional)
	if topic == nil {
		writeUnknownTopicError(tree, positional, out)
		return 2
	}

	switch resolveFormat(format, isTTY) {
	case "json":
		return writeTopicJSON(topic, out)
	case "markdown":
		return writeTopicMarkdown(topic, out)
	default:
		return writeTopicText(topic, out, isTTY)
	}
}

func resolveFormat(f string, isTTY bool) string {
	switch f {
	case "json", "markdown", "text":
		return f
	case "auto", "":
		if isTTY {
			return "text"
		}
		return "markdown"
	default:
		// Invalid formats fall through to text; test-enforced.
		return "text"
	}
}

func writeTopicText(t *Topic, out io.Writer, isTTY bool) int {
	toks := renderer.Tokenize(t.Body)
	renderer.RenderText(out, toks, isTTY)
	if len(t.SeeAlso) > 0 {
		fmt.Fprintln(out, "\nSEE ALSO")
		for _, s := range t.SeeAlso {
			fmt.Fprintf(out, "  • %s\n", s)
		}
	}
	return 0
}

func writeTopicMarkdown(t *Topic, out io.Writer) int {
	renderer.RenderMarkdown(out, t.Body, t.SeeAlso)
	return 0
}

func writeTopicJSON(t *Topic, out io.Writer) int {
	d := t.Descriptor()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(d)
	return 0
}

func writeFullTreeJSON(tree *Tree, out io.Writer, version string) int {
	payload := renderer.HelpPayload{
		Schema:  1,
		Version: version,
		Topics:  tree.WalkDescriptors(),
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
	return 0
}

func writeTreeSummary(tree *Tree, out io.Writer, isTTY bool) int {
	fmt.Fprintln(out, "cyoda help — topic reference")
	fmt.Fprintln(out)
	buckets := map[string][]*Topic{}
	for _, t := range tree.Root.Children {
		buckets[t.Stability] = append(buckets[t.Stability], t)
	}
	for _, stab := range []string{"stable", "evolving", "experimental"} {
		list := buckets[stab]
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Path[0] < list[j].Path[0]
		})
		title := strings.Title(stab)
		if stab == "experimental" {
			title = "Experimental — content pending"
		}
		fmt.Fprintln(out, title)
		for _, t := range list {
			fmt.Fprintf(out, "  %-16s %s\n", t.Path[0], renderer.ExtractSynopsis(t.Body))
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "Run 'cyoda help <topic>' for details.")
	return 0
}

func writeUnknownTopicError(tree *Tree, args []string, out io.Writer) {
	// Find the nearest existing parent and list its children.
	parent := tree.Root
	matched := 0
	for i, seg := range args {
		found := false
		for _, c := range parent.Children {
			if len(c.Path) > 0 && c.Path[len(c.Path)-1] == seg {
				parent = c
				matched = i + 1
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	missing := args[matched]
	if matched == 0 {
		fmt.Fprintf(out, "cyoda help: no such topic: %q. Run 'cyoda help' to list available topics.\n", missing)
		return
	}
	parentPath := strings.Join(args[:matched], " ")
	var kids []string
	for _, c := range parent.Children {
		kids = append(kids, c.Path[len(c.Path)-1])
	}
	sort.Strings(kids)
	fmt.Fprintf(out, "cyoda help: topic %q has no subtopic %q. Available: %s. Run 'cyoda help %s' for an overview.\n",
		parentPath, missing, strings.Join(kids, ", "), parentPath)
}
```

- [ ] **Step 9.4: Run tests**

```bash
go test ./cmd/cyoda/help/ -run TestRunHelp -v
```
Expected: all 6 subtests PASS.

- [ ] **Step 9.5: Commit**

```bash
git add cmd/cyoda/help/command.go cmd/cyoda/help/command_test.go
git commit -m "feat(help): RunHelp CLI dispatcher with --format resolution

Covers topic / subtopic / unknown-topic-with-sibling-list / format
override / summary-on-no-args. Exit code 2 on unknown topic. Summary
groups by stability with bucket titles; topics within a bucket are
alphabetical."
```

---

### Task 10: Content-test infrastructure + test #9 (all 13 top-level frames)

**Files:**
- Modify: `cmd/cyoda/help/help_test.go`

- [ ] **Step 10.1: Write failing tests**

Append to `cmd/cyoda/help/help_test.go`:

```go
// The authoritative list of top-level topics for v0.6.1.
var topLevelTopicsV061 = []string{
	"cli", "config", "errors", "crud", "search", "analytics",
	"models", "workflows", "run", "helm", "telemetry",
	"openapi", "grpc", "quickstart",
}

// TestAllTopLevelTopicsPresent guards against accidental deletion of a
// top-level topic.
func TestAllTopLevelTopicsPresent(t *testing.T) {
	t.Skip("skipped until the top-level content stubs land in Task 12")
}
```

Note: the test is `t.Skip`'d until Task 12 authors the real content. We land the test file now so Task 12 can flip the skip and see it fail/pass.

- [ ] **Step 10.2: Commit the skipped test**

```bash
git add cmd/cyoda/help/help_test.go
git commit -m "test(help): top-level-topics invariant as a skipped placeholder

Top-level list is pinned here — Task 12 lands the actual content and
flips the t.Skip to assertions. Declared now so the expected list is
reviewable alongside the engine."
```

---

### Task 11: `--version` flag handler

**Files:**
- Modify: `cmd/cyoda/main.go`

- [ ] **Step 11.1: Write failing test**

Since `main()` is hard to test directly, test a helper `printVersion` in a main_test.go:

Create `cmd/cyoda/main_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintVersion_IncludesAllFields(t *testing.T) {
	version = "1.2.3"
	commit = "abc1234"
	buildDate = "2026-04-23T12:00:00Z"
	defer func() { version, commit, buildDate = "dev", "unknown", "unknown" }()

	var buf bytes.Buffer
	printVersion(&buf)
	s := buf.String()
	for _, want := range []string{"1.2.3", "abc1234", "2026-04-23T12:00:00Z"} {
		if !strings.Contains(s, want) {
			t.Errorf("printVersion output missing %q: %q", want, s)
		}
	}
}
```

- [ ] **Step 11.2: Run failing test**

```bash
go test ./cmd/cyoda/ -run TestPrintVersion -v
```
Expected: FAIL — `printVersion` undefined.

- [ ] **Step 11.3: Add `printVersion` + flag handler**

Modify `cmd/cyoda/main.go` — add the helper (after `var ( version, commit, buildDate ... )`):

```go
import "io"

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "cyoda version %s (commit %s, built %s)\n", version, commit, buildDate)
}
```

Modify the dispatch switch at `main.go:36-48` to add `--version`/`-v` cases:

```go
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h":
			printHelp()
			return
		case "--version", "-v":
			printVersion(os.Stdout)
			return
		case "init":
			os.Exit(runInit(os.Args[2:]))
		case "health":
			os.Exit(runHealth(os.Args[2:]))
		case "migrate":
			os.Exit(runMigrate(os.Args[2:]))
		}
	}
```

- [ ] **Step 11.4: Run tests, verify pass**

```bash
go test ./cmd/cyoda/ -run TestPrintVersion -v
./bin/cyoda --version 2>&1 || (go build -o /tmp/cyoda-test ./cmd/cyoda && /tmp/cyoda-test --version)
```
Expected: test PASS; manual `cyoda --version` prints `cyoda version dev (commit unknown, built unknown)`.

- [ ] **Step 11.5: Commit**

```bash
git add cmd/cyoda/main.go cmd/cyoda/main_test.go
git commit -m "feat(cli): --version/-v prints ldflag-injected version info

Uses the existing version/commit/buildDate vars (already ldflag-
injected by .goreleaser.yaml:26). Single-line parse-friendly format:
'cyoda version <v> (commit <c>, built <date>)'.

Fix for the previously-missing flag — the ldflag wiring existed but
no handler was wired into the CLI dispatch."
```

---

### Task 12: Author all 13 top-level topic stubs + enable test #9

**Files:**
- Create: `cmd/cyoda/help/content/*.md` (13 files)
- Modify: `cmd/cyoda/help/help.go` (add `//go:embed content/*` directive)
- Modify: `cmd/cyoda/help/help_test.go` (un-skip test #9)

- [ ] **Step 12.1: Add go:embed**

In `cmd/cyoda/help/help.go`, add near the top-level:

```go
import "embed"

//go:embed content
var embeddedContent embed.FS

// DefaultTree is the tree loaded from embedded OSS content. Populated
// at first call; panics if content is malformed (a compile-time
// guarantee would be preferable, but go:embed can't enforce topic
// structure).
var DefaultTree = func() *Tree {
	t, err := Load(embeddedContent)
	if err != nil {
		panic(fmt.Sprintf("help: failed to load embedded content: %v", err))
	}
	return t
}()
```

- [ ] **Step 12.2: Author the 13 stubs**

Create `cmd/cyoda/help/content/cli.md`:

```markdown
---
topic: cli
title: "cyoda CLI — subcommands and conventions"
stability: experimental
---

# cli

**Content pending in v0.6.1.** See the cyoda-go README for current external documentation while this topic is authored.
```

Note: `cli` ships as `experimental` at first; Task 14 upgrades to `stable` when the full content lands.

Create the other 12 with analogous content (changing `topic`, `title`):

```
config, errors, crud, search, analytics, models, workflows, run,
helm, telemetry, openapi, grpc, quickstart
```

Each file uses the same two-line body form (`# <topic>` heading + one `Content pending` sentence).

- [ ] **Step 12.3: Un-skip test #9**

In `cmd/cyoda/help/help_test.go`, replace the `t.Skip` body with:

```go
func TestAllTopLevelTopicsPresent(t *testing.T) {
	tree := DefaultTree
	for _, name := range topLevelTopicsV061 {
		if tree.Find([]string{name}) == nil {
			t.Errorf("top-level topic %q missing from embedded content", name)
		}
	}
}
```

- [ ] **Step 12.4: Run tests**

```bash
go test ./cmd/cyoda/help/ -run TestAllTopLevelTopicsPresent -v
```
Expected: PASS — all 13 stubs present.

- [ ] **Step 12.5: Commit**

```bash
git add cmd/cyoda/help/help.go cmd/cyoda/help/help_test.go cmd/cyoda/help/content/
git commit -m "feat(help): 13 top-level topic stubs + DefaultTree go:embed

Every top-level topic listed in issue #80 now has a stub file
mounted at cmd/cyoda/help/content/. Test #9 enforces the invariant
against accidental deletion.

All 13 ship as stability: experimental for this commit. Tasks 14-16
upgrade cli/config/errors to stable as real content lands."
```

---

### Task 13: Wire `help` subcommand + rewire `--help` to `help cli`

**Files:**
- Modify: `cmd/cyoda/main.go`

- [ ] **Step 13.1: Write failing test**

Append to `cmd/cyoda/main_test.go`:

```go
import "os"

func TestHelpSubcommand_ExistsAndDispatches(t *testing.T) {
	// Capture stdout via a pipe.
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	code := runHelpCmd([]string{"cli"})
	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "cli") {
		t.Errorf("output missing 'cli': %q", buf.String())
	}
}
```

- [ ] **Step 13.2: Run failing test**

```bash
go test ./cmd/cyoda/ -run TestHelpSubcommand -v
```
Expected: FAIL — `runHelpCmd` undefined.

- [ ] **Step 13.3: Implement**

Modify `cmd/cyoda/main.go`:

```go
import (
	"golang.org/x/term"

	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help"
)

// runHelpCmd is the entry point for `cyoda help [args...]`.
func runHelpCmd(args []string) int {
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	return help.RunHelp(help.DefaultTree, args, os.Stdout, version, isTTY)
}
```

Update the dispatch switch:

```go
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h":
			// Delegate to the help subsystem so there is a single source
			// of truth. Matches `cyoda help cli` exactly.
			os.Exit(runHelpCmd([]string{"cli"}))
		case "--version", "-v":
			printVersion(os.Stdout)
			return
		case "help":
			os.Exit(runHelpCmd(os.Args[2:]))
		case "init":
			os.Exit(runInit(os.Args[2:]))
		case "health":
			os.Exit(runHealth(os.Args[2:]))
		case "migrate":
			os.Exit(runMigrate(os.Args[2:]))
		}
	}
```

- [ ] **Step 13.4: Run tests**

```bash
go test ./cmd/cyoda/ -v
```
Expected: all tests PASS.

- [ ] **Step 13.5: Commit**

```bash
git add cmd/cyoda/main.go cmd/cyoda/main_test.go
git commit -m "feat(cli): wire 'help' subcommand, delegate --help to 'help cli'

cyoda --help (and -h) now renders the 'cli' help topic instead of
calling the hand-written printHelp(). cyoda help [args...] is the
new canonical entry; printHelp is NOT deleted in this commit
(happens in Task 17 after migration-parity test lands)."
```

---

### Task 14: Author `cli` topic tree (6 files)

**Files:**
- Modify: `cmd/cyoda/help/content/cli.md` (upgrade from stub)
- Create: `cmd/cyoda/help/content/cli/{serve,init,migrate,health,help}.md`

- [ ] **Step 14.1: Write the cli.md topic**

Content mirrors existing `printHelp()` at `cmd/cyoda/main.go:258-393` but organised into the man-page template. Each section below is the authored content.

`cmd/cyoda/help/content/cli.md`:

```markdown
---
topic: cli
title: "cyoda CLI — subcommand reference"
stability: stable
see_also:
  - config
  - run
  - quickstart
---

# cli

## NAME

cli — the cyoda command-line interface.

## SYNOPSIS

`cyoda [<subcommand>] [<flags>]`

## DESCRIPTION

cyoda is a Go binary that embeds the full platform: API server, schema engine, workflow runner, and storage plugins. Invoked with no subcommand, it starts the server using environment-provided configuration. Subcommands provide operational affordances — `init` for first-run bootstrap, `health` for liveness probes, `migrate` for schema migrations.

Global flags `--help` (or `-h`) and `--version` (or `-v`) are recognized before subcommand dispatch.

## OPTIONS

- `--help`, `-h` — Renders `cyoda help cli`. No separate quick-flags page.
- `--version`, `-v` — Prints the binary's ldflag-injected version, commit SHA, and build date. Exits 0.

## EXAMPLES

```
# Start the server with defaults
cyoda

# First-run bootstrap then start
cyoda init && cyoda

# Check version of an installed binary
cyoda --version
```

## SEE ALSO

- config
- run
- quickstart
```

- [ ] **Step 14.2: Write `cli/serve.md`**

```markdown
---
topic: cli.serve
title: "cyoda serve — start the API server"
stability: stable
see_also:
  - config.database
  - config.grpc
  - run
---

# cli.serve

## NAME

cli.serve — start the cyoda API server.

## SYNOPSIS

`cyoda` (no subcommand; serving is the default mode)

## DESCRIPTION

Starting with no subcommand loads configuration from environment variables, validates the IAM mode, and binds the REST (`8080`), gRPC (`9090`), and admin (`9091`) listeners. The server is single-process, multi-tenant, and stateful — storage is provided by one of the pluggable backends (memory, sqlite, or postgres); see `cyoda help config database` for backend selection.

## EXAMPLES

```
# Default (sqlite, mock IAM)
cyoda

# Postgres backend, JWT required
CYODA_STORAGE_BACKEND=postgres \
  CYODA_POSTGRES_URL="postgres://user:pass@host:5432/cyoda" \
  CYODA_REQUIRE_JWT=true \
  cyoda
```

## SEE ALSO

- config.database
- config.grpc
- run
```

- [ ] **Step 14.3: Write `cli/init.md`**, **cli/migrate.md**, **cli/health.md**, **cli/help.md**

Each follows the same template, covering one subcommand. Reference `cmd/cyoda/main.go` for the current behaviors:

- `init` — `cmd/cyoda/main.go:func runInit`: creates `$XDG_DATA_HOME/cyoda/cyoda.db` with defaults, honors `--force`
- `migrate` — runs storage-plugin-specific migrations
- `health` — probes the server's `/healthz` endpoint with `--timeout`
- `help` — meta-topic: describes the help subsystem itself and the topic-tree stability contract

Write each file with the NAME/SYNOPSIS/DESCRIPTION/OPTIONS/EXAMPLES/SEE ALSO template. **`cli/help.md` MUST include the topic-tree stability contract** as per spec acceptance criterion:

```markdown
## STABILITY

Topic additions are non-breaking. Renaming or removing a topic requires a deprecation window and an entry in CONTRIBUTING.md. Topic paths are stable for the duration of a major version.
```

- [ ] **Step 14.4: Run tests**

```bash
go test ./cmd/cyoda/help/ -v
```
Expected: all tests PASS, including `TestAllTopLevelTopicsPresent`.

- [ ] **Step 14.5: Commit**

```bash
git add cmd/cyoda/help/content/cli.md cmd/cyoda/help/content/cli/
git commit -m "docs(help): author cli topic + 5 subtopics (cli tree stable)

cli.md + cli/{serve,init,migrate,health,help}.md cover the
subcommand surface the binary ships today. Each follows the
man-page NAME/SYNOPSIS/DESCRIPTION/OPTIONS/EXAMPLES/SEE ALSO
template. cli/help.md carries the topic-tree stability contract.

cli.md and all five subtopics ship as stability: stable."
```

---

### Task 15: Author `config` topic tree (5 files) + enable test #11

**Files:**
- Modify: `cmd/cyoda/help/content/config.md`
- Create: `cmd/cyoda/help/content/config/{database,auth,grpc,schema}.md`
- Modify: `cmd/cyoda/help/help_test.go`

- [ ] **Step 15.1: Write the config topic tree**

`cmd/cyoda/help/content/config.md` — overview + precedence:

```markdown
---
topic: config
title: "cyoda configuration reference"
stability: stable
see_also:
  - cli
  - run
---

# config

## NAME

config — environment-driven configuration for cyoda.

## SYNOPSIS

All configuration is environment variables prefixed with `CYODA_`. Topics group related variables:

- `config.database` — storage backend selection, per-backend connection settings
- `config.auth` — IAM mode, JWT issuer, admin controls
- `config.grpc` — gRPC listener + compute-node credentials
- `config.schema` — schema-extension log tuning

## DESCRIPTION

### Precedence

Explicit command-line flags beat environment variables, which beat default values. cyoda uses environment variables as the primary configuration surface. The `_FILE` suffix pattern allows reading a secret from a file path instead of the variable value — e.g. `CYODA_POSTGRES_URL_FILE=/etc/secrets/db-url` takes precedence over `CYODA_POSTGRES_URL` if both are set.

### Profile loader

`CYODA_PROFILES` is a comma-separated list of profile names. For each name `N`, a file `cyoda.N.env` is loaded from the working directory (and then from each directory in `CYODA_PROFILE_DIRS`) before the process's own environment is consulted. This supports local development without exporting many variables.

## SEE ALSO

- cli
- run
```

`cmd/cyoda/help/content/config/database.md` — must mention all `CYODA_POSTGRES_*`, `CYODA_SQLITE_*`, `CYODA_STORAGE_*`, `CYODA_SCHEMA_*` vars. Similar structure:

```markdown
---
topic: config.database
title: "cyoda database configuration"
stability: stable
see_also:
  - config.schema
---

# config.database

## NAME

config.database — storage backend selection and connection settings.

## SYNOPSIS

`CYODA_STORAGE_BACKEND` selects one of `memory`, `sqlite`, `postgres`. Per-backend vars configure each.

## OPTIONS

- `CYODA_STORAGE_BACKEND` (default `sqlite`) — storage backend: `memory`, `sqlite`, `postgres`.

**SQLite** (default):
- `CYODA_SQLITE_DB_PATH` (default `$XDG_DATA_HOME/cyoda/cyoda.db`) — database file.
- `CYODA_SQLITE_BUSY_TIMEOUT_MS` (default `5000`) — busy timeout for lock contention.
- `CYODA_SCHEMA_SAVEPOINT_INTERVAL` (default `64`) — interval for savepoint writes in the schema-extension log.
- `CYODA_SCHEMA_EXTEND_MAX_RETRIES` (default `8`) — retry budget on `SQLITE_BUSY` during schema extension.

**Postgres**:
- `CYODA_POSTGRES_URL`, `CYODA_POSTGRES_URL_FILE` — connection URL (or file containing same).
- `CYODA_POSTGRES_MAX_CONNS` (default `25`) — pool size upper bound.
- `CYODA_POSTGRES_MIN_CONNS` (default `5`) — pool size lower bound.

## SEE ALSO

- config.schema
```

Similarly author `config/auth.md` (CYODA_IAM_*, CYODA_JWT_*, CYODA_REQUIRE_JWT, admin endpoints), `config/grpc.md` (CYODA_GRPC_*, CYODA_COMPUTE_*), and `config/schema.md` (CYODA_SCHEMA_*).

**Important:** every `CYODA_*` var referenced anywhere under `cmd`, `app`, `plugins`, `internal` (excluding `_test.go` and test-only prefixes) must appear in at least one of these files. Test #11 enforces this.

- [ ] **Step 15.2: Add test #11 (env-var coverage)**

Append to `cmd/cyoda/help/help_test.go`:

```go
import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

var envVarPattern = regexp.MustCompile(`CYODA_[A-Z][A-Z0-9_]*`)

// Allow-listed test-only vars that should NOT appear in config/*.md.
var testOnlyEnvPrefixes = []string{"CYODA_TEST_", "CYODA_MARKER", "CYODA_DEBUG"}

func isTestOnly(v string) bool {
	for _, p := range testOnlyEnvPrefixes {
		if v == p || len(v) >= len(p) && v[:len(p)] == p {
			return true
		}
	}
	return false
}

// TestConfig_EnvVarCoverage asserts every CYODA_* env var referenced in
// source also appears in config/**/*.md. Scope: cmd, app, plugins,
// internal (excluding _test.go).
func TestConfig_EnvVarCoverage(t *testing.T) {
	// Locate repo root: tree is loaded from embed; we need to scan source
	// from the filesystem. Walk up until we find go.mod.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Skip("cannot locate repo root; test skipped (likely running against installed module)")
			return
		}
		root = parent
	}
	// Extract referenced vars from source.
	referenced := extractEnvVars(t, root, []string{"cmd", "app", "plugins", "internal"}, true)
	// Extract documented vars from help content.
	documented := extractEnvVars(t, filepath.Join(root, "cmd/cyoda/help/content/config"), nil, false)

	for v := range referenced {
		if isTestOnly(v) {
			continue
		}
		if _, ok := documented[v]; !ok {
			t.Errorf("CYODA_* var %q referenced in source but not documented under config/**/*.md", v)
		}
	}
}

// extractEnvVars greps for CYODA_* in files beneath root/dirs (or root
// directly if dirs is nil). excludeTests=true skips *_test.go files.
func extractEnvVars(t *testing.T, root string, dirs []string, excludeTests bool) map[string]bool {
	out := map[string]bool{}
	targets := dirs
	if targets == nil {
		targets = []string{"."}
	}
	for _, d := range targets {
		base := filepath.Join(root, d)
		args := []string{"-rhoE", `CYODA_[A-Z][A-Z0-9_]*`, base}
		if excludeTests {
			args = append([]string{"--include=*.go", "--exclude=*_test.go"}, args...)
			args = append([]string{"-rhoE", `CYODA_[A-Z][A-Z0-9_]*`, base},
				// above append clobbered; rewrite args correctly:
			)
		}
		// simpler: explicit flags first
		grepArgs := []string{"-rhoE", `CYODA_[A-Z][A-Z0-9_]*`}
		if excludeTests {
			grepArgs = append(grepArgs, "--include=*.go", "--exclude=*_test.go")
		}
		grepArgs = append(grepArgs, base)
		cmd := exec.Command("grep", grepArgs...)
		bs, _ := cmd.Output() // grep exits 1 on no-match; ignore
		for _, line := range bytes.Split(bs, []byte("\n")) {
			s := strings.TrimSpace(string(line))
			if s != "" && envVarPattern.MatchString(s) {
				out[s] = true
			}
		}
	}
	return out
}
```

- [ ] **Step 15.3: Run tests**

```bash
go test ./cmd/cyoda/help/ -run "TestConfig_EnvVarCoverage|TestAllTopLevelTopicsPresent" -v
```
Expected: PASS. If any missing vars are reported, add them to the appropriate `config/*.md` until the test passes.

- [ ] **Step 15.4: Commit**

```bash
git add cmd/cyoda/help/content/config.md cmd/cyoda/help/content/config/ cmd/cyoda/help/help_test.go
git commit -m "docs(help): author config topic + 4 subtopics (stable)

config.md + config/{database,auth,grpc,schema}.md cover every
CYODA_* env var referenced by the binary. Test #11
(TestConfig_EnvVarCoverage) greps cmd app plugins internal
(excluding _test.go + allow-listed test prefixes) and asserts each
referenced var appears in at least one config topic file.

Prevents silent doc drift when new env vars are added."
```

---

### Task 16: Author `errors` topic tree + enable test #12

**Files:**
- Modify: `cmd/cyoda/help/content/errors.md`
- Create: `cmd/cyoda/help/content/errors/<CODE>.md` (32 files — one per existing `ErrCode*`)
- Modify: `cmd/cyoda/help/help_test.go`

- [ ] **Step 16.1: Write `errors.md` overview**

```markdown
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

REST responses use RFC 7807 Problem Details:

```
HTTP/1.1 <status> <phrase>
Content-Type: application/problem+json

{
  "type": "about:blank",
  "title": "<short phrase>",
  "status": <http-status>,
  "detail": "<human-readable>",
  "code": "<MACHINE_CODE>"
}
```

gRPC responses map `code` into the status-detail header via the standard gRPC error-model.

## OPTIONS

See subtopics for each `code`:

- `errors.MODEL_NOT_FOUND`
- `errors.MODEL_NOT_LOCKED`
- `errors.ENTITY_NOT_FOUND`
- ... (32 codes total; full list via `cyoda help errors`)

## SEE ALSO

- openapi
```

- [ ] **Step 16.2: Author 32 code files (one per existing `ErrCode*`)**

For each `ErrCode*` constant in `internal/common/error_codes.go`, create `cmd/cyoda/help/content/errors/<CODE>.md` with template:

```markdown
---
topic: errors.<CODE>
title: "<CODE> — <short phrase>"
stability: stable
see_also:
  - errors
---

# errors.<CODE>

## NAME

<CODE> — <one-line description>.

## SYNOPSIS

HTTP: `<status>` `<phrase>`. Retryable: `<yes|no>`.

## DESCRIPTION

<1-2 paragraphs: what triggers it, what the operator/client should do.>

## SEE ALSO

- errors
```

All 32 codes must appear. Authoritative list:

```
BAD_REQUEST, CLUSTER_NODE_NOT_REGISTERED, COMPUTE_MEMBER_DISCONNECTED,
CONFLICT, DISPATCH_FORWARD_FAILED, DISPATCH_TIMEOUT, ENTITY_NOT_FOUND,
EPOCH_MISMATCH, FORBIDDEN, IDEMPOTENCY_CONFLICT, MODEL_NOT_FOUND,
MODEL_NOT_LOCKED, NO_COMPUTE_MEMBER_FOR_TAG, NOT_IMPLEMENTED,
POLYMORPHIC_SLOT, SEARCH_JOB_ALREADY_TERMINAL, SEARCH_JOB_NOT_FOUND,
SEARCH_RESULT_LIMIT, SEARCH_SHARD_TIMEOUT, SERVER_ERROR,
TRANSACTION_EXPIRED, TRANSACTION_NODE_UNAVAILABLE, TRANSACTION_NOT_FOUND,
TRANSITION_NOT_FOUND, TX_CONFLICT, TX_COORDINATOR_NOT_CONFIGURED,
TX_NO_STATE, TX_REQUIRED, UNAUTHORIZED, VALIDATION_FAILED,
WORKFLOW_FAILED, WORKFLOW_NOT_FOUND
```

- [ ] **Step 16.3: Add test #12 (ErrCode parity)**

Append to `cmd/cyoda/help/help_test.go`:

```go
var errCodePattern = regexp.MustCompile(`ErrCode[A-Z][A-Za-z0-9]+\s*=\s*"([A-Z0-9_]+)"`)

// TestErrCode_Parity asserts every ErrCode* in internal/common/error_codes.go
// has a matching errors/<CODE>.md topic file.
func TestErrCode_Parity(t *testing.T) {
	wd, _ := os.Getwd()
	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Skip("cannot locate repo root")
			return
		}
		root = parent
	}
	src, err := os.ReadFile(filepath.Join(root, "internal/common/error_codes.go"))
	if err != nil {
		t.Fatalf("read error_codes.go: %v", err)
	}
	defined := map[string]bool{}
	for _, m := range errCodePattern.FindAllStringSubmatch(string(src), -1) {
		defined[m[1]] = true
	}
	errorsDir := filepath.Join(root, "cmd/cyoda/help/content/errors")
	entries, err := os.ReadDir(errorsDir)
	if err != nil {
		t.Fatalf("read errors/: %v", err)
	}
	documented := map[string]bool{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") && e.Name() != "errors.md" {
			documented[strings.TrimSuffix(e.Name(), ".md")] = true
		}
	}
	for code := range defined {
		if !documented[code] {
			t.Errorf("ErrCode %q defined in error_codes.go but no errors/%s.md", code, code)
		}
	}
	for code := range documented {
		if !defined[code] {
			t.Errorf("errors/%s.md exists but no matching ErrCode in error_codes.go", code)
		}
	}
}
```

- [ ] **Step 16.4: Run tests**

```bash
go test ./cmd/cyoda/help/ -run TestErrCode_Parity -v
```
Expected: PASS. Fix any mismatches that surface.

- [ ] **Step 16.5: Commit**

```bash
git add cmd/cyoda/help/content/errors/ cmd/cyoda/help/content/errors.md cmd/cyoda/help/help_test.go
git commit -m "docs(help): author errors topic + 32 code subtopics + parity test

errors.md overview + one errors/<CODE>.md per existing ErrCode* in
internal/common/error_codes.go. Test #12 (TestErrCode_Parity)
asserts both directions — every ErrCode has a .md file, and every
.md file has a matching ErrCode. Prevents drift in either direction.

All 32 code subtopics ship as stability: stable."
```

---

### Task 17: Delete `printHelp()` + add content-migration parity test (#11b)

**Files:**
- Modify: `cmd/cyoda/main.go` (remove `printHelp()` + its callers)
- Modify: `cmd/cyoda/help/help_test.go` (add test #11b)

- [ ] **Step 17.1: Write the failing migration test**

Append to `cmd/cyoda/help/help_test.go`:

```go
// Phrases that MUST appear somewhere under cli/*.md or config/*.md
// after the printHelp() migration. Pins content that the env-var
// grep (test #11) alone doesn't cover.
var printHelpMustAppearPhrases = []string{
	"_FILE",            // secret-from-file pattern
	"--force",          // cyoda init flag
	"--timeout",        // cyoda health flag
	"CYODA_PROFILES",   // profile loader (config.md covers this)
	"mock",             // mock IAM default warning
	"docker",           // run-docker.sh reference or docker run example
}

func TestPrintHelp_ContentMigrationParity(t *testing.T) {
	wd, _ := os.Getwd()
	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Skip("cannot locate repo root")
			return
		}
		root = parent
	}
	// Walk cli/*.md + config/*.md and concat their bodies.
	var combined strings.Builder
	for _, dir := range []string{"cmd/cyoda/help/content/cli", "cmd/cyoda/help/content/config"} {
		filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
				return nil
			}
			b, _ := os.ReadFile(p)
			combined.Write(b)
			combined.WriteString("\n")
			return nil
		})
	}
	// Also include the top-level cli.md and config.md.
	for _, rel := range []string{"cmd/cyoda/help/content/cli.md", "cmd/cyoda/help/content/config.md"} {
		b, _ := os.ReadFile(filepath.Join(root, rel))
		combined.Write(b)
		combined.WriteString("\n")
	}
	for _, phrase := range printHelpMustAppearPhrases {
		if !strings.Contains(combined.String(), phrase) {
			t.Errorf("phrase %q missing from cli/*.md + config/*.md — printHelp content not fully migrated", phrase)
		}
	}
}
```

- [ ] **Step 17.2: Run failing test**

```bash
go test ./cmd/cyoda/help/ -run TestPrintHelp_ContentMigrationParity -v
```
Expected: the test should PASS if Tasks 14/15 authored the content correctly. If a phrase is missing, extend the relevant `cli/*.md` or `config/*.md` file.

- [ ] **Step 17.3: Delete `printHelp()` from `cmd/cyoda/main.go`**

Find and remove the `printHelp()` function. Also remove `printStorageHelp()` if it's exclusively called from `printHelp()`.

- [ ] **Step 17.4: Run all tests**

```bash
go test ./cmd/cyoda/... -v
go vet ./...
```
Expected: all PASS.

- [ ] **Step 17.5: Commit**

```bash
git add cmd/cyoda/main.go cmd/cyoda/help/help_test.go
git commit -m "refactor(cli): delete printHelp(), migration complete

The cli and config help topics now cover everything printHelp()
documented. Test #11b (phrase list) asserts subcommand descriptions,
flags, profile loader, and run modes all migrated intact. --help
already delegates to 'help cli' (Task 13), so no user-facing
behavior change."
```

---

### Task 18: Add `HELP_TOPIC_NOT_FOUND` error code + its topic file

**Files:**
- Modify: `internal/common/error_codes.go`
- Create: `cmd/cyoda/help/content/errors/HELP_TOPIC_NOT_FOUND.md`

- [ ] **Step 18.1: Add the constant**

In `internal/common/error_codes.go`, add to an appropriate block:

```go
const (
	// Help subsystem
	ErrCodeHelpTopicNotFound = "HELP_TOPIC_NOT_FOUND"
)
```

- [ ] **Step 18.2: Author the topic file**

`cmd/cyoda/help/content/errors/HELP_TOPIC_NOT_FOUND.md`:

```markdown
---
topic: errors.HELP_TOPIC_NOT_FOUND
title: "HELP_TOPIC_NOT_FOUND — help topic not found"
stability: stable
see_also:
  - errors
---

# errors.HELP_TOPIC_NOT_FOUND

## NAME

HELP_TOPIC_NOT_FOUND — requested help topic does not exist.

## SYNOPSIS

HTTP: `404 Not Found`. Retryable: no.

## DESCRIPTION

Returned by `GET {ContextPath}/help/{topic}` when `{topic}` is well-formed (matches `[A-Za-z0-9._-]+`) but does not resolve to any topic in the tree. Clients should `GET {ContextPath}/help` to discover available topic paths.

## SEE ALSO

- errors
```

- [ ] **Step 18.3: Run tests**

```bash
go test ./cmd/cyoda/help/ -run TestErrCode_Parity -v
```
Expected: PASS.

- [ ] **Step 18.4: Commit**

```bash
git add internal/common/error_codes.go cmd/cyoda/help/content/errors/HELP_TOPIC_NOT_FOUND.md
git commit -m "feat(errors): add ErrCodeHelpTopicNotFound + its topic file

Bundled together per spec — test #12 enforces parity, so the code
and its documentation ship as a single change. Returned by the REST
help handler (next task) when a well-formed topic path doesn't
resolve."
```

---

### Task 19: REST help handler (`internal/api/help.go`) + tests #14-19

**Files:**
- Create: `internal/api/help.go`
- Create: `internal/api/help_test.go`
- Modify: `app/app.go` (register the help routes)

- [ ] **Step 19.1: Write failing tests**

In `internal/api/help_test.go`:

```go
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help"
	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help/renderer"
)

func testServer(t *testing.T, contextPath string) *httptest.Server {
	mux := http.NewServeMux()
	RegisterHelpRoutes(mux, help.DefaultTree, contextPath)
	return httptest.NewServer(mux)
}

func TestGetFullTree(t *testing.T) {
	srv := testServer(t, "/api")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/help")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var payload renderer.HelpPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Schema != 1 {
		t.Errorf("schema = %d", payload.Schema)
	}
	if len(payload.Topics) == 0 {
		t.Error("no topics in payload")
	}
}

func TestGetSingleTopic(t *testing.T) {
	srv := testServer(t, "/api")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/help/cli")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var d renderer.TopicDescriptor
	json.NewDecoder(resp.Body).Decode(&d)
	if d.Topic != "cli" {
		t.Errorf("topic = %q", d.Topic)
	}
}

func TestGetUnknownTopic_404_RFC7807(t *testing.T) {
	srv := testServer(t, "/api")
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/api/help/widgetry")
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/problem+json") {
		t.Errorf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "HELP_TOPIC_NOT_FOUND") {
		t.Errorf("body missing code: %q", body)
	}
}

func TestMalformedTopicPath_400(t *testing.T) {
	srv := testServer(t, "/api")
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/api/help/foo%20bar")
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "BAD_REQUEST") {
		t.Errorf("body missing code: %q", body)
	}
}

func TestCORSHeadersPresent(t *testing.T) {
	srv := testServer(t, "/api")
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/api/help")
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS header missing or wrong: %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestRespectsContextPath(t *testing.T) {
	srv := testServer(t, "/v1/api")
	defer srv.Close()
	// Correct path
	resp, _ := http.Get(srv.URL + "/v1/api/help")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("customized context path failed: %d", resp.StatusCode)
	}
	// Default path should 404 when context is customized
	resp2, _ := http.Get(srv.URL + "/api/help")
	defer resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Errorf("default /api/help should not respond when ContextPath is /v1/api")
	}
}
```

- [ ] **Step 19.2: Run failing tests**

```bash
go test ./internal/api/ -run "TestGet|TestMalformed|TestCORS|TestRespectsContextPath" -v
```
Expected: FAIL — `RegisterHelpRoutes` undefined.

- [ ] **Step 19.3: Implement handler**

In `internal/api/help.go`:

```go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help"
	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help/renderer"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

var topicPathPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Version injected via ldflag or test setter.
var helpBinaryVersion = "dev"

// SetHelpBinaryVersion wires the version string displayed in /api/help.
// Called from main.go during bootstrap.
func SetHelpBinaryVersion(v string) { helpBinaryVersion = v }

// RegisterHelpRoutes mounts GET {contextPath}/help and
// GET {contextPath}/help/{topic}. contextPath must NOT have a trailing
// slash.
func RegisterHelpRoutes(mux *http.ServeMux, tree *help.Tree, contextPath string) {
	prefix := strings.TrimRight(contextPath, "/") + "/help"
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != prefix {
			// Trailing slash or extra path: fall through to the subpath
			// handler below by 404'ing here — only exact match handles
			// the full-tree response.
			writeProblem(w, 404, "Not Found", "no such help topic: "+strings.TrimPrefix(r.URL.Path, prefix+"/"), common.ErrCodeHelpTopicNotFound)
			return
		}
		writeCORS(w)
		serveFullTree(w, tree)
	})
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		writeCORS(w)
		topic := strings.TrimPrefix(r.URL.Path, prefix+"/")
		if !topicPathPattern.MatchString(topic) {
			writeProblem(w, 400, "Bad Request", "invalid topic path: contains disallowed characters", common.ErrCodeBadRequest)
			return
		}
		segs := strings.Split(topic, ".")
		node := tree.Find(segs)
		if node == nil {
			writeProblem(w, 404, "Not Found", "no such help topic: "+topic, common.ErrCodeHelpTopicNotFound)
			return
		}
		serveSingleTopic(w, node)
	})
}

func serveFullTree(w http.ResponseWriter, tree *help.Tree) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(renderer.HelpPayload{
		Schema:  1,
		Version: helpBinaryVersion,
		Topics:  tree.WalkDescriptors(),
	})
}

func serveSingleTopic(w http.ResponseWriter, t *help.Topic) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(t.Descriptor())
}

func writeCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

// writeProblem writes an RFC 7807 Problem Details response.
func writeProblem(w http.ResponseWriter, status int, title, detail, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"about:blank","title":%q,"status":%d,"detail":%q,"code":%q}`,
		title, status, detail, code)
}
```

- [ ] **Step 19.4: Wire up `app/app.go`**

Find where other routes are registered and add:

```go
import "github.com/cyoda-platform/cyoda-go/cmd/cyoda/help"

// After other RegisterXxxRoutes calls:
api.SetHelpBinaryVersion(version) // or whatever the app's version var is
api.RegisterHelpRoutes(mux, help.DefaultTree, cfg.ContextPath)
```

Exact location depends on the existing registration pattern. Read `app/app.go` around the existing route registrations.

- [ ] **Step 19.5: Run tests**

```bash
go test ./internal/api/ -v
go test ./... -short
```
Expected: all PASS.

- [ ] **Step 19.6: Commit**

```bash
git add internal/api/help.go internal/api/help_test.go app/app.go
git commit -m "feat(api): REST help endpoint — GET {ContextPath}/help[/{topic}]

Full tree at the base path, single descriptor at the subpath.
Unauthenticated (content is public in the binary). RFC 7807
problem-details for errors: 404 HELP_TOPIC_NOT_FOUND on unknown,
400 BAD_REQUEST on malformed topic path ([A-Za-z0-9._-]+). CORS
allow-origin * on all help endpoints.

Six unit tests cover full-tree response, single topic, unknown
topic 404, malformed path 400, CORS header, and ContextPath
honoring."
```

---

### Task 20: Content-test #10 (markdown subset linter)

**Files:**
- Modify: `cmd/cyoda/help/help_test.go`

- [ ] **Step 20.1: Write the test**

Append to `cmd/cyoda/help/help_test.go`:

```go
// TestContentMarkdownSubsetLinter rejects any help file using markdown
// constructs outside the pinned subset. Enforces the tokenizer's
// scope — tables, nested lists, HTML, blockquotes.
func TestContentMarkdownSubsetLinter(t *testing.T) {
	err := fs.WalkDir(embeddedContent, "content", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		raw, rerr := fs.ReadFile(embeddedContent, p)
		if rerr != nil {
			return rerr
		}
		_, body, ferr := parseFrontMatter(raw)
		if ferr != nil {
			return nil // front-matter test covers this
		}
		issues := renderer.FindUnsupported(body)
		for _, iss := range issues {
			t.Errorf("%s: %s", p, iss)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
```

Note: the `renderer` package is imported in `help.go` already via Task 8.

- [ ] **Step 20.2: Run the test**

```bash
go test ./cmd/cyoda/help/ -run TestContentMarkdownSubsetLinter -v
```
Expected: PASS. If any file is flagged, edit the content to use the supported subset.

- [ ] **Step 20.3: Commit**

```bash
git add cmd/cyoda/help/help_test.go
git commit -m "test(help): enforce markdown-subset linter on all content

Walks every embedded content file, runs FindUnsupported on the
stripped body, and fails the test run if any file uses tables,
nested lists, HTML blocks, or blockquotes. Matches the pinned
subset per spec §Supported markdown subset."
```

---

### Task 21: Goreleaser hooks for help assets

**Files:**
- Modify: `.goreleaser.yaml`

- [ ] **Step 21.1: Add `before.hooks`**

Find the existing `before:` block (or create if not present) in `.goreleaser.yaml`. Add:

```yaml
before:
  hooks:
    - go mod tidy
    - bash -c 'mkdir -p dist && tar -czf "dist/cyoda_help_${VERSION#v}.tar.gz" -C cmd/cyoda/help content/'
    - bash -c 'go run ./cmd/cyoda help --format=json > "dist/cyoda_help_${VERSION#v}.json"'
```

(Goreleaser populates `$VERSION` with the current tag.)

- [ ] **Step 21.2: Add `release.extra_files`**

In the `release:` block:

```yaml
release:
  extra_files:
    - glob: ./dist/cyoda_help_*.tar.gz
    - glob: ./dist/cyoda_help_*.json
```

- [ ] **Step 21.3: Add `after.hooks` for SHA256SUMS extension**

```yaml
after:
  hooks:
    - bash -c 'cd dist && sha256sum cyoda_help_*.tar.gz cyoda_help_*.json >> SHA256SUMS'
```

- [ ] **Step 21.4: Verify locally**

```bash
# Ensure a clean state
rm -rf dist/
# Simulate a snapshot run
goreleaser release --snapshot --clean --skip=publish --skip=sign --skip=sbom
# Confirm the artifacts
ls -la dist/cyoda_help_*.tar.gz dist/cyoda_help_*.json
grep cyoda_help dist/SHA256SUMS
```
Expected: both assets exist in `dist/`; SHA256SUMS contains hashes for both.

- [ ] **Step 21.5: Commit**

```bash
git add .goreleaser.yaml
git commit -m "chore(release): emit help.tar.gz + help.json as release assets

before.hooks generate the two assets in dist/ after --clean runs.
release.extra_files globs them into the GitHub Release. after.hooks
append SHA256SUMS entries (goreleaser's default checksum covers
archives: only, not extra_files)."
```

---

### Task 22: Release-smoke assertion

**Files:**
- Modify: `.github/workflows/release-smoke.yml`

- [ ] **Step 22.1: Add assertion step**

Find the section of `release-smoke.yml` after `goreleaser` runs. Add:

```yaml
- name: Assert help assets generated
  run: |
    test -f dist/cyoda_help_0.0.0.tar.gz || { echo "help tar.gz missing"; exit 1; }
    test -f dist/cyoda_help_0.0.0.json || { echo "help json missing"; exit 1; }
    jq . dist/cyoda_help_0.0.0.json > /dev/null || { echo "help json invalid"; exit 1; }
    grep -q cyoda_help dist/SHA256SUMS || { echo "SHA256SUMS missing help assets"; exit 1; }
```

Note: snapshot mode produces version `0.0.0` when no tag exists. Adapt the version prefix if the snapshot uses a different default in the repo's current setup.

- [ ] **Step 22.2: Commit**

```bash
git add .github/workflows/release-smoke.yml
git commit -m "ci: assert help release assets in release-smoke

Validates both files exist, the JSON is well-formed, and the
SHA256SUMS file covers them. Runs on every PR that touches
.goreleaser.yaml, cmd/cyoda/**, deploy/docker/Dockerfile, or
.github/workflows/release-smoke.yml."
```

---

### Task 23: Documentation updates (README, CONTRIBUTING)

**Files:**
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`

- [ ] **Step 23.1: Add README pointer**

In `README.md`, add a short paragraph near the top, under an "## Documentation" or similar heading:

```markdown
## Documentation

Authoritative reference for flags, env vars, endpoints, and error codes ships in the binary itself:

    cyoda help                  # topic index
    cyoda help cli              # CLI reference
    cyoda help config database  # database config vars
    cyoda help errors MODEL_NOT_FOUND

A running server exposes the same tree over HTTP at `{ContextPath}/help` (default `/api/help`). Release assets include `cyoda_help_<version>.{tar.gz,json}` for offline / tooling consumption.
```

- [ ] **Step 23.2: Add CONTRIBUTING stability contract**

In `CONTRIBUTING.md`, add:

```markdown
## Help topic tree

The `cyoda help` topic tree is a stable interface. Topic paths (e.g. `config.database`, `errors.MODEL_NOT_FOUND`) are committed for the duration of a major version — tooling, documentation sites, and AI agents rely on them.

### Additions
New topics may be added freely at any point under existing parent paths. Adding a top-level topic is also permitted but update the hardcoded list in `cmd/cyoda/help/help_test.go` (`topLevelTopicsV061`) at the same time.

### Renames / removals
A rename or removal requires:
1. A deprecation window of at least one minor release — the old path continues to work (renders the new topic's content with a deprecation notice).
2. An entry in the release notes calling out the change.
3. An update to CONTRIBUTING.md that documents the new path.

### Stability markers
Per-topic `stability:` value governs what consumers should expect:
- `stable` — content semantics locked. Wording may evolve; structure does not.
- `evolving` — may be reorganised between minors. No path changes without deprecation.
- `experimental` — may be reorganised or removed without deprecation. Used for stubs and early drafts.
```

- [ ] **Step 23.3: Commit**

```bash
git add README.md CONTRIBUTING.md
git commit -m "docs: README pointer to cyoda help + CONTRIBUTING stability contract

README gains a short Documentation section pointing at the in-
binary help surface (CLI + REST + release asset). CONTRIBUTING.md
documents the topic-tree stability contract — additions free,
renames/removals deprecation-windowed, per-topic stability markers
defined."
```

---

### Task 24: Gate 5 — full verification

**Files:**
- No file changes; verification only.

- [ ] **Step 24.1: Full test suite**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/v0.6.1-help
go test ./... > /tmp/gate5.log 2>&1
echo "exit=$?"
grep -c "^ok" /tmp/gate5.log
grep -c "^FAIL" /tmp/gate5.log
```
Expected: exit 0, all packages ok, 0 FAIL.

- [ ] **Step 24.2: Per-plugin submodule tests**

```bash
(cd plugins/memory && go test ./...) || echo FAIL
(cd plugins/postgres && go test ./...) || echo FAIL
(cd plugins/sqlite && go test ./...) || echo FAIL
```
Expected: all PASS (no changes to plugins; baseline should still be green).

- [ ] **Step 24.3: Race detector (one-shot)**

```bash
go test -race ./... > /tmp/gate5-race.log 2>&1
echo "exit=$?"
grep "WARNING: DATA RACE\|^FAIL" /tmp/gate5-race.log || echo "no races"
```
Expected: exit 0, no races reported.

- [ ] **Step 24.4: go vet**

```bash
go vet ./...
```
Expected: clean.

- [ ] **Step 24.5: Manual CLI smoke**

```bash
go build -o /tmp/cyoda-test ./cmd/cyoda

/tmp/cyoda-test --version
/tmp/cyoda-test --help | head
/tmp/cyoda-test help
/tmp/cyoda-test help cli
/tmp/cyoda-test help errors MODEL_NOT_FOUND
/tmp/cyoda-test help --format=json | jq .schema
/tmp/cyoda-test help --format=json cli.serve | jq .topic
# Pipe detection
/tmp/cyoda-test help cli | grep -vc $'\x1b'  # should have no ANSI when piped
```
Expected: all produce sensible output; piped output has no ANSI.

- [ ] **Step 24.6: Goreleaser snapshot (full pipeline)**

```bash
git tag v0.0.0-test
goreleaser release --snapshot --clean --skip=publish --skip=sign --skip=sbom
git tag -d v0.0.0-test

# Confirm assets
ls -la dist/cyoda_help_*.tar.gz dist/cyoda_help_*.json
grep cyoda_help dist/SHA256SUMS
jq .schema dist/cyoda_help_*.json
```
Expected: assets present, SHA256SUMS covers them, JSON schema version is 1.

- [ ] **Step 24.7: Commit the verification log (optional)**

If anything surfaced, fix it via one of the earlier tasks. Otherwise no commit needed.

- [ ] **Step 24.8: Push the branch**

```bash
git -c "credential.helper=!f() { echo username=x-access-token; echo password=$GH_TOKEN; }; f" push origin feat/v0.6.1-help
```

---

## Summary

23 tasks produce:

- ~1400 LOC Go across `cmd/cyoda/help/`, `internal/api/help.go`, `cmd/cyoda/main.go`
- 14 content tests (front-matter parse, load, overlay, tree walk, 5 renderer tests, markdown linter, env var parity, ErrCode parity, printHelp migration parity, top-level topics invariant)
- 6 REST tests (full tree, single topic, 404, 400, CORS, ContextPath)
- ~3500 lines of markdown content across 50 files
- Release asset pipeline via goreleaser hooks
- Release-smoke CI assertion
- README + CONTRIBUTING updates

Ready to tag v0.6.1 once merged to main.
