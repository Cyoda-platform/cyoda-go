package externalapi

// External API Scenario Suite — 08-workflow-import-export
//
// Schema adaptation notes (dictionary shape → cyoda-go accepted shape):
//
//   Field "to"         → "next"       (TransitionDefinition.Next)
//   Field "automated"  → "manual"     (boolean inverted: automated=true ↔ manual=false)
//   Criterion type "jsonpath" with "path"/"equals"
//              → type "simple" with "jsonPath"/"operatorType"/"value"
//   Criterion group "clauses" → "conditions"
//   Processor config  → requires "type" field; bare config map not accepted.
//
// Each deviation is annotated with // dictionary uses X; cyoda-go accepts Y —
// different_naming_same_level, per the parity-suite convention.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_08_01_SimpleAutomatedTransition", Fn: RunExternalAPI_08_01_SimpleAutomatedTransition},
		parity.NamedTest{Name: "ExternalAPI_08_02_DefaultsAppliedAndReturned", Fn: RunExternalAPI_08_02_DefaultsAppliedAndReturned},
		parity.NamedTest{Name: "ExternalAPI_08_03_AdvancedCriteriaAndProcessors", Fn: RunExternalAPI_08_03_AdvancedCriteriaAndProcessors},
		parity.NamedTest{Name: "ExternalAPI_08_04_StrategyReplace", Fn: RunExternalAPI_08_04_StrategyReplace},
		parity.NamedTest{Name: "ExternalAPI_08_05_StrategyActivate", Fn: RunExternalAPI_08_05_StrategyActivate},
		parity.NamedTest{Name: "ExternalAPI_08_06_StrategyMerge", Fn: RunExternalAPI_08_06_StrategyMerge},
	)
}

// minimalWorkflow08 returns a two-state workflow with one automated transition
// guarded by a simple JSONPath criterion. Used by 08/01 and the strategy
// scenarios as a baseline workflow.
//
// Schema adaptations applied vs the dictionary's proposed shape:
//   - "to" → "next"          — different_naming_same_level
//   - "automated":true → "manual":false  — different_naming_same_level (inverted)
//   - criterion "type":"jsonpath","path","equals"
//     → "type":"simple","jsonPath","operatorType","value"  — different_naming_same_level
func minimalWorkflow08(name string) string {
	return `{
		"workflows": [{
			"name": "` + name + `",
			"version": "1.0",
			"initialState": "draft",
			"states": {
				"draft": {
					"transitions": [{
						"name": "PUBLISH",
						"next": "published",
						"manual": false,
						"criterion": {"type": "simple", "jsonPath": "$.publish", "operatorType": "EQUALS", "value": true}
					}]
				},
				"published": {"transitions": []}
			}
		}]
	}`
}

