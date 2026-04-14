package token_test

import (
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/cluster/token"
)

func TestNewSigner_SecretTooShort(t *testing.T) {
	_, err := token.NewSigner([]byte("short"))
	if err == nil {
		t.Fatal("expected error for short secret")
	}
	if err != token.ErrSecretTooShort {
		t.Errorf("expected ErrSecretTooShort, got %v", err)
	}
}

func TestNewSigner_SecretExactly32Bytes(t *testing.T) {
	secret := []byte("exactly-32-bytes-long-secret!!!!")
	if len(secret) != 32 {
		t.Fatalf("test setup: secret length = %d, want 32", len(secret))
	}
	s, err := token.NewSigner(secret)
	if err != nil {
		t.Fatalf("expected no error for 32-byte secret, got %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil signer")
	}
}

func TestRoundTrip(t *testing.T) {
	signer, _ := token.NewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	tok, err := signer.Issue("node-1", "tx-uuid-abc", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}

	claims, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", claims.NodeID, "node-1")
	}
	if claims.TxRef != "tx-uuid-abc" {
		t.Errorf("TxRef = %q, want %q", claims.TxRef, "tx-uuid-abc")
	}
}

func TestExpiredToken(t *testing.T) {
	signer, _ := token.NewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	tok, err := signer.Issue("node-1", "tx-uuid-abc", time.Now().Add(-1*time.Second))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	_, err = signer.Verify(tok)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if err != token.ErrTokenExpired {
		t.Errorf("err = %v, want ErrTokenExpired", err)
	}
}

func TestTamperedToken(t *testing.T) {
	signer, _ := token.NewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	tok, err := signer.Issue("node-1", "tx-uuid-abc", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	tampered := []byte(tok)
	tampered[len(tampered)/2] ^= 0xFF
	_, err = signer.Verify(string(tampered))
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestWrongSecret(t *testing.T) {
	signer1, _ := token.NewSigner([]byte("secret-one-at-least-32-bytes!!!!"))
	signer2, _ := token.NewSigner([]byte("secret-two-at-least-32-bytes!!!!"))

	tok, err := signer1.Issue("node-1", "tx-uuid-abc", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	_, err = signer2.Verify(tok)
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}
