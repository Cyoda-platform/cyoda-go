package auth

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// trustedKIDPattern is the character whitelist enforced on trusted-key
// identifiers across every lifecycle endpoint (register, delete, invalidate,
// reactivate). Allowed: ASCII alphanumerics plus '-', '_', '.', length 1..128.
// Anything else (control characters, slashes, query syntax, JSON-breaking
// punctuation, unicode confusables) is rejected at the boundary so neither
// the persistence layer nor downstream logs ever see attacker-controlled
// fragments outside this safe set.
var trustedKIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// registerTrustedKeyRequest is the JSON body for POST /oauth/keys/trusted.
// Matches Cyoda Cloud's RegisterTrustedKeyRequest schema.
type registerTrustedKeyRequest struct {
	KeyID     string          `json:"keyId"`
	JWK       json.RawMessage `json:"jwk"`
	Audience  string          `json:"audience"`
	Issuers   []string        `json:"issuers,omitempty"`
	ValidFrom *string         `json:"validFrom,omitempty"`
	ValidTo   *string         `json:"validTo,omitempty"`
}

// trustedKeyInfoResponse is the JSON response for trusted key info.
type trustedKeyInfoResponse struct {
	KID       string  `json:"kid"`
	Audience  string  `json:"audience"`
	Active    bool    `json:"active"`
	ValidFrom string  `json:"validFrom"`
	ValidTo   *string `json:"validTo,omitempty"`
}

// TrustedKeysHandler handles HTTP requests for trusted key management.
type TrustedKeysHandler struct {
	trustedKeyStore TrustedKeyStore
}

// NewTrustedKeysHandler creates a new TrustedKeysHandler.
func NewTrustedKeysHandler(store TrustedKeyStore) *TrustedKeysHandler {
	return &TrustedKeysHandler{trustedKeyStore: store}
}

// ServeHTTP routes requests based on method and path. Requires ROLE_ADMIN.
func (h *TrustedKeysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")

	// POST /oauth/keys/trusted/{keyId}/invalidate
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/invalidate") {
		h.handleInvalidate(w, r, path)
		return
	}

	// POST /oauth/keys/trusted/{keyId}/reactivate
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/reactivate") {
		h.handleReactivate(w, r, path)
		return
	}

	// GET /oauth/keys/trusted
	if r.Method == http.MethodGet && path == "/oauth/keys/trusted" {
		h.handleList(w, r)
		return
	}

	// POST /oauth/keys/trusted
	if r.Method == http.MethodPost && path == "/oauth/keys/trusted" {
		h.handleRegister(w, r)
		return
	}

	// DELETE /oauth/keys/trusted/{keyId}
	if r.Method == http.MethodDelete {
		h.handleDelete(w, r, path)
		return
	}

	common.WriteError(w, r, common.Operational(
		http.StatusNotFound, common.ErrCodeNotFound, "not found"))
}

func (h *TrustedKeysHandler) handleList(w http.ResponseWriter, _ *http.Request) {
	keys := h.trustedKeyStore.List()
	resp := make([]trustedKeyInfoResponse, 0, len(keys))
	for _, tk := range keys {
		resp = append(resp, toTrustedKeyInfoResponse(tk))
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Headers/body have already been flushed; emitting a second
		// http.Error here would only append garbage. Log and let the
		// client observe the truncated stream — same convention as
		// the rest of the codebase (see internal/common/errors.go,
		// internal/api/admin.go).
		slog.Debug("failed to encode response", "pkg", "auth", "error", err)
	}
}

