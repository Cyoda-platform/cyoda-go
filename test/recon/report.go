//go:build cyoda_recon

package recon

import (
	"fmt"
	"strings"
)

// generateReport produces a Markdown reconciliation report from a RunReport.
func generateReport(run RunReport) string {
	var b strings.Builder

	// Header
	b.WriteString("# Reconciliation Report\n\n")
	b.WriteString(fmt.Sprintf("**Date:** %s\n", run.Date.Format("2006-01-02T15:04:05Z")))
	b.WriteString(fmt.Sprintf("**Cyoda-Go:** %s\n", run.CyodaGo))
	b.WriteString(fmt.Sprintf("**Cyoda Cloud:** %s\n\n", run.CyodaCloud))

	// Per-scenario sections
	totalSteps := 0
	totalStatusMatch := 0
	totalBodyMatch := 0
	totalDiffs := 0

	type scenarioSummary struct {
		name        string
		steps       int
		statusMatch int
		bodyMatch   int
		diffCount   int
	}
	var summaries []scenarioSummary

	for _, sc := range run.Scenarios {
		b.WriteString(fmt.Sprintf("## Scenario: %s\n\n", sc.ScenarioName))

		scStatusMatch := 0
		scBodyMatch := 0
		scDiffs := 0

		for i, step := range sc.Steps {
			b.WriteString(fmt.Sprintf("### Step %d: %s\n", i+1, step.StepName))
			b.WriteString(fmt.Sprintf("- **Request:** `%s`\n", step.Request))

			statusEmoji := "\u2705"
			if !step.StatusMatch {
				statusEmoji = "\u274c"
			}
			b.WriteString(fmt.Sprintf("- **Status:** Cyoda-Go: %d | Cyoda Cloud: %d %s\n",
				step.MiniStatus, step.CloudStatus, statusEmoji))

			if step.StatusMatch {
				scStatusMatch++
			}

			if len(step.Exclusions) > 0 {
				b.WriteString(fmt.Sprintf("- **Exclusions applied:** %s\n",
					strings.Join(formatExclusions(step.Exclusions), ", ")))
			}

			if step.BodyMatch {
				b.WriteString("- **Body differences:** none\n")
				scBodyMatch++
			} else {
				scDiffs++
				b.WriteString("- **Body differences:**\n\n")
				b.WriteString("```diff\n")
				b.WriteString(step.BodyDiff)
				b.WriteString("```\n")
			}
			b.WriteString("\n")
		}

		summaries = append(summaries, scenarioSummary{
			name:        sc.ScenarioName,
			steps:       len(sc.Steps),
			statusMatch: scStatusMatch,
			bodyMatch:   scBodyMatch,
			diffCount:   scDiffs,
		})

		totalSteps += len(sc.Steps)
		totalStatusMatch += scStatusMatch
		totalBodyMatch += scBodyMatch
		totalDiffs += scDiffs
	}

	// Summary table
	b.WriteString("## Summary\n\n")
	b.WriteString("| Scenario | Steps | Status Match | Body Match | Differences |\n")
	b.WriteString("|----------|-------|-------------|------------|-------------|\n")
	for _, s := range summaries {
		b.WriteString(fmt.Sprintf("| %s | %d | %d/%d | %d/%d | %d |\n",
			s.name, s.steps, s.statusMatch, s.steps, s.bodyMatch, s.steps, s.diffCount))
	}
	b.WriteString(fmt.Sprintf("| **Total** | **%d** | **%d/%d** | **%d/%d** | **%d** |\n",
		totalSteps, totalStatusMatch, totalSteps, totalBodyMatch, totalSteps, totalDiffs))

	return b.String()
}

// formatExclusions wraps each exclusion in backticks.
func formatExclusions(exclusions []string) []string {
	formatted := make([]string, len(exclusions))
	for i, e := range exclusions {
		formatted[i] = "`" + e + "`"
	}
	return formatted
}
