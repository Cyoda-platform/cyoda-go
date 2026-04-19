package dispatch_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/dispatch"
)

const testHMACSecret = "test-secret-key"

func makeProcessorReq() *dispatch.DispatchProcessorRequest {
	return &dispatch.DispatchProcessorRequest{
		Entity:     json.RawMessage(`{"amount":100}`),
		EntityMeta: spi.EntityMeta{ID: "ent-1", TenantID: "t1"},
		Processor: spi.ProcessorDefinition{
			Type: "HTTP",
			Name: "calc",
		},
		WorkflowName:   "wf",
		TransitionName: "run",
		TxID:           "tx-1",
		TenantID:       "t1",
	}
}

func makeCriteriaReq() *dispatch.DispatchCriteriaRequest {
	return &dispatch.DispatchCriteriaRequest{
		Entity:         json.RawMessage(`{"status":"pending"}`),
		EntityMeta:     spi.EntityMeta{ID: "ent-2", TenantID: "t2"},
		Criterion:      json.RawMessage(`{"type":"ALWAYS_TRUE"}`),
		Target:         "TRANSITION",
		WorkflowName:   "wf",
		TransitionName: "approve",
		TxID:           "tx-2",
		TenantID:       "t2",
	}
}

// verifyHMAC checks that the X-Dispatch-HMAC header matches the body.
func verifyHMAC(t *testing.T, r *http.Request, secret string) {
	t.Helper()
	sig := r.Header.Get("X-Dispatch-HMAC")
	if sig == "" {
		t.Fatal("X-Dispatch-HMAC header missing")
	}
	// body already read by handler; verify against what was signed
	// We'll verify in a separate read of the body by the test server handler,
	// so we accept any non-empty value here. More thorough check done in
	// TestHTTPForwarder_HMACSignature below.
}

func TestHTTPForwarder_ProcessorSuccess(t *testing.T) {
	wantResp := dispatch.DispatchProcessorResponse{
		EntityData: json.RawMessage(`{"amount":200}`),
		Success:    true,
		Warnings:   []string{"adjusted"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/dispatch/processor" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %q", r.Method)
		}
		verifyHMAC(t, r, testHMACSecret)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wantResp)
	}))
	defer srv.Close()

	f := dispatch.NewHTTPForwarder([]byte(testHMACSecret), 5*time.Second).AllowLoopbackForTesting()
	resp, err := f.ForwardProcessor(context.Background(), srv.URL, makeProcessorReq())
	if err != nil {
		t.Fatalf("ForwardProcessor: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true")
	}
	if string(resp.EntityData) != `{"amount":200}` {
		t.Errorf("EntityData = %s, want {\"amount\":200}", resp.EntityData)
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0] != "adjusted" {
		t.Errorf("Warnings = %v, want [adjusted]", resp.Warnings)
	}
}

func TestHTTPForwarder_CriteriaSuccess(t *testing.T) {
	wantResp := dispatch.DispatchCriteriaResponse{
		Matches: true,
		Success: true,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/dispatch/criteria" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		verifyHMAC(t, r, testHMACSecret)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wantResp)
	}))
	defer srv.Close()

	f := dispatch.NewHTTPForwarder([]byte(testHMACSecret), 5*time.Second).AllowLoopbackForTesting()
	resp, err := f.ForwardCriteria(context.Background(), srv.URL, makeCriteriaReq())
	if err != nil {
		t.Fatalf("ForwardCriteria: %v", err)
	}
	if !resp.Matches {
		t.Errorf("Matches = false, want true")
	}
	if !resp.Success {
		t.Errorf("Success = false, want true")
	}
}

func TestHTTPForwarder_PeerUnreachable(t *testing.T) {
	// localhost:1 is guaranteed unreachable (privileged port, never listening)
	f := dispatch.NewHTTPForwarder([]byte(testHMACSecret), 2*time.Second).AllowLoopbackForTesting()

	_, err := f.ForwardProcessor(context.Background(), "http://localhost:1", makeProcessorReq())
	if err == nil {
		t.Fatal("expected error for unreachable peer, got nil")
	}

	_, err = f.ForwardCriteria(context.Background(), "http://localhost:1", makeCriteriaReq())
	if err == nil {
		t.Fatal("expected error for unreachable peer, got nil")
	}
}

func TestHTTPForwarder_HMACSignature(t *testing.T) {
	secret := []byte("verify-me")

	var capturedSig string
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSig = r.Header.Get("X-Dispatch-HMAC")
		capturedBody, _ = io.ReadAll(r.Body)

		resp := dispatch.DispatchProcessorResponse{Success: true}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	f := dispatch.NewHTTPForwarder(secret, 5*time.Second).AllowLoopbackForTesting()
	_, err := f.ForwardProcessor(context.Background(), srv.URL, makeProcessorReq())
	if err != nil {
		t.Fatalf("ForwardProcessor: %v", err)
	}

	// Compute expected HMAC over the captured body
	mac := hmac.New(sha256.New, secret)
	mac.Write(capturedBody)
	expected := hex.EncodeToString(mac.Sum(nil))

	if capturedSig != expected {
		t.Errorf("X-Dispatch-HMAC = %q, want %q", capturedSig, expected)
	}
}

// TestHTTPForwarder_AddrWithoutScheme verifies that the forwarder handles
// addresses without http:// scheme (as produced by gossip NODE_ADDR like
// "cyoda-go-node-2:8123"). Regression test for unsupported protocol error.
func TestHTTPForwarder_AddrWithoutScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(dispatch.DispatchProcessorResponse{Success: true})
	}))
	defer srv.Close()

	// Strip the "http://" from the test server URL to simulate gossip NODE_ADDR
	addrWithoutScheme := srv.Listener.Addr().String() // e.g., "127.0.0.1:PORT"

	f := dispatch.NewHTTPForwarder([]byte(testHMACSecret), 5*time.Second).AllowLoopbackForTesting()
	resp, err := f.ForwardProcessor(context.Background(), addrWithoutScheme, makeProcessorReq())
	if err != nil {
		t.Fatalf("ForwardProcessor with schemeless addr should work: %v", err)
	}
	if !resp.Success {
		t.Error("expected Success=true")
	}
}

func TestHTTPForwarder_PeerReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := dispatch.NewHTTPForwarder([]byte(testHMACSecret), 5*time.Second).AllowLoopbackForTesting()
	_, err := f.ForwardProcessor(context.Background(), srv.URL, makeProcessorReq())
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}
