package renderer

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// RenderMarkdown writes the topic's body to w, stripping any body-level
// SEE ALSO section (case-sensitive H2 "## SEE ALSO" through the next
// H1/H2 or end-of-file), and appending the authoritative see_also from
// front-matter as a fresh SEE ALSO section when non-empty.
func RenderMarkdown(w io.Writer, body []byte, seeAlso []string) {
	stripped := StripSeeAlsoSection(body)
	_, _ = w.Write(stripped)
	// Ensure single trailing newline before the SEE ALSO we append.
	if !bytes.HasSuffix(stripped, []byte("\n")) {
		fmt.Fprintln(w)
	}
	if len(seeAlso) == 0 {
		return
	}
	fmt.Fprintln(w, "\n## SEE ALSO")
	fmt.Fprintln(w)
	for _, s := range seeAlso {
		fmt.Fprintf(w, "- %s\n", s)
	}
}

// StripSeeAlsoSection returns src with any "## SEE ALSO" section
// removed (H2 through the next H2/H1, or EOF). It is exported so
// callers that render via other paths (e.g. text mode) can strip the
// body SEE ALSO before passing to their own renderer and appending
// the authoritative front-matter list themselves.
func StripSeeAlsoSection(src []byte) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	skipping := false
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		// A "## SEE ALSO" heading always starts a skip section —
		// including a second one inside an already-skipping region.
		if trimmed == "## SEE ALSO" {
			skipping = true
			continue
		}
		// Any other H1/H2 ends the skip section and falls through
		// so the heading itself is written.
		if skipping && (strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ")) {
			skipping = false
		}
		if skipping {
			continue
		}
		fmt.Fprintln(&out, line)
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}
