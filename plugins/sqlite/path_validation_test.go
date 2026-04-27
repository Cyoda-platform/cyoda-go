package sqlite

import (
	"errors"
	"testing"
)

// TestValidateJSONPath_Accepts ensures well-formed dotted-identifier paths pass.
func TestValidateJSONPath_Accepts(t *testing.T) {
	valid := []string{
		"state",
		"city",
		"name",
		"nested.field",
		"a.b.c",
		"field_1",
		"UserID",
		"order42",
		"_private",
	}
	for _, p := range valid {
		if err := validateJSONPath(p); err != nil {
			t.Errorf("validateJSONPath(%q) returned unexpected error: %v", p, err)
		}
	}
}

// TestValidateJSONPath_AcceptsHyphenatedSegments ensures field names that
// contain hyphens (e.g. "some-array", "some-object") are accepted.
// Hyphens are safe inside single-quoted SQLite JSON-path literals — they
// cannot break out of the surrounding quote and are valid JSON key characters.
func TestValidateJSONPath_AcceptsHyphenatedSegments(t *testing.T) {
	valid := []string{
		"some-array",
		"some-array.some-object",
		"some-array.some-object.some-key",
		"field-name",
		"a-b-c",
	}
	for _, p := range valid {
		if err := validateJSONPath(p); err != nil {
			t.Errorf("validateJSONPath(%q) returned unexpected error: %v", p, err)
		}
	}
}

// TestValidateJSONPath_RejectsInjection ensures classic SQL-injection
// payloads are rejected before they can reach json_extract(...,'$.<path>').
//
// NOTE: single-hyphen and double-hyphen paths (e.g. "a-b", "a--b") are
// NOT injection vectors — hyphens are inert inside single-quoted SQLite
// string literals and are valid JSON key characters. Those paths are
// accepted (see TestValidateJSONPath_AcceptsHyphenatedSegments). Only
// characters that can break out of a single-quoted SQL literal, or that
// are structurally invalid (whitespace, empty segments, etc.) are rejected.
func TestValidateJSONPath_RejectsInjection(t *testing.T) {
	malicious := []string{
		// Single-quote escape — the core injection vector.
		"state')--",
		"state') UNION SELECT 1 --",
		"a'b",
		// Block-comment sequences (/* breaks the string context).
		"a/*b*/c",
		// SQL statement terminators.
		"a;b",
		";DROP TABLE entities",
		// Whitespace and control characters.
		"a b",
		"a\nb",
		"a\tb",
		// Empty segments / malformed dotting.
		"",
		".",
		".foo",
		"foo.",
		"a..b",
		// Backslash / quote characters outright.
		`a"b`,
		`a\b`,
	}
	for _, p := range malicious {
		err := validateJSONPath(p)
		if err == nil {
			t.Errorf("validateJSONPath(%q) = nil, want non-nil (injection payload accepted)", p)
			continue
		}
		if !errors.Is(err, ErrInvalidFilterPath) {
			t.Errorf("validateJSONPath(%q) = %v, want wraps ErrInvalidFilterPath", p, err)
		}
	}
}
