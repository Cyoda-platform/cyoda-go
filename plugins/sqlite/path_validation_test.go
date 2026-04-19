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

// TestValidateJSONPath_RejectsInjection ensures classic SQL-injection
// payloads are rejected before they can reach json_extract(...,'$.<path>').
func TestValidateJSONPath_RejectsInjection(t *testing.T) {
	malicious := []string{
		// Single-quote escape — the core injection vector.
		"state')--",
		"state') UNION SELECT 1 --",
		"a'b",
		// Line-comment / block-comment sequences.
		"a--b",
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
