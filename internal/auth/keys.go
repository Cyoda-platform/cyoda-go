package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// KeysHandler handles HTTP requests for key pair management.
type KeysHandler struct {
	keyStore KeyStore
}

// NewKeysHandler creates a new KeysHandler.
func NewKeysHandler(keyStore KeyStore) *KeysHandler {
	return &KeysHandler{keyStore: keyStore}
}

// keysInfoResponse is the JSON response for key pair info.
type keysInfoResponse struct {
	KID       string `json:"kid"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"createdAt"`
}

// ServeHTTP routes key pair management requests.
func (h *KeysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const basePath = "/oauth/keys/keypair"

	path := r.URL.Path
	if !strings.HasPrefix(path, basePath) {
		http.NotFound(w, r)
		return
	}

	// Strip base path to get the remainder
	remainder := strings.TrimPrefix(path, basePath)
	remainder = strings.TrimPrefix(remainder, "/")

	// Route: POST /oauth/keys/keypair (no remainder)
	if remainder == "" && r.Method == http.MethodPost {
		h.issueKeyPair(w, r)
		return
	}

	// Route: GET /oauth/keys/keypair/current
	if remainder == "current" && r.Method == http.MethodGet {
		h.getCurrent(w, r)
		return
	}

	// Routes with keyId: {keyId}, {keyId}/invalidate, {keyId}/reactivate
	parts := strings.SplitN(remainder, "/", 2)
	keyID := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	if keyID == "" {
		http.NotFound(w, r)
		return
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		h.deleteKeyPair(w, r, keyID)
	case action == "invalidate" && r.Method == http.MethodPost:
		h.invalidateKeyPair(w, r, keyID)
	case action == "reactivate" && r.Method == http.MethodPost:
		h.reactivateKeyPair(w, r, keyID)
	default:
		http.NotFound(w, r)
	}
}

func (h *KeysHandler) issueKeyPair(w http.ResponseWriter, _ *http.Request) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	kidBytes := make([]byte, 16)
	if _, err := rand.Read(kidBytes); err != nil {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}
	kid := hex.EncodeToString(kidBytes)

	now := time.Now().UTC()
	kp := &KeyPair{
		KID:        kid,
		PublicKey:  &privateKey.PublicKey,
		PrivateKey: privateKey,
		Active:     true,
		CreatedAt:  now,
	}

	if err := h.keyStore.Save(kp); err != nil {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	resp := keysInfoResponse{
		KID:       kid,
		Active:    true,
		CreatedAt: now.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func (h *KeysHandler) getCurrent(w http.ResponseWriter, _ *http.Request) {
	kp, err := h.keyStore.GetActive()
	if err != nil {
		http.Error(w, "no active key pair found", http.StatusNotFound)
		return
	}

	resp := keysInfoResponse{
		KID:       kp.KID,
		Active:    kp.Active,
		CreatedAt: kp.CreatedAt.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *KeysHandler) deleteKeyPair(w http.ResponseWriter, _ *http.Request, keyID string) {
	if err := h.keyStore.Delete(keyID); err != nil {
		http.Error(w, fmt.Sprintf("key pair not found: %s", keyID), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *KeysHandler) invalidateKeyPair(w http.ResponseWriter, _ *http.Request, keyID string) {
	if err := h.keyStore.Invalidate(keyID); err != nil {
		http.Error(w, fmt.Sprintf("key pair not found: %s", keyID), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *KeysHandler) reactivateKeyPair(w http.ResponseWriter, _ *http.Request, keyID string) {
	if err := h.keyStore.Reactivate(keyID); err != nil {
		http.Error(w, fmt.Sprintf("key pair not found: %s", keyID), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}
