package dispatch

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DispatchForwarder sends processor and criteria dispatch requests to a peer node.
type DispatchForwarder interface {
	ForwardProcessor(ctx context.Context, addr string, req *DispatchProcessorRequest) (*DispatchProcessorResponse, error)
	ForwardCriteria(ctx context.Context, addr string, req *DispatchCriteriaRequest) (*DispatchCriteriaResponse, error)
}

// HTTPForwarder implements DispatchForwarder over HTTP with HMAC-SHA256 authentication.
type HTTPForwarder struct {
	hmacSecret    []byte
	client        *http.Client
	allowLoopback bool
}

// NewHTTPForwarder constructs an HTTPForwarder with the given HMAC secret and request timeout.
// Loopback peer addresses are rejected by default; see AllowLoopbackForTesting.
func NewHTTPForwarder(hmacSecret []byte, timeout time.Duration) *HTTPForwarder {
	return &HTTPForwarder{
		hmacSecret: hmacSecret,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// AllowLoopbackForTesting opts the forwarder out of the loopback SSRF
// guard so unit and integration tests can target an httptest.Server on
// 127.0.0.1. Link-local, unspecified, and multicast addresses are still
// rejected. Never call this in production — it re-opens the SSRF pivot
// the guard was written to close. Returns the receiver for fluent use
// at construction sites: `NewHTTPForwarder(...).AllowLoopbackForTesting()`.
func (f *HTTPForwarder) AllowLoopbackForTesting() *HTTPForwarder {
	f.allowLoopback = true
	return f
}

// ForwardProcessor POSTs a processor dispatch request to the peer at addr and returns the response.
func (f *HTTPForwarder) ForwardProcessor(ctx context.Context, addr string, req *DispatchProcessorRequest) (*DispatchProcessorResponse, error) {
	if err := validatePeerAddress(addr, f.allowLoopback); err != nil {
		return nil, err
	}
	var resp DispatchProcessorResponse
	if err := f.forward(ctx, ensureScheme(addr)+"/internal/dispatch/processor", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ForwardCriteria POSTs a criteria dispatch request to the peer at addr and returns the response.
func (f *HTTPForwarder) ForwardCriteria(ctx context.Context, addr string, req *DispatchCriteriaRequest) (*DispatchCriteriaResponse, error) {
	if err := validatePeerAddress(addr, f.allowLoopback); err != nil {
		return nil, err
	}
	var resp DispatchCriteriaResponse
	if err := f.forward(ctx, ensureScheme(addr)+"/internal/dispatch/criteria", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ensureScheme prepends http:// if the address has no scheme.
func ensureScheme(addr string) string {
	if !strings.Contains(addr, "://") {
		return "http://" + addr
	}
	return addr
}

// forward marshals reqBody, signs it, POSTs to url, and decodes the response into respBody.
func (f *HTTPForwarder) forward(ctx context.Context, url string, reqBody any, respBody any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("dispatch forward: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("dispatch forward: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dispatch-HMAC", f.sign(body))

	httpResp, err := f.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("dispatch forward: HTTP POST %s: %w", url, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, 512))
		return fmt.Errorf("dispatch forward: peer returned %d: %s", httpResp.StatusCode, raw)
	}

	if err := json.NewDecoder(httpResp.Body).Decode(respBody); err != nil {
		return fmt.Errorf("dispatch forward: decode response from %s: %w", url, err)
	}
	return nil
}

// sign returns the hex-encoded HMAC-SHA256 of body using the configured secret.
func (f *HTTPForwarder) sign(body []byte) string {
	mac := hmac.New(sha256.New, f.hmacSecret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
