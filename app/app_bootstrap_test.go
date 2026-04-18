package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestBootstrapSecret_JwtModeRequired verifies that in jwt mode, an
// unset CYODA_BOOTSTRAP_CLIENT_SECRET is a fatal startup error.
func TestBootstrapSecret_JwtModeRequired(t *testing.T) {
	cfg := bootstrapTestConfig("jwt", "")
	_, err := validateBootstrapConfig(cfg)
	if err == nil {
		t.Fatal("expected error when CYODA_BOOTSTRAP_CLIENT_SECRET unset in jwt mode; got nil")
	}
	if !strings.Contains(err.Error(), "CYODA_BOOTSTRAP_CLIENT_SECRET") {
		t.Errorf("error should name the missing env var; got: %v", err)
	}
}

// TestBootstrapSecret_MockModeIgnored verifies that mock mode doesn't
// require the bootstrap secret.
func TestBootstrapSecret_MockModeIgnored(t *testing.T) {
	cfg := bootstrapTestConfig("mock", "")
	_, err := validateBootstrapConfig(cfg)
	if err != nil {
		t.Errorf("mock mode should not require bootstrap secret; got: %v", err)
	}
}

// TestBootstrapSecret_NoStdoutPrint verifies that no secret value is
// ever written to the slog default handler.
func TestBootstrapSecret_NoStdoutPrint(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const secret = "canary-secret-value-must-not-appear-in-logs"
	cfg := bootstrapTestConfig("jwt", secret)

	if _, err := validateBootstrapConfig(cfg); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	if strings.Contains(buf.String(), secret) {
		t.Errorf("bootstrap client secret MUST NOT appear in logs; output:\n%s", buf.String())
	}
}

// bootstrapTestConfig builds a minimal Config for bootstrap validation tests.
func bootstrapTestConfig(iamMode, clientSecret string) *Config {
	cfg := DefaultConfig()
	cfg.IAM.Mode = iamMode
	cfg.Bootstrap.ClientSecret = clientSecret
	return &cfg
}
