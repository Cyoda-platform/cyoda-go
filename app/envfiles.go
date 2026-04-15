package app

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// LoadEnvFiles loads environment variables from .env files using a
// profile-based layering system inspired by Spring's spring.profiles.active.
//
// Loading order (later values override earlier):
//  1. .env              — base defaults
//  2. .env.{profile}    — per-profile overrides, in declaration order
//
// Profiles are specified via CYODA_PROFILES (comma-separated).
// Example: CYODA_PROFILES=postgres,otel loads .env, .env.postgres, .env.otel.
//
// Real environment variables (set in the shell) always take precedence over
// values from any .env file.
//
// Missing files are silently skipped.
func LoadEnvFiles() {
	profiles := splitProfiles(os.Getenv("CYODA_PROFILES"))

	files := []string{".env"}
	for _, p := range profiles {
		files = append(files, ".env."+p)
	}

	// Read all files into a merged map. Later files override earlier ones.
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

	// Set only vars that are NOT already in the real environment.
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
