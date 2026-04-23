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
