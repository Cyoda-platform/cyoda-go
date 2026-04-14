package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

func strPtr(s string) *string { return &s }

func generateTestJWK(t *testing.T) json.RawMessage {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	jwk := map[string]string{
		"kty": "RSA",
		"n":   n,
		"e":   e,
		"kid": "test-kid",
		"alg": "RS256",
		"use": "sig",
	}
	data, _ := json.Marshal(jwk)
	return json.RawMessage(data)
}

func TestTrustedKeysHandler_RegisterAndList(t *testing.T) {
	store := NewInMemoryTrustedKeyStore()
	handler := NewTrustedKeysHandler(store)

	jwk := generateTestJWK(t)
	validTo := "2027-01-01T00:00:00Z"
	body := registerTrustedKeyRequest{
		KeyID:          "ext-key-1",
		JWK: jwk,
		Audience:     "my-service",
		ValidFrom: strPtr("2026-01-01T00:00:00Z"),
		ValidTo:      &validTo,
	}
	bodyBytes, _ := json.Marshal(body)

	// POST register
	req := httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var created trustedKeyInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if created.KID != "ext-key-1" {
		t.Errorf("expected kid ext-key-1, got %s", created.KID)
	}
	if created.Audience != "my-service" {
		t.Errorf("expected audience my-service, got %s", created.Audience)
	}
	if !created.Active {
		t.Error("expected active to be true")
	}
	if created.ValidFrom != "2026-01-01T00:00:00Z" {
		t.Errorf("expected validFrom 2026-01-01T00:00:00Z, got %s", created.ValidFrom)
	}
	if created.ValidTo == nil || *created.ValidTo != "2027-01-01T00:00:00Z" {
		t.Errorf("expected validTo 2027-01-01T00:00:00Z, got %v", created.ValidTo)
	}

	// GET list
	req = httptest.NewRequest(http.MethodGet, "/oauth/keys/trusted", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var list []trustedKeyInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 key, got %d", len(list))
	}
	if list[0].KID != "ext-key-1" {
		t.Errorf("expected kid ext-key-1, got %s", list[0].KID)
	}
}

func TestTrustedKeysHandler_Invalidate(t *testing.T) {
	store := NewInMemoryTrustedKeyStore()
	handler := NewTrustedKeysHandler(store)

	jwk := generateTestJWK(t)
	body := registerTrustedKeyRequest{
		KeyID:          "ext-key-2",
		JWK: jwk,
		Audience:     "svc",
		ValidFrom: strPtr("2026-01-01T00:00:00Z"),
	}
	bodyBytes, _ := json.Marshal(body)

	// Register
	req := httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	// Invalidate
	req = httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted/ext-key-2/invalidate", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify inactive via store
	tk, err := store.Get("ext-key-2")
	if err != nil {
		t.Fatalf("failed to get key: %v", err)
	}
	if tk.Active {
		t.Error("expected key to be inactive after invalidation")
	}
}

func TestTrustedKeysHandler_Reactivate(t *testing.T) {
	store := NewInMemoryTrustedKeyStore()
	handler := NewTrustedKeysHandler(store)

	jwk := generateTestJWK(t)
	body := registerTrustedKeyRequest{
		KeyID:          "ext-key-3",
		JWK: jwk,
		Audience:     "svc",
		ValidFrom: strPtr("2026-01-01T00:00:00Z"),
	}
	bodyBytes, _ := json.Marshal(body)

	// Register
	req := httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	// Invalidate first
	req = httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted/ext-key-3/invalidate", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for invalidate, got %d", rec.Code)
	}

	// Reactivate
	req = httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted/ext-key-3/reactivate", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for reactivate, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify active via store
	tk, err := store.Get("ext-key-3")
	if err != nil {
		t.Fatalf("failed to get key: %v", err)
	}
	if !tk.Active {
		t.Error("expected key to be active after reactivation")
	}
}

func TestTrustedKeysHandler_Delete(t *testing.T) {
	store := NewInMemoryTrustedKeyStore()
	handler := NewTrustedKeysHandler(store)

	jwk := generateTestJWK(t)
	body := registerTrustedKeyRequest{
		KeyID:          "ext-key-4",
		JWK: jwk,
		Audience:     "svc",
		ValidFrom: strPtr("2026-01-01T00:00:00Z"),
	}
	bodyBytes, _ := json.Marshal(body)

	// Register
	req := httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	// Delete
	req = httptest.NewRequest(http.MethodDelete, "/oauth/keys/trusted/ext-key-4", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify list is empty
	req = httptest.NewRequest(http.MethodGet, "/oauth/keys/trusted", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var list []trustedKeyInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list after delete, got %d", len(list))
	}
}

func TestTrustedKeysHandler_RegisterInvalidJWK(t *testing.T) {
	store := NewInMemoryTrustedKeyStore()
	handler := NewTrustedKeysHandler(store)

	body := registerTrustedKeyRequest{
		KeyID:    "bad-key",
		JWK:     json.RawMessage(`{"kty":"RSA"}`),
		Audience: "svc",
		ValidFrom: strPtr("2026-01-01T00:00:00Z"),
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTrustedKeysHandler_DeleteNotFound(t *testing.T) {
	store := NewInMemoryTrustedKeyStore()
	handler := NewTrustedKeysHandler(store)

	req := httptest.NewRequest(http.MethodDelete, "/oauth/keys/trusted/nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
