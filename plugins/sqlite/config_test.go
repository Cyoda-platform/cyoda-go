package sqlite

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDefaultDBPathResolved_LinuxWithXDG(t *testing.T) {
	got := defaultDBPathResolved("linux",
		func(key string) string {
			if key == "XDG_DATA_HOME" {
				return "/tmp/xdg"
			}
			return ""
		},
		func() (string, error) { return "/home/u", nil },
	)
	want := filepath.Join("/tmp/xdg", "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_LinuxNoXDG(t *testing.T) {
	got := defaultDBPathResolved("linux",
		func(key string) string { return "" },
		func() (string, error) { return "/home/u", nil },
	)
	want := filepath.Join("/home/u", ".local", "share", "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_macOSNoXDG(t *testing.T) {
	got := defaultDBPathResolved("darwin",
		func(key string) string { return "" },
		func() (string, error) { return "/Users/u", nil },
	)
	want := filepath.Join("/Users/u", ".local", "share", "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_WindowsWithLocalAppData(t *testing.T) {
	got := defaultDBPathResolved("windows",
		func(key string) string {
			if key == "LocalAppData" {
				return `C:\Users\u\AppData\Local`
			}
			return ""
		},
		func() (string, error) { return `C:\Users\u`, nil },
	)
	want := filepath.Join(`C:\Users\u\AppData\Local`, "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_WindowsNoLocalAppData(t *testing.T) {
	got := defaultDBPathResolved("windows",
		func(key string) string { return "" },
		func() (string, error) { return `C:\Users\u`, nil },
	)
	want := filepath.Join(`C:\Users\u`, "AppData", "Local", "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_HomeLookupFails(t *testing.T) {
	got := defaultDBPathResolved("linux",
		func(key string) string { return "" },
		func() (string, error) { return "", errors.New("no home") },
	)
	if got != "cyoda.db" {
		t.Fatalf("expected fallback %q, got %q", "cyoda.db", got)
	}
}

func TestDefaultDBPath_DelegatesToResolved(t *testing.T) {
	got := DefaultDBPath()
	if got == "" {
		t.Fatal("DefaultDBPath returned empty")
	}
	if !filepath.IsAbs(got) && got != "cyoda.db" {
		t.Fatalf("expected absolute path or fallback literal, got %q", got)
	}
}
