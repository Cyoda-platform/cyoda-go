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

func TestRenderMarkdown_StripsMultipleSeeAlsoSections(t *testing.T) {
	body := []byte(`# Title

Body.

## SEE ALSO

- first-old

## OTHER

more body

## SEE ALSO

- second-old
`)
	seeAlso := []string{"canonical"}
	var buf bytes.Buffer
	RenderMarkdown(&buf, body, seeAlso)
	out := buf.String()
	if strings.Contains(out, "first-old") || strings.Contains(out, "second-old") {
		t.Errorf("both body SEE ALSO sections must be stripped; got %q", out)
	}
	if !strings.Contains(out, "## OTHER") || !strings.Contains(out, "more body") {
		t.Errorf("non-SEE-ALSO sections must be preserved; got %q", out)
	}
	if !strings.Contains(out, "canonical") {
		t.Errorf("front-matter see_also must appear; got %q", out)
	}
}

func TestRenderMarkdown_StripsConsecutiveSeeAlsoSections(t *testing.T) {
	// Two consecutive SEE ALSO sections with no intervening H1/H2 heading.
	// The second one must also be stripped, not emitted as a heading.
	body := []byte(`# Title

Body.

## SEE ALSO

- first-old

## SEE ALSO

- second-old
`)
	seeAlso := []string{"canonical"}
	var buf bytes.Buffer
	RenderMarkdown(&buf, body, seeAlso)
	out := buf.String()
	if strings.Contains(out, "first-old") || strings.Contains(out, "second-old") {
		t.Errorf("both consecutive SEE ALSO sections must be stripped; got %q", out)
	}
	if strings.Count(out, "## SEE ALSO") != 1 {
		t.Errorf("exactly one SEE ALSO heading (the appended one) must appear; got %q", out)
	}
	if !strings.Contains(out, "canonical") {
		t.Errorf("front-matter see_also must appear; got %q", out)
	}
}
