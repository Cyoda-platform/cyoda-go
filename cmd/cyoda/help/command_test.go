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
	if !strings.Contains(s, `"schema": 1`) && !strings.Contains(s, `"schema":1`) {
		t.Errorf("json full-tree output missing schema field: %q", s)
	}
	if !strings.Contains(s, `"version": "0.6.1"`) && !strings.Contains(s, `"version":"0.6.1"`) {
		t.Errorf("json full-tree output missing version field: %q", s)
	}
}

func TestRunHelp_FormatJSONSingleTopic(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"--format=json", "cli"}, &out, "0.6.1", false)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, `"topic": "cli"`) && !strings.Contains(s, `"topic":"cli"`) {
		t.Errorf("single-topic json malformed: %q", s)
	}
	// Single topic should NOT include the HelpPayload wrapper fields.
	if strings.Contains(s, `"topics":[`) || strings.Contains(s, `"topics": [`) {
		t.Errorf("single-topic output should not include wrapper: %q", s)
	}
}

func TestRunHelp_UnknownFormat_Exit2(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"--format=bogus"}, &out, "0.6.1", false)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "bogus") {
		t.Errorf("error must name the bad format: %q", out.String())
	}
}
