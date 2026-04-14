package dispatch

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// fakeLocalDispatcher implements spi.ExternalProcessingService for testing.
type fakeLocalDispatcher struct {
	processorResult *common.Entity
	processorErr    error
	criteriaResult  bool
	criteriaErr     error
}

func (f *fakeLocalDispatcher) DispatchProcessor(
	_ context.Context,
	_ *common.Entity,
	_ common.ProcessorDefinition,
	_, _, _ string,
) (*common.Entity, error) {
	return f.processorResult, f.processorErr
}

func (f *fakeLocalDispatcher) DispatchCriteria(
	_ context.Context,
	_ *common.Entity,
	_ json.RawMessage,
	_, _, _, _, _ string,
) (bool, error) {
	return f.criteriaResult, f.criteriaErr
}

// newTestDispatchHandler creates a DispatchHandler without secret length validation,
// for use in tests that predate the minimum-length requirement.
func newTestDispatchHandler(local *fakeLocalDispatcher, secret []byte) *DispatchHandler {
	return &DispatchHandler{local: local, hmacSecret: secret}
}

// sign computes the HMAC-SHA256 of body using secret — mirrors HTTPForwarder.sign().
func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestHandler_ProcessorSuccess(t *testing.T) {
	secret := []byte("test-secret")
	resultData := json.RawMessage(`{"output":42}`)
	fake := &fakeLocalDispatcher{
		processorResult: &common.Entity{
			Meta: common.EntityMeta{ID: "ent-1"},
			Data: []byte(`{"output":42}`),
		},
	}

	handler := newTestDispatchHandler(fake, secret)
	mux := http.NewServeMux()
	handler.Register(mux)

	req := DispatchProcessorRequest{
		Entity:         json.RawMessage(`{"foo":"bar"}`),
		EntityMeta:     common.EntityMeta{ID: "ent-1"},
		Processor:      common.ProcessorDefinition{Name: "proc1", Type: "SCRIPT"},
		WorkflowName:   "wf",
		TransitionName: "t1",
		TxID:           "tx-1",
		TenantID:       "tenant-a",
		UserID:         "user-1",
		Roles:          []string{"ROLE_USER"},
	}
	body, _ := json.Marshal(req)

	httpReq := httptest.NewRequest(http.MethodPost, "/internal/dispatch/processor", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dispatch-HMAC", sign(secret, body))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp DispatchProcessorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %s)", resp.Error)
	}
	if string(resp.EntityData) != string(resultData) {
		t.Errorf("expected entity data %s, got %s", resultData, resp.EntityData)
	}
}

