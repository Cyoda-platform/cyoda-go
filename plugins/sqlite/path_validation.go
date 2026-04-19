package sqlite

import (
	"errors"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// ErrInvalidFilterPath is returned when a Filter.Path or OrderSpec.Path
// contains characters that could break out of a JSON-path literal in a
// json_extract expression. Sentinel for callers that want to distinguish
// input validation errors from storage errors.
var ErrInvalidFilterPath = errors.New("invalid filter path")

// validateJSONPath enforces a strict dotted-identifier grammar on paths that
// are interpolated into json_extract(..., '$.<path>') expressions.
//
// Allowed: segments of ASCII letters, digits, and underscore, separated by
// single '.' characters. At least one segment, no empty segments, no
// leading/trailing dots. This rejects every character that could terminate
// the surrounding single-quoted SQL literal or otherwise inject SQL —
// notably ', ", \, ;, -, /, *, whitespace, and control bytes.
//
// The grammar is intentionally narrower than the full SQLite JSON path
// grammar (which accepts bracketed indices and Unicode identifiers). If a
// genuine use case ever needs those forms, extend this validator rather
// than bypassing it.
func validateJSONPath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: empty", ErrInvalidFilterPath)
	}
	segmentStart := 0
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c == '.' {
			if i == segmentStart {
				return fmt.Errorf("%w: empty segment", ErrInvalidFilterPath)
			}
			segmentStart = i + 1
			continue
		}
		if !isIdentByte(c) {
			return fmt.Errorf("%w: disallowed character %q at offset %d", ErrInvalidFilterPath, c, i)
		}
	}
	if segmentStart == len(path) {
		return fmt.Errorf("%w: trailing dot", ErrInvalidFilterPath)
	}
	return nil
}

func isIdentByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '_':
		return true
	}
	return false
}

// validateFilterPaths walks a Filter tree and returns the first invalid path
// it encounters. Leaf nodes without a Path (IsNull tree operators etc.) are
// skipped; only nodes whose Path will be interpolated into SQL are checked.
func validateFilterPaths(f spi.Filter) error {
	switch f.Op {
	case spi.FilterAnd, spi.FilterOr:
		for _, c := range f.Children {
			if err := validateFilterPaths(c); err != nil {
				return err
			}
		}
		return nil
	}
	if f.Path == "" {
		return nil
	}
	return validateJSONPath(f.Path)
}

// validateOrderSpecs checks every OrderSpec.Path.
func validateOrderSpecs(specs []spi.OrderSpec) error {
	for _, s := range specs {
		if s.Path == "" {
			continue
		}
		if err := validateJSONPath(s.Path); err != nil {
			return err
		}
	}
	return nil
}
