package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_SubmitAsyncSearch_POST(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`"job-abc-123"`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	jobID, err := c.SubmitAsyncSearch(t, "orders", 1, `{"type":"group","conditions":[]}`)
	if err != nil {
		t.Fatalf("SubmitAsyncSearch: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if gotPath != "/api/search/async/orders/1" {
		t.Errorf("path: got %q", gotPath)
	}
	if !strings.Contains(gotBody, `"type":"group"`) {
		t.Errorf("body: got %q", gotBody)
	}
	if jobID != "job-abc-123" {
		t.Errorf("jobID: got %q want job-abc-123", jobID)
	}
}
