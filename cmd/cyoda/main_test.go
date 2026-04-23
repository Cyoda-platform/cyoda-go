package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintVersion_IncludesAllFields(t *testing.T) {
	version = "1.2.3"
	commit = "abc1234"
	buildDate = "2026-04-23T12:00:00Z"
	defer func() { version, commit, buildDate = "dev", "unknown", "unknown" }()

	var buf bytes.Buffer
	printVersion(&buf)
	s := buf.String()
	for _, want := range []string{"1.2.3", "abc1234", "2026-04-23T12:00:00Z"} {
		if !strings.Contains(s, want) {
			t.Errorf("printVersion output missing %q: %q", want, s)
		}
	}
}
