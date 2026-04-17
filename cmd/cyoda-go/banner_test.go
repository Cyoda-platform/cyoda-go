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
