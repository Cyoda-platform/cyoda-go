package main

import (
	"bytes"
	"testing"

	"github.com/cyoda-platform/cyoda-go/app"
)

func TestPrintBannerTo_Suppressed(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "true")
	var buf bytes.Buffer
	printBannerTo(&buf, app.DefaultConfig())
	if buf.Len() != 0 {
		t.Fatalf("expected empty output when suppressed, got %q", buf.String())
	}
}

func TestPrintBannerTo_NotSuppressed(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "")
	var buf bytes.Buffer
	printBannerTo(&buf, app.DefaultConfig())
	if buf.Len() == 0 {
		t.Fatal("expected banner output, got empty")
	}
}

func TestMockAuthWarning_EmittedInMockMode(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "")
	var buf bytes.Buffer
	cfg := app.DefaultConfig()
	cfg.IAM.Mode = "mock"
	printMockAuthWarningTo(&buf, cfg)
	if !bytes.Contains(buf.Bytes(), []byte("MOCK AUTH IS ACTIVE")) {
		t.Fatalf("expected MOCK AUTH warning, got %q", buf.String())
	}
}

func TestMockAuthWarning_NotEmittedInJWTMode(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "")
	var buf bytes.Buffer
	cfg := app.DefaultConfig()
	cfg.IAM.Mode = "jwt"
	printMockAuthWarningTo(&buf, cfg)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning in jwt mode, got %q", buf.String())
	}
}

func TestMockAuthWarning_SuppressedByFlag(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "true")
	var buf bytes.Buffer
	cfg := app.DefaultConfig()
	cfg.IAM.Mode = "mock"
	printMockAuthWarningTo(&buf, cfg)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning when suppressed, got %q", buf.String())
	}
}
