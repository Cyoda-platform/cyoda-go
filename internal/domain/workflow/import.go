package workflow

import "github.com/cyoda-platform/cyoda-go/internal/common"

// applyImportMode merges incoming workflows with existing ones based on the import mode.
func applyImportMode(existing, incoming []common.WorkflowDefinition, mode string) []common.WorkflowDefinition {
	switch mode {
	case "REPLACE":
		return incoming

	case "ACTIVATE":
		// Build incoming name set.
		incomingNames := make(map[string]bool, len(incoming))
		for _, wf := range incoming {
			incomingNames[wf.Name] = true
		}

		// Deactivate existing workflows not in the import.
		var result []common.WorkflowDefinition
		for _, wf := range existing {
			if !incomingNames[wf.Name] {
				wf.Active = false
				result = append(result, wf)
			}
		}
		result = append(result, incoming...)
		return result

	default: // MERGE (default)
		// Build name→workflow map from existing.
		merged := make(map[string]common.WorkflowDefinition, len(existing)+len(incoming))
		order := make([]string, 0, len(existing)+len(incoming))
		for _, wf := range existing {
			merged[wf.Name] = wf
			order = append(order, wf.Name)
		}
		// Overlay incoming by name.
		for _, wf := range incoming {
			if _, exists := merged[wf.Name]; !exists {
				order = append(order, wf.Name)
			}
			merged[wf.Name] = wf
		}
		result := make([]common.WorkflowDefinition, 0, len(order))
		for _, name := range order {
			result = append(result, merged[name])
		}
		return result
	}
}
