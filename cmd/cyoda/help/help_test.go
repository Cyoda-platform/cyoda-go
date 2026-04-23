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
