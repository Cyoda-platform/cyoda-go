package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/auth"
)

// Regression test for issue #68 item 9 (originally surfaced as #97): if the
// JWKS cache is keyed on `kid` alone, then two issuers advertising different
// keys under the same `kid` can confuse the validator — a token signed by
// issuer A's key but claiming issuer B can be accepted because a cache lookup
// on `kid` returns whichever key was loaded first.
//
// The fix is to key the cache on (issuer, kid). This test exercises the
// behavioural contract end-to-end: two httptest JWKS endpoints publish
// distinct keys under the same kid, two validators are bound to the
// respective issuers, and we verify cross-issuer tokens are rejected.

func jwksJSONForKey(t *testing.T, kid string, pub *rsa.PublicKey) []byte {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	body, err := json.Marshal(map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"kid": kid,
				"use": "sig",
				"alg": "RS256",
				"n":   n,
				"e":   e,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return body
}

func TestJWKSValidator_CrossIssuerSignatureConfusionRejected(t *testing.T) {
	// Two distinct issuer key pairs, both advertising the same KID.
	keyA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen keyA: %v", err)
	}
	keyB, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen keyB: %v", err)
	}

	const sharedKID = "shared-kid"

	bodyA := jwksJSONForKey(t, sharedKID, &keyA.PublicKey)
	bodyB := jwksJSONForKey(t, sharedKID, &keyB.PublicKey)

	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bodyA)
	}))
	t.Cleanup(srvA.Close)

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bodyB)
	}))
	t.Cleanup(srvB.Close)

	const issuerA = "https://issuer.example/A"
	const issuerB = "https://issuer.example/B"

	srcA := auth.NewHTTPJWKSSourceWithTransportForTesting(srvA.URL, issuerA, time.Minute, &http.Transport{})
	srcB := auth.NewHTTPJWKSSourceWithTransportForTesting(srvB.URL, issuerB, time.Minute, &http.Transport{})

	vA := auth.NewValidatorFromSource(srcA, issuerA)
	vB := auth.NewValidatorFromSource(srcB, issuerB)

	now := float64(time.Now().Unix())
	exp := float64(time.Now().Add(time.Hour).Unix())

	// Confusion attempt 1: token signed with keyA but claiming iss=issuerB.
	// Validator B should reject (signature won't match keyB which is what
	// validator B's source serves under sharedKID).
	confusionToken := signTestToken(t, keyA, sharedKID, map[string]any{
		"iss":          issuerB, // claim wrong issuer
		"exp":          exp,
		"iat":          now,
		"caas_user_id": "attacker",
		"caas_org_id":  "org-attack",
	})
	if _, err := vB.Validate(confusionToken); err == nil {
		t.Fatal("validator B accepted a token signed by issuer A's key — cross-issuer confusion not blocked")
	}

	// Confusion attempt 2: token signed with keyA and correctly claiming
	// iss=issuerA, but presented to validator B. Validator B must reject on
	// issuer mismatch even before reaching the signature check; importantly
	// it must not somehow cache or surface keyA's key under sharedKID for
	// itself.
	correctlyClaimedToken := signTestToken(t, keyA, sharedKID, map[string]any{
		"iss":          issuerA,
		"exp":          exp,
		"iat":          now,
		"caas_user_id": "userA",
		"caas_org_id":  "org-A",
	})
	if _, err := vB.Validate(correctlyClaimedToken); err == nil {
		t.Fatal("validator B accepted a token whose iss claim doesn't match its configured issuer")
	}

	// Sanity: legitimate single-issuer paths still work.
	tokenA := signTestToken(t, keyA, sharedKID, map[string]any{
		"iss":          issuerA,
		"exp":          exp,
		"iat":          now,
		"caas_user_id": "userA",
		"caas_org_id":  "org-A",
	})
	if _, err := vA.Validate(tokenA); err != nil {
		t.Fatalf("validator A rejected its own legitimate token: %v", err)
	}

	tokenB := signTestToken(t, keyB, sharedKID, map[string]any{
		"iss":          issuerB,
		"exp":          exp,
		"iat":          now,
		"caas_user_id": "userB",
		"caas_org_id":  "org-B",
	})
	if _, err := vB.Validate(tokenB); err != nil {
		t.Fatalf("validator B rejected its own legitimate token: %v", err)
	}
}

// TestHTTPJWKSSource_DistinctIssuersServeDistinctKeysUnderSameKID exercises
// the issuer-bound cache contract directly at the KeySource level: two
// sources (one per issuer) backed by JWKS endpoints that advertise
// different keys under the same KID must each return their own key — not
// the other source's. If a global, kid-only cache were in use across
// sources, the second GetKey could leak the first source's key.
func TestHTTPJWKSSource_DistinctIssuersServeDistinctKeysUnderSameKID(t *testing.T) {
	keyA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen keyA: %v", err)
	}
	keyB, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen keyB: %v", err)
	}

	const sharedKID = "shared"

	bodyA := jwksJSONForKey(t, sharedKID, &keyA.PublicKey)
	bodyB := jwksJSONForKey(t, sharedKID, &keyB.PublicKey)

	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bodyA)
	}))
	t.Cleanup(srvA.Close)

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bodyB)
	}))
	t.Cleanup(srvB.Close)

	srcA := auth.NewHTTPJWKSSourceWithTransportForTesting(srvA.URL, "iss-A", time.Minute, &http.Transport{})
	srcB := auth.NewHTTPJWKSSourceWithTransportForTesting(srvB.URL, "iss-B", time.Minute, &http.Transport{})

	gotA, err := srcA.GetKey(sharedKID)
	if err != nil {
		t.Fatalf("srcA.GetKey: %v", err)
	}
	gotB, err := srcB.GetKey(sharedKID)
	if err != nil {
		t.Fatalf("srcB.GetKey: %v", err)
	}

	if gotA.N.Cmp(keyA.N) != 0 || gotA.E != keyA.E {
		t.Errorf("srcA returned wrong key for shared kid; cache may have leaked across issuers")
	}
	if gotB.N.Cmp(keyB.N) != 0 || gotB.E != keyB.E {
		t.Errorf("srcB returned wrong key for shared kid; cache may have leaked across issuers")
	}
	if gotA.N.Cmp(gotB.N) == 0 {
		t.Errorf("srcA and srcB returned the same key under shared kid; issuer-bound cache violated")
	}

	// Lookup of a kid not present must surface ErrKeyNotFound, not a
	// cross-issuer hit.
	if _, err := srcA.GetKey("different-kid"); !errors.Is(err, auth.ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound for unknown kid, got %v", err)
	}
}
