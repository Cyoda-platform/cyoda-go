package renderer

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiReset = "\x1b[0m"
)

// Pre-compiled inline regexes. Order matters — match code spans before
// bold so backtick-delimited ** isn't bolded.
var (
	reCode   = regexp.MustCompile("`([^`]+)`")
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*]+)\*`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// RenderText writes tokens to w as ANSI-colourised (when isTTY) or
// plain-text (when !isTTY) output.
func RenderText(w io.Writer, tokens []Token, isTTY bool) {
	for i, tok := range tokens {
		if i > 0 {
			fmt.Fprint(w, "\n")
		}
		switch tok.Kind {
		case KindHeading:
			text := applyInline(tok.Text, isTTY)
			if isTTY {
				fmt.Fprintf(w, "%s%s%s\n", ansiBold, text, ansiReset)
			} else {
				fmt.Fprintf(w, "%s\n", text)
			}
		case KindParagraph:
			fmt.Fprintln(w, applyInline(tok.Text, isTTY))
		case KindBullet:
			fmt.Fprintf(w, "  • %s\n", applyInline(tok.Text, isTTY))
		case KindCodeBlock:
			for _, line := range strings.Split(tok.Text, "\n") {
				if isTTY {
					fmt.Fprintf(w, "%s  %s%s\n", ansiDim, line, ansiReset)
				} else {
					fmt.Fprintf(w, "  %s\n", line)
				}
			}
		case KindRule:
			if isTTY {
				fmt.Fprintf(w, "%s──────────────%s\n", ansiDim, ansiReset)
			} else {
				fmt.Fprintf(w, "──────────────\n")
			}
		}
	}
}

// applyInline walks the inline markers (**bold**, *italic*, `code`,
// [text](url)) and either emits ANSI codes (TTY) or strips the markers
// (non-TTY). Code spans are replaced first so ** inside backticks isn't
// mistakenly bolded.
func applyInline(s string, isTTY bool) string {
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		if isTTY {
			return ansiDim + inner + ansiReset
		}
		return inner
	})
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		if isTTY {
			return ansiBold + inner + ansiReset
		}
		return inner
	})
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		if isTTY {
			return ansiBold + inner + ansiReset
		}
		return inner
	})
	s = reLink.ReplaceAllString(s, "$1 ($2)")
	return s
}
