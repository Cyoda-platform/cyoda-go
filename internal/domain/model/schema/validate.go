package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// MaxValidationDepth caps recursion in Validate to defend against stack
// exhaustion from deeply nested user-supplied documents. At roughly 8 bytes
// per nesting level a 10MB body could otherwise encode hundreds of thousands
// of levels and crash the goroutine. 256 is well above any realistic JSON
// nesting and well below the stack-blow threshold.
const MaxValidationDepth = 256

// depthExceededMessage is the message half of DepthExceededError's wire
// text, shared with Validate's rendering of it. Computed from
// MaxValidationDepth rather than duplicated as a literal, so the two can
// never drift out of sync if the cap ever changes.
var depthExceededMessage = fmt.Sprintf("validation depth exceeded (max %d)", MaxValidationDepth)

// ErrorKind classifies a ValidationError so handlers can branch on
// specific failure modes without matching error message text.
type ErrorKind int

const (
	// ErrKindGeneric covers validation failures that do not map to a
	// more specific kind (shape mismatches, malformed schema entries).
	ErrKindGeneric ErrorKind = iota

	// ErrKindUnknownElement fires when a data document carries a field
	// that the validating schema does not declare. In practice this is
	// the "stale schema" signal handlers use to decide whether to
	// refresh from authoritative storage and retry (see
	// internal/domain/entity/handler.go).
	ErrKindUnknownElement

	// ErrKindIncompatibleType fires when a leaf value's inferred DataType
	// is not assignable to any of the schema's declared DataTypes for
	// that path (e.g. submitting "abc" against an INTEGER field, or 13.111
	// against an INTEGER field that has not been widened by an extension).
	// Equivalent to Cloud's FoundIncompatibleTypeWithEntityModelException;
	// surfaces the dictionary-aligned INCOMPATIBLE_TYPE error code at the
	// HTTP boundary.
	ErrKindIncompatibleType
)

