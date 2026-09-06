package schema

import (
	"encoding/json"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// ChangeReason names why the stored model did not already admit a value.
type ChangeReason int

const (
	// ReasonLeafType — a leaf declares scalar types, none of which admits
	// this value.
	ReasonLeafType ChangeReason = iota
	// ReasonNewField — an object does not declare this field.
	ReasonNewField
	// ReasonNewKind — a path does not declare the value's JSON kind.
	ReasonNewKind
	// ReasonArrayWidth — an array is wider than any observed so far.
	ReasonArrayWidth
	// ReasonArrayElement — an array observed without content learns its
	// element type.
	ReasonArrayElement
	// ReasonNullable — a node declaring no scalar is observed as null.
	ReasonNullable
)

// Change is one observation the stored model does not admit, together with
// what admitting it would cost. Extend refuses the write when any change
// exceeds the configured level; Validate renders every change as a
// ValidationError.
type Change struct {
	Path     string
	Reason   ChangeReason
	Required spi.ChangeLevel
	Observed DataType   // ReasonLeafType, and ReasonNewKind for a scalar value; zero otherwise
	Declared []DataType // ReasonLeafType only; what the leaf declares
	// DeclaredKinds names the kinds the node declared when the change was
	// recorded — "object", "array", "scalar", joined with " or ", or
	// "no value" — in the wording Validate has always used. Recorded here so
	// error rendering never has to reach back to the node.
	DeclaredKinds string
	Value         any // the value that forced the change, for error rendering
}

// Admit walks data against model, one value at a time, and reports what the
// model would have to become to hold it.
//
// This is the single traversal the write path runs. The previous design
// converted the document into a throwaway model first, which discarded every
// value before any decision was made and fused array elements into one
// description — so a value-aware rule could not be evaluated at all, and
// [2147483648, "hello"] could not be judged element by element.
//
// The overlay is SPARSE: it describes only the leaves and branches that must
// change, so Merge(model, overlay) moves exactly the paths the traversal
// objected to. Merging a full walk of the document instead widened unrelated
// fields — a leaf's declared types depended on what else the document
// happened to carry.
//
// A nil overlay with no changes means the model already admits the document.
func Admit(model *ModelNode, data any) (*ModelNode, []Change, error) {
	a := &admitter{}
	overlay, err := a.node(model, data, "", 0, spi.ChangeLevelType)
	if err != nil {
		return nil, nil, err
	}
	return overlay, a.changes, nil
}

type admitter struct {
	changes []Change
}

func (a *admitter) record(c Change) { a.changes = append(a.changes, c) }

// node admits one value against one model node.
//
// scalarLevel is the level a scalar-shaped change costs at this position:
// ChangeLevelType normally, ChangeLevelArrayElements directly on an array's
// element. It is preserved through nested array levels and reset when
// descending into an object's children, which is exactly where the element
// rules stop applying.
func (a *admitter) node(model *ModelNode, data any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	if depth >= MaxValidationDepth {
		return nil, fmt.Errorf("%s: validation depth exceeded (max %d)", displayPath(path), MaxValidationDepth)
	}

	switch v := data.(type) {
	case nil:
		return a.null(model, path, scalarLevel), nil
	case map[string]any:
		if model.Object() == nil {
			return a.wrongKind(model, data, path, scalarLevel), nil
		}
		return a.object(model, v, path, depth, scalarLevel)
	case []any:
		if model.Array() == nil {
			return a.wrongKind(model, data, path, scalarLevel), nil
		}
		return a.array(model, v, path, depth, scalarLevel)
	case float64:
		return nil, fmt.Errorf("%s: received float64 value; callers must use json.UseNumber() decoding", displayPath(path))
	case json.Number, string, bool:
		return a.scalar(model, data, path, scalarLevel)
	default:
		return nil, fmt.Errorf("%s: unsupported type: %T", displayPath(path), data)
	}
}

// scalar is §4's rule: the value's JSON kind must match a declared type's
// kind, and that type must admit the value.
func (a *admitter) scalar(model *ModelNode, data any, path string, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	observed := inferDataType(data)

	if s := model.Scalar(); s != nil {
		if holdsScalar(s.Types(), data) {
			return nil, nil
		}
		// The scalar kind IS declared here — the value's kind is not the
		// complaint, its type is.
		a.record(Change{
			Path: path, Reason: ReasonLeafType, Required: scalarLevel,
			Observed: observed, Declared: s.Types(), DeclaredKinds: declaredKindNames(model), Value: data,
		})
		return NewLeafNode(observed), nil
	}

	// The node declares no scalar at all. Establishing the first kinds on a
	// node that declares nothing is the nullable-marker promotion, which keeps
	// the level it has always had; adding a scalar beside a declared container
	// is a new branch, which is more fundamental than a new field.
	required := spi.ChangeLevelStructural
	if len(model.Kinds()) == 0 {
		required = scalarLevel
	}
	a.record(Change{
		Path: path, Reason: ReasonNewKind, Required: required,
		Observed: observed, Declared: model.DeclaredTypes(), DeclaredKinds: declaredKindNames(model), Value: data,
	})
	return NewLeafNode(observed), nil
}

// holdsScalar answers §4's per-kind table for one value against one declared
// set. A JSON number is not a string and a JSON string is not a number, so
// STRING does not become a universal sink.
func holdsScalar(declared []DataType, data any) bool {
	switch v := data.(type) {
	case json.Number:
		dec, err := ParseDecimal(string(v))
		if err != nil {
			return false
		}
		for _, dt := range declared {
			if AdmitsNumeric(dt, dec) {
				return true
			}
		}
		return false

	case string:
		// Every JSON string is a string. A temporal type additionally admits
		// a string whose classification is EXACTLY that type: a
		// LOCAL_DATE_TIME parser accepts an offset-bearing timestamp by
		// discarding the offset, and those denote different instants, so the
		// value would be stored unfindable.
		classified, isTemporal := ClassifyTemporalString(v)
		for _, dt := range declared {
			if dt == String {
				return true
			}
			if isTemporal && dt == classified {
				return true
			}
		}
		return false

	case bool:
		for _, dt := range declared {
			if dt == Boolean {
				return true
			}
		}
		return false
	}
	return false
}

// null is the nullable marker. A node that already declares a scalar admits
// null and records nothing; a node that declares none charges the promotion.
func (a *admitter) null(model *ModelNode, path string, scalarLevel spi.ChangeLevel) *ModelNode {
	if model.Nullable() || model.Scalar() != nil {
		return nil
	}
	a.record(Change{
		Path: path, Reason: ReasonNullable, Required: scalarLevel,
		Observed: Null, Declared: model.DeclaredTypes(), DeclaredKinds: declaredKindNames(model),
	})
	overlay := NewLeafNode(Null)
	overlay.SetNullable()
	return overlay
}

// wrongKind records a path gaining a kind it does not declare. A node that
// declares nothing at all is the nullable-marker promotion instead.
func (a *admitter) wrongKind(model *ModelNode, data any, path string, scalarLevel spi.ChangeLevel) *ModelNode {
	required := spi.ChangeLevelStructural
	if len(model.Kinds()) == 0 {
		required = scalarLevel
	}
	var observed DataType
	switch data.(type) {
	case json.Number, string, bool:
		observed = inferDataType(data)
	}
	a.record(Change{
		Path: path, Reason: ReasonNewKind, Required: required,
		Observed: observed, Declared: model.DeclaredTypes(), DeclaredKinds: declaredKindNames(model), Value: data,
	})
	switch data.(type) {
	case map[string]any:
		return NewObjectNode()
	default:
		return NewArrayNode(NewLeafNode(Null))
	}
}

// object and array are stubbed here; Task 7 fills in the container traversal.
func (a *admitter) object(model *ModelNode, m map[string]any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	return nil, fmt.Errorf("admit: object traversal not yet implemented")
}

func (a *admitter) array(model *ModelNode, arr []any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	return nil, fmt.Errorf("admit: array traversal not yet implemented")
}
