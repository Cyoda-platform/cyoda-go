package schema

import (
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// changeLevelRank maps each ChangeLevel to its position in the permission hierarchy.
// Higher rank means more permissive. Empty string maps to -1 (nothing allowed).
func changeLevelRank(level spi.ChangeLevel) int {
	switch level {
	case spi.ChangeLevelArrayLength:
		return 0
	case spi.ChangeLevelArrayElements:
		return 1
	case spi.ChangeLevelType:
		return 2
	case spi.ChangeLevelStructural:
		return 3
	default:
		return -1
	}
}

// levelPermits returns true if the configured level permits the required level.
func levelPermits(configured, required spi.ChangeLevel) bool {
	return changeLevelRank(configured) >= changeLevelRank(required)
}

// Extend admits data against existing, constrained by the given change level.
// When existing already holds every value, existing is returned unchanged.
// When a change is needed that exceeds the permitted level, an error names
// the path, the level the change costs and the level configured.
//
// The verdict and the resulting model come from one traversal, so they cannot
// diverge. They used to agree only by coincidence — the gate and TypeSet.Add
// happened to give the same answers — and that coincidence would not have
// survived a value-aware gate.
func Extend(existing *ModelNode, data any, level spi.ChangeLevel) (*ModelNode, error) {
	overlay, changes, err := Admit(existing, data)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return existing, nil
	}
	for _, c := range changes {
		if !levelPermits(level, c.Required) {
			return nil, changeLevelError(c, level)
		}
	}
	return Merge(existing, overlay), nil
}

// changeLevelError renders a refused change in the wording the API has always
// used. The message shape is asserted by the e2e suites; do not reword it.
func changeLevelError(c Change, level spi.ChangeLevel) error {
	switch c.Reason {
	case ReasonLeafType:
		return fmt.Errorf("type change at %s requires %s level, but level is %q",
			displayPath(c.Path), c.Required, level)
	case ReasonNewField:
		return fmt.Errorf("new field %q at %s requires STRUCTURAL level, but level is %q",
			lastSegment(c.Path), displayPath(c.Path), level)
	case ReasonNewKind:
		return fmt.Errorf("new %s branch at %s requires %s level, but level is %q",
			kindNameFor(c.Value), displayPath(c.Path), c.Required, level)
	case ReasonArrayWidth:
		return fmt.Errorf("array width change at %s requires %s level, but level is %q",
			displayPath(c.Path), c.Required, level)
	case ReasonArrayElement:
		return fmt.Errorf("array element type at %s requires ARRAY_ELEMENTS level, but level is %q",
			displayPath(c.Path), level)
	case ReasonNullable:
		return fmt.Errorf("nullable marker at %s requires %s level, but level is %q",
			displayPath(c.Path), c.Required, level)
	}
	return fmt.Errorf("schema change at %s requires %s level, but level is %q",
		displayPath(c.Path), c.Required, level)
}

// lastSegment returns the final path component — the field name a
// ReasonNewField change was recorded against.
func lastSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i+1:]
		}
	}
	return path
}

// kindNameFor names a value's kind the way the change-level gate always has:
// the NodeKind spelling, so the message a client sees is unchanged.
func kindNameFor(v any) string {
	switch v.(type) {
	case map[string]any:
		return KindObject.String()
	case []any:
		return KindArray.String()
	default:
		return KindLeaf.String()
	}
}
