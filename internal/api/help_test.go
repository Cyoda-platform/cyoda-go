package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help"
	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help/renderer"
)

func helpTestServer(t *testing.T, contextPath string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	RegisterHelpRoutes(mux, help.DefaultTree, contextPath, "dev")
	return httptest.NewServer(mux)
}

func TestGetFullTree(t *testing.T) {
	srv := helpTestServer(t, "/api")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/help")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var payload renderer.HelpPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Schema != 1 {
		t.Errorf("schema = %d", payload.Schema)
	}
	if len(payload.Topics) == 0 {
		t.Error("no topics in payload")
	}
}

func TestGetSingleTopic(t *testing.T) {
	srv := helpTestServer(t, "/api")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/help/cli")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var d renderer.TopicDescriptor
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Topic != "cli" {
		t.Errorf("topic = %q", d.Topic)
	}
}

func TestGetUnknownTopic_404_RFC7807(t *testing.T) {
	srv := helpTestServer(t, "/api")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/help/widgetry")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/problem+json") {
		t.Errorf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "HELP_TOPIC_NOT_FOUND") {
		t.Errorf("body missing code: %q", body)
	}
}

func TestMalformedTopicPath_400(t *testing.T) {
	srv := helpTestServer(t, "/api")
	defer srv.Close()
	// %20 decodes to space — not in [A-Za-z0-9._-]
	resp, err := http.Get(srv.URL + "/api/help/foo%20bar")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "BAD_REQUEST") {
		t.Errorf("body missing code: %q", body)
	}
}

func TestCORSHeadersPresent(t *testing.T) {
	srv := helpTestServer(t, "/api")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/help")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS header missing: %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestRespectsContextPath(t *testing.T) {
	srv := helpTestServer(t, "/v1/api")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/api/help")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("customized context path failed: %d", resp.StatusCode)
	}
	resp2, err := http.Get(srv.URL + "/api/help")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Errorf("default /api/help should not respond when ContextPath is /v1/api")
	}
}
