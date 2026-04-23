package renderer

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderText_HeadingBoldWhenTTY(t *testing.T) {
	tokens := []Token{{Kind: KindHeading, Level: 1, Text: "cli"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, true)
	out := buf.String()
	if !strings.Contains(out, "\x1b[1m") {
		t.Errorf("H1 must emit bold ANSI on TTY; got %q", out)
	}
	if !strings.Contains(out, "cli") {
		t.Errorf("H1 text missing from output: %q", out)
	}
}

func TestRenderText_NoANSIWhenNotTTY(t *testing.T) {
	tokens := []Token{{Kind: KindHeading, Level: 1, Text: "cli"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("non-TTY output must NOT contain ANSI; got %q", out)
	}
	if !strings.Contains(out, "cli") {
		t.Errorf("H1 text missing: %q", out)
	}
}

func TestRenderText_Bullets(t *testing.T) {
	tokens := []Token{
		{Kind: KindBullet, Text: "one"},
		{Kind: KindBullet, Text: "two"},
	}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if !strings.Contains(out, "  • one") || !strings.Contains(out, "  • two") {
		t.Errorf("bullets should render as '  • ...'; got %q", out)
	}
}

func TestRenderText_InlineBoldStripsMarkers(t *testing.T) {
	tokens := []Token{{Kind: KindParagraph, Text: "hello **world** done"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if !strings.Contains(out, "world") {
		t.Errorf("bold text missing: %q", out)
	}
	if strings.Contains(out, "**") {
		t.Errorf("asterisks must be stripped in non-TTY text: %q", out)
	}
}

func TestRenderText_InlineBoldEmitsANSIOnTTY(t *testing.T) {
	tokens := []Token{{Kind: KindParagraph, Text: "hello **world** done"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, true)
	out := buf.String()
	if !strings.Contains(out, "\x1b[1m") {
		t.Errorf("TTY output must emit bold ANSI; got %q", out)
	}
}

func TestRenderText_CodeBlockIndented(t *testing.T) {
	tokens := []Token{{Kind: KindCodeBlock, Text: "line1\nline2"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if !strings.Contains(out, "  line1") || !strings.Contains(out, "  line2") {
		t.Errorf("code block must be 2-space-indented: %q", out)
	}
}

func TestRenderText_LinksFlattenToTextAndURL(t *testing.T) {
	tokens := []Token{{Kind: KindParagraph, Text: "see [docs](https://x/y)"}}
	var buf bytes.Buffer
	RenderText(&buf, tokens, false)
	out := buf.String()
	if !strings.Contains(out, "docs (https://x/y)") {
		t.Errorf("link should render as 'text (url)'; got %q", out)
	}
}
