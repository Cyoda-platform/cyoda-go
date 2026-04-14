//go:build cyoda_recon

package recon

import (
	"strings"
	"testing"
)

func TestDiffIdentical(t *testing.T) {
	a := []byte(`{"name":"Alice","age":30}`)
	b := []byte(`{"name":"Alice","age":30}`)
	result := diffJSON(a, b, nil)
	if !result.match {
		t.Errorf("expected match, got diff:\n%s", result.diff)
	}
}

func TestDiffValueChange(t *testing.T) {
	a := []byte(`{"name":"Alice","age":30}`)
	b := []byte(`{"name":"Alice","age":31}`)
	result := diffJSON(a, b, nil)
	if result.match {
		t.Error("expected diff for different age")
	}
	if !strings.Contains(result.diff, "30") || !strings.Contains(result.diff, "31") {
		t.Errorf("diff should show old and new age values:\n%s", result.diff)
	}
}

func TestDiffWithExclusion(t *testing.T) {
	a := []byte(`{"name":"Alice","id":"aaa"}`)
	b := []byte(`{"name":"Alice","id":"bbb"}`)
	result := diffJSON(a, b, []string{"/id"})
	if !result.match {
		t.Errorf("expected match after excluding /id, got diff:\n%s", result.diff)
	}
}

func TestDiffMissingField(t *testing.T) {
	a := []byte(`{"name":"Alice"}`)
	b := []byte(`{"name":"Alice","extra":"field"}`)
	result := diffJSON(a, b, nil)
	if result.match {
		t.Error("expected diff for missing field")
	}
	if !strings.Contains(result.diff, "extra") {
		t.Errorf("diff should mention the extra field:\n%s", result.diff)
	}
}

func TestDiffNonJSON(t *testing.T) {
	a := []byte(`not json`)
	b := []byte(`also not json`)
	result := diffJSON(a, b, nil)
	if result.match {
		t.Error("expected diff for different non-JSON strings")
	}
}

func TestDiffNestedExclusion(t *testing.T) {
	a := []byte(`{"data":{"id":"aaa","name":"Alice"}}`)
	b := []byte(`{"data":{"id":"bbb","name":"Alice"}}`)
	result := diffJSON(a, b, []string{"/data/id"})
	if !result.match {
		t.Errorf("expected match after excluding /data/id, got diff:\n%s", result.diff)
	}
}

func TestDiffArrayOrderIgnored(t *testing.T) {
	a := []byte(`[{"name":"Bob"},{"name":"Alice"}]`)
	b := []byte(`[{"name":"Alice"},{"name":"Bob"}]`)
	result := diffJSON(a, b, nil)
	if !result.match {
		t.Errorf("arrays with same elements in different order should match, got diff:\n%s", result.diff)
	}
}

func TestDiffShowsUnifiedFormat(t *testing.T) {
	a := []byte(`{"name":"Alice"}`)
	b := []byte(`{"name":"Bob"}`)
	result := diffJSON(a, b, nil)
	if !strings.Contains(result.diff, "--- Cyoda-Go") {
		t.Errorf("expected unified diff header, got:\n%s", result.diff)
	}
	if !strings.Contains(result.diff, "+++ Cyoda Cloud") {
		t.Errorf("expected unified diff header, got:\n%s", result.diff)
	}
}
