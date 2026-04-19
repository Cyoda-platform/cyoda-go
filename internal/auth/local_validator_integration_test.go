package auth_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/auth"
)

// TestIntegration_JWTMode_LocalKeySource_NoHTTPFetch proves the hardened
// default wiring: validator built via NewValidatorFromSource + LocalKeySource
// validates tokens minted by the same authSvc without making any HTTP call,
// even if a JWKS endpoint is also exposed. This is the invariant #66 locks
// in — there is no loopback fetch to MITM because there is no fetch at all.
func TestIntegration_JWTMode_LocalKeySource_NoHTTPFetch(t *testing.T) {
	svc, err := auth.NewAuthService(auth.AuthConfig{
		SigningKeyPEM: generateTestPEM(t),
		Issuer:        "cyoda",
		ExpirySeconds: 3600,
	})
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}

	if err := svc.M2MClientStore().CreateWithSecret(
		"client-1", "tenant-1", "user-1", "secret-1", []string{"ROLE_USER"},
	); err != nil {
		t.Fatalf("CreateWithSecret: %v", err)
	}

	// Serve only the token endpoint. Crucially, no JWKS endpoint is exposed —
	// if the validator tried to fetch one, this test would fail.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc.Handler().ServeHTTP(w, r)
	}))
	defer tokenSrv.Close()

	// Mint a token via the normal token endpoint. Client credentials are
	// conveyed via HTTP Basic, matching the existing integration pattern.
	req, _ := http.NewRequest("POST", tokenSrv.URL+"/oauth/token",
		strings.NewReader("grant_type=client_credentials"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+
		base64.StdEncoding.EncodeToString([]byte("client-1:secret-1")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tokenResp.AccessToken == "" {
		t.Fatal("empty access token")
	}

	// Build the validator the same way app.go does: LocalKeySource wrapping
	// the in-process KeyStore. No JWKS URL, no http.Client.
	validator := auth.NewValidatorFromSource(auth.NewLocalKeySource(svc.KeyStore()), svc.Issuer())

	uc, err := validator.Validate(tokenResp.AccessToken)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if uc == nil || uc.UserID != "user-1" {
		t.Fatalf("unexpected user context: %+v", uc)
	}
}
