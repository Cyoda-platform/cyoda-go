package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestGoreleaserHelpJSONHasVersionLdflags is a regression test for issue #101.
//
// The .goreleaser.yaml before-hooks generate cyoda_help_<version>.json by
// invoking `go run ./cmd/cyoda help --format=json`. `go run` does not apply
// the `builds:` ldflags, so without an explicit -ldflags flag on the hook
// itself, main.version keeps its source default ("dev") and the JSON asset
// ships with `"version": "dev"` instead of the tagged release version.
//
// This test fails if the hook is missing -ldflags '-X main.version=...'.
func TestGoreleaserHelpJSONHasVersionLdflags(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	helpHookRe := regexp.MustCompile(`(?m)^.*go run .*\./cmd/cyoda.*help.*--format=json.*$`)
	matches := helpHookRe.FindAllString(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("no help-JSON generation hook found in .goreleaser.yaml — did it move?")
	}
	for _, hook := range matches {
		if !strings.Contains(hook, "-ldflags") || !strings.Contains(hook, "-X main.version=") {
			t.Errorf("help-JSON hook missing -ldflags with -X main.version=...:\n  %s", hook)
		}
	}
}
