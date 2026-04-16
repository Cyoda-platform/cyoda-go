package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// config holds parsed plugin configuration from CYODA_SQLITE_* env vars.
type config struct {
	Path            string
	AutoMigrate     bool
	BusyTimeout     time.Duration
	CacheSizeKiB    int
	SearchScanLimit int
}

// parseConfig reads CYODA_SQLITE_* env vars via the injected getenv.
func parseConfig(getenv func(string) string) (config, error) {
	cfg := config{
		Path:            envStringFn(getenv, "CYODA_SQLITE_PATH", defaultDBPath()),
		AutoMigrate:     envBoolFn(getenv, "CYODA_SQLITE_AUTO_MIGRATE", true),
		BusyTimeout:     envDurationFn(getenv, "CYODA_SQLITE_BUSY_TIMEOUT", 5*time.Second),
		CacheSizeKiB:    envIntFn(getenv, "CYODA_SQLITE_CACHE_SIZE", 64000),
		SearchScanLimit: envIntFn(getenv, "CYODA_SQLITE_SEARCH_SCAN_LIMIT", 100_000),
	}
	if cfg.Path == "" {
		return cfg, fmt.Errorf("CYODA_SQLITE_PATH resolved to empty string")
	}
	return cfg, nil
}

// defaultDBPath returns $XDG_DATA_HOME/cyoda-go/cyoda.db per FreeDesktop spec.
func defaultDBPath() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "cyoda.db"
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "cyoda-go", "cyoda.db")
}

func envStringFn(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntFn(getenv func(string) string, key string, fallback int) int {
	v := getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBoolFn(getenv func(string) string, key string, fallback bool) bool {
	v := getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDurationFn(getenv func(string) string, key string, fallback time.Duration) time.Duration {
	v := getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
