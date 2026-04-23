package help

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
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

func TestParseFrontMatter_RejectsMissingTitle(t *testing.T) {
	src := []byte(`---
topic: cli
stability: stable
---

body
`)
	_, _, err := parseFrontMatter(src)
	if err == nil {
		t.Fatal("parseFrontMatter must reject missing title field")
	}
	if !strings.Contains(err.Error(), "title") {
		t.Errorf("error must mention 'title': %v", err)
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
	if !strings.Contains(err.Error(), "stability") {
		t.Errorf("error must mention 'stability': %v", err)
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

func TestTreeFind_NilRoot(t *testing.T) {
	tree := &Tree{}
	if got := tree.Find([]string{"anything"}); got != nil {
		t.Errorf("Find on nil Root must return nil; got %v", got)
	}
}

func TestTreeFind_EmptyPathReturnsRoot(t *testing.T) {
	tree := &Tree{Root: &Topic{}}
	if got := tree.Find([]string{}); got != tree.Root {
		t.Errorf("Find with empty path must return Root")
	}
}

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

func TestTopicDescriptor_SeeAlsoAlwaysNonNil(t *testing.T) {
	// Topics without see_also must still serialize as "see_also":[]
	// so API consumers can rely on the field being a JSON array.
	topic := &Topic{
		Path:      []string{"x"},
		Title:     "x",
		Stability: "stable",
		Body:      []byte("body"),
	}
	d := topic.Descriptor()
	if d.SeeAlso == nil {
		t.Error("SeeAlso must be non-nil empty slice, got nil")
	}
	if len(d.SeeAlso) != 0 {
		t.Errorf("SeeAlso should be empty, got %v", d.SeeAlso)
	}
}

func TestTree_WalkDescriptors_NilRoot(t *testing.T) {
	tree := &Tree{}
	got := tree.WalkDescriptors()
	if got != nil {
		t.Errorf("WalkDescriptors on nil Root = %v, want nil", got)
	}
}

func TestTree_WalkDescriptors_GrandchildPreOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"content/a.md": &fstest.MapFile{Data: []byte(`---
topic: a
title: a
stability: stable
---
`)},
		"content/a/b.md": &fstest.MapFile{Data: []byte(`---
topic: a.b
title: ab
stability: stable
---
`)},
		"content/a/b/c.md": &fstest.MapFile{Data: []byte(`---
topic: a.b.c
title: abc
stability: stable
---
`)},
		"content/a/d.md": &fstest.MapFile{Data: []byte(`---
topic: a.d
title: ad
stability: stable
---
`)},
	}
	tree, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := tree.WalkDescriptors()
	want := []string{"a", "a.b", "a.b.c", "a.d"}
	if len(got) != len(want) {
		t.Fatalf("got %d descriptors, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Topic != want[i] {
			t.Errorf("got[%d].Topic = %q, want %q", i, got[i].Topic, want[i])
		}
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
	if len(got[0].Children) != 1 || got[0].Children[0] != "a.b" {
		t.Errorf("parent Children = %+v, want [a.b]", got[0].Children)
	}
}

// topLevelTopicsV061 is the authoritative list of top-level topics
// for v0.6.1. Task 12 authors the stubs; this list pins them.
var topLevelTopicsV061 = []string{
	"cli", "config", "errors", "crud", "search", "analytics",
	"models", "workflows", "run", "helm", "telemetry",
	"openapi", "grpc", "quickstart",
}

// TestAllTopLevelTopicsPresent guards against accidental deletion of a
// top-level topic. Skipped here — Task 12 lands the actual content
// files and flips this to real assertions.
func TestAllTopLevelTopicsPresent(t *testing.T) {
	t.Skip("skipped until the top-level content stubs land in Task 12")
}
