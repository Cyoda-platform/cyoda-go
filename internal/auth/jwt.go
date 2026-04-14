package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// Sign creates a signed RS256 JWT token.
func Sign(claims map[string]any, privateKey *rsa.PrivateKey, kid string) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal claims: %w", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64
	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("signing failed: %w", err)
	}

	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sigB64, nil
}

// ParsedToken holds a parsed but not yet verified JWT.
type ParsedToken struct {
	Header       map[string]any
	Claims       map[string]any
	Signature    []byte
	SigningInput string
}

// Parse splits and decodes a JWT without verifying.
func Parse(tokenString string) (*ParsedToken, error) {
	parts := strings.SplitN(tokenString, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token: expected 3 parts, got %d", len(parts))
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid header encoding: %w", err)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid claims encoding: %w", err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding: %w", err)
	}

	var header map[string]any
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("invalid header JSON: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, fmt.Errorf("invalid claims JSON: %w", err)
	}

	return &ParsedToken{
		Header:       header,
		Claims:       claims,
		Signature:    sigBytes,
		SigningInput: parts[0] + "." + parts[1],
	}, nil
}

// Verify checks the RS256 signature. ALWAYS uses SHA256 regardless of alg header.
func Verify(signingInput string, signature []byte, publicKey *rsa.PublicKey) error {
	hash := sha256.Sum256([]byte(signingInput))
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash[:], signature)
}

// ValidateClaims checks exp and iat claims.
func ValidateClaims(claims map[string]any, clockSkew time.Duration) error {
	now := time.Now()
	if exp, ok := claims["exp"].(float64); ok {
		expTime := time.Unix(int64(exp), 0)
		if now.After(expTime.Add(clockSkew)) {
			return fmt.Errorf("token expired")
		}
	} else {
		return fmt.Errorf("missing exp claim")
	}
	iat, ok := claims["iat"].(float64)
	if !ok {
		return fmt.Errorf("missing iat claim")
	}
	iatTime := time.Unix(int64(iat), 0)
	if iatTime.After(now.Add(clockSkew)) {
		return fmt.Errorf("iat is in the future")
	}

	// Validate nbf (not before) if present.
	if nbf, ok := claims["nbf"].(float64); ok {
		nbfTime := time.Unix(int64(nbf), 0)
		if now.Before(nbfTime.Add(-clockSkew)) {
			return fmt.Errorf("token not yet valid (nbf)")
		}
	}

	return nil
}

// decodeBase64URL decodes a base64url string, tolerating trailing '=' padding.
func decodeBase64URL(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

// ParseRSAPrivateKeyFromPEM parses a PEM-encoded RSA private key.
func ParseRSAPrivateKeyFromPEM(pemData []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 format
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA private key")
	}
	return rsaKey, nil
}
