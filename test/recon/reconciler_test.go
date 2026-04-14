//go:build cyoda_recon

package recon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReconcilerIdenticalServers(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mini := httptest.NewServer(handler)
	defer mini.Close()
	cloud := httptest.NewServer(handler)
	defer cloud.Close()

	scenario := Scenario{
		Name:  "Identical",
		Setup: func() map[string]string { return nil },
		Steps: []Step{
			{Name: "Get status", Method: "GET", PathTemplate: "/health"},
		},
	}

	report := reconcile(scenario, mini.URL, cloud.URL, cloud.Client())

	if report.ScenarioName != "Identical" {
		t.Fatalf("expected scenario name Identical, got %s", report.ScenarioName)
	}
	if len(report.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(report.Steps))
	}
	sr := report.Steps[0]
	if !sr.StatusMatch {
		t.Errorf("expected status match, got mini=%d cloud=%d", sr.MiniStatus, sr.CloudStatus)
	}
	if !sr.BodyMatch {
		t.Errorf("expected body match, got diff:\n%s", sr.BodyDiff)
	}
}

func TestReconcilerDifferentResponses(t *testing.T) {
	miniHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"Alice","age":30}`))
	})
	cloudHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"Alice","age":31}`))
	})

	mini := httptest.NewServer(miniHandler)
	defer mini.Close()
	cloud := httptest.NewServer(cloudHandler)
	defer cloud.Close()

	scenario := Scenario{
		Name:  "Different",
		Setup: func() map[string]string { return nil },
		Steps: []Step{
			{Name: "Get user", Method: "GET", PathTemplate: "/user"},
		},
	}

	report := reconcile(scenario, mini.URL, cloud.URL, cloud.Client())
	sr := report.Steps[0]

	if !sr.StatusMatch {
		t.Errorf("expected status match (both 200)")
	}
	if sr.BodyMatch {
		t.Error("expected body mismatch for different age values")
	}
	if !strings.Contains(sr.BodyDiff, "30") || !strings.Contains(sr.BodyDiff, "31") {
		t.Errorf("expected diff to show age values, got:\n%s", sr.BodyDiff)
	}
}

func TestReconcilerStatusMismatch(t *testing.T) {
	miniHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cloudHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	mini := httptest.NewServer(miniHandler)
	defer mini.Close()
	cloud := httptest.NewServer(cloudHandler)
	defer cloud.Close()

	scenario := Scenario{
		Name:  "StatusMismatch",
		Setup: func() map[string]string { return nil },
		Steps: []Step{
			{Name: "Get missing", Method: "GET", PathTemplate: "/missing"},
		},
	}

	report := reconcile(scenario, mini.URL, cloud.URL, cloud.Client())
	sr := report.Steps[0]

	if sr.StatusMatch {
		t.Error("expected status mismatch")
	}
	if sr.MiniStatus != 200 {
		t.Errorf("expected mini status 200, got %d", sr.MiniStatus)
	}
	if sr.CloudStatus != 404 {
		t.Errorf("expected cloud status 404, got %d", sr.CloudStatus)
	}
}

func TestReconcilerPlaceholders(t *testing.T) {
	var capturedPath string
	var capturedBody string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			capturedBody = string(data)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	mini := httptest.NewServer(handler)
	defer mini.Close()
	cloud := httptest.NewServer(handler)
	defer cloud.Close()

	scenario := Scenario{
		Name: "Placeholders",
		Setup: func() map[string]string {
			return map[string]string{"modelName": "TestModel"}
		},
		Steps: []Step{
			{
				Name:         "Import model",
				Method:       "POST",
				PathTemplate: "/model/import/JSON/SAMPLE_DATA/{modelName}/1",
				Body:         `{"entity":"{modelName}"}`,
			},
		},
	}

	reconcile(scenario, mini.URL, cloud.URL, cloud.Client())

	// The last request captured will be the cloud request (mini runs first).
	// Both should have the same resolved path.
	if capturedPath != "/model/import/JSON/SAMPLE_DATA/TestModel/1" {
		t.Errorf("expected resolved path /model/import/JSON/SAMPLE_DATA/TestModel/1, got %s", capturedPath)
	}
	if !strings.Contains(capturedBody, "TestModel") {
		t.Errorf("expected resolved body containing TestModel, got %s", capturedBody)
	}
}

func TestReconcilerCapture(t *testing.T) {
	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/create" {
			w.Write([]byte(fmt.Sprintf(`[{"entityIds":[{"id":"id-%d"}],"transactionId":"tx-%d"}]`, callCount, callCount)))
		} else {
			w.Write([]byte(fmt.Sprintf(`{"path":"%s"}`, r.URL.Path)))
		}
	})

	srv1 := httptest.NewServer(handler)
	defer srv1.Close()
	srv2 := httptest.NewServer(handler)
	defer srv2.Close()

	scenario := Scenario{
		Name:  "Capture Test",
		Setup: func() map[string]string { return map[string]string{"model": "TestModel"} },
		Steps: []Step{
			{
				Name:         "Create",
				Method:       "POST",
				PathTemplate: "/create",
				Body:         `{"name":"test"}`,
				ExpectStatus: 200,
				Capture: map[string]string{
					"entityId": "0.entityIds.0.id",
				},
			},
			{
				Name:         "Get by captured ID",
				Method:       "GET",
				PathTemplate: "/entity/{entityId}",
				ExpectStatus: 200,
			},
		},
	}

	report := reconcile(scenario, srv1.URL, srv2.URL, srv2.Client())

	if len(report.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(report.Steps))
	}
	// The request path should contain the captured ID
	if !strings.Contains(report.Steps[1].Request, "/entity/id-") {
		t.Errorf("expected captured ID in path, got %s", report.Steps[1].Request)
	}
}