func TestHandler_CriteriaSuccess(t *testing.T) {
	secret := []byte("test-secret")
	fake := &fakeLocalDispatcher{criteriaResult: true}

	handler := newTestDispatchHandler(fake, secret)
	mux := http.NewServeMux()
	handler.Register(mux)

	req := DispatchCriteriaRequest{
		Entity:         json.RawMessage(`{"foo":"bar"}`),
		EntityMeta:     common.EntityMeta{ID: "ent-2"},
		Criterion:      json.RawMessage(`{"type":"eq","field":"x","value":1}`),
		Target:         "target",
		WorkflowName:   "wf",
		TransitionName: "t1",
		ProcessorName:  "proc1",
		TxID:           "tx-2",
		TenantID:       "tenant-a",
		UserID:         "user-1",
		Roles:          []string{"ROLE_USER"},
	}
	body, _ := json.Marshal(req)

	httpReq := httptest.NewRequest(http.MethodPost, "/internal/dispatch/criteria", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dispatch-HMAC", sign(secret, body))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp DispatchCriteriaResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}
	if !resp.Matches {
		t.Errorf("expected matches=true")
	}
}

func TestHandler_MissingHMAC(t *testing.T) {
	secret := []byte("test-secret")
	fake := &fakeLocalDispatcher{}
	handler := newTestDispatchHandler(fake, secret)
	mux := http.NewServeMux()
	handler.Register(mux)

	body := []byte(`{}`)
	httpReq := httptest.NewRequest(http.MethodPost, "/internal/dispatch/processor", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	// No X-Dispatch-HMAC header

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestHandler_InvalidHMAC(t *testing.T) {
	secret := []byte("test-secret")
	fake := &fakeLocalDispatcher{}
	handler := newTestDispatchHandler(fake, secret)
	mux := http.NewServeMux()
	handler.Register(mux)

	body := []byte(`{"entityMeta":{"ID":"x"}}`)
	httpReq := httptest.NewRequest(http.MethodPost, "/internal/dispatch/processor", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dispatch-HMAC", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestNewDispatchHandler_SecretTooShort(t *testing.T) {
	_, err := NewDispatchHandler(&fakeLocalDispatcher{}, []byte("short"))
	if err == nil {
		t.Fatal("expected error for short secret")
	}
	if !errors.Is(err, ErrHMACSecretTooShort) {
		t.Errorf("expected ErrHMACSecretTooShort, got %v", err)
	}
}

func TestNewDispatchHandler_SecretValid(t *testing.T) {
	h, err := NewDispatchHandler(&fakeLocalDispatcher{}, []byte("at-least-32-bytes-long-secret!!!"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestHandler_ProcessorError_SanitizedResponse(t *testing.T) {
	secret := []byte("at-least-32-bytes-long-secret!!!")
	fake := &fakeLocalDispatcher{
		processorErr: fmt.Errorf("connection refused: dial tcp 10.0.0.1:5432"),
	}
	handler, _ := NewDispatchHandler(fake, secret)
	mux := http.NewServeMux()
	handler.Register(mux)

	req := DispatchProcessorRequest{
		Entity:         json.RawMessage(`{"foo":"bar"}`),
		EntityMeta:     common.EntityMeta{ID: "ent-1"},
		Processor:      common.ProcessorDefinition{Name: "proc1", Type: "SCRIPT"},
		WorkflowName:   "wf",
		TransitionName: "t1",
		TxID:           "tx-1",
		TenantID:       "tenant-a",
		UserID:         "user-1",
		Roles:          []string{"ROLE_USER"},
	}
	body, _ := json.Marshal(req)

	httpReq := httptest.NewRequest(http.MethodPost, "/internal/dispatch/processor", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dispatch-HMAC", sign(secret, body))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httpReq)

	var resp DispatchProcessorResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if strings.Contains(resp.Error, "10.0.0.1") {
		t.Errorf("error response must not contain internal details, got %q", resp.Error)
	}
	if strings.Contains(resp.Error, "connection refused") {
		t.Errorf("error response must not contain internal details, got %q", resp.Error)
	}
}

func TestHandler_CriteriaError_SanitizedResponse(t *testing.T) {
	secret := []byte("at-least-32-bytes-long-secret!!!")
	fake := &fakeLocalDispatcher{
		criteriaErr: fmt.Errorf("pq: password authentication failed for user admin"),
	}
	handler, _ := NewDispatchHandler(fake, secret)
	mux := http.NewServeMux()
	handler.Register(mux)

	req := DispatchCriteriaRequest{
		Entity:         json.RawMessage(`{"foo":"bar"}`),
		EntityMeta:     common.EntityMeta{ID: "ent-2"},
		Criterion:      json.RawMessage(`{"type":"eq"}`),
		Target:         "target",
		WorkflowName:   "wf",
		TransitionName: "t1",
		ProcessorName:  "proc1",
		TxID:           "tx-2",
		TenantID:       "tenant-a",
		UserID:         "user-1",
		Roles:          []string{"ROLE_USER"},
	}
	body, _ := json.Marshal(req)

	httpReq := httptest.NewRequest(http.MethodPost, "/internal/dispatch/criteria", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dispatch-HMAC", sign(secret, body))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httpReq)

	var resp DispatchCriteriaResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if strings.Contains(resp.Error, "password") {
		t.Errorf("error response must not contain internal details, got %q", resp.Error)
	}
}
