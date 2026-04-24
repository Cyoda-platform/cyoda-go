package renderer

import (
	"encoding/json"
	"reflect"
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
		Actions:   []string{},
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back TopicDescriptor
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, d) {
		t.Errorf("roundtrip mismatch:\n got:  %+v\n want: %+v", back, d)
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
	syn := ExtractSynopsis(body)
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
	secs := ExtractSections(body)
	if len(secs) != 3 {
		t.Fatalf("got %d sections, want 3: %+v", len(secs), secs)
	}
	if secs[0].Name != "SYNOPSIS" || secs[1].Name != "DESCRIPTION" || secs[2].Name != "EXAMPLES" {
		t.Errorf("section names: %+v", secs)
	}
}

func TestExtractTagline_PrefersNameSection(t *testing.T) {
	body := []byte(`# cli

## NAME

cli — the cyoda command-line interface.

## DESCRIPTION

A long paragraph that should NOT be used.
`)
	got := ExtractTagline(body)
	want := "the cyoda command-line interface."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractTagline_StripsInlineMarkers(t *testing.T) {
	body := []byte(`## NAME

config — uses **bold** and ` + "`" + `code` + "`" + ` markers.
`)
	got := ExtractTagline(body)
	want := "uses bold and code markers."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractTagline_CollapsesWhitespace(t *testing.T) {
	body := []byte(`## DESCRIPTION

Line one wraps
across multiple
newlines.
`)
	got := ExtractTagline(body)
	want := "Line one wraps across multiple newlines."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractTagline_TruncatesLongText(t *testing.T) {
	long := strings.Repeat("abc ", 40) // 160 chars
	body := []byte("## NAME\n\ntopic — " + long + "\n")
	got := ExtractTagline(body)
	if len([]rune(got)) != 80 {
		t.Errorf("got len %d, want 80: %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("got %q, want trailing …", got)
	}
}

func TestExtractTagline_StubWithoutName(t *testing.T) {
	body := []byte(`# topic

**Content pending in v0.6.1.** See the cyoda-go README for current external documentation while this topic is authored.
`)
	got := ExtractTagline(body)
	// Should strip markers, collapse whitespace, truncate.
	if strings.Contains(got, "**") {
		t.Errorf("should strip bold markers: %q", got)
	}
	if !strings.HasPrefix(got, "Content pending in v0.6.1.") {
		t.Errorf("should start with first sentence: %q", got)
	}
}