func (h *TrustedKeysHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	// Limit request body to 1MB to prevent abuse.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req registerTrustedKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.WriteError(w, r, common.Operational(
			http.StatusBadRequest, common.ErrCodeBadRequest, "invalid request body"))
		return
	}

	if !trustedKIDPattern.MatchString(req.KeyID) {
		slog.Info("trusted key register: invalid keyId format", "kid", req.KeyID)
		common.WriteError(w, r, common.Operational(
			http.StatusBadRequest, common.ErrCodeBadRequest, "invalid keyId format"))
		return
	}

	pubKey, err := parseRSAPublicKeyFromJWK(req.JWK)
	if err != nil {
		common.WriteError(w, r, common.Operational(
			http.StatusBadRequest, common.ErrCodeBadRequest, "invalid jwk"))
		return
	}

	validFrom := time.Now()
	if req.ValidFrom != nil {
		parsed, err := time.Parse(time.RFC3339, *req.ValidFrom)
		if err != nil {
			common.WriteError(w, r, common.Operational(
				http.StatusBadRequest, common.ErrCodeBadRequest, "invalid validFrom: expected RFC3339 format"))
			return
		}
		validFrom = parsed
	}

	var validTo *time.Time
	if req.ValidTo != nil {
		t, err := time.Parse(time.RFC3339, *req.ValidTo)
		if err != nil {
			common.WriteError(w, r, common.Operational(
				http.StatusBadRequest, common.ErrCodeBadRequest, "invalid validTo: expected RFC3339 format"))
			return
		}
		validTo = &t
	}

	tk := &TrustedKey{
		KID:       req.KeyID,
		PublicKey: pubKey,
		Audience:  req.Audience,
		Issuers:   req.Issuers,
		Active:    true,
		ValidFrom: validFrom,
		ValidTo:   validTo,
	}

	if err := h.trustedKeyStore.Register(tk); err != nil {
		// Forward classified AppErrors verbatim (e.g. 409 registry-full
		// from #34/2). Anything else is a 5xx — route through
		// common.Internal so the body is the generic ticket shape and the
		// raw error stays in the slog record (#68 item 14).
		var appErr *common.AppError
		if errors.As(err, &appErr) {
			common.WriteError(w, r, appErr)
			return
		}
		common.WriteError(w, r, common.Internal("register trusted key", err))
		return
	}

	resp := toTrustedKeyInfoResponse(tk)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Debug("failed to encode response", "error", err)
	}
}

