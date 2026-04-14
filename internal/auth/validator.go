package auth

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// JWKSValidator validates JWT tokens by fetching public keys from a JWKS endpoint.
type JWKSValidator struct {
	jwksURL   string
	issuer    string
	cache     map[string]*rsa.PublicKey
	lastFetch time.Time
	cacheTTL  time.Duration
	mu        sync.RWMutex
	client    *http.Client
}

// NewJWKSValidator creates a new JWKSValidator that fetches keys from jwksURL.
func NewJWKSValidator(jwksURL, issuer string, cacheTTL time.Duration) *JWKSValidator {
	return &JWKSValidator{
		jwksURL:  jwksURL,
		issuer:   issuer,
		cache:    make(map[string]*rsa.PublicKey),
		cacheTTL: cacheTTL,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Validate parses and validates a JWT token string, returning a UserContext on success.
func (v *JWKSValidator) Validate(tokenString string) (*common.UserContext, error) {
	parsed, err := Parse(tokenString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	kid, ok := parsed.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, fmt.Errorf("missing kid in token header")
	}

	publicKey, err := v.getKey(kid)
	if err != nil {
		return nil, err
	}

	if err := Verify(parsed.SigningInput, parsed.Signature, publicKey); err != nil {
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}

	if err := ValidateClaims(parsed.Claims, 30*time.Second); err != nil {
		return nil, fmt.Errorf("claims validation failed: %w", err)
	}

	iss, _ := parsed.Claims["iss"].(string)
	if iss != v.issuer {
		return nil, fmt.Errorf("untrusted token issuer")
	}

	uc, err := v.buildUserContext(parsed.Claims)
	if err != nil {
		return nil, fmt.Errorf("failed to build user context: %w", err)
	}

	return uc, nil
}

// getKey retrieves the public key for the given kid, refreshing the cache if needed.
func (v *JWKSValidator) getKey(kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key, found := v.cache[kid]
	stale := time.Since(v.lastFetch) > v.cacheTTL
	v.mu.RUnlock()

	if found && !stale {
		return key, nil
	}

	// Cache miss or stale — refresh
	if err := v.refreshCache(); err != nil {
		return nil, fmt.Errorf("failed to refresh JWKS cache: %w", err)
	}

	v.mu.RLock()
	key, found = v.cache[kid]
	v.mu.RUnlock()

	if !found {
		return nil, fmt.Errorf("kid %q not found in JWKS", kid)
	}

	return key, nil
}

// refreshCache fetches the JWKS endpoint and updates the key cache.
func (v *JWKSValidator) refreshCache() error {
	resp, err := v.client.Get(v.jwksURL)
	if err != nil {
		return fmt.Errorf("JWKS fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	// Limit response body to 1 MB to prevent OOM from misconfigured/compromised endpoints.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read JWKS response: %w", err)
	}

	keys, err := parseJWKSResponse(body)
	if err != nil {
		return fmt.Errorf("failed to parse JWKS response: %w", err)
	}

	v.mu.Lock()
	v.cache = keys
	v.lastFetch = time.Now()
	v.mu.Unlock()

	return nil
}

// parseJWKSResponse parses a JWKS JSON response into a map of kid to RSA public keys.
func parseJWKSResponse(body []byte) (map[string]*rsa.PublicKey, error) {
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			KID string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		return nil, fmt.Errorf("invalid JWKS JSON: %w", err)
	}

	result := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.KID == "" || k.Kty != "RSA" {
			continue
		}
		nBytes, err := decodeBase64URL(k.N)
		if err != nil {
			return nil, fmt.Errorf("invalid base64url for n (kid=%s): %w", k.KID, err)
		}
		eBytes, err := decodeBase64URL(k.E)
		if err != nil {
			return nil, fmt.Errorf("invalid base64url for e (kid=%s): %w", k.KID, err)
		}

		n := new(big.Int).SetBytes(nBytes)
		e := int(new(big.Int).SetBytes(eBytes).Int64())

		result[k.KID] = &rsa.PublicKey{N: n, E: e}
	}

	return result, nil
}

// buildUserContext extracts user information from JWT claims.
func (v *JWKSValidator) buildUserContext(claims map[string]any) (*common.UserContext, error) {
	userID, _ := claims["caas_user_id"].(string)
	if userID == "" {
		userID, _ = claims["sub"].(string)
	}
	if userID == "" {
		return nil, fmt.Errorf("missing user identity (caas_user_id or sub claim)")
	}

	orgID, _ := claims["caas_org_id"].(string)
	if orgID == "" {
		return nil, fmt.Errorf("missing caas_org_id claim")
	}

	// OBO tokens carry user_roles, client_credentials tokens carry scopes.
	// Try user_roles first (OBO), fall back to scopes (client_credentials).
	roles := extractStringSlice(claims["user_roles"])
	if len(roles) == 0 {
		roles = extractStringSlice(claims["scopes"])
	}

	return &common.UserContext{
		UserID:   userID,
		UserName: userID,
		Tenant: common.Tenant{
			ID:   common.TenantID(orgID),
			Name: orgID,
		},
		Roles: roles,
	}, nil
}

// extractStringSlice converts a claim value to []string, handling both []interface{} and []string.
func extractStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []string:
		result := make([]string, len(val))
		copy(result, val)
		return result
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}
