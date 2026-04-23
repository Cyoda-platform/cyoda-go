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
