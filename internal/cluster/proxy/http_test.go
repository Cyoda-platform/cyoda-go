package proxy_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/cluster/proxy"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/token"
	"github.com/cyoda-platform/cyoda-go/internal/spi"
)

// fakeRegistry is a test double for spi.NodeRegistry that supports multiple
// nodes with configurable alive status.
type fakeRegistry struct {
	nodes map[string]spi.NodeInfo
}

func newFakeRegistry(nodes ...spi.NodeInfo) *fakeRegistry {
	m := make(map[string]spi.NodeInfo, len(nodes))
	for _, n := range nodes {
		m[n.NodeID] = n
	}
	return &fakeRegistry{nodes: m}
}

func (r *fakeRegistry) Register(_ context.Context, nodeID, addr string) error {
	r.nodes[nodeID] = spi.NodeInfo{NodeID: nodeID, Addr: addr, Alive: true}
	return nil
}

func (r *fakeRegistry) Lookup(_ context.Context, nodeID string) (string, bool, error) {
	info, ok := r.nodes[nodeID]
	if !ok {
		return "", false, nil
	}
	return info.Addr, info.Alive, nil
}

func (r *fakeRegistry) List(_ context.Context) ([]spi.NodeInfo, error) {
	out := make([]spi.NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	return out, nil
}

func (r *fakeRegistry) Deregister(_ context.Context, nodeID string) error {
	delete(r.nodes, nodeID)
	return nil
}

// mustNewSigner creates a token.Signer or panics — for use in tests only.
func mustNewSigner(secret []byte) *token.Signer {
	s, err := token.NewSigner(secret)
	if err != nil {
		panic(fmt.Sprintf("mustNewSigner: %v", err))
	}
	return s
}

// localHandler returns a handler that writes "local" to the response body.
func localHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "local")
	})
}

func TestHTTPProxy_NoToken_ServesLocally(t *testing.T) {
	signer := mustNewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	reg := newFakeRegistry(spi.NodeInfo{NodeID: "node-1", Addr: "http://localhost:9999", Alive: true})

	mw := proxy.HTTPRouting(signer, reg, "node-1", 5*time.Second)
	handler := mw(localHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "local" {
		t.Fatalf("expected 'local', got %q", rec.Body.String())
	}
}

func TestHTTPProxy_TokenForSelf_ServesLocally(t *testing.T) {
	signer := mustNewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	reg := newFakeRegistry(spi.NodeInfo{NodeID: "node-1", Addr: "http://localhost:9999", Alive: true})

	tok, err := signer.Issue("node-1", "tx-123", time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	mw := proxy.HTTPRouting(signer, reg, "node-1", 5*time.Second)
	handler := mw(localHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(proxy.TxTokenHeader, tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "local" {
		t.Fatalf("expected 'local', got %q", rec.Body.String())
	}
}

func TestHTTPProxy_TokenForOtherNode_Proxies(t *testing.T) {
	// Start a fake remote node that responds with "remote".
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "remote")
	}))
	defer remote.Close()

	signer := mustNewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	reg := newFakeRegistry(
		spi.NodeInfo{NodeID: "node-1", Addr: "http://localhost:9999", Alive: true},
		spi.NodeInfo{NodeID: "node-2", Addr: remote.URL, Alive: true},
	)

	tok, err := signer.Issue("node-2", "tx-456", time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	mw := proxy.HTTPRouting(signer, reg, "node-1", 5*time.Second)
	handler := mw(localHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(proxy.TxTokenHeader, tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "remote" {
		t.Fatalf("expected 'remote', got %q", body)
	}
}

func TestHTTPProxy_TokenForDeadNode_Returns503(t *testing.T) {
	signer := mustNewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	reg := newFakeRegistry(
		spi.NodeInfo{NodeID: "node-1", Addr: "http://localhost:9999", Alive: true},
		spi.NodeInfo{NodeID: "node-2", Addr: "http://localhost:9998", Alive: false},
	)

	tok, err := signer.Issue("node-2", "tx-789", time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	mw := proxy.HTTPRouting(signer, reg, "node-1", 5*time.Second)
	handler := mw(localHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(proxy.TxTokenHeader, tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHTTPProxy_ExpiredToken_Returns400(t *testing.T) {
	signer := mustNewSigner([]byte("test-secret-key-at-least-32-bytes!"))
	reg := newFakeRegistry(spi.NodeInfo{NodeID: "node-1", Addr: "http://localhost:9999", Alive: true})

	// Issue a token that expired in the past.
	tok, err := signer.Issue("node-2", "tx-expired", time.Now().Add(-1*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	mw := proxy.HTTPRouting(signer, reg, "node-1", 5*time.Second)
	handler := mw(localHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set(proxy.TxTokenHeader, tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
