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
	Observed DataType // ReasonLeafType, and ReasonNewKind for a scalar value; zero otherwise
	// Declared is the node's declared types at the moment of the change:
	// what a leaf's scalar branch declares for ReasonLeafType, or
	// model.DeclaredTypes() — [NULL] for a nullable-only node, nil for one
	// declaring nothing — for ReasonNewKind and ReasonNullable. Task 8 needs
	// it in both cases to render ErrKindIncompatibleType exactly as the old
	// Validate did.
	Declared []DataType
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
			return a.wrongKind(model, data, path, depth, scalarLevel)
		}
		return a.object(model, v, path, depth, scalarLevel)
	case []any:
		if model.Array() == nil {
			return a.wrongKind(model, data, path, depth, scalarLevel)
		}
		return a.array(model, v, path, depth, scalarLevel)
	case float64:
		return nil, fmt.Errorf("%s: received float64 value; callers must use json.UseNumber() decoding", displayPath(path))
	case json.Number, string, bool:
		return a.scalar(model, data, path, depth, scalarLevel)
	default:
		return nil, fmt.Errorf("%s: unsupported type: %T", displayPath(path), data)
	}
}

// scalar is §4's rule: the value's JSON kind must match a declared type's
// kind, and that type must admit the value. A node with no scalar branch at
// all is the same "path gains a kind it does not declare" case a container
// value hits against a node lacking that branch — wrongKind is the single
// site of that policy.
func (a *admitter) scalar(model *ModelNode, data any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	s := model.Scalar()
	if s == nil {
		return a.wrongKind(model, data, path, depth, scalarLevel)
	}
	if holdsScalar(s.Types(), data) {
		return nil, nil
	}
	// The scalar kind IS declared here — the value's kind is not the
	// complaint, its type is.
	observed := inferDataType(data)
	a.record(Change{
		Path: path, Reason: ReasonLeafType, Required: scalarLevel,
		Observed: observed, Declared: s.Types(), DeclaredKinds: declaredKindNames(model), Value: data,
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

// wrongKind is the single site of "a path gains a kind it does not declare":
// a container against a node lacking that branch (called directly from
// node), and a scalar against a node with no scalar branch (called from
// scalar). Establishing the first kind on a node that declares none is the
// nullable-marker promotion, which keeps the level it has always had; adding
// a kind beside one already declared is a new branch, more fundamental than
// a new field.
//
// For a container value the overlay is derived with describeAt rather than
// returned as an empty container: an empty object or an array whose element
// is Null would merge to an overlay that admits nothing the document actually
// carried. describeAt continues at the SAME depth wrongKind itself received —
// wrongKind stands in for object/array at this exact position when the
// branch does not exist, and object/array are always called with the depth
// the enclosing node call received, not depth+1 — so the MaxValidationDepth
// guard node already ran for this position applies unchanged. describeAt can
// still fail one or more levels down: a deeply nested container can exceed
// MaxValidationDepth, or an object inside it can carry a field name the query
// grammar cannot address — so wrongKind reports that failure rather than
// swallowing it.
func (a *admitter) wrongKind(model *ModelNode, data any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
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
	case map[string]any, []any:
		return describeAt(data, path, depth)
	default:
		return NewLeafNode(observed), nil
	}
}

// object admits a JSON object against a node's object branch. Children of an
// object are ordinary positions again: an array's element rules do not reach
// through a nested object.
//
// scalarLevel is deliberately not read here: every child is re-entered at the
// fixed spi.ChangeLevelType below, which IS the reset the scalarLevel
// discipline requires on descent into an object (see extend.go's checkBranch,
// KindObject case, which does the same unconditional reset). Do not thread
// the parameter through in its place — that would restore whatever level was
// in force above the object, defeating the reset.
func (a *admitter) object(model *ModelNode, m map[string]any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	var overlay *ModelNode
	ensure := func() *ModelNode {
		if overlay == nil {
			overlay = NewObjectNode()
		}
		return overlay
	}

	obj := model.Object()
	for name, val := range m {
		childPath := path + "." + name
		child := obj.Child(name)

		if child == nil {
			// A field the model does not declare. Validate the key before it
			// can become a schema field — this is the one point both
			// field-set-establishing ingresses share.
			if err := ValidateFieldName(path, name); err != nil {
				return nil, err
			}
			a.record(Change{
				Path: childPath, Reason: ReasonNewField,
				Required: spi.ChangeLevelStructural, DeclaredKinds: declaredKindNames(model), Value: val,
			})
			derived, err := describeAt(val, childPath, depth+1)
			if err != nil {
				return nil, err
			}
			ensure().SetChild(name, derived)
			continue
		}

		childOverlay, err := a.node(child, val, childPath, depth+1, spi.ChangeLevelType)
		if err != nil {
			return nil, err
		}
		if childOverlay != nil {
			ensure().SetChild(name, childOverlay)
		}
	}
	// A field the model declares but the document omits is not a change: the
	// model describes known structure, not required fields.
	return overlay, nil
}

// array admits a JSON array against a node's array branch, judging each
// element individually. The old walk fused every element into one description
// before anything was judged, so [2147483648, "hello"] became a single entry
// meaning "large integer or text" and the number had already been widened.
//
// scalarLevel is deliberately not read here: an already-typed element always
// recurses at the fixed spi.ChangeLevelArrayElements below, and a
// newly-learned element is charged that same level directly — both are the
// re-assertion extend.go's checkBranch (KindArray case) always did, not a
// value threaded down from above. Do not thread the parameter through in
// its place.
func (a *admitter) array(model *ModelNode, arr []any, path string, depth int, scalarLevel spi.ChangeLevel) (*ModelNode, error) {
	exArr := model.Array()
	elemPath := path + "[]"

	var elemOverlay *ModelNode
	widened := false

	if exArr.Element() == nil {
		// The array was observed, but never with content, so it declares no
		// element type. Learning one is the same promotion a node declaring
		// no kind undergoes, at the level an array element's changes cost —
		// and an EMPTY array still triggers it: importer.Walk has always
		// represented [] as an array whose element is Null (walkArray's
		// NewLeafNode(Null)), so an incoming array from that walk always has
		// a non-nil element, empty or not. checkBranch charged
		// ARRAY_ELEMENTS whenever the existing element was nil and the
		// incoming one was not, with no separate case for an empty incoming
		// array — matching that here means charging it regardless of len(arr).
		a.record(Change{
			Path: elemPath, Reason: ReasonArrayElement,
			Required: spi.ChangeLevelArrayElements, DeclaredKinds: declaredKindNames(model),
		})
		if len(arr) == 0 {
			elemOverlay = NewLeafNode(Null)
		} else {
			for _, item := range arr {
				derived, err := describeAt(item, elemPath, depth+1)
				if err != nil {
					return nil, err
				}
				if elemOverlay == nil {
					elemOverlay = derived
				} else {
					elemOverlay = Merge(elemOverlay, derived)
				}
			}
		}
	} else {
		// ARRAY_ELEMENTS applies at an array's element and keeps applying
		// through further array levels.
		for _, item := range arr {
			itemOverlay, err := a.node(exArr.Element(), item, elemPath, depth+1, spi.ChangeLevelArrayElements)
			if err != nil {
				return nil, err
			}
			if itemOverlay == nil {
				continue
			}
			if elemOverlay == nil {
				elemOverlay = itemOverlay
			} else {
				elemOverlay = Merge(elemOverlay, itemOverlay)
			}
		}
	}

	if len(arr) > exArr.MaxWidth() {
		a.record(Change{
			Path: path, Reason: ReasonArrayWidth,
			Required: spi.ChangeLevelArrayLength, DeclaredKinds: declaredKindNames(model),
		})
		widened = true
	}

	if elemOverlay == nil && !widened {
		return nil, nil
	}
	overlay := NewArrayNode(elemOverlay)
	if widened {
		overlay.ObserveArrayWidth(len(arr))
	}
	return overlay, nil
}

// Describe derives the model fragment a value implies, with no stored model
// to compare against — the shape a brand-new field or element takes. It is
// the depth-0 entry point into describeAt; see that doc comment for the
// mechanics.
func Describe(v any, path string) (*ModelNode, error) {
	return describeAt(v, path, 0)
}

// describeAt is Describe's recursive form, carrying the depth a brand-new
// subtree has reached so it stays bounded by the same MaxValidationDepth
// guard node enforces. Every value under a brand-new field or element is
// itself new, so object's new-field branch and array's element-learning
// branch both re-enter describeAt rather than node — and without threading
// depth through those re-entries, a document nested deeper than
// MaxValidationDepth under a single brand-new field would recurse
// unbounded instead of failing closed: node's own guard is never reached,
// because none of these calls go through node.
//
// It runs in a throwaway admitter whose recorded changes are discarded: only
// the overlay's content is wanted here, never a verdict.
//
// A container value dispatches straight into object/array against a node
// that already declares the container's kind but has no children, rather
// than through node against emptyNode(): emptyNode declares no branch at
// all, so node would route a container back through wrongKind, which for a
// container value calls describeAt — looping forever.
//
// object's empty-object case ({}) still needs an explicit non-nil fallback:
// object returns nil for "no change" when its input map has no keys, which
// is indistinguishable at that call site from "nothing new here" — but
// importer.Walk has always recorded an empty object as declaring KindObject
// with no children (walkObject's bare NewObjectNode()), and describeAt must
// produce the same shape or a field whose only observed value is {} would
// vanish from the derived model instead of declaring an (empty) branch.
// array needs no equivalent fallback: it is seeded with a nil element here,
// so it always takes the "array declares no element yet" branch below, which
// unconditionally charges and sets an element — including NewLeafNode(Null)
// for an empty array, matching walkArray's NewArrayNode(NewLeafNode(Null))
// — so array never returns nil when called from here.
func describeAt(v any, path string, depth int) (*ModelNode, error) {
	if depth >= MaxValidationDepth {
		return nil, fmt.Errorf("%s: validation depth exceeded (max %d)", displayPath(path), MaxValidationDepth)
	}
	a := &admitter{}
	switch val := v.(type) {
	case map[string]any:
		overlay, err := a.object(NewObjectNode(), val, path, depth, spi.ChangeLevelType)
		if err != nil {
			return nil, err
		}
		if overlay == nil {
			overlay = NewObjectNode()
		}
		return overlay, nil
	case []any:
		return a.array(NewArrayNode(nil), val, path, depth, spi.ChangeLevelType)
	default:
		return a.node(emptyNode(), v, path, depth, spi.ChangeLevelType)
	}
}

// emptyNode is a node declaring nothing: every value is a change against it.
func emptyNode() *ModelNode { return spi.NewEmptyNode() }
