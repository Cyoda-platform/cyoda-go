package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestGoreleaserBrewHasNoPostInstall is the regression test for issue #96.
//
// Homebrew sandboxes the post_install hook and denies writes to $HOME.
// Our previous formula tried to invoke `cyoda init` from post_install,
// which always failed on macOS and emitted a warning on every install.
// The generated Caveats block already tells users how to run init when
// they want to — no auto-init at install time is needed.
//
// This test fails if `.goreleaser.yaml` regrows a `post_install:` stanza
// under `brews:`.
func TestGoreleaserBrewHasNoPostInstall(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	// Match a `post_install:` YAML key indented under any block. The
	// goreleaser `brews:` schema only places post_install under a brew
	// entry, so any occurrence is the regression we're guarding.
	postInstallRe := regexp.MustCompile(`(?m)^\s*post_install\s*:`)
	if postInstallRe.MatchString(string(data)) {
		t.Errorf("`.goreleaser.yaml` contains a post_install stanza — the Homebrew formula must not auto-invoke `cyoda init` (issue #96).")
	}
}
