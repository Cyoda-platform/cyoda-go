// Package help embeds and renders the cyoda help topic tree.
package help

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// FrontMatter is the YAML header on every help topic source file.
type FrontMatter struct {
	Topic          string   `yaml:"topic"`
	Title          string   `yaml:"title"`
	Stability      string   `yaml:"stability"`
	SeeAlso        []string `yaml:"see_also,omitempty"`
	VersionAdded   string   `yaml:"version_added,omitempty"`
	SeeAlsoReplace bool     `yaml:"see_also_replace,omitempty"`
}

var frontMatterDelim = []byte("---\n")

// parseFrontMatter extracts the YAML front-matter from a markdown source
// and returns the parsed header, the body (front-matter stripped), and
// any error. Malformed front-matter or missing required fields are
// errors — this fails at tree-load time, not at query time.
func parseFrontMatter(src []byte) (*FrontMatter, []byte, error) {
	if !bytes.HasPrefix(src, frontMatterDelim) {
		return nil, nil, fmt.Errorf("front-matter missing: file must start with '---\\n'")
	}
	rest := src[len(frontMatterDelim):]
	end := bytes.Index(rest, frontMatterDelim)
	if end < 0 {
		return nil, nil, fmt.Errorf("front-matter unterminated: no closing '---' found")
	}
	header := rest[:end]
	body := bytes.TrimLeft(rest[end+len(frontMatterDelim):], "\n")

	fm := &FrontMatter{}
	if err := yaml.Unmarshal(header, fm); err != nil {
		return nil, nil, fmt.Errorf("front-matter YAML: %w", err)
	}
	if fm.Topic == "" {
		return nil, nil, fmt.Errorf("front-matter: required field 'topic' is empty")
	}
	if fm.Title == "" {
		return nil, nil, fmt.Errorf("front-matter: required field 'title' is empty")
	}
	switch fm.Stability {
	case "stable", "evolving", "experimental":
		// ok
	default:
		return nil, nil, fmt.Errorf("front-matter: stability must be stable|evolving|experimental, got %q", fm.Stability)
	}
	return fm, body, nil
}
