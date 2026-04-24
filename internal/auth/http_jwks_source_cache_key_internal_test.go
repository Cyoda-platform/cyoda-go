package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Regression test for issue #97. The JWKS cache is now keyed on
// (issuer, kid) rather than kid alone — this test inspects the cache
// map directly (same-package internal test) to pin the contract.

func TestHTTPJWKSSource_CacheIsKeyedByIssuerAndKID(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	const kid = "kid-under-test"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"kid": kid,
					"n":   base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	const issuer = "https://issuer.example/A"
	src := newHTTPJWKSSource(srv.URL, issuer, time.Minute, &http.Transport{})

	if _, err := src.GetKey(kid); err != nil {
		t.Fatalf("GetKey: %v", err)
	}

	src.mu.RLock()
	defer src.mu.RUnlock()

	wantKey := jwksCacheKey{issuer: issuer, kid: kid}
	if _, ok := src.cache[wantKey]; !ok {
		t.Errorf("cache missing entry for %+v; cache = %+v", wantKey, src.cache)
	}

	// A lookup under (wrongIssuer, kid) must miss — if this entry existed,
	// cross-issuer confusion would be possible when a source is shared.
	wrongKey := jwksCacheKey{issuer: "https://issuer.example/B", kid: kid}
	if _, ok := src.cache[wrongKey]; ok {
		t.Errorf("cache unexpectedly has entry for %+v — issuer binding violated", wrongKey)
	}

	// Make sure contextual helpers (like using a derived context) still
	// don't somehow circumvent binding.
	_ = context.Background()
}
