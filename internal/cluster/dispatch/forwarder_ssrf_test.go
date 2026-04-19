package dispatch_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/cluster/dispatch"
)

// TestHTTPForwarder_RejectsLoopbackAddresses documents the SSRF guard on
// the cluster forwarder. Registry entries are trusted by default, but if
// an attacker can influence them (e.g. via a rogue node join, a config
// mistake, or a compromise of a peer) the forwarder must not proxy
// HMAC-signed requests to loopback or link-local addresses — doing so
// would let that attacker reach in-process databases, cloud metadata
// endpoints, or other services bound on 127.0.0.1.
//
// The guard is enforced at address validation time; the HTTP call must
// never be dialled.
func TestHTTPForwarder_RejectsLoopbackAddresses(t *testing.T) {
	fw := dispatch.NewHTTPForwarder([]byte("test-secret-long-enough-for-constructor-checks"), time.Second)

	cases := []string{
		"127.0.0.1:8080",
		"http://127.0.0.1:8080",
		"127.0.0.2:9000", // 127.0.0.0/8 range
		"localhost:8080",
		"[::1]:8080",
		"http://[::1]:8080",
	}
	for _, addr := range cases {
		_, err := fw.ForwardProcessor(context.Background(), addr, makeProcessorReq())
		if err == nil {
			t.Errorf("ForwardProcessor(%q) accepted loopback address", addr)
			continue
		}
		if !errors.Is(err, dispatch.ErrForbiddenPeerAddress) {
			t.Errorf("ForwardProcessor(%q) = %v, want wraps ErrForbiddenPeerAddress", addr, err)
		}
	}
}

// TestHTTPForwarder_RejectsLinkLocalAddresses blocks the AWS/GCP/Azure
// metadata-service SSRF vector. 169.254.169.254 is the canonical target;
// the broader 169.254.0.0/16 range and IPv6 fe80::/10 are equally unsafe.
func TestHTTPForwarder_RejectsLinkLocalAddresses(t *testing.T) {
	fw := dispatch.NewHTTPForwarder([]byte("test-secret-long-enough-for-constructor-checks"), time.Second)

	cases := []string{
		"169.254.169.254:80",                // AWS / Azure metadata
		"http://169.254.169.254/latest/api/", // full URL form
		"169.254.0.1:8080",                  // broader link-local range
		"[fe80::1]:8080",                    // IPv6 link-local
	}
	for _, addr := range cases {
		_, err := fw.ForwardCriteria(context.Background(), addr, makeCriteriaReq())
		if err == nil {
			t.Errorf("ForwardCriteria(%q) accepted link-local address", addr)
			continue
		}
		if !errors.Is(err, dispatch.ErrForbiddenPeerAddress) {
			t.Errorf("ForwardCriteria(%q) = %v, want wraps ErrForbiddenPeerAddress", addr, err)
		}
	}
}

// TestHTTPForwarder_AcceptsRoutableAddress is the sanity-check happy
// path: a routable address with a valid response must still work.
func TestHTTPForwarder_AcceptsRoutableAddress(t *testing.T) {
	// We don't need real network traffic — we just need the address to
	// survive validation and the request to attempt connection. Pick an
	// address known to be unreachable (TEST-NET-1) with a tiny timeout;
	// the guard must let it through and the call must fail with a
	// *network* error, not ErrForbiddenPeerAddress.
	fw := dispatch.NewHTTPForwarder([]byte("test-secret-long-enough-for-constructor-checks"), 50*time.Millisecond)
	_, err := fw.ForwardProcessor(context.Background(), "192.0.2.1:8080", makeProcessorReq())
	if err == nil {
		t.Fatal("expected network error for unreachable TEST-NET-1 address")
	}
	if errors.Is(err, dispatch.ErrForbiddenPeerAddress) {
		t.Fatalf("guard incorrectly rejected routable address: %v", err)
	}
	// Sanity check that this is a connection/network error, not a guard
	// error — the exact text depends on the transport but includes the
	// URL we passed.
	if !strings.Contains(err.Error(), "192.0.2.1") {
		t.Fatalf("expected error to reference the target addr, got: %v", err)
	}
}
