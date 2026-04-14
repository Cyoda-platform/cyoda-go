package schema

import (
	"fmt"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// changeLevelRank maps each ChangeLevel to its position in the permission hierarchy.
// Higher rank means more permissive. Empty string maps to -1 (nothing allowed).
func changeLevelRank(level common.ChangeLevel) int {
	switch level {
	case common.ChangeLevelArrayLength:
		return 0
	case common.ChangeLevelArrayElements:
		return 1
	case common.ChangeLevelType:
		return 2
	case common.ChangeLevelStructural:
		return 3
	default:
		return -1
	}
}

// levelPermits returns true if the configured level permits the required level.
func levelPermits(configured, required common.ChangeLevel) bool {
	return changeLevelRank(configured) >= changeLevelRank(required)
}

// Extend merges incoming into existing, constrained by the given change level.
// If no changes are needed (incoming conforms to existing), the existing model is returned.
// If the incoming data requires a change that exceeds the permitted level, an error is returned.
func Extend(existing, incoming *ModelNode, level common.ChangeLevel) (*ModelNode, error) {
	changed, err := checkAndExtend(existing, incoming, level, "")
	if err != nil {
		return nil, err
	}
	if !changed {
		return existing, nil
	}
	return Merge(existing, incoming), nil
}

// checkAndExtend walks both trees recursively comparing nodes.
// It returns (true, nil) if changes are needed and permitted,
// (false, nil) if no changes are needed, or (false, error) if a change is forbidden.
func checkAndExtend(existing, incoming *ModelNode, level common.ChangeLevel, path string) (bool, error) {
	if existing == nil || incoming == nil {
		return false, nil
	}

	changed := false

	// Check children: new fields in incoming that don't exist in existing
	for name, inChild := range incoming.Children() {
		childPath := path + "." + name
		exChild := existing.Child(name)
		if exChild == nil {
			// New field — requires STRUCTURAL
			if !levelPermits(level, common.ChangeLevelStructural) {
				return false, fmt.Errorf("new field %q at %s requires STRUCTURAL level, but level is %q", name, childPath, level)
			}
			changed = true
			continue
		}

		// Both exist — recurse
		childChanged, err := checkAndExtend(exChild, inChild, level, childPath)
		if err != nil {
			return false, err
		}
		if childChanged {
			changed = true
		}
	}

	// Check leaf type widening: both are leaves with different types
	if existing.Kind() == KindLeaf && incoming.Kind() == KindLeaf {
		if !existing.Types().Equal(incoming.Types()) {
			if !levelPermits(level, common.ChangeLevelType) {
				return false, fmt.Errorf("type change at %s requires TYPE level, but level is %q", path, level)
			}
			changed = true
		}
	}

	// Check array-specific changes
	if existing.Kind() == KindArray && incoming.Kind() == KindArray {
		// Element type widening
		if existing.Element() != nil && incoming.Element() != nil {
			elemChanged, err := checkElementWidening(existing.Element(), incoming.Element(), level, path)
			if err != nil {
				return false, err
			}
			if elemChanged {
				changed = true
			}
		}

		// Array width change
		if existing.Info() != nil && incoming.Info() != nil {
			if incoming.Info().MaxWidth() > existing.Info().MaxWidth() {
				if !levelPermits(level, common.ChangeLevelArrayLength) {
					return false, fmt.Errorf("array width change at %s requires ARRAY_LENGTH level, but level is %q", path, level)
				}
				changed = true
			}
		}
	}

	return changed, nil
}

// checkElementWidening checks if array element types differ and whether the change is permitted.
func checkElementWidening(existingElem, incomingElem *ModelNode, level common.ChangeLevel, path string) (bool, error) {
	// For leaf elements, check type widening at the ARRAY_ELEMENTS level
	if existingElem.Kind() == KindLeaf && incomingElem.Kind() == KindLeaf {
		if !existingElem.Types().Equal(incomingElem.Types()) {
			if !levelPermits(level, common.ChangeLevelArrayElements) {
				return false, fmt.Errorf("array element type change at %s requires ARRAY_ELEMENTS level, but level is %q", path, level)
			}
			return true, nil
		}
	}

	// For object elements, recurse into children
	if existingElem.Kind() == KindObject && incomingElem.Kind() == KindObject {
		return checkAndExtend(existingElem, incomingElem, level, path+"[]")
	}

	return false, nil
}
