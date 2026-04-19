package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type keyInfoResponse struct {
	KID       string `json:"kid"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"createdAt"`
}

func TestKeysHandler_IssueKeyPair(t *testing.T) {
	store := NewInMemoryKeyStore()
	handler := NewKeysHandler(store)

	req := adminReq(http.MethodPost, "/oauth/keys/keypair", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}

	var resp keyInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.KID == "" {
		t.Fatal("expected non-empty kid")
	}
	if !resp.Active {
		t.Fatal("expected active to be true")
	}
	if resp.CreatedAt == "" {
		t.Fatal("expected non-empty createdAt")
	}

	// Verify key is in store
	kp, err := store.Get(resp.KID)
	if err != nil {
		t.Fatalf("key not found in store: %v", err)
	}
	if kp.PrivateKey == nil {
		t.Fatal("expected private key to be stored")
	}
	if kp.PublicKey == nil {
		t.Fatal("expected public key to be stored")
	}
}

func TestKeysHandler_GetCurrent(t *testing.T) {
	store := NewInMemoryKeyStore()
	handler := NewKeysHandler(store)

	// Issue a key pair first
	issueReq := adminReq(http.MethodPost, "/oauth/keys/keypair", nil)
	issueRec := httptest.NewRecorder()
	handler.ServeHTTP(issueRec, issueReq)

	var issued keyInfoResponse
	if err := json.NewDecoder(issueRec.Body).Decode(&issued); err != nil {
		t.Fatalf("failed to decode issue response: %v", err)
	}

	// Get current
	req := adminReq(http.MethodGet, "/oauth/keys/keypair/current", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp keyInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.KID != issued.KID {
		t.Fatalf("expected kid %s, got %s", issued.KID, resp.KID)
	}
	if !resp.Active {
		t.Fatal("expected active to be true")
	}

	// Verify private key is NOT in response body
	bodyBytes := rec.Body.Bytes()
	var raw map[string]interface{}
	// Re-read from the recorded response
	_ = json.Unmarshal(bodyBytes, &raw)
	if _, ok := raw["privateKey"]; ok {
		t.Fatal("private key must not be in response")
	}
}

func TestKeysHandler_Invalidate(t *testing.T) {
	store := NewInMemoryKeyStore()
	handler := NewKeysHandler(store)

	// Issue a key pair
	issueReq := adminReq(http.MethodPost, "/oauth/keys/keypair", nil)
	issueRec := httptest.NewRecorder()
	handler.ServeHTTP(issueRec, issueReq)

	var issued keyInfoResponse
	if err := json.NewDecoder(issueRec.Body).Decode(&issued); err != nil {
		t.Fatalf("failed to decode issue response: %v", err)
	}

	// Invalidate
	req := adminReq(http.MethodPost, "/oauth/keys/keypair/"+issued.KID+"/invalidate", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	// Verify key is no longer active
	kp, err := store.Get(issued.KID)
	if err != nil {
		t.Fatalf("key not found: %v", err)
	}
	if kp.Active {
		t.Fatal("expected key to be inactive after invalidation")
	}
}

func TestKeysHandler_Reactivate(t *testing.T) {
	store := NewInMemoryKeyStore()
	handler := NewKeysHandler(store)

	// Issue a key pair
	issueReq := adminReq(http.MethodPost, "/oauth/keys/keypair", nil)
	issueRec := httptest.NewRecorder()
	handler.ServeHTTP(issueRec, issueReq)

	var issued keyInfoResponse
	if err := json.NewDecoder(issueRec.Body).Decode(&issued); err != nil {
		t.Fatalf("failed to decode issue response: %v", err)
	}

	// Invalidate first
	invReq := adminReq(http.MethodPost, "/oauth/keys/keypair/"+issued.KID+"/invalidate", nil)
	invRec := httptest.NewRecorder()
	handler.ServeHTTP(invRec, invReq)

	// Reactivate
	req := adminReq(http.MethodPost, "/oauth/keys/keypair/"+issued.KID+"/reactivate", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	// Verify key is active again
	kp, err := store.Get(issued.KID)
	if err != nil {
		t.Fatalf("key not found: %v", err)
	}
	if !kp.Active {
		t.Fatal("expected key to be active after reactivation")
	}
}

func TestKeysHandler_Delete(t *testing.T) {
	store := NewInMemoryKeyStore()
	handler := NewKeysHandler(store)

	// Issue a key pair
	issueReq := adminReq(http.MethodPost, "/oauth/keys/keypair", nil)
	issueRec := httptest.NewRecorder()
	handler.ServeHTTP(issueRec, issueReq)

	var issued keyInfoResponse
	if err := json.NewDecoder(issueRec.Body).Decode(&issued); err != nil {
		t.Fatalf("failed to decode issue response: %v", err)
	}

	// Delete
	req := adminReq(http.MethodDelete, "/oauth/keys/keypair/"+issued.KID, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}

	// Verify key is gone
	_, err := store.Get(issued.KID)
	if err == nil {
		t.Fatal("expected error getting deleted key")
	}
}

func TestKeysHandler_GetCurrent_NoActiveKey(t *testing.T) {
	store := NewInMemoryKeyStore()
	handler := NewKeysHandler(store)

	req := adminReq(http.MethodGet, "/oauth/keys/keypair/current", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}
