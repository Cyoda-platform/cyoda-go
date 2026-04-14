//go:build cyoda_recon

package recon

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// reconcile executes a scenario against both targets and returns a report.
func reconcile(scenario Scenario, miniBaseURL, cloudBaseURL string, cloudClient *http.Client) ScenarioReport {
	basePlaceholders := scenario.Setup()
	miniPlaceholders := clonePlaceholders(basePlaceholders)
	cloudPlaceholders := clonePlaceholders(basePlaceholders)

	report := ScenarioReport{
		ScenarioName: scenario.Name,
		Steps:        make([]StepReport, 0, len(scenario.Steps)),
	}

	for _, step := range scenario.Steps {
		miniPath := resolvePlaceholders(step.PathTemplate, miniPlaceholders)
		cloudPath := resolvePlaceholders(step.PathTemplate, cloudPlaceholders)
		miniBody := resolvePlaceholders(step.Body, miniPlaceholders)
		cloudBody := resolvePlaceholders(step.Body, cloudPlaceholders)

		miniStatus, miniResp := doRequest(http.DefaultClient, miniBaseURL+miniPath, step.Method, miniBody)
		cloudStatus, cloudResp := doRequest(cloudClient, cloudBaseURL+cloudPath, step.Method, cloudBody)

		// Capture values from each side independently.
		for name, path := range step.Capture {
			miniPlaceholders[name] = gjson.Get(string(miniResp), path).String()
			cloudPlaceholders[name] = gjson.Get(string(cloudResp), path).String()
		}

		sr := StepReport{
			StepName:    step.Name,
			Request:     step.Method + " " + miniPath,
			MiniStatus:  miniStatus,
			CloudStatus: cloudStatus,
			StatusMatch: miniStatus == cloudStatus,
			Exclusions:  step.Exclusions,
			BodyMatch:   true,
		}

		if !step.SkipBodyCompare && len(miniResp) > 0 && len(cloudResp) > 0 {
			dr := diffJSON(miniResp, cloudResp, step.Exclusions)
			sr.BodyMatch = dr.match
			sr.BodyDiff = dr.diff
		}

		report.Steps = append(report.Steps, sr)
	}

	return report
}

// clonePlaceholders returns a shallow copy of the placeholder map.
func clonePlaceholders(m map[string]string) map[string]string {
	if m == nil {
		return make(map[string]string)
	}
	clone := make(map[string]string, len(m))
	for k, v := range m {
		clone[k] = v
	}
	return clone
}

// doRequest sends an HTTP request and returns the status code and response body.
func doRequest(client *http.Client, url, method, body string) (int, []byte) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return 0, []byte("request error: " + err.Error())
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, []byte("transport error: " + err.Error())
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, []byte("read error: " + err.Error())
	}

	return resp.StatusCode, respBody
}

// resolvePlaceholders replaces {key} tokens in the template with values from the map.
func resolvePlaceholders(template string, placeholders map[string]string) string {
	result := template
	for k, v := range placeholders {
		result = strings.ReplaceAll(result, "{"+k+"}", v)
	}
	return result
}
