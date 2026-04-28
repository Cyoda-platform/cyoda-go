package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/common"
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

// TestKeysHandler_NotFoundResponses_DoNotEchoKID covers the lifecycle
// (delete/invalidate/reactivate) endpoints. The handler previously echoed
// the attacker-controllable {keyId} path segment back into the response
// body via fmt.Sprintf, breaking the RFC 9457 contract and giving an
// observability-style oracle to a malformed-input prober. Bodies must be
// problem+json with errorCode=NOT_FOUND, and the submitted KID must not
// appear in the response detail.
func TestKeysHandler_NotFoundResponses_DoNotEchoKID(t *testing.T) {
	// Choose a marker that survives the simple path-splitting router (no '/').
	const malicious = "ATTACKER_REFLECTED_KID_BEACON"
	cases := []struct {
		name string
		req  *http.Request
	}{
		{"delete", adminReq(http.MethodDelete, "/oauth/keys/keypair/"+malicious, nil)},
		{"invalidate", adminReq(http.MethodPost, "/oauth/keys/keypair/"+malicious+"/invalidate", nil)},
		{"reactivate", adminReq(http.MethodPost, "/oauth/keys/keypair/"+malicious+"/reactivate", nil)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewInMemoryKeyStore()
			handler := NewKeysHandler(store)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tc.req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d (body=%q)", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Fatalf("expected Content-Type=application/problem+json, got %q", ct)
			}
			var pd common.ProblemDetail
			if err := json.NewDecoder(rec.Body).Decode(&pd); err != nil {
				t.Fatalf("failed to decode problem-detail: %v", err)
			}
			// The detail field is the part the handler emits — it must not
			// echo the attacker-controllable KID. The instance field is
			// legitimately the request URI per RFC 9457 and is not in scope.
			if strings.Contains(pd.Detail, malicious) {
				t.Fatalf("problem detail must not echo submitted KID, got %q", pd.Detail)
			}
			code, _ := pd.Props["errorCode"].(string)
			if code != common.ErrCodeNotFound {
				t.Errorf("expected errorCode=%s, got %s", common.ErrCodeNotFound, code)
			}
		})
	}
}

// TestKeysHandler_GetCurrent_NoActiveKey_RFC9457 ensures the existing
// "no active key pair" 404 likewise emits problem+json with the standard
// NOT_FOUND code (it previously used http.Error with a plain string body).
func TestKeysHandler_GetCurrent_NoActiveKey_RFC9457(t *testing.T) {
	store := NewInMemoryKeyStore()
	handler := NewKeysHandler(store)

	req := adminReq(http.MethodGet, "/oauth/keys/keypair/current", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected Content-Type=application/problem+json, got %q", ct)
	}
	var pd common.ProblemDetail
	if err := json.NewDecoder(rec.Body).Decode(&pd); err != nil {
		t.Fatalf("failed to decode problem-detail: %v", err)
	}
	code, _ := pd.Props["errorCode"].(string)
	if code != common.ErrCodeNotFound {
		t.Errorf("expected errorCode=%s, got %s", common.ErrCodeNotFound, code)
	}
}
