package dispatch_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/cluster/dispatch"
)

// testSecret is a 32-byte shared secret used across AEAD tests. Not the
// real production key — purely for deterministic test construction.
var testSecret = bytes.Repeat([]byte{0xAB}, 32)

// aeadFixedClock returns a stable time function for tests that manipulate time.
func aeadFixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// signAndBuildRequest signs body and returns an http.Request ready to Verify.
// Mirrors the real forwarder.forward path without needing a server.
func signAndBuildRequest(t *testing.T, auth dispatch.PeerAuth, method, path string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	wire, err := auth.Sign(req, body)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	// Install the wire body as the request body (Sign doesn't attach it —
	// that's the caller's responsibility, matching forwarder.forward).
	req.Body = io.NopCloser(bytes.NewReader(wire))
	req.ContentLength = int64(len(wire))
	return req
}

func TestAEADPeerAuth_RoundTrip(t *testing.T) {
	a, err := dispatch.NewAEADPeerAuth(testSecret, 30*time.Second)
	if err != nil {
		t.Fatalf("NewAEADPeerAuth: %v", err)
	}

	plaintext := []byte(`{"hello":"world"}`)
	req := signAndBuildRequest(t, a, "POST", "/internal/dispatch/processor", plaintext)

	got, id, err := a.Verify(req)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", got, plaintext)
	}
	if id.AuthMethod() != "aead-v1" {
		t.Fatalf("identity auth method = %q, want aead-v1", id.AuthMethod())
	}
	if id.NodeID() != "" {
		t.Fatalf("shared-key identity should have empty NodeID, got %q", id.NodeID())
	}
}

func TestAEADPeerAuth_RejectsShortSecret(t *testing.T) {
	short := bytes.Repeat([]byte{0xAB}, 31)
	if _, err := dispatch.NewAEADPeerAuth(short, 30*time.Second); err == nil {
		t.Fatal("expected error for secret < 32 bytes")
	}
}

