package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestBootstrapSecret_JwtMode_IDSetSecretUnset_Rejects verifies that in jwt mode,
// setting CYODA_BOOTSTRAP_CLIENT_ID without CYODA_BOOTSTRAP_CLIENT_SECRET is rejected.
func TestBootstrapSecret_JwtMode_IDSetSecretUnset_Rejects(t *testing.T) {
	cfg := bootstrapTestConfig("jwt", "client-id", "")
	_, err := validateBootstrapConfig(cfg)
	if err == nil {
		t.Fatal("expected error when CYODA_BOOTSTRAP_CLIENT_ID set but CYODA_BOOTSTRAP_CLIENT_SECRET unset in jwt mode; got nil")
	}
	if !strings.Contains(err.Error(), "CYODA_BOOTSTRAP_CLIENT_SECRET") {
		t.Errorf("error should name the missing env var; got: %v", err)
	}
}

// TestBootstrapSecret_JwtMode_SecretSetIDUnset_Rejects verifies that in jwt mode,
// setting CYODA_BOOTSTRAP_CLIENT_SECRET without CYODA_BOOTSTRAP_CLIENT_ID is rejected.
func TestBootstrapSecret_JwtMode_SecretSetIDUnset_Rejects(t *testing.T) {
	cfg := bootstrapTestConfig("jwt", "", "some-secret")
	_, err := validateBootstrapConfig(cfg)
	if err == nil {
		t.Fatal("expected error when CYODA_BOOTSTRAP_CLIENT_SECRET set but CYODA_BOOTSTRAP_CLIENT_ID unset in jwt mode; got nil")
	}
	if !strings.Contains(err.Error(), "CYODA_BOOTSTRAP_CLIENT_ID") {
		t.Errorf("error should name the missing env var; got: %v", err)
	}
}

// TestBootstrapSecret_JwtMode_BothEmpty_OK verifies that jwt mode with neither
// ID nor secret set is a legitimate startup (JWKS-only auth, no bootstrap M2M client).
func TestBootstrapSecret_JwtMode_BothEmpty_OK(t *testing.T) {
	cfg := bootstrapTestConfig("jwt", "", "")
	_, err := validateBootstrapConfig(cfg)
	if err != nil {
		t.Errorf("jwt mode with neither ID nor secret should be valid (no bootstrap M2M client); got: %v", err)
	}
}

// TestBootstrapSecret_MockModeIgnored verifies that mock mode doesn't
// require the bootstrap secret.
func TestBootstrapSecret_MockModeIgnored(t *testing.T) {
	cfg := bootstrapTestConfig("mock", "", "")
	_, err := validateBootstrapConfig(cfg)
	if err != nil {
		t.Errorf("mock mode should not require bootstrap secret; got: %v", err)
	}
}

// TestBootstrapSecret_NotLogged verifies that no secret value is
// ever written to the slog default handler.
func TestBootstrapSecret_NotLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const secret = "canary-secret-value-must-not-appear-in-logs"
	cfg := bootstrapTestConfig("jwt", "some-client-id", secret)

	if _, err := validateBootstrapConfig(cfg); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	if strings.Contains(buf.String(), secret) {
		t.Errorf("bootstrap client secret MUST NOT appear in logs; output:\n%s", buf.String())
	}
}

// bootstrapTestConfig builds a minimal Config for bootstrap validation tests.
func bootstrapTestConfig(iamMode, clientID, clientSecret string) *Config {
	cfg := DefaultConfig()
	cfg.IAM.Mode = iamMode
	cfg.Bootstrap.ClientID = clientID
	cfg.Bootstrap.ClientSecret = clientSecret
	return &cfg
}
