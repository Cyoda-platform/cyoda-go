package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_CancelAsyncSearch_PUT(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"isCancelled":true,"cancelled":true,"currentSearchJobStatus":"CANCELLED"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	if err := c.CancelAsyncSearch(t, "job-abc-123"); err != nil {
		t.Fatalf("CancelAsyncSearch: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method: got %q want PUT", gotMethod)
	}
	if gotPath != "/api/search/async/job-abc-123/cancel" {
		t.Errorf("path: got %q", gotPath)
	}
}

func TestClient_GetAsyncSearchResults_GET(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"content":[{"id":"00000000-0000-0000-0000-000000000001","data":{"k":1}}],"page":{"number":0,"size":1000,"totalElements":1,"totalPages":1}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	page, err := c.GetAsyncSearchResults(t, "job-abc-123")
	if err != nil {
		t.Fatalf("GetAsyncSearchResults: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method: got %q want GET", gotMethod)
	}
	if gotPath != "/api/search/async/job-abc-123" {
		t.Errorf("path: got %q", gotPath)
	}
	if len(page.Content) != 1 {
		t.Errorf("content len: got %d want 1", len(page.Content))
	}
	if page.Page.TotalElements != 1 {
		t.Errorf("totalElements: got %d want 1", page.Page.TotalElements)
	}
}

func TestClient_GetAsyncSearchStatus_GET(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"searchJobStatus":"SUCCESSFUL","createTime":"2026-04-25T12:00:00.000Z","entitiesCount":2,"calculationTimeMillis":50,"expirationDate":"2026-04-26T12:00:00.000Z"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	status, err := c.GetAsyncSearchStatus(t, "job-abc-123")
	if err != nil {
		t.Fatalf("GetAsyncSearchStatus: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method: got %q want GET", gotMethod)
	}
	if gotPath != "/api/search/async/job-abc-123/status" {
		t.Errorf("path: got %q", gotPath)
	}
	if status != "SUCCESSFUL" {
		t.Errorf("status: got %q want SUCCESSFUL", status)
	}
}

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

func TestClient_AwaitAsyncSearchResults_Success(t *testing.T) {
	var statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/search/async/orders/"):
			_, _ = w.Write([]byte(`"job-1"`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/search/async/job-1/status":
			n := statusCalls.Add(1)
			if n < 2 {
				_, _ = w.Write([]byte(`{"searchJobStatus":"RUNNING"}`))
			} else {
				_, _ = w.Write([]byte(`{"searchJobStatus":"SUCCESSFUL"}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/search/async/job-1":
			_, _ = w.Write([]byte(`{"content":[{"id":"00000000-0000-0000-0000-000000000001","data":{}}],"page":{"totalElements":1}}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	page, err := c.AwaitAsyncSearchResults(t, "orders", 1, `{}`, 5*time.Second)
	if err != nil {
		t.Fatalf("AwaitAsyncSearchResults: %v", err)
	}
	if len(page.Content) != 1 {
		t.Errorf("content len: got %d want 1", len(page.Content))
	}
}

func TestClient_AwaitAsyncSearchResults_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`"job-timeout"`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(`{"searchJobStatus":"RUNNING"}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.AwaitAsyncSearchResults(t, "orders", 1, `{}`, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout in error message, got: %v", err)
	}
}

func TestClient_AwaitAsyncSearchResults_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`"job-fail"`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(`{"searchJobStatus":"FAILED"}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.AwaitAsyncSearchResults(t, "orders", 1, `{}`, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for FAILED status, got nil")
	}
	if !strings.Contains(err.Error(), "FAILED") {
		t.Errorf("expected FAILED in error message, got: %v", err)
	}
}
