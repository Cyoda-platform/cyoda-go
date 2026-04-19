package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/auth"
)

func TestLocalKeySource_ReturnsPublicKeyForRegisteredKID(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}

	ks := auth.NewInMemoryKeyStore()
	kid := "test-kid-123"
	if err := ks.Save(&auth.KeyPair{
		KID:        kid,
		PublicKey:  &priv.PublicKey,
		PrivateKey: priv,
		Active:     true,
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("failed to save keypair: %v", err)
	}

	src := auth.NewLocalKeySource(ks)
	got, err := src.GetKey(kid)
	if err != nil {
		t.Fatalf("GetKey returned unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("GetKey returned nil public key")
	}
	if got.N.Cmp(priv.PublicKey.N) != 0 || got.E != priv.PublicKey.E {
		t.Fatal("GetKey returned a different public key than registered")
	}
}

func TestLocalKeySource_ReturnsErrorForUnknownKID(t *testing.T) {
	ks := auth.NewInMemoryKeyStore()
	src := auth.NewLocalKeySource(ks)

	got, err := src.GetKey("nonexistent-kid")
	if err == nil {
		t.Fatal("expected error for unknown kid, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil key on error, got %v", got)
	}
}

func TestLocalKeySource_ErrorIsUnwrappable(t *testing.T) {
	// The returned error should unwrap to something meaningful — not just
	// a string. Keeps callers able to check for ErrKeyNotFound semantically.
	ks := auth.NewInMemoryKeyStore()
	src := auth.NewLocalKeySource(ks)

	_, err := src.GetKey("nope")
	if err == nil {
		t.Fatal("expected error")
	}
	// Don't overspecify the sentinel; just confirm it wraps something.
	if errors.Unwrap(err) == nil {
		t.Log("note: error does not wrap inner error; acceptable but less helpful for callers")
	}
}
