//go:build cyoda_recon

package recon

import (
	"strings"
	"testing"
	"time"
)

func newTestRunReport() RunReport {
	return RunReport{
		Date:       time.Date(2026, 3, 22, 14, 30, 0, 0, time.UTC),
		CyodaGo:  "in-process",
		CyodaCloud: "https://api.cyoda.example.com",
		Scenarios: []ScenarioReport{
			{
				ScenarioName: "Import and Export Model",
				Steps: []StepReport{
					{
						StepName:    "Import JSON sample data",
						Request:     "POST /model/import/JSON/SAMPLE_DATA/Person/1",
						MiniStatus:  200,
						CloudStatus: 200,
						StatusMatch: true,
						BodyMatch:   true,
					},
					{
						StepName:    "Export as JSON_SCHEMA",
						Request:     "GET /model/export/JSON_SCHEMA/Person/1",
						MiniStatus:  200,
						CloudStatus: 200,
						StatusMatch: true,
						Exclusions:  []string{"/modelId", "/modelUpdateDate"},
						BodyMatch:   false,
						BodyDiff: `--- Cyoda-Go
+++ Cyoda Cloud
  {
-   "type": "integer"
+   "type": "number"
  }
`,
					},
				},
			},
		},
	}
}

func TestReportContainsHeader(t *testing.T) {
	report := generateReport(newTestRunReport())

	if !strings.Contains(report, "# Reconciliation Report") {
		t.Error("expected report to contain title")
	}
	if !strings.Contains(report, "2026-03-22T14:30:00Z") {
		t.Error("expected report to contain date")
	}
	if !strings.Contains(report, "https://api.cyoda.example.com") {
		t.Error("expected report to contain Cyoda Cloud URL")
	}
	if !strings.Contains(report, "in-process") {
		t.Error("expected report to contain Cyoda-Go label")
	}
}

func TestReportContainsScenario(t *testing.T) {
	report := generateReport(newTestRunReport())

	if !strings.Contains(report, "## Scenario: Import and Export Model") {
		t.Error("expected report to contain scenario heading")
	}
	if !strings.Contains(report, "Import JSON sample data") {
		t.Error("expected report to contain step name")
	}
	if !strings.Contains(report, "Export as JSON_SCHEMA") {
		t.Error("expected report to contain second step name")
	}
}

func TestReportContainsSummaryTable(t *testing.T) {
	report := generateReport(newTestRunReport())

	if !strings.Contains(report, "## Summary") {
		t.Error("expected report to contain Summary section")
	}
	if !strings.Contains(report, "Import and Export Model") {
		t.Error("expected summary to contain scenario name")
	}
	if !strings.Contains(report, "| **Total**") {
		t.Error("expected summary to contain Total row")
	}
}

func TestReportShowsDiffBlock(t *testing.T) {
	report := generateReport(newTestRunReport())

	if !strings.Contains(report, "```diff") {
		t.Error("expected report to show diff code block")
	}
	if !strings.Contains(report, "--- Cyoda-Go") {
		t.Error("expected unified diff header")
	}
	if !strings.Contains(report, "/modelId") {
		t.Error("expected report to show exclusions")
	}
}

func TestReportBodyMatchNone(t *testing.T) {
	run := RunReport{
		Date:       time.Now(),
		CyodaGo:  "in-process",
		CyodaCloud: "https://example.com",
		Scenarios: []ScenarioReport{
			{
				ScenarioName: "All Match",
				Steps: []StepReport{
					{StepName: "Step 1", StatusMatch: true, BodyMatch: true},
				},
			},
		},
	}
	report := generateReport(run)
	if !strings.Contains(report, "Body differences:** none") {
		t.Error("expected 'none' for matching body")
	}
}