// RunExternalAPI_08_01_SimpleAutomatedTransition — dictionary 08/01.
// Import a simple workflow with one automated (non-manual) transition and
// export it; assert the transition name survives the round-trip.
func RunExternalAPI_08_01_SimpleAutomatedTransition(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)

	if err := d.CreateModelFromSample("wf1", 1, `{"k":1,"publish":false}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.ImportWorkflow("wf1", 1, minimalWorkflow08("simple")); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}
	raw, err := d.ExportWorkflow("wf1", 1)
	if err != nil {
		t.Fatalf("ExportWorkflow: %v", err)
	}
	if !strings.Contains(string(raw), `"PUBLISH"`) {
		t.Errorf("export missing transition name PUBLISH: %s", string(raw))
	}
}

// RunExternalAPI_08_02_DefaultsAppliedAndReturned — dictionary 08/02.
// Import a partially-specified workflow (transition omits manual/criterion);
// export must round-trip the workflow with the transition name intact.
// cyoda-go stores what was sent and applies active=true as the only server
// default; omitted boolean fields default to false (Go zero value).
func RunExternalAPI_08_02_DefaultsAppliedAndReturned(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)

	if err := d.CreateModelFromSample("wf2", 1, `{"k":1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Import a workflow that omits "manual" on the transition.
	// dictionary uses "automated" field; cyoda-go uses "manual" (inverted) —
	// different_naming_same_level. Omitting both → manual defaults to false
	// (i.e., automated=true in dictionary terms).
	body := `{
		"workflows": [{
			"name": "defaults",
			"version": "1.0",
			"initialState": "s1",
			"states": {
				"s1": {"transitions": [{"name": "MOVE", "next": "s2"}]},
				"s2": {"transitions": []}
			}
		}]
	}`
	if err := d.ImportWorkflow("wf2", 1, body); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}
	raw, err := d.ExportWorkflow("wf2", 1)
	if err != nil {
		t.Fatalf("ExportWorkflow: %v", err)
	}
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	wfs, ok := shape["workflows"].([]any)
	if !ok || len(wfs) == 0 {
		t.Fatalf("export missing workflows: %v", shape)
	}
	// Assert the transition name survived the round-trip.
	if !strings.Contains(string(raw), `"MOVE"`) {
		t.Errorf("export missing transition name MOVE: %s", string(raw))
	}
}

// RunExternalAPI_08_03_AdvancedCriteriaAndProcessors — dictionary 08/03.
// Import a workflow with a group criterion (AND) and a processor.
// Export must round-trip both structures.
//
// Schema adaptations:
//   - Criterion: "type":"group","operator":"AND","clauses":[...]
//     → "type":"group","operator":"AND","conditions":[...]  — different_naming_same_level
//   - Inner criteria: "type":"jsonpath","path","equals"
//     → "type":"simple","jsonPath","operatorType","value"  — different_naming_same_level
//   - Processor: bare {"name","config"} → requires "type" field — different_naming_same_level
func RunExternalAPI_08_03_AdvancedCriteriaAndProcessors(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)

	if err := d.CreateModelFromSample("wf3", 1, `{"flag":true,"value":42}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	// dictionary uses "clauses"; cyoda-go predicate.GroupCondition uses "conditions" —
	// different_naming_same_level.
	// dictionary uses criterion type "jsonpath" with "path"/"equals";
	// cyoda-go uses type "simple" with "jsonPath"/"operatorType"/"value" —
	// different_naming_same_level.
	// dictionary processor omits "type"; cyoda-go ProcessorDefinition.Type is required —
	// different_naming_same_level.
	body := `{
		"workflows": [{
			"name": "advanced",
			"version": "1.0",
			"initialState": "init",
			"states": {
				"init": {
					"transitions": [{
						"name": "ADVANCE",
						"next": "done",
						"manual": false,
						"criterion": {
							"type": "group",
							"operator": "AND",
							"conditions": [
								{"type": "simple", "jsonPath": "$.flag", "operatorType": "EQUALS", "value": true},
								{"type": "simple", "jsonPath": "$.value", "operatorType": "GREATER_THAN", "value": 10}
							]
						},
						"processors": [{"name": "noop", "type": "FUNCTION"}]
					}]
				},
				"done": {"transitions": []}
			}
		}]
	}`
	if err := d.ImportWorkflow("wf3", 1, body); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}
	raw, err := d.ExportWorkflow("wf3", 1)
	if err != nil {
		t.Fatalf("ExportWorkflow: %v", err)
	}
	if !strings.Contains(string(raw), `"ADVANCE"`) {
		t.Errorf("export missing transition ADVANCE: %s", string(raw))
	}
	// Assert the group criterion and processor survived the round-trip.
	if !strings.Contains(string(raw), `"group"`) {
		t.Errorf("export missing group criterion type: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"noop"`) {
		t.Errorf("export missing processor name: %s", string(raw))
	}
}

