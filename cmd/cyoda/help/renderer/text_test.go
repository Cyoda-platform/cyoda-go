package renderer

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderText_EmitsANSIOnDarkStyle(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("# Title\n\nBody.\n"), "dark")
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if !strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("dark style output should contain ANSI: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "Title") {
		t.Errorf("output missing heading content: %q", buf.String())
	}
}

func TestRenderText_EmitsANSIOnLightStyle(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("# Title\n\nBody.\n"), "light")
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if !strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("light style output should contain ANSI: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "Title") {
		t.Errorf("output missing heading content: %q", buf.String())
	}
}

func TestRenderText_NoANSIOnEmptyStyle(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("# Title\n\nBody.\n"), "")
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("empty style must NOT emit ANSI: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "Title") {
		t.Errorf("output missing heading content: %q", buf.String())
	}
}

func TestRenderText_FencedCodeBlockRenders(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("```\nhello\n```\n"), "")
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("code block content missing: %q", buf.String())
	}
}

func TestRenderText_BulletsRender(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("- one\n- two\n"), "")
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, "one") || !strings.Contains(s, "two") {
		t.Errorf("bullets missing: %q", s)
	}
}

// hasBackgroundColor reports whether s contains an ANSI SGR sequence that
// sets a background color. Glamour combines foreground and background into a
// single CSI sequence (e.g. "\x1b[38;5;203;48;5;254m"), so we cannot simply
// search for "\x1b[48;". Instead we look for the "48;" token inside any CSI
// parameter list.
func hasBackgroundColor(s string) bool {
	// Walk through each ESC [ ... m sequence.
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		// Scan to the 'm' terminator.
		end := strings.IndexByte(s[i+2:], 'm')
		if end < 0 {
			continue
		}
		params := s[i+2 : i+2+end]
		if strings.Contains(params, "48;") {
			return true
		}
		i += 2 + end
	}
	return false
}

// TestRenderText_NoGreyBackgroundLight verifies that the light style does not
// emit background-color ANSI escape codes on inline code spans. Glamour's
// built-in light preset uses a pale grey fill (48;5;254) that looks
// washed out on white terminals; our custom style clears it.
func TestRenderText_NoGreyBackgroundLight(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderText(&buf, []byte("use `init` flag\n"), "light"); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if hasBackgroundColor(buf.String()) {
		t.Errorf("light style must not emit background-color ANSI on inline code: %q", buf.String())
	}
}

// TestRenderText_NoGreyBackgroundDark verifies that the dark style does not
// emit background-color ANSI escape codes on inline code spans. Glamour's
// built-in dark preset uses a dark grey fill that we also clear for
// consistency.
func TestRenderText_NoGreyBackgroundDark(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderText(&buf, []byte("use `init` flag\n"), "dark"); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if hasBackgroundColor(buf.String()) {
		t.Errorf("dark style must not emit background-color ANSI on inline code: %q", buf.String())
	}
}

// TestRenderText_LightTealOnInlineCode verifies that the light style emits a
// teal foreground (#4FB8B0 → SGR 38;2;79;184;176 in truecolor) on inline code
// spans. We force truecolor via COLORTERM so glamour/lipgloss does not
// downgrade to a 256-color approximation in the test environment.
func TestRenderText_LightTealOnInlineCode(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	var buf bytes.Buffer
	if err := RenderText(&buf, []byte("use `init` now\n"), "light"); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	// #4FB8B0 in truecolor SGR: 38;2;79;184;176 (0x4F=79, 0xB8=184, 0xB0=176)
	if !strings.Contains(out, "38;2;79;184;176") {
		t.Errorf("light style inline code must use brand aqua (#4FB8B0 → 38;2;79;184;176); got %q", out)
	}
}

// TestRenderText_DarkTealOnInlineCode verifies that the dark style emits a
// brand aqua foreground (#5FD7D7 → SGR 38;2;95;215;215 in truecolor) on
// inline code spans. This matches 256-color index 80 used in the banner.
func TestRenderText_DarkTealOnInlineCode(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	var buf bytes.Buffer
	if err := RenderText(&buf, []byte("use `init` now\n"), "dark"); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	// #5FD7D7 in truecolor SGR: 38;2;95;215;215 (hex 0xD7 = 215)
	if !strings.Contains(out, "38;2;95;215;215") {
		t.Errorf("dark style inline code must use brand aqua (#5FD7D7 → 38;2;95;215;215); got %q", out)
	}
}

// hasBoldAndTeal reports whether the output contains a CSI sequence that
// carries both SGR 1 (bold) and a teal truecolor foreground in the same
// escape block. Parameters can appear in any order, so we parse each CSI
// block's parameter set and check for membership.
func hasBoldAndTeal(out, tealSGR string) bool {
	// tealSGR is the "38;2;R;G;B" suffix without the surrounding CSI/m.
	for i := 0; i < len(out); i++ {
		if out[i] != '\x1b' || i+1 >= len(out) || out[i+1] != '[' {
			continue
		}
		end := strings.IndexByte(out[i+2:], 'm')
		if end < 0 {
			continue
		}
		params := out[i+2 : i+2+end]
		if strings.Contains(params, tealSGR) && strings.Contains(params, "1") {
			// Verify "1" is a standalone parameter, not part of "10", "31", etc.
			for _, p := range strings.Split(params, ";") {
				if p == "1" {
					return true
				}
			}
		}
		i += 2 + end
	}
	return false
}

// TestRenderText_InlineCodeIsBold verifies that the light cyoda theme emits
// SGR bold (1) together with the brand aqua truecolor foreground on inline code
// spans. Both must appear in the same CSI escape block.
func TestRenderText_InlineCodeIsBold(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	var buf bytes.Buffer
	if err := RenderText(&buf, []byte("use `init` now\n"), "light"); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	// Light brand aqua is #4FB8B0 → 38;2;79;184;176 (0x4F=79, 0xB8=184, 0xB0=176)
	if !hasBoldAndTeal(out, "38;2;79;184;176") {
		t.Errorf("inline code must emit Bold + brand aqua SGR in same CSI block; got %q", out)
	}
}

// TestRenderText_FencedCodeBlockIsBold verifies that the light cyoda theme
// emits SGR bold (1) for plain (untagged) fenced code block content. Plain
// fences are routed through Chroma's "Text" token type — the teal colour is
// not present there (Chroma manages its own palette), so we check for SGR 1
// anywhere in the output rather than requiring bold+teal in the same sequence.
func TestRenderText_FencedCodeBlockIsBold(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderText(&buf, []byte("```\nhello\n```\n"), "light"); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	// Check that at least one CSI block contains the standalone "1" bold param.
	if !hasStandaloneBold(out) {
		t.Errorf("fenced code block must emit SGR bold (1); got %q", out)
	}
}

// hasStandaloneBold reports whether the output contains a CSI sequence with
// the standalone parameter "1" (bold), i.e. "1" delimited by ';' or at the
// start/end of the parameter list, not as part of "10", "21", etc.
func hasStandaloneBold(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		end := strings.IndexByte(s[i+2:], 'm')
		if end < 0 {
			continue
		}
		params := s[i+2 : i+2+end]
		for _, p := range strings.Split(params, ";") {
			if p == "1" {
				return true
			}
		}
		i += 2 + end
	}
	return false
}