func TestAEADPeerAuth_TamperedCiphertextFails(t *testing.T) {
	a, _ := dispatch.NewAEADPeerAuth(testSecret, 30*time.Second)
	req := signAndBuildRequest(t, a, "POST", "/internal/dispatch/processor", []byte(`{"a":1}`))

	wire, _ := io.ReadAll(req.Body)
	// Flip a byte in the ciphertext region (past the 12-byte nonce prefix).
	wire[len(wire)-1] ^= 0x01
	req.Body = io.NopCloser(bytes.NewReader(wire))

	if _, _, err := a.Verify(req); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestAEADPeerAuth_TamperedNonceFails(t *testing.T) {
	a, _ := dispatch.NewAEADPeerAuth(testSecret, 30*time.Second)
	req := signAndBuildRequest(t, a, "POST", "/internal/dispatch/processor", []byte(`{"a":1}`))
	wire, _ := io.ReadAll(req.Body)
	wire[0] ^= 0xFF
	req.Body = io.NopCloser(bytes.NewReader(wire))

	if _, _, err := a.Verify(req); err == nil {
		t.Fatal("tampered nonce was accepted")
	}
}

func TestAEADPeerAuth_CrossEndpointReplayRejected(t *testing.T) {
	a, _ := dispatch.NewAEADPeerAuth(testSecret, 30*time.Second)
	// Sign for processor, try to verify as criteria.
	req := signAndBuildRequest(t, a, "POST", "/internal/dispatch/processor", []byte(`{"a":1}`))
	wire, _ := io.ReadAll(req.Body)

	replay := httptest.NewRequest("POST", "/internal/dispatch/criteria", bytes.NewReader(wire))
	replay.Header.Set("X-Dispatch-Timestamp", req.Header.Get("X-Dispatch-Timestamp"))
	replay.Header.Set("Content-Type", req.Header.Get("Content-Type"))

	if _, _, err := a.Verify(replay); err == nil {
		t.Fatal("cross-endpoint replay (processor → criteria) was accepted")
	}
}

func TestAEADPeerAuth_MethodChangeRejected(t *testing.T) {
	a, _ := dispatch.NewAEADPeerAuth(testSecret, 30*time.Second)
	req := signAndBuildRequest(t, a, "POST", "/internal/dispatch/processor", []byte(`{"a":1}`))
	wire, _ := io.ReadAll(req.Body)

	replay := httptest.NewRequest("PUT", "/internal/dispatch/processor", bytes.NewReader(wire))
	replay.Header.Set("X-Dispatch-Timestamp", req.Header.Get("X-Dispatch-Timestamp"))
	replay.Header.Set("Content-Type", req.Header.Get("Content-Type"))

	if _, _, err := a.Verify(replay); err == nil {
		t.Fatal("method change (POST → PUT) was accepted")
	}
}

func TestAEADPeerAuth_StaleTimestampRejected(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	a, _ := dispatch.NewAEADPeerAuthWithClockForTesting(testSecret, 30*time.Second, aeadFixedClock(base))

	// Sign at T.
	req := httptest.NewRequest("POST", "/internal/dispatch/processor", nil)
	wire, err := a.Sign(req, []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ts := req.Header.Get("X-Dispatch-Timestamp")

	// Advance clock past skew window (60s > 30s).
	a.SetClockForTesting(aeadFixedClock(base.Add(60 * time.Second)))

	// Verify at T+60s — outside 30s skew.
	req2 := httptest.NewRequest("POST", "/internal/dispatch/processor", bytes.NewReader(wire))
	req2.Header.Set("X-Dispatch-Timestamp", ts)
	req2.Header.Set("Content-Type", "application/cyoda-dispatch-v1")

	_, _, err = a.Verify(req2)
	if err == nil {
		t.Fatal("expected stale-timestamp rejection, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timestamp") {
		t.Fatalf("expected timestamp error, got: %v", err)
	}
}

func TestAEADPeerAuth_ReplayWithinWindowRejected(t *testing.T) {
	a, _ := dispatch.NewAEADPeerAuth(testSecret, 30*time.Second)

	req := httptest.NewRequest("POST", "/internal/dispatch/processor", nil)
	wire, err := a.Sign(req, []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ts := req.Header.Get("X-Dispatch-Timestamp")

	buildReq := func() *http.Request {
		r := httptest.NewRequest("POST", "/internal/dispatch/processor", bytes.NewReader(wire))
		r.Header.Set("X-Dispatch-Timestamp", ts)
		r.Header.Set("Content-Type", "application/cyoda-dispatch-v1")
		return r
	}

	if _, _, err := a.Verify(buildReq()); err != nil {
		t.Fatalf("first verify should succeed: %v", err)
	}
	if _, _, err := a.Verify(buildReq()); err == nil {
		t.Fatal("replay within window should be rejected, got nil error")
	}
}

func TestAEADPeerAuth_DifferentSecretsIncompatible(t *testing.T) {
	a1, _ := dispatch.NewAEADPeerAuth(testSecret, 30*time.Second)
	otherSecret := bytes.Repeat([]byte{0xCD}, 32)
	a2, _ := dispatch.NewAEADPeerAuth(otherSecret, 30*time.Second)

	req := httptest.NewRequest("POST", "/internal/dispatch/processor", nil)
	wire, _ := a1.Sign(req, []byte(`{"a":1}`))
	ts := req.Header.Get("X-Dispatch-Timestamp")

	recv := httptest.NewRequest("POST", "/internal/dispatch/processor", bytes.NewReader(wire))
	recv.Header.Set("X-Dispatch-Timestamp", ts)
	recv.Header.Set("Content-Type", "application/cyoda-dispatch-v1")

	if _, _, err := a2.Verify(recv); err == nil {
		t.Fatal("a2 accepted an envelope sealed by a1 with a different secret")
	}
}

func TestAEADPeerAuth_DerivedKeyDiffersFromRawSecret(t *testing.T) {
	// Ensures HKDF is actually running. If the impl ever regresses to using
	// the raw secret directly, this test fails.
	derived := dispatch.DeriveDispatchKeyForTesting(testSecret)
	if bytes.Equal(derived, testSecret) {
		t.Fatal("derived dispatch key equals raw secret — HKDF not applied")
	}
	if len(derived) != 32 {
		t.Fatalf("derived key length = %d, want 32", len(derived))
	}
}

func TestAEADPeerAuth_WireBodyIsNotPlaintextJSON(t *testing.T) {
	// Confidentiality smoke test: the wire body does not contain the
	// plaintext JSON secret, proving the envelope encrypts rather than
	// merely authenticates.
	a, _ := dispatch.NewAEADPeerAuth(testSecret, 30*time.Second)
	secret := `{"secret_marker":"SENSITIVE_PAYLOAD_7f3a9b"}`

	req := httptest.NewRequest("POST", "/internal/dispatch/processor", nil)
	wire, err := a.Sign(req, []byte(secret))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if strings.Contains(string(wire), "SENSITIVE_PAYLOAD_7f3a9b") {
		t.Fatal("wire body contains plaintext marker — payload is not encrypted")
	}
	if strings.Contains(string(wire), "secret_marker") {
		t.Fatal("wire body contains plaintext JSON key — payload is not encrypted")
	}
}
