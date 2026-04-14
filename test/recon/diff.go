//go:build cyoda_recon

package recon

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// diffResult holds the outcome of comparing two JSON responses.
type diffResult struct {
	match bool
	diff  string // unified diff text (empty if match)
}

// diffJSON compares two JSON byte slices after removing excluded paths.
// Returns whether they match and a human-readable unified diff if not.
func diffJSON(a, b []byte, exclusions []string) diffResult {
	cleanA := stripAndFormat(a, exclusions)
	cleanB := stripAndFormat(b, exclusions)

	if cleanA == cleanB {
		return diffResult{match: true}
	}

	diff := unifiedDiff(cleanA, cleanB, "Cyoda-Go", "Cyoda Cloud")
	return diffResult{match: false, diff: diff}
}

// stripAndFormat parses JSON, removes excluded paths, sorts arrays of objects
// for stable comparison, and returns pretty-printed JSON with sorted keys.
// If the input is not valid JSON, returns the raw string.
func stripAndFormat(data []byte, exclusions []string) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}

	for _, path := range exclusions {
		v = removePath(v, path)
	}

	v = sortArrays(v)

	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(pretty)
}

// sortArrays recursively sorts arrays of objects by a deterministic key
// so that element ordering differences don't produce false diffs.
func sortArrays(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			val[k] = sortArrays(child)
		}
		return val
	case []any:
		// Recursively sort children first.
		for i, elem := range val {
			val[i] = sortArrays(elem)
		}
		// Sort the array elements by their JSON representation for stability.
		sort.SliceStable(val, func(i, j int) bool {
			ki := sortKey(val[i])
			kj := sortKey(val[j])
			return ki < kj
		})
		return val
	default:
		return v
	}
}

// sortKey returns a stable string key for sorting array elements.
func sortKey(v any) string {
	switch val := v.(type) {
	case map[string]any:
		// Use common identifier fields as sort key.
		for _, key := range []string{"modelName", "name", "id", "entityName"} {
			if s, ok := val[key]; ok {
				return fmt.Sprintf("%v", s)
			}
		}
		// Fall back to JSON serialization.
		b, _ := json.Marshal(val)
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// removePath removes a JSON Pointer path (e.g., "/modelId") from a parsed JSON value.
func removePath(v any, pointer string) any {
	parts := splitPointer(pointer)
	if len(parts) == 0 {
		return v
	}
	return removePathRecursive(v, parts)
}

func removePathRecursive(v any, parts []string) any {
	if len(parts) == 0 {
		return v
	}

	switch val := v.(type) {
	case map[string]any:
		key := parts[0]
		if len(parts) == 1 {
			delete(val, key)
		} else if child, ok := val[key]; ok {
			val[key] = removePathRecursive(child, parts[1:])
		}
		return val
	case []any:
		// Apply removal to all array elements.
		for i, elem := range val {
			val[i] = removePathRecursive(elem, parts)
		}
		return val
	default:
		return v
	}
}

// splitPointer splits a JSON Pointer (e.g., "/a/b/c") into path segments.
func splitPointer(pointer string) []string {
	if pointer == "" || pointer == "/" {
		return nil
	}
	// Remove leading "/"
	trimmed := strings.TrimPrefix(pointer, "/")
	return strings.Split(trimmed, "/")
}

// unifiedDiff produces a simple unified diff between two strings.
func unifiedDiff(a, b, labelA, labelB string) string {
	linesA := strings.Split(a, "\n")
	linesB := strings.Split(b, "\n")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- %s\n", labelA))
	sb.WriteString(fmt.Sprintf("+++ %s\n", labelB))

	// Simple line-by-line diff — not a full LCS algorithm, but readable for JSON.
	maxLines := len(linesA)
	if len(linesB) > maxLines {
		maxLines = len(linesB)
	}

	for i := 0; i < maxLines; i++ {
		var lineA, lineB string
		if i < len(linesA) {
			lineA = linesA[i]
		}
		if i < len(linesB) {
			lineB = linesB[i]
		}

		if lineA == lineB {
			sb.WriteString("  " + lineA + "\n")
		} else {
			if i < len(linesA) {
				sb.WriteString("- " + lineA + "\n")
			}
			if i < len(linesB) {
				sb.WriteString("+ " + lineB + "\n")
			}
		}
	}

	return sb.String()
}
