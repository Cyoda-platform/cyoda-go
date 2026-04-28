package schema_test

import (
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// buildNestedModel constructs an object model schema nested `depth` levels
// deep along the field name "a". Each level is an object containing a single
// child "a" whose type matches the next level.
func buildNestedModel(depth int) *schema.ModelNode {
	if depth <= 0 {
		return schema.NewLeafNode(schema.String)
	}
	node := schema.NewObjectNode()
	node.SetChild("a", buildNestedModel(depth-1))
	return node
}

// buildNestedData constructs a `{"a": {"a": ...}}` document nested `depth`
// levels deep, terminated with a string leaf.
func buildNestedData(depth int) any {
	if depth <= 0 {
		return "leaf"
	}
	return map[string]any{"a": buildNestedData(depth - 1)}
}

// TestValidateRejectsExcessivelyDeepDocument confirms that the validator
// terminates with a depth-exceeded error rather than recursing without bound
// when the data document nests deeper than schema.MaxValidationDepth. This is
// the H4 stack-exhaustion fix: at ~8 bytes/level a 10MB body could encode
// hundreds of thousands of levels and blow the goroutine stack. The depth
// guard caps recursion well below the stack-blow threshold.
func TestValidateRejectsExcessivelyDeepDocument(t *testing.T) {
	const tooDeep = 1000
	if tooDeep <= schema.MaxValidationDepth {
		t.Fatalf("test invariant: tooDeep (%d) must exceed MaxValidationDepth (%d)", tooDeep, schema.MaxValidationDepth)
	}

	// Model only needs to be deep enough for validation to descend into the
	// document — the data drives the recursion, not the schema. Using a
	// matching-depth model exercises the same code path the schema validator
	// uses for entity create.
	model := buildNestedModel(tooDeep)
	data := buildNestedData(tooDeep)

	// Must not panic; a stack-overflow surfaces as a goroutine crash, not a
	// panic recoverable here, so the test relying on a clean error return is
	// itself the regression guard.
	errs := schema.Validate(model, data)
	if len(errs) == 0 {
		t.Fatalf("expected validation error for excessively deep document, got none")
	}

	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "validation depth exceeded") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one error mentioning 'validation depth exceeded', got: %v", errs)
	}
}

// TestValidateAcceptsDocumentAtDepthBoundary confirms a document nested
// exactly to MaxValidationDepth-1 (the deepest legal level) still validates.
// This guards against an off-by-one tightening of the cap.
func TestValidateAcceptsDocumentAtDepthBoundary(t *testing.T) {
	depth := schema.MaxValidationDepth - 1
	model := buildNestedModel(depth)
	data := buildNestedData(depth)

	errs := schema.Validate(model, data)
	if len(errs) != 0 {
		t.Errorf("expected document at depth %d (= MaxValidationDepth-1) to validate cleanly, got: %v", depth, errs)
	}
}
