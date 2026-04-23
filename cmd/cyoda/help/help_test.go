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
