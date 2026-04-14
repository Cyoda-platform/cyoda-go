package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// getToken obtains a JWT token via client_credentials grant.
// The token endpoint uses HTTP Basic Auth for client authentication.
func getToken(t *testing.T, clientID, clientSecret string) string {
	t.Helper()
	data := url.Values{
		"grant_type": {"client_credentials"},
	}
	req, err := http.NewRequest("POST", serverURL+"/api/oauth/token", strings.NewReader(data.Encode()))
	if err != nil {
		t.Fatalf("failed to create token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("token request returned %d: %s", resp.StatusCode, body)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode token response: %v", err)
	}
	token, ok := result["access_token"].(string)
	if !ok || token == "" {
		t.Fatalf("no access_token in response: %v", result)
	}
	return token
}

// authRequest creates an authenticated HTTP request.
func authRequest(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	token := getToken(t, "test-client", "test-secret")
	req, err := http.NewRequest(method, serverURL+path, body)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// doAuth performs an authenticated HTTP request and returns the response.
func doAuth(t *testing.T, method, path string, body string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := authRequest(t, method, path, bodyReader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	return resp
}

// readBody reads and returns the response body as a string, closing it.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return string(body)
}

// queryDB executes a SQL query against the test database with tenant set.
func queryDB(t *testing.T, tenantID, sql string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	// NOTE: test-only — tenantID is a hardcoded constant, not user input. Do not use this pattern in production code.
	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL app.current_tenant = '%s'", tenantID))
	if err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var count int
	err = tx.QueryRow(ctx, sql, args...).Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	return count
}
