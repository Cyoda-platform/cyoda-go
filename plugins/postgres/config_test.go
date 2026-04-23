package postgres

import (
	"math"
	"strconv"
	"testing"
)

// TestParseConfig_MaxConnsInt32Overflow_FallsBackToDefault asserts that a
// CYODA_POSTGRES_MAX_CONNS value outside int32 range does not silently
// truncate on the int-to-int32 conversion. Values above math.MaxInt32
// must fall back to the default with a logged warning.
func TestParseConfig_MaxConnsInt32Overflow_FallsBackToDefault(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "CYODA_POSTGRES_URL":
			return "postgres://test"
		case "CYODA_POSTGRES_MAX_CONNS":
			return strconv.FormatInt(int64(math.MaxInt32)+1, 10) // 2147483648
		}
		return ""
	}
	cfg, err := parseConfig(getenv)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.MaxConns != 25 {
		t.Errorf("MaxConns = %d, want default 25 (value %d is out of int32 range)", cfg.MaxConns, int64(math.MaxInt32)+1)
	}
}

// TestParseConfig_MinConnsInt32Overflow_FallsBackToDefault — same for min.
func TestParseConfig_MinConnsInt32Overflow_FallsBackToDefault(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "CYODA_POSTGRES_URL":
			return "postgres://test"
		case "CYODA_POSTGRES_MIN_CONNS":
			return strconv.FormatInt(int64(math.MaxInt32)+1, 10)
		}
		return ""
	}
	cfg, err := parseConfig(getenv)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.MinConns != 5 {
		t.Errorf("MinConns = %d, want default 5 (value %d is out of int32 range)", cfg.MinConns, int64(math.MaxInt32)+1)
	}
}

// TestParseConfig_MaxConnsInRange_Preserved asserts happy-path: valid
// int32 values round-trip unchanged.
func TestParseConfig_MaxConnsInRange_Preserved(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "CYODA_POSTGRES_URL":
			return "postgres://test"
		case "CYODA_POSTGRES_MAX_CONNS":
			return "100"
		case "CYODA_POSTGRES_MIN_CONNS":
			return "20"
		}
		return ""
	}
	cfg, err := parseConfig(getenv)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.MaxConns != 100 || cfg.MinConns != 20 {
		t.Errorf("MaxConns/MinConns = %d/%d, want 100/20", cfg.MaxConns, cfg.MinConns)
	}
}