func (h *TrustedKeysHandler) handleDelete(w http.ResponseWriter, r *http.Request, path string) {
	keyID, ok := validateLifecycleKID(w, r, path, "/oauth/keys/trusted/")
	if !ok {
		return
	}
	if err := h.trustedKeyStore.Delete(keyID); err != nil {
		// Generic client message; full detail logged server-side (#34 item 6).
		// errorCode TRUSTED_KEY_NOT_FOUND so the 404 is programmatically
		// distinguishable from BAD_REQUEST 400s (#34/6 follow-up).
		slog.Info("trusted-key delete: not found", "kid", keyID, "err", err.Error())
		common.WriteError(w, r, common.Operational(http.StatusNotFound, common.ErrCodeTrustedKeyNotFound, "key not found"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *TrustedKeysHandler) handleInvalidate(w http.ResponseWriter, r *http.Request, path string) {
	// path: /oauth/keys/trusted/{keyId}/invalidate
	trimmed := strings.TrimSuffix(path, "/invalidate")
	keyID, ok := validateLifecycleKID(w, r, trimmed, "/oauth/keys/trusted/")
	if !ok {
		return
	}
	if err := h.trustedKeyStore.Invalidate(keyID); err != nil {
		// Generic client message; full detail logged server-side (#68 item 14).
		// errorCode TRUSTED_KEY_NOT_FOUND for coherence with HTTP 404 status
		// (#34/6 follow-up).
		slog.Info("trusted-key invalidate: not found", "kid", keyID, "err", err.Error())
		common.WriteError(w, r, common.Operational(http.StatusNotFound, common.ErrCodeTrustedKeyNotFound, "key not found"))
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *TrustedKeysHandler) handleReactivate(w http.ResponseWriter, r *http.Request, path string) {
	// path: /oauth/keys/trusted/{keyId}/reactivate
	trimmed := strings.TrimSuffix(path, "/reactivate")
	keyID, ok := validateLifecycleKID(w, r, trimmed, "/oauth/keys/trusted/")
	if !ok {
		return
	}
	if err := h.trustedKeyStore.Reactivate(keyID); err != nil {
		// Generic client message; full detail logged server-side (#68 item 14).
		// errorCode TRUSTED_KEY_NOT_FOUND for coherence with HTTP 404 status
		// (#34/6 follow-up).
		slog.Info("trusted-key reactivate: not found", "kid", keyID, "err", err.Error())
		common.WriteError(w, r, common.Operational(http.StatusNotFound, common.ErrCodeTrustedKeyNotFound, "key not found"))
		return
	}
	w.WriteHeader(http.StatusOK)
}

// validateLifecycleKID extracts the {keyId} path segment and rejects
// anything that fails the trustedKIDPattern whitelist with a 400 problem
// detail. Returns (keyID, true) on success or ("", false) when the
// response has already been written.
func validateLifecycleKID(w http.ResponseWriter, r *http.Request, path, prefix string) (string, bool) {
	keyID := extractKeyID(path, prefix)
	if !trustedKIDPattern.MatchString(keyID) {
		slog.Info("trusted key lifecycle: invalid keyId format", "kid", keyID, "path", r.URL.Path)
		common.WriteError(w, r, common.Operational(
			http.StatusBadRequest, common.ErrCodeBadRequest, "invalid keyId format"))
		return "", false
	}
	return keyID, true
}

// extractKeyID returns the key ID from a path like /oauth/keys/trusted/{keyId}.
func extractKeyID(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	kid := strings.TrimPrefix(path, prefix)
	if kid == "" || strings.Contains(kid, "/") {
		return ""
	}
	return kid
}

// parseRSAPublicKeyFromJWK parses an RSA public key from a JWK JSON object.
func parseRSAPublicKeyFromJWK(jwkData json.RawMessage) (*rsa.PublicKey, error) {
	if len(jwkData) == 0 {
		return nil, fmt.Errorf("empty JWK")
	}
	var jwk struct {
		Kty string `json:"kty"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	if err := json.Unmarshal(jwkData, &jwk); err != nil {
		return nil, fmt.Errorf("failed to parse JWK: %w", err)
	}
	if jwk.Kty != "RSA" {
		return nil, fmt.Errorf("unsupported key type: %s (only RSA supported)", jwk.Kty)
	}
	if jwk.N == "" || jwk.E == "" {
		return nil, fmt.Errorf("missing n or e in JWK")
	}

	nBytes, err := decodeBase64URL(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("invalid n value: %w", err)
	}
	eBytes, err := decodeBase64URL(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("invalid e value: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	eBig := new(big.Int).SetBytes(eBytes)
	e, err := validateRSAPublicExponent(eBig)
	if err != nil {
		return nil, err
	}

	return &rsa.PublicKey{N: n, E: e}, nil
}

// validateRSAPublicExponent enforces the integrity invariants on an RSA
// public-key exponent (#34 item 4): positive, fits in int, and odd. RFC 3447
// allows e in [3, 2^256-1] and the practical universe of public exponents
// (3, 17, 65537) all fit comfortably; rejecting anything that overflows int
// avoids the silent-truncation hazard at int(big.Int.Int64()) call sites.
func validateRSAPublicExponent(e *big.Int) (int, error) {
	if e.Sign() <= 0 {
		return 0, fmt.Errorf("rsa exponent must be positive")
	}
	if !e.IsInt64() {
		return 0, fmt.Errorf("rsa exponent does not fit in int64")
	}
	v := e.Int64()
	if v > int64(math.MaxInt) {
		return 0, fmt.Errorf("rsa exponent does not fit in int")
	}
	if v&1 == 0 {
		return 0, fmt.Errorf("rsa exponent must be odd")
	}
	return int(v), nil
}

// toTrustedKeyInfoResponse converts a TrustedKey to its JSON response representation.
func toTrustedKeyInfoResponse(tk *TrustedKey) trustedKeyInfoResponse {
	resp := trustedKeyInfoResponse{
		KID:       tk.KID,
		Audience:  tk.Audience,
		Active:    tk.Active,
		ValidFrom: tk.ValidFrom.Format(time.RFC3339),
	}
	if tk.ValidTo != nil {
		s := tk.ValidTo.Format(time.RFC3339)
		resp.ValidTo = &s
	}
	return resp
}
