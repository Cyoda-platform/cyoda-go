package dispatch_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/cluster/dispatch"
)

// testSharedSecret is a 32-byte shared secret used across tests that need to
// construct a PeerAuth. Not the real production key.
var testSharedSecret = bytes.Repeat([]byte{0xAB}, 32)

// newTestPeerAuth builds a default PeerAuth (AEAD, 30s skew) from
// testSharedSecret. Test helper; production uses NewAEADPeerAuth in app.go.
func newTestPeerAuth(t *testing.T) dispatch.PeerAuth {
	t.Helper()
	auth, err := dispatch.NewAEADPeerAuth(testSharedSecret, 30*time.Second)
	if err != nil {
		t.Fatalf("newTestPeerAuth: %v", err)
	}
	return auth
}
