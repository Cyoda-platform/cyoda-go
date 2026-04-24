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
