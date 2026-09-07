package schema

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidFieldName marks a field name that the wire jsonPath grammar cannot
// spell. Both ingresses that establish a model's field set — the sample-data
// model import (via importer.Walk) and the ChangeLevel-driven schema
// extension on an entity write (via Admit) — funnel through this check, so
// this sentinel is the single classification signal their handlers key on to
// answer 400 VALIDATION_FAILED.
//
// The rule exists because the two halves of the platform must agree on which
// fields exist. The query surface addresses a field by a jsonPath whose
// segments are ASCII letters, digits, "_" and "-", and offers no escape hatch:
// bracket-quoted access is rejected, and no evaluator in the stack resolves it.
// Recording a field outside that charset would therefore guarantee data that
// can be stored and never queried — an answer that is silently wrong rather
// than unavailable. cyoda-go fails closed instead: the field is refused at the
// door, with a diagnostic naming the key to rename.
var ErrInvalidFieldName = errors.New("invalid field name")

// InvalidFieldNameError is ValidateFieldName's typed failure. Unwrap keeps
// errors.Is(err, ErrInvalidFieldName) true for every existing caller — the
// sentinel is still the classification signal handlers key on — while Path
// gives a caller that only holds the raw error (Validate's backstop, when a
// bad name bubbles up as a hard abort rather than a Change) enough to
// render a ValidationError without parsing the message string apart. Path
// is Admit's own path convention (leading dot, no "$" prefix): parent + "."
// + name.
type InvalidFieldNameError struct {
	Path   string
	Parent string
	Name   string
}

// maxFieldNameDiagnosticRunes bounds how much of the offending name and
// parent path this diagnostic echoes back to the caller. Both are
// caller-supplied and unbounded in principle — a pathological field name or a
// deeply-nested parent path would otherwise inflate a 400 VALIDATION_FAILED
// body (both strings appear in the message, each further widened by %q's
// escaping) to a multiple of the attacker-supplied size (security review L3).
const maxFieldNameDiagnosticRunes = 128

// truncateForDiagnostic caps s to maxFieldNameDiagnosticRunes runes with a
// trailing "…" marker, for inclusion in this error's client-facing message
// only — it does not touch InvalidFieldNameError's own Path/Parent/Name
// fields, which callers use programmatically and must stay exact.
func truncateForDiagnostic(s string) string {
	r := []rune(s)
	if len(r) <= maxFieldNameDiagnosticRunes {
		return s
	}
	return string(r[:maxFieldNameDiagnosticRunes]) + "…"
}

func (e *InvalidFieldNameError) Error() string {
	return fmt.Sprintf("%s: %q in object at %q — a field name must be addressable as a jsonPath segment: "+
		"ASCII letters, digits, %q and %q only, and not empty; rename the field",
		ErrInvalidFieldName, truncateForDiagnostic(e.Name), renderParentForMessage(truncateForDiagnostic(e.Parent)), "_", "-")
}

func (e *InvalidFieldNameError) Unwrap() error { return ErrInvalidFieldName }

// ValidateFieldName reports whether name is usable as a single jsonPath
// segment, i.e. whether a query could ever address the field. parent is the
// canonical path of the object that declares it, carried only so the
// diagnostic can point at the offending location.
//
// The check delegates to [IsSegmentName], the one definition of the segment
// charset the query-side path grammar is built from. A whole-segment check is
// exactly the rule a bare field name needs: it admits no subscript (a field
// name must denote one node) and no "." (that would spell two segments and
// name a field no lookup could distinguish from a nested one).
func ValidateFieldName(parent, name string) error {
	if IsSegmentName(name) {
		return nil
	}
	return &InvalidFieldNameError{Path: parent + "." + name, Parent: parent, Name: name}
}

// renderParentForMessage normalises parent onto the "$"-rooted spelling both
// doors' diagnostics use, without changing what callers pass in. Admit names
// the root "" and a nested object ".outer" — its Change.Path convention,
// which changeLevelError and other callers still render unprefixed — while
// Describe (and importer.Walk, which is Describe's only caller) already
// starts from "$". Normalising here, at the one place the string reaches the
// user, is what keeps the two doors' diagnostics identical
// (docs/cloud-parity/model-field-name-grammar.md) without threading a "$"
// prefix through Admit's own path convention.
func renderParentForMessage(parent string) string {
	if parent == "" {
		return "$"
	}
	if !strings.HasPrefix(parent, "$") {
		return "$" + parent
	}
	return parent
}
