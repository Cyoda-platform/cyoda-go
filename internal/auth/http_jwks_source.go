package auth

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// httpJWKSSource fetches RSA public keys from a JWKS HTTP endpoint, caching
// the result for cacheTTL. Its http.Transport pins TLS 1.3 as the minimum
// version and the response must carry a JSON content-type — any deviation is
// treated as a potentially hostile substitution and rejected.
type httpJWKSSource struct {
	jwksURL   string
	cacheTTL  time.Duration
	client    *http.Client
	mu        sync.RWMutex
	cache     map[string]*rsa.PublicKey
	lastFetch time.Time
}

// NewHTTPJWKSSource returns a KeySource that fetches JWKS from the given URL.
// The client pins TLS 1.3 and validates Content-Type on each response.
func NewHTTPJWKSSource(jwksURL string, cacheTTL time.Duration) KeySource {
	return newHTTPJWKSSource(jwksURL, cacheTTL, defaultJWKSTransport())
}

// NewHTTPJWKSSourceWithTransportForTesting returns a KeySource with a
// caller-supplied transport. Reserved for tests that need to trust an
// httptest.Server's self-signed certificate. Production code MUST use
// NewHTTPJWKSSource; grep-enforced.
func NewHTTPJWKSSourceWithTransportForTesting(jwksURL string, cacheTTL time.Duration, transport *http.Transport) KeySource {
	return newHTTPJWKSSource(jwksURL, cacheTTL, transport)
}

// NewHTTPJWKSSourceWithRootCAsForTesting returns a KeySource built via the
// production transport assembly (TLS 1.3 pinned, no InsecureSkipVerify) with
// the given CertPool substituted as RootCAs. Tests use this to verify that
// the **production** MinVersion is TLS 1.3 end-to-end against an httptest
// TLS server — not just the test-transport variant. Production code MUST
// use NewHTTPJWKSSource; grep-enforced.
func NewHTTPJWKSSourceWithRootCAsForTesting(jwksURL string, cacheTTL time.Duration, rootCAs *x509.CertPool) KeySource {
	transport := defaultJWKSTransport()
	transport.TLSClientConfig.RootCAs = rootCAs
	return newHTTPJWKSSource(jwksURL, cacheTTL, transport)
}

func newHTTPJWKSSource(jwksURL string, cacheTTL time.Duration, transport *http.Transport) *httpJWKSSource {
	return &httpJWKSSource{
		jwksURL:  jwksURL,
		cacheTTL: cacheTTL,
		cache:    make(map[string]*rsa.PublicKey),
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
	}
}

// defaultJWKSTransport returns the production JWKS transport: TLS 1.3 minimum,
// system root CAs, no InsecureSkipVerify.
func defaultJWKSTransport() *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
		},
	}
}

func (s *httpJWKSSource) GetKey(kid string) (*rsa.PublicKey, error) {
	s.mu.RLock()
	key, found := s.cache[kid]
	stale := time.Since(s.lastFetch) > s.cacheTTL
	s.mu.RUnlock()

	if found && !stale {
		return key, nil
	}

	// Stale cache is treated as no cache: a refresh failure returns an error
	// even if a previously-cached key for this kid is still in memory. This
	// is fail-closed — better to reject a token than to validate against a
	// key we can no longer confirm is current. Matches pre-refactor behaviour.
	if err := s.refreshCache(); err != nil {
		return nil, fmt.Errorf("failed to refresh JWKS cache: %w", err)
	}

	s.mu.RLock()
	key, found = s.cache[kid]
	s.mu.RUnlock()

	if !found {
		return nil, fmt.Errorf("%w: %q", ErrKeyNotFound, kid)
	}
	return key, nil
}

// refreshCache fetches the JWKS endpoint and refreshes the key cache. It
// rejects any response whose Content-Type is not JSON-shaped.
func (s *httpJWKSSource) refreshCache() error {
	resp, err := s.client.Get(s.jwksURL)
	if err != nil {
		return fmt.Errorf("JWKS fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	if err := validateJWKSContentType(resp.Header.Get("Content-Type")); err != nil {
		return err
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

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = keys
	s.lastFetch = time.Now()
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

// validateJWKSContentType accepts application/json and application/jwk-set+json
// (RFC 7517 §8.5.1), with or without parameters like charset. Anything else —
// text/html, empty, application/xml — is rejected as a potentially hostile
// substitution of the JWKS response.
func validateJWKSContentType(header string) error {
	if header == "" {
		return fmt.Errorf("JWKS response missing Content-Type header")
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return fmt.Errorf("JWKS response has malformed Content-Type %q: %w", header, err)
	}
	mediaType = strings.ToLower(mediaType)
	switch mediaType {
	case "application/json", "application/jwk-set+json":
		return nil
	default:
		return fmt.Errorf("JWKS response has unexpected Content-Type %q (want application/json or application/jwk-set+json)", mediaType)
	}
}