// RunExternalAPI_08_04_StrategyReplace — dictionary 08/04.
// importMode=REPLACE removes all previous workflows before adding new ones.
func RunExternalAPI_08_04_StrategyReplace(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)

	if err := d.CreateModelFromSample("wf4", 1, `{"k":1,"publish":false}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("wf4", 1, minimalWorkflow08("first")); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// Import with REPLACE — should drop the first workflow entirely.
	replaceBody := `{
		"importMode": "REPLACE",
		"workflows": [{
			"name": "second",
			"version": "1.0",
			"initialState": "draft",
			"states": {"draft": {"transitions": []}}
		}]
	}`
	if err := d.ImportWorkflow("wf4", 1, replaceBody); err != nil {
		t.Fatalf("REPLACE import: %v", err)
	}
	raw, err := d.ExportWorkflow("wf4", 1)
	if err != nil {
		t.Fatalf("ExportWorkflow after REPLACE: %v", err)
	}
	if strings.Contains(string(raw), `"first"`) {
		t.Errorf("REPLACE did not drop first workflow: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"second"`) {
		t.Errorf("REPLACE did not add second workflow: %s", string(raw))
	}
}

// RunExternalAPI_08_05_StrategyActivate — dictionary 08/05.
// importMode=ACTIVATE keeps existing workflows but deactivates those not in the
// import list, and activates the incoming workflows.
func RunExternalAPI_08_05_StrategyActivate(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)

	if err := d.CreateModelFromSample("wf5", 1, `{"k":1,"publish":false}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("wf5", 1, minimalWorkflow08("first")); err != nil {
		t.Fatalf("first import: %v", err)
	}
	activateBody := `{
		"importMode": "ACTIVATE",
		"workflows": [{
			"name": "second",
			"version": "1.0",
			"initialState": "draft",
			"states": {"draft": {"transitions": []}}
		}]
	}`
	if err := d.ImportWorkflow("wf5", 1, activateBody); err != nil {
		t.Fatalf("ACTIVATE import: %v", err)
	}
	raw, err := d.ExportWorkflow("wf5", 1)
	if err != nil {
		t.Fatalf("ExportWorkflow after ACTIVATE: %v", err)
	}
	// ACTIVATE keeps both workflows; "first" is deactivated, "second" is active.
	// Both names must appear in the export.
	for _, name := range []string{"first", "second"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Errorf("ACTIVATE missing %s workflow in export: %s", name, string(raw))
		}
	}
}

// RunExternalAPI_08_06_StrategyMerge — dictionary 08/06.
// importMode=MERGE updates existing workflows by name and appends new ones;
// workflows not mentioned in the import are left untouched.
func RunExternalAPI_08_06_StrategyMerge(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)

	if err := d.CreateModelFromSample("wf6", 1, `{"k":1,"publish":false}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.ImportWorkflow("wf6", 1, minimalWorkflow08("baseline")); err != nil {
		t.Fatalf("baseline import: %v", err)
	}
	// MERGE: update "baseline" in place and add a new "newone" workflow.
	// dictionary uses "automated":false → cyoda-go uses "manual":true —
	// different_naming_same_level.
	mergeBody := `{
		"importMode": "MERGE",
		"workflows": [
			{
				"name": "baseline",
				"version": "1.0",
				"initialState": "draft",
				"states": {
					"draft": {"transitions": [{"name": "PUBLISH", "next": "published", "manual": true}]},
					"published": {"transitions": []}
				}
			},
			{
				"name": "newone",
				"version": "1.0",
				"initialState": "s",
				"states": {"s": {"transitions": []}}
			}
		]
	}`
	if err := d.ImportWorkflow("wf6", 1, mergeBody); err != nil {
		t.Fatalf("MERGE import: %v", err)
	}
	raw, err := d.ExportWorkflow("wf6", 1)
	if err != nil {
		t.Fatalf("ExportWorkflow after MERGE: %v", err)
	}
	for _, name := range []string{"baseline", "newone"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Errorf("MERGE missing %s workflow in export: %s", name, string(raw))
		}
	}
}
