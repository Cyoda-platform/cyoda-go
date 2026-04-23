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
