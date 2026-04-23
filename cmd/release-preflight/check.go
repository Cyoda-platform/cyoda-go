package main

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// Violation is a single pre-flight finding that must be addressed before a
// release tag can be built.
type Violation struct {
	// Kind categorises the finding: "pseudo-version" or "replace".
	Kind string
	// Module is the offending module path.
	Module string
	// Detail is a human-readable specifier (the pseudo-version string or
	// the replace RHS).
	Detail string
}

// pseudoVersionRe matches all three pseudo-version forms defined by
// `go help pseudo-versions`. All three share the same terminal suffix —
// a 14-digit timestamp and a 12-hex commit SHA, hyphen-separated — which
// is the simplest anchor to match against:
//
//	vX.0.0-<14 digits>-<12 hex>                  (form 1: no base)
//	vX.Y.Z-<prerelease>.0.<14 digits>-<12 hex>   (form 2: on a prerelease base)
//	vX.Y.(Z+1)-0.<14 digits>-<12 hex>            (form 3: on a stable base)
//
// Capturing groups: [1] = module path, [2] = full pseudo-version.
// The separator before the 14-digit timestamp is `-` in form 1 (no base)
// and `.` in forms 2/3 (the timestamp is the final dot-separated component
// of the prerelease segment). [-.] covers both.
var pseudoVersionRe = regexp.MustCompile(`^(\S+)\s+(v\S*[-.][0-9]{14}-[0-9a-f]{12})\b`)

// checkPseudoVersions scans `go list -m all` output and returns violations
// for modules resolving to a pseudo-version whose path starts with
// orgPrefix. Upstream pseudo-versions (outside orgPrefix) are tolerated —
// they are a normal Go ecosystem artifact and do not threaten release
// reproducibility of our own artefacts.
//
// Passing an empty orgPrefix disables the filter and flags every
// pseudo-version (matches the legacy shell behavior; retained for tests).
func checkPseudoVersions(goListOutput []byte, orgPrefix string) []Violation {
	var violations []Violation
	scanner := bufio.NewScanner(bytes.NewReader(goListOutput))
	for scanner.Scan() {
		line := scanner.Text()
		m := pseudoVersionRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		mod := m[1]
		if orgPrefix != "" && !strings.HasPrefix(mod, orgPrefix) {
			continue
		}
		violations = append(violations, Violation{
			Kind:   "pseudo-version",
			Module: mod,
			Detail: m[2],
		})
	}
	return violations
}

// checkReplaces scans go.mod source and returns violations for replace
// directives whose right-hand side is a module path rather than a local
// filesystem path. Local-path replaces of the form `=> ./path`,
// `=> ../path`, or `=> /abs/path` are required for in-repo multi-module
// layouts and are allowed.
//
// Supports both single-line (`replace X => Y`) and block (`replace ( ... )`)
// directive forms. Lines starting with `//` (comments) are ignored.
func checkReplaces(goModContent []byte) []Violation {
	var violations []Violation
	scanner := bufio.NewScanner(bytes.NewReader(goModContent))
	inBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || trimmed == "" {
			continue
		}
		if !inBlock && strings.HasPrefix(trimmed, "replace (") {
			inBlock = true
			continue
		}
		if inBlock && trimmed == ")" {
			inBlock = false
			continue
		}
		// Determine the replace expression:
		//   single-line:  "replace X [vVer] => Y [vVer]"
		//   inside block: "X [vVer] => Y [vVer]"
		var expr string
		switch {
		case strings.HasPrefix(trimmed, "replace "):
			expr = strings.TrimPrefix(trimmed, "replace ")
		case inBlock:
			expr = trimmed
		default:
			continue
		}
		parts := strings.SplitN(expr, "=>", 2)
		if len(parts) != 2 {
			continue
		}
		rhs := strings.TrimSpace(parts[1])
		rhsFields := strings.Fields(rhs)
		if len(rhsFields) == 0 {
			continue
		}
		rhsFirst := rhsFields[0]
		// Local paths start with ./, ../, or / (absolute). Anything else
		// points at a module path and is flagged.
		if strings.HasPrefix(rhsFirst, "./") || strings.HasPrefix(rhsFirst, "../") || strings.HasPrefix(rhsFirst, "/") {
			continue
		}
		lhsFields := strings.Fields(strings.TrimSpace(parts[0]))
		if len(lhsFields) == 0 {
			continue
		}
		violations = append(violations, Violation{
			Kind:   "replace",
			Module: lhsFields[0],
			Detail: expr,
		})
	}
	return violations
}

// formatViolations renders violations as a multi-line human-readable
// report. The caller prints this to stderr and exits non-zero.
func formatViolations(violations []Violation) string {
	if len(violations) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Release pre-flight found %d issue(s):\n\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(&b, "  [%s] %s\n      %s\n", v.Kind, v.Module, v.Detail)
	}
	b.WriteString("\nRemediation:\n")
	b.WriteString("  pseudo-version — tag the module and run 'go get <module>@<tag>' before tagging\n")
	b.WriteString("  replace        — remove with 'go mod edit -dropreplace <module>', then 'go mod tidy'\n")
	return b.String()
}
