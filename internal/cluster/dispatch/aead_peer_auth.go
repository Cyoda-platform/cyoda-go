package dispatch

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/hkdf"
)

// Wire-level constants. Versioned in the Content-Type so a future envelope
// format can coexist with this one if a migration is ever required.
const (
	DispatchContentType  = "application/cyoda-dispatch-v1"
	DispatchTimestampHdr = "X-Dispatch-Timestamp"

	// dispatchMaxBodySize caps how much an attacker can force the handler
	// to buffer before we reject the envelope.
	dispatchMaxBodySize = 10 * 1024 * 1024

	// nonceCacheCapacity is the replay-cache ceiling — see nonceCache.
	nonceCacheCapacity = 100_000

	// hkdfInfo separates the dispatch key from memberlist's gossip key
	// derived from the same shared secret. Do not change: it's the binding
	// between key material versions.
	hkdfInfo = "cyoda-dispatch-v1"

	authMethodAEADv1 = "aead-v1"
)

// ErrSharedSecretTooShort is returned by NewAEADPeerAuth when the provided
// shared secret is under the 32-byte minimum.
var ErrSharedSecretTooShort = errors.New("shared secret must be at least 32 bytes")

// AEADPeerAuth implements PeerAuth using AES-256-GCM with an HKDF-derived key.
//
// On the wire, the request body is [nonce(12) || ciphertext||tag]. The
// associated data binds HTTP method, path, and timestamp — preventing
// cross-endpoint replays without re-encrypting on each route. A sliding
// nonce cache rejects repeated envelopes within the skew window.
type AEADPeerAuth struct {
	gcm     cipher.AEAD
	nonces  *nonceCache
	skew    time.Duration
	clockFn func() time.Time
}

// NewAEADPeerAuth returns an AEADPeerAuth keyed by HKDF-SHA256 over the
// given shared secret. The secret is the same cluster-wide value as
// CYODA_HMAC_SECRET; HKDF separates the dispatch key from the memberlist
// gossip key so a compromise of one primitive does not extend to the other.
func NewAEADPeerAuth(sharedSecret []byte, skew time.Duration) (*AEADPeerAuth, error) {
	return newAEADPeerAuth(sharedSecret, skew, time.Now)
}

// NewAEADPeerAuthWithClockForTesting is an AEADPeerAuth whose notion of "now"
// is controlled by the caller. Reserved for tests that exercise timestamp
// skew and replay-TTL behaviour. Production code MUST use NewAEADPeerAuth.
func NewAEADPeerAuthWithClockForTesting(sharedSecret []byte, skew time.Duration, clockFn func() time.Time) (*AEADPeerAuth, error) {
	return newAEADPeerAuth(sharedSecret, skew, clockFn)
}

func newAEADPeerAuth(sharedSecret []byte, skew time.Duration, clockFn func() time.Time) (*AEADPeerAuth, error) {
	if len(sharedSecret) < 32 {
		return nil, ErrSharedSecretTooShort
	}
	key := deriveDispatchKey(sharedSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("build AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build AES-GCM: %w", err)
	}
	return &AEADPeerAuth{
		gcm:     gcm,
		nonces:  newNonceCache(skew*2, nonceCacheCapacity, clockFn),
		skew:    skew,
		clockFn: clockFn,
	}, nil
}

// SetClockForTesting swaps the clock function for both the AEAD and its
// nonce cache. Reserved for tests that need to manipulate observed time.
// Production code MUST NOT call this.
func (a *AEADPeerAuth) SetClockForTesting(clockFn func() time.Time) {
	a.clockFn = clockFn
	a.nonces.nowFn = clockFn
}

// DeriveDispatchKeyForTesting exposes the HKDF key derivation for a single
// test that proves derivation actually runs. Production code uses it
// internally only.
func DeriveDispatchKeyForTesting(sharedSecret []byte) []byte {
	return deriveDispatchKey(sharedSecret)
}

func deriveDispatchKey(sharedSecret []byte) []byte {
	r := hkdf.New(sha256.New, sharedSecret, nil, []byte(hkdfInfo))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		// hkdf.New's Reader cannot fail to produce 32 bytes from any
		// non-empty secret; guard is belt-and-suspenders.
		panic(fmt.Sprintf("hkdf derivation failed: %v", err))
	}
	return out
}

// Sign wraps body in an AEAD envelope, sets the Content-Type and timestamp
// headers, and returns the wire bytes. The caller replaces the request body
// with the returned slice.
func (a *AEADPeerAuth) Sign(req *http.Request, body []byte) ([]byte, error) {
	ts := strconv.FormatInt(a.clockFn().Unix(), 10)

	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	ad := buildAD(req.Method, req.URL.Path, ts)
	ct := a.gcm.Seal(nil, nonce, body, ad)

	wire := make([]byte, 0, len(nonce)+len(ct))
	wire = append(wire, nonce...)
	wire = append(wire, ct...)

	req.Header.Set("Content-Type", DispatchContentType)
	req.Header.Set(DispatchTimestampHdr, ts)
	return wire, nil
}

// Verify validates the request's timestamp skew, AEAD envelope, and nonce
// freshness. On success it returns the decrypted plaintext and a PeerIdentity
// describing the authenticated peer.
func (a *AEADPeerAuth) Verify(r *http.Request) ([]byte, PeerIdentity, error) {
	tsStr := r.Header.Get(DispatchTimestampHdr)
	if tsStr == "" {
		return nil, PeerIdentity{}, errors.New("missing X-Dispatch-Timestamp header")
	}
	tsUnix, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return nil, PeerIdentity{}, fmt.Errorf("malformed timestamp: %w", err)
	}
	tsTime := time.Unix(tsUnix, 0)

	// Skew check happens first — cheap rejection of stale/future envelopes
	// before we touch the body.
	now := a.clockFn()
	diff := now.Sub(tsTime)
	if diff < 0 {
		diff = -diff
	}
	if diff > a.skew {
		return nil, PeerIdentity{}, fmt.Errorf("timestamp outside skew window: %v > %v", diff, a.skew)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, dispatchMaxBodySize))
	if err != nil {
		return nil, PeerIdentity{}, fmt.Errorf("read body: %w", err)
	}

	nonceSize := a.gcm.NonceSize()
	if len(body) < nonceSize+a.gcm.Overhead() {
		return nil, PeerIdentity{}, errors.New("body too short for AEAD envelope")
	}
	nonce := body[:nonceSize]
	ct := body[nonceSize:]

	ad := buildAD(r.Method, r.URL.Path, tsStr)
	pt, err := a.gcm.Open(nil, nonce, ct, ad)
	if err != nil {
		return nil, PeerIdentity{}, fmt.Errorf("AEAD open failed: %w", err)
	}

	// Record the nonce only after successful decrypt. A flood of bogus
	// nonces that fail AEAD.Open never enters the cache.
	if a.nonces.checkAndRecord(nonce, tsTime) {
		return nil, PeerIdentity{}, errors.New("duplicate nonce — replay rejected")
	}

	return pt, PeerIdentity{authMethod: authMethodAEADv1}, nil
}

// buildAD constructs the Associated Data for AES-GCM. Binding method, path,
// and timestamp to ciphertext prevents cross-endpoint and timestamp-strip
// replays without adding an outer signature.
func buildAD(method, path, ts string) []byte {
	ad := make([]byte, 0, len(method)+1+len(path)+1+len(ts))
	ad = append(ad, method...)
	ad = append(ad, '\n')
	ad = append(ad, path...)
	ad = append(ad, '\n')
	ad = append(ad, ts...)
	return ad
}
