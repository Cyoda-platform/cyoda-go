package renderer

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderText_EmitsANSIOnTTY(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("# Title\n\nBody.\n"), true)
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if !strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("TTY output should contain ANSI: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "Title") {
		t.Errorf("output missing heading content: %q", buf.String())
	}
}

func TestRenderText_NoANSIOffTTY(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("# Title\n\nBody.\n"), false)
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("non-TTY output must NOT contain ANSI: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "Title") {
		t.Errorf("output missing heading content: %q", buf.String())
	}
}

func TestRenderText_FencedCodeBlockRenders(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("```\nhello\n```\n"), false)
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("code block content missing: %q", buf.String())
	}
}

func TestRenderText_BulletsRender(t *testing.T) {
	var buf bytes.Buffer
	err := RenderText(&buf, []byte("- one\n- two\n"), false)
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, "one") || !strings.Contains(s, "two") {
		t.Errorf("bullets missing: %q", s)
	}
}
