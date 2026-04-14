//go:build cyoda_recon

package recon

import "time"

type Scenario struct {
	Name  string
	Setup func() map[string]string
	Steps []Step
}

type Step struct {
	Name            string
	Method          string
	PathTemplate    string
	Body            string
	ExpectStatus    int
	Exclusions      []string
	SkipBodyCompare bool              // skip body comparison (e.g., when bodies are just different UUIDs)
	Capture         map[string]string // placeholder name → gjson path into response body
}

type StepReport struct {
	StepName    string
	Request     string
	MiniStatus  int
	CloudStatus int
	StatusMatch bool
	Exclusions  []string
	BodyMatch   bool
	BodyDiff    string // unified diff of JSON after stripping exclusions (empty if match)
}

type ScenarioReport struct {
	ScenarioName string
	Steps        []StepReport
}

type RunReport struct {
	Date       time.Time
	CyodaGo    string
	CyodaCloud string
	Scenarios  []ScenarioReport
}
