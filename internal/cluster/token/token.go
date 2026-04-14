package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrTokenExpired   = errors.New("token expired")
	ErrTokenInvalid   = errors.New("token invalid")
	ErrTokenTampered  = errors.New("token signature mismatch")
	ErrSecretTooShort = errors.New("HMAC secret must be at least 32 bytes")
)

type Claims struct {
	NodeID    string `json:"n"`
	TxRef     string `json:"t"`
	ExpiresAt int64  `json:"e"`
}

type Signer struct {
	secret []byte
}

func NewSigner(secret []byte) (*Signer, error) {
	if len(secret) < 32 {
		return nil, ErrSecretTooShort
	}
	return &Signer{secret: secret}, nil
}

func (s *Signer) Issue(nodeID, txRef string, expiresAt time.Time) (string, error) {
	claims := Claims{
		NodeID:    nodeID,
		TxRef:     txRef,
		ExpiresAt: expiresAt.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	sig := s.sign(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

func (s *Signer) Verify(tok string) (*Claims, error) {
	dot := -1
	for i := len(tok) - 1; i >= 0; i-- {
		if tok[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return nil, ErrTokenInvalid
	}

	payloadB64 := tok[:dot]
	sigB64 := tok[dot+1:]

	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, ErrTokenInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, ErrTokenInvalid
	}

	expected := s.sign(payload)
	if !hmac.Equal(sig, expected) {
		return nil, ErrTokenTampered
	}

	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrTokenInvalid
	}

	if time.Now().Unix() > claims.ExpiresAt {
		return nil, ErrTokenExpired
	}

	return &claims, nil
}

func (s *Signer) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}
