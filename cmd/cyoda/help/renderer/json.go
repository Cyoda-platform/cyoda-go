package renderer

import (
	"bufio"
	"bytes"
	"strings"
)

// TopicDescriptor is the stable JSON shape consumed by release assets,
// the REST API, and external tooling. Field additions are allowed
// without schema bump; field removal or semantic change bumps the
// HelpPayload.Schema integer.
type TopicDescriptor struct {
	Topic     string    `json:"topic"`
	Path      []string  `json:"path"`
	Title     string    `json:"title"`
	Synopsis  string    `json:"synopsis"`
	Body      string    `json:"body"`
	Sections  []Section `json:"sections"`
	SeeAlso   []string  `json:"see_also"`
	Stability string    `json:"stability"`
	Children  []string  `json:"children,omitempty"`
}

// Section is a single H2-delimited block within a topic document.
type Section struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// HelpPayload wraps the full-tree response of /api/help and the release
// JSON asset. Schema integer is the additive-only versioning key —
// consumers check it before parsing.
type HelpPayload struct {
	Schema  int               `json:"schema"`
	Version string            `json:"version"`
	Topics  []TopicDescriptor `json:"topics"`
}

// ExtractSynopsis returns the first paragraph under the DESCRIPTION
// H2 section. If absent, falls back to the first paragraph anywhere
// after the H1.
func ExtractSynopsis(body []byte) string {
	secs := ExtractSections(body)
	for _, s := range secs {
		if s.Name == "DESCRIPTION" {
			return firstParagraph(s.Body)
		}
	}
	return firstParagraph(string(body))
}

func firstParagraph(s string) string {
	for _, p := range strings.Split(strings.TrimSpace(s), "\n\n") {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return trimmed
	}
	return ""
}

// ExtractSections splits body into H2-delimited sections. The section
// Name is the H2 text as-is; Body is everything between this H2 and the
// next H2 or end-of-file. H1 is ignored.
func ExtractSections(body []byte) []Section {
	var out []Section
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var cur *Section
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			if cur != nil {
				cur.Body = strings.TrimSpace(cur.Body)
				out = append(out, *cur)
			}
			cur = &Section{Name: strings.TrimSpace(line[3:])}
			continue
		}
		if cur != nil {
			cur.Body += line + "\n"
		}
	}
	if cur != nil {
		cur.Body = strings.TrimSpace(cur.Body)
		out = append(out, *cur)
	}
	return out
}