// ValidationError describes a single validation failure at a specific path.
//
// ExpectedTypes and ActualType are only populated when Kind is
// ErrKindIncompatibleType — they carry the structured context the entity
// handler renders into RFC 9457 problem-detail Props (`expectedType`,
// `actualType`).
type ValidationError struct {
	Path          string
	Message       string
	Kind          ErrorKind
	ExpectedTypes []DataType
	ActualType    DataType
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// HasUnknownSchemaElement reports whether any of the validation
// errors in errs classify as ErrKindUnknownElement — the stale-schema
// signal. Handlers use this to decide whether to force a cache
// refresh and re-validate once before surfacing a 4xx to the client.
func HasUnknownSchemaElement(errs []ValidationError) bool {
	for _, e := range errs {
		if e.Kind == ErrKindUnknownElement {
			return true
		}
	}
	return false
}

// FirstIncompatibleType returns a pointer to the first ErrKindIncompatibleType
// entry in errs, or nil if none is present. Handlers use this to surface the
// dictionary-aligned INCOMPATIBLE_TYPE response with structured Props
// (path, expectedType, actualType) instead of the generic BAD_REQUEST.
func FirstIncompatibleType(errs []ValidationError) *ValidationError {
	for i := range errs {
		if errs[i].Kind == ErrKindIncompatibleType {
			return &errs[i]
		}
	}
	return nil
}

// Validate checks whether data conforms to the model without extending it.
// It returns a slice of validation errors; an empty slice means the data is
// valid.
//
// This asks Admit the same question Extend asks and renders every change as
// a refusal, so strict validation and the change-level gate cannot disagree
// about what a field accepts.
func Validate(model *ModelNode, data any) []ValidationError {
	_, changes, err := Admit(model, data)
	if err != nil {
		if ve, ok := depthExceededValidationError(err); ok {
			return []ValidationError{ve}
		}
		return []ValidationError{{Message: err.Error(), Kind: ErrKindGeneric}}
	}
	errs := make([]ValidationError, 0, len(changes))
	for _, c := range changes {
		errs = append(errs, validationErrorFor(c))
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// depthExceededValidationError recognises Admit's *DepthExceededError and
// renders it into the ValidationError shape this package has always used
// for the failure: Path and Message as separate fields, not the path folded
// into the message text. errors.As, not string matching — a generic
// {Message: err.Error()} would have doubled the path into the
// ValidationError.Error() rendering (which already prefixes Path), dropped
// the machine-readable Path field, and silently degraded the moment
// DepthExceededError's wording drifted from whatever a string match
// expected.
func depthExceededValidationError(err error) (ValidationError, bool) {
	var de *DepthExceededError
	if !errors.As(err, &de) {
		return ValidationError{}, false
	}
	return ValidationError{
		Path:    wirePath(de.Path),
		Message: depthExceededMessage,
		Kind:    ErrKindGeneric,
	}, true
}

// wirePath adapts Admit's path convention onto the wire shape Validate's
// ValidationError.Path has always used.
//
// Admit (and Extend, which shares its traversal) name every path from an
// implicit root: a top-level field is ".price", a nested one ".outer.b".
// Validate's ValidationError.Path predates Admit and has always been bare —
// "price", "outer.b" — because entity handler responses echo it verbatim as
// the `fieldPath` problem-detail property (see
// TestCreateEntity_IncompatibleType_ReturnsSpecificCode in
// internal/domain/entity/handler_test.go, which asserts fieldPath == "price"
// for a root-level field, not ".price"). Extend's own error strings keep
// Admit's leading dot unchanged — the change-level gate has always named a
// root-level path that way — so this trim applies only on the Validate side.
func wirePath(p string) string {
	return strings.TrimPrefix(p, ".")
}

func validationErrorFor(c Change) ValidationError {
	switch c.Reason {
	case ReasonLeafType:
		expected := make([]DataType, len(c.Declared))
		copy(expected, c.Declared)
		return ValidationError{
			Path:          wirePath(c.Path),
			Message:       fmt.Sprintf("value of type %s is not compatible with %v", c.Observed, c.Declared),
			Kind:          ErrKindIncompatibleType,
			ExpectedTypes: expected,
			ActualType:    c.Observed,
		}
	case ReasonNewField:
		return ValidationError{
			Path:    wirePath(c.Path),
			Message: "unexpected field not present in model",
			Kind:    ErrKindUnknownElement,
		}
	case ReasonNewKind:
		return validationErrorForNewKind(c)
	case ReasonArrayWidth:
		// The array's element type is fine — the document simply carries
		// more elements than the model has ever seen at this path. Naming
		// both counts is the accurate statement; the generic "expected X,
		// got Y" template would have described the array's own JSON kind
		// (which the document DID supply correctly, same defect as
		// ReasonArrayElement's old default-case rendering) instead of the
		// actual mismatch, which is a count, not a kind.
		n := 0
		if arr, ok := c.Value.([]any); ok {
			n = len(arr)
		}
		return ValidationError{
			Path:    wirePath(c.Path),
			Message: fmt.Sprintf("array wider than observed: %d elements, model has seen at most %d", n, c.ObservedWidth),
			Kind:    ErrKindGeneric,
		}
	case ReasonArrayElement:
		// The array's element was never observed at all — there is no
		// declared element type to compare the document's content against,
		// so this does not fit the "expected X, got Y" template the other
		// reasons use (that would either misname a heterogeneous array's
		// content with a single Y, or describe the array's own JSON kind,
		// which the document DID supply correctly — the old rendering's
		// "got null" was simply wrong for a document holding a real array).
		// Name the actual situation instead: admitting content here is
		// schema learning, and c.Value (the array itself) confirms what the
		// document supplied rather than what the model failed to find.
		return ValidationError{
			Path:    wirePath(c.Path),
			Message: "array element type has never been observed; the document supplies " + JSONKindName(c.Value),
			Kind:    ErrKindGeneric,
		}
	default:
		return ValidationError{
			Path:    wirePath(c.Path),
			Message: "expected " + c.DeclaredKinds + ", got " + JSONKindName(c.Value),
			Kind:    ErrKindGeneric,
		}
	}
}

// validationErrorForNewKind renders ReasonNewKind — a value whose JSON kind
// the node does not declare at all — matching the wire wording this package
// has always used for the identical cases.
//
// A scalar value against a node with no object or array branch is exactly
// the "kind IS scalar, so the complaint is the TYPE" case, rendered
// identically to ReasonLeafType (ErrKindIncompatibleType). A container value
// against such a node gets "expected scalar, got object/array" — pinned
// verbatim by internal/e2e/model_kind_enforcement_test.go and
// internal/grpc/model_kind_enforcement_test.go. Anything else (a node that
// already declares some kind, so a genuinely new branch is being added) gets
// the generic "expected <kinds>, got <kind>".
func validationErrorForNewKind(c Change) ValidationError {
	declaresContainer := strings.Contains(c.DeclaredKinds, "object") || strings.Contains(c.DeclaredKinds, "array")

	switch c.Value.(type) {
	case json.Number, string, bool:
		if !declaresContainer {
			expected := make([]DataType, len(c.Declared))
			copy(expected, c.Declared)
			return ValidationError{
				Path:          wirePath(c.Path),
				Message:       fmt.Sprintf("value of type %s is not compatible with %v", c.Observed, c.Declared),
				Kind:          ErrKindIncompatibleType,
				ExpectedTypes: expected,
				ActualType:    c.Observed,
			}
		}
	case map[string]any, []any:
		if !declaresContainer {
			return ValidationError{
				Path:    wirePath(c.Path),
				Message: "expected scalar, got " + JSONKindName(c.Value),
				Kind:    ErrKindGeneric,
			}
		}
	}
	return ValidationError{
		Path:    wirePath(c.Path),
		Message: "expected " + c.DeclaredKinds + ", got " + JSONKindName(c.Value),
		Kind:    ErrKindGeneric,
	}
}

// InferDataType maps a Go value (typically from JSON decoding with
// UseNumber) to a DataType using the same classifier the walker uses.
// This ensures validation sees the same classification as schema
// inference.
func InferDataType(v any) DataType {
	return inferDataType(v)
}

// inferDataType is the internal implementation of InferDataType.
func inferDataType(v any) DataType {
	switch n := v.(type) {
	case bool:
		return Boolean
	case json.Number:
		d, err := ParseDecimal(string(n))
		if err != nil {
			// Malformed — conservatively say String (validation will fail).
			return String
		}
		stripped := d.StripTrailingZeros()
		if stripped.Scale() <= 0 {
			var bigVal *big.Int
			if stripped.Scale() == 0 {
				bigVal = stripped.Unscaled()
			} else {
				// Guard against DoS: a huge negative scale (e.g. 1e1_000_000_000)
				// would make Exp(10, -scale, nil) materialise a billion-digit big.Int.
				// Compute the approximate decimal digit count without expansion:
				//   digits = (significant digits in coefficient) + (-scale)
				// Int128 max ≈ 1.7×10^38 has 39 decimal digits; any integer needing
				// ≥ 40 digits to express is definitively UnboundInteger — skip Exp.
				const int128MaxDigits = 39
				digits := stripped.Precision() + int(-int64(stripped.Scale()))
				if digits > int128MaxDigits {
					return UnboundInteger
				}
				factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-stripped.Scale())), nil)
				bigVal = new(big.Int).Mul(stripped.Unscaled(), factor)
			}
			return ClassifyInteger(bigVal)
		}
		return ClassifyDecimal(stripped)
	case string:
		// Content-sniff ISO-8601 strings into their most specific temporal
		// subtype so a date-shaped value is classified (and later compared)
		// chronologically rather than lexically. Matches the search leaf
		// kernel's classification of stored temporal values exactly, so
		// discovery, validation, and evaluation all agree on the subtype.
		// A non-temporal string stays String (unchanged).
		if dt, ok := ClassifyTemporalString(n); ok {
			return dt
		}
		return String
	case nil:
		return Null
	default:
		// No float64/int/int64 fallbacks. Callers must use json.UseNumber.
		// If something leaks through, map to String so validation fails noisily.
		return String
	}
}

// JSONKindName names a decoded value's JSON kind in the wire vocabulary, so a
// rejection tells the caller what they sent in the terms their document is
// written in rather than in Go's type names.
func JSONKindName(data any) string {
	switch data.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		// Unreachable for json.Decoder output with UseNumber; naming the kind
		// generically keeps a leaked type out of the response.
		return "value"
	}
}

// declaredKindNames names the kinds a container node declares, so a rejection
// tells the caller what the field does accept rather than only what it does not.
func declaredKindNames(node *ModelNode) string {
	names := make([]string, 0, len(node.Kinds()))
	if node.Object() != nil {
		names = append(names, "object")
	}
	if node.Array() != nil {
		names = append(names, "array")
	}
	if node.Scalar() != nil {
		names = append(names, "scalar")
	}
	if len(names) == 0 {
		return "no value"
	}
	return strings.Join(names, " or ")
}
