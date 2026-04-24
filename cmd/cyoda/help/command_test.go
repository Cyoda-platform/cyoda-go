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
	code := RunHelp(testTree(t), []string{}, &out, "0.6.1", false, "")
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
	code := RunHelp(testTree(t), []string{"cli"}, &out, "0.6.1", false, "")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "Operate the binary.") {
		t.Errorf("cli body missing: %q", out.String())
	}
}

func TestRunHelp_Subtopic(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"cli", "serve"}, &out, "0.6.1", false, "")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "Serve API.") {
		t.Errorf("cli serve body missing: %q", out.String())
	}
}

func TestRunHelp_UnknownTopic_Exit2(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"widgetry"}, &out, "0.6.1", false, "")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "widgetry") {
		t.Errorf("error should name the topic: %q", out.String())
	}
}

func TestRunHelp_FormatJSON(t *testing.T) {
	var out bytes.Buffer
	code := RunHelp(testTree(t), []string{"--format=json"}, &out, "0.6.1", false, "")
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
	code := RunHelp(testTree(t), []string{"--format=json", "cli"}, &out, "0.6.1", false, "")
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
	code := RunHelp(testTree(t), []string{"--format=bogus"}, &out, "0.6.1", false, "")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "bogus") {
		t.Errorf("error must name the bad format: %q", out.String())
	}
}

func TestRunHelp_NoDuplicateSeeAlso(t *testing.T) {
	// Build a small tree with a topic whose body includes "## SEE ALSO"
	// and whose front-matter see_also is set.
	fsys := fstest.MapFS{
		"content/x.md": &fstest.MapFile{Data: []byte(`---
topic: x
title: x
stability: stable
see_also:
  - y
---

# x

body text

## SEE ALSO

- body-y
- body-z
`)},
		"content/y.md": &fstest.MapFile{Data: []byte(`---
topic: y
title: y
stability: stable
---

# y

body
`)},
	}
	tree, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var out bytes.Buffer
	// isTTY=true forces text mode, where the duplicate used to appear.
	code := RunHelp(tree, []string{"x"}, &out, "0.6.1", true, "")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	// Count occurrences of "SEE ALSO" — should be exactly one.
	seeAlsoCount := strings.Count(s, "SEE ALSO")
	if seeAlsoCount != 1 {
		t.Errorf("SEE ALSO appears %d times, want 1:\n%s", seeAlsoCount, s)
	}
	// Body's see-also content ("body-y", "body-z") must not appear.
	if strings.Contains(s, "body-y") || strings.Contains(s, "body-z") {
		t.Errorf("body-level see_also must be stripped, but appeared:\n%s", s)
	}
	// Front-matter's see_also must appear.
	if !strings.Contains(s, "y") {
		t.Errorf("front-matter see_also ('y') missing:\n%s", s)
	}
}

func TestRunHelp_SeeAlsoUsesCLISyntax(t *testing.T) {
	fsys := fstest.MapFS{
		"content/a.md": &fstest.MapFile{Data: []byte(`---
topic: a
title: a
stability: stable
see_also:
  - errors.VALIDATION_FAILED
---

# a
`)},
		"content/errors.md": &fstest.MapFile{Data: []byte(`---
topic: errors
title: errors
stability: stable
---

# errors
`)},
		"content/errors/VALIDATION_FAILED.md": &fstest.MapFile{Data: []byte(`---
topic: errors.VALIDATION_FAILED
title: vf
stability: stable
---

# vf
`)},
	}
	tree, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var out bytes.Buffer
	// isTTY=true forces text mode where CLI-syntax bullets are required.
	code := RunHelp(tree, []string{"a"}, &out, "0.6.1", true, "")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	if strings.Contains(s, "errors.VALIDATION_FAILED") {
		t.Errorf("SEE ALSO must show space-separated form, not dotted:\n%s", s)
	}
	if !strings.Contains(s, "errors VALIDATION_FAILED") {
		t.Errorf("SEE ALSO must contain 'errors VALIDATION_FAILED':\n%s", s)
	}
}
