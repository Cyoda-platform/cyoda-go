package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/joho/godotenv"
)

// LoadEnvFiles loads environment variables from .env files using a
// profile-based layering system inspired by Spring's spring.profiles.active.
//
// Loading order (later values override earlier; shell env always wins):
//  1. System config (per OS) — lowest precedence among loaded files
//  2. User config (per OS)
//  3. CWD .env
//  4. CWD .env.{profile} in declaration order
//  5. Shell environment — never overridden
//
// Profiles are specified via CYODA_PROFILES (comma-separated).
// Example: CYODA_PROFILES=postgres,otel loads .env, .env.postgres, .env.otel.
//
// Missing files are silently skipped.
func LoadEnvFiles() {
	profiles := splitProfiles(os.Getenv("CYODA_PROFILES"))

	// Load order (later values override earlier; shell env always wins):
	//   1. System config (per OS) — lowest precedence among loaded files
	//   2. User config (per OS)
	//   3. CWD .env
	//   4. CWD .env.<profile> in declaration order
	//   5. Shell environment — never overridden (handled below)
	var files []string
	files = append(files, SystemConfigPaths()...)
	if u := UserConfigPath(); u != "" {
		files = append(files, u)
	}
	files = append(files, ".env")
	for _, p := range profiles {
		files = append(files, ".env."+p)
	}

	merged := make(map[string]string)
	var loaded []string
	for _, f := range files {
		vars, err := godotenv.Read(f)
		if err != nil {
			continue // file doesn't exist or is unreadable — skip silently
		}
		loaded = append(loaded, f)
		for k, v := range vars {
			merged[k] = v
		}
	}

	applied := 0
	for k, v := range merged {
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
			applied++
		}
	}

	if len(loaded) > 0 {
		slog.Info("loaded env files",
			"files", loaded,
			"profiles", profiles,
			"vars_applied", applied,
		)
	} else if len(profiles) > 0 {
		slog.Warn("CYODA_PROFILES set but no .env files found",
			"profiles", profiles,
			"searched", files,
		)
	}
}

// UserConfigPath returns the OS-appropriate path to the per-user cyoda
// config file (not necessarily existing on disk). Callers: LoadEnvFiles
// (for autoload) and the 'cyoda init' subcommand (to know where to write).
// Returns "" if the user home directory cannot be determined and the
// relevant OS env var is unset.
func UserConfigPath() string {
	return userConfigPathResolved(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func userConfigPathResolved(goos string, getenv func(string) string, home func() (string, error)) string {
	if goos == "windows" {
		if ad := getenv("AppData"); ad != "" {
			return filepath.Join(ad, "cyoda", "cyoda.env")
		}
		h, err := home()
		if err != nil {
			return ""
		}
		return filepath.Join(h, "AppData", "Roaming", "cyoda", "cyoda.env")
	}
	// Linux + macOS: XDG
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "cyoda", "cyoda.env")
	}
	h, err := home()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".config", "cyoda", "cyoda.env")
}

// SystemConfigPaths returns the OS-appropriate system-wide cyoda config
// paths (not necessarily existing on disk). macOS returns an empty slice
// by design — Homebrew formulas cannot cleanly write to a system path.
func SystemConfigPaths() []string {
	return systemConfigPathsResolved(runtime.GOOS, os.Getenv)
}

func systemConfigPathsResolved(goos string, getenv func(string) string) []string {
	switch goos {
	case "linux":
		return []string{"/etc/cyoda/cyoda.env"}
	case "windows":
		if pd := getenv("ProgramData"); pd != "" {
			return []string{filepath.Join(pd, "cyoda", "cyoda.env")}
		}
		return nil
	default: // darwin and anything else
		return nil
	}
}

// ProfileBanner returns a human-readable string for the startup banner.
func ProfileBanner() string {
	profiles := os.Getenv("CYODA_PROFILES")
	if profiles == "" {
		return "(none)"
	}
	return profiles
}

func splitProfiles(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			if err := validateProfileName(trimmed); err != nil {
				slog.Warn("skipping invalid profile name", "profile", trimmed, "error", err)
				continue
			}
			result = append(result, trimmed)
		}
	}
	return result
}

// validateProfileName rejects profile names that could cause path traversal.
func validateProfileName(name string) error {
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return fmt.Errorf("profile name must not contain path separators or '..'")
	}
	return nil
}
