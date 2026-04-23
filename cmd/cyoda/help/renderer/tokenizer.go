// Package renderer parses and renders the cyoda help markdown subset.
// The supported subset is intentionally tight so the tokenizer stays
// small. See /docs/superpowers/specs/2026-04-23-cyoda-help-subsystem-design.md
// §Supported markdown subset.
//
// CLI output from this package uses fmt.Fprint to injected writers.
// This is NOT operational logging — the project's log/slog-exclusive
// rule does not apply here.
package renderer

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// TokenKind enumerates the supported block-level shapes.
type TokenKind int

const (
	KindParagraph TokenKind = iota + 1
	KindHeading
	KindBullet
	KindCodeBlock
	KindRule
)

// Token is a single block-level parsed element.
type Token struct {
	Kind  TokenKind
	Level int    // heading level 1-3 only
	Text  string // flattened content (paragraph/heading/bullet) or code body
}

// Tokenize parses the supported subset. Unsupported constructs are
// silently ignored by the tokenizer; FindUnsupported is the linter that
// catches them at content-test time.
func Tokenize(src []byte) []Token {
	var out []Token
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var paraBuf []string
	flushPara := func() {
		if len(paraBuf) > 0 {
			out = append(out, Token{Kind: KindParagraph, Text: strings.TrimSpace(strings.Join(paraBuf, " "))})
			paraBuf = nil
		}
	}
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		// Horizontal rule.
		if trimmed == "---" {
			flushPara()
			out = append(out, Token{Kind: KindRule})
			continue
		}

		// Blank line ends paragraphs/bullets.
		if trimmed == "" {
			flushPara()
			continue
		}

		// Headings — match longest prefix first to avoid ### matching ##.
		if strings.HasPrefix(trimmed, "### ") {
			flushPara()
			out = append(out, Token{Kind: KindHeading, Level: 3, Text: strings.TrimSpace(trimmed[4:])})
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			flushPara()
			out = append(out, Token{Kind: KindHeading, Level: 2, Text: strings.TrimSpace(trimmed[3:])})
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			flushPara()
			out = append(out, Token{Kind: KindHeading, Level: 1, Text: strings.TrimSpace(trimmed[2:])})
			continue
		}

		// Bullets.
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flushPara()
			out = append(out, Token{Kind: KindBullet, Text: strings.TrimSpace(trimmed[2:])})
			continue
		}

		// Fenced code block.
		if strings.HasPrefix(trimmed, "```") {
			flushPara()
			var body []string
			for sc.Scan() {
				inner := sc.Text()
				if strings.HasPrefix(strings.TrimSpace(inner), "```") {
					break
				}
				body = append(body, inner)
			}
			out = append(out, Token{Kind: KindCodeBlock, Text: strings.Join(body, "\n")})
			continue
		}

		// Fallthrough: paragraph continuation.
		paraBuf = append(paraBuf, trimmed)
	}
	flushPara()
	return out
}

// Issue describes a disallowed-markdown site.
type Issue struct {
	Line int
	Kind string
	Text string
}

// String formats an Issue for human-readable output.
func (i Issue) String() string { return fmt.Sprintf("line %d: %s (%q)", i.Line, i.Kind, i.Text) }

// FindUnsupported scans for disallowed markdown constructs. Returns
// non-empty issues when any are present.
func FindUnsupported(src []byte) []Issue {
	var issues []Issue
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNum := 0
	inFenced := false
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFenced = !inFenced
			continue
		}
		if inFenced {
			continue
		}
		// Pipe table rows.
		if strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
			issues = append(issues, Issue{Line: lineNum, Kind: "pipe table", Text: line})
			continue
		}
		// HTML blocks.
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && len(trimmed) > 2 {
			issues = append(issues, Issue{Line: lineNum, Kind: "html block", Text: line})
			continue
		}
		// Nested list (indented bullet).
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			t := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
				issues = append(issues, Issue{Line: lineNum, Kind: "nested list", Text: line})
				continue
			}
		}
		// Blockquote.
		if strings.HasPrefix(trimmed, "> ") {
			issues = append(issues, Issue{Line: lineNum, Kind: "blockquote", Text: line})
			continue
		}
	}
	return issues
}
