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
