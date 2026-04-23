package renderer

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
)

// namePrefixPattern matches the leading "<topic> — " or "<topic> - " in a
// NAME section so it can be stripped; the topic name is already shown in the
// summary's first column.
var namePrefixPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+\s*[—-]\s*`)

// Inline markdown patterns for stripping markers in plain-text extraction.
// Order matters: code spans before bold so backtick-delimited ** isn't bolded.
var (
	reCode   = regexp.MustCompile("`([^`]+)`")
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*]+)\*`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// flattenToPlainText strips inline markdown markers and collapses all
// whitespace (including newlines) into single spaces.
func flattenToPlainText(s string) string {
	// Strip inline markers in the same order as applyInline: code before bold.
	s = reCode.ReplaceAllString(s, "$1")
	s = reBold.ReplaceAllString(s, "$1")
	s = reItalic.ReplaceAllString(s, "$1")
	s = reLink.ReplaceAllString(s, "$1")
	// Collapse any whitespace run (spaces, tabs, newlines) into a single space.
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// stripTopicPrefix removes the leading "<topic> — " or "<topic> - " prefix
// from a NAME section body, leaving just the description.
func stripTopicPrefix(s string) string {
	return namePrefixPattern.ReplaceAllString(s, "")
}

// truncate returns s if it fits within max runes, otherwise clips at max-1
// runes and appends "…".
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimRight(string(runes[:max-1]), " ") + "…"
}

// ExtractTagline returns a short single-line description suitable for the
// summary view. Preference order:
//  1. First paragraph of the NAME section, with "<topic> — " prefix stripped
//  2. First paragraph of the DESCRIPTION section (via ExtractSynopsis)
//  3. First non-heading paragraph anywhere (fallback of fallbacks)
//
// Output always has inline markers stripped, whitespace collapsed to single
// spaces, and is truncated to 80 runes with a trailing "…" if cut.
func ExtractTagline(body []byte) string {
	secs := ExtractSections(body)
	for _, s := range secs {
		if s.Name == "NAME" {
			p := firstParagraph(s.Body)
			if p != "" {
				return truncate(flattenToPlainText(stripTopicPrefix(p)), 80)
			}
		}
	}
	// Fallback to the existing synopsis logic.
	return truncate(flattenToPlainText(ExtractSynopsis(body)), 80)
}

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
