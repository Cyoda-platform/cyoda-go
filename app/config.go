package app

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/cluster"
	"github.com/cyoda-platform/cyoda-go/internal/contract"
)

type Config struct {
	HTTPPort           int
	ContextPath        string
	ErrorResponseMode  string
	MaxStateVisits     int
	LogLevel           string
	IAM                IAMConfig
	GRPC               GRPCConfig
	Admin              AdminConfig
	Bootstrap          BootstrapConfig
	StorageBackend     string
	StartupTimeout     time.Duration
	Cluster            cluster.Config
	SearchSnapshotTTL  time.Duration
	SearchReapInterval time.Duration
	OTelEnabled        bool
	// ExternalProcessing overrides the default gRPC processor dispatcher.
	// Used in tests to inject a LocalProcessingService.
	ExternalProcessing contract.ExternalProcessingService
}

type AdminConfig struct {
	Port        int
	BindAddress string
}

type GRPCConfig struct {
	Port              int
	KeepAliveInterval int // seconds
	KeepAliveTimeout  int // seconds
}

type IAMConfig struct {
	Mode           string
	MockUserID     string
	MockUserName   string
	MockTenantID   string
	MockTenantName string
	MockRoles      []string
	JWTSigningKey  string // PEM-encoded RSA private key (CYODA_JWT_SIGNING_KEY)
	JWTIssuer      string // JWT issuer claim (CYODA_JWT_ISSUER)
	JWTExpiry      int    // Token expiry in seconds (CYODA_JWT_EXPIRY_SECONDS)
	RequireJWT     bool   // CYODA_REQUIRE_JWT — when true, refuses to start unless mode=jwt and signing key set
}

type BootstrapConfig struct {
	ClientID     string // CYODA_BOOTSTRAP_CLIENT_ID
	ClientSecret string // CYODA_BOOTSTRAP_CLIENT_SECRET (optional, generated if empty)
	TenantID     string // CYODA_BOOTSTRAP_TENANT_ID
	UserID       string // CYODA_BOOTSTRAP_USER_ID
	Roles        string // CYODA_BOOTSTRAP_ROLES (comma-separated)
}

func DefaultConfig() Config {
	return Config{
		HTTPPort:          envInt("CYODA_HTTP_PORT", 8080),
		ContextPath:       envString("CYODA_CONTEXT_PATH", "/api"),
		ErrorResponseMode: envString("CYODA_ERROR_RESPONSE_MODE", "sanitized"),
		MaxStateVisits:    envInt("CYODA_MAX_STATE_VISITS", 10),
		LogLevel:          envString("CYODA_LOG_LEVEL", "info"),
		GRPC: GRPCConfig{
			Port:              envInt("CYODA_GRPC_PORT", 9090),
			KeepAliveInterval: envInt("CYODA_KEEPALIVE_INTERVAL", 10),
			KeepAliveTimeout:  envInt("CYODA_KEEPALIVE_TIMEOUT", 30),
		},
		Bootstrap: BootstrapConfig{
			ClientID:     envString("CYODA_BOOTSTRAP_CLIENT_ID", ""),
			ClientSecret: envString("CYODA_BOOTSTRAP_CLIENT_SECRET", ""),
			TenantID:     envString("CYODA_BOOTSTRAP_TENANT_ID", "default-tenant"),
			UserID:       envString("CYODA_BOOTSTRAP_USER_ID", "admin"),
			Roles:        envString("CYODA_BOOTSTRAP_ROLES", "ROLE_ADMIN,ROLE_M2M"),
		},
		SearchSnapshotTTL:  envDuration("CYODA_SEARCH_SNAPSHOT_TTL", 1*time.Hour),
		SearchReapInterval: envDuration("CYODA_SEARCH_REAP_INTERVAL", 5*time.Minute),
		OTelEnabled:        envBool("CYODA_OTEL_ENABLED", false),
		StorageBackend:     envString("CYODA_STORAGE_BACKEND", "memory"),
		Admin: AdminConfig{
			Port:        envInt("CYODA_ADMIN_PORT", 9091),
			BindAddress: envString("CYODA_ADMIN_BIND_ADDRESS", "127.0.0.1"),
		},
		StartupTimeout:     envDuration("CYODA_STARTUP_TIMEOUT", 30*time.Second),
		IAM: IAMConfig{
			Mode:           envString("CYODA_IAM_MODE", "mock"),
			MockUserID:     "mock-user-001",
			MockUserName:   "Mock User",
			MockTenantID:   "mock-tenant",
			MockTenantName: "Mock Tenant",
			MockRoles:      mockRolesFromEnv([]string{"ROLE_ADMIN", "ROLE_M2M"}),
			JWTSigningKey:  envPEM("CYODA_JWT_SIGNING_KEY"),
			JWTIssuer:      envString("CYODA_JWT_ISSUER", "cyoda"),
			JWTExpiry:      envInt("CYODA_JWT_EXPIRY_SECONDS", 3600),
			RequireJWT:     envBool("CYODA_REQUIRE_JWT", false),
		},
		Cluster: cluster.Config{
			Enabled:                envBool("CYODA_CLUSTER_ENABLED", false),
			NodeID:                 envString("CYODA_NODE_ID", ""),
			NodeAddr:               envString("CYODA_NODE_ADDR", "http://localhost:8080"),
			GossipAddr:             envString("CYODA_GOSSIP_ADDR", ":7946"),
			SeedNodes:              splitCSV(envString("CYODA_SEED_NODES", "")),
			StabilityWindow:        envDuration("CYODA_GOSSIP_STABILITY_WINDOW", 2*time.Second),
			TxTTL:                  envDuration("CYODA_TX_TTL", 60*time.Second),
			TxReapInterval:         envDuration("CYODA_TX_REAP_INTERVAL", 10*time.Second),
			ProxyTimeout:           envDuration("CYODA_PROXY_TIMEOUT", 30*time.Second),
			OutcomeTTL:             envDuration("CYODA_TX_OUTCOME_TTL", 5*time.Minute),
			HMACSecret:             envHex("CYODA_HMAC_SECRET"),
			DispatchWaitTimeout:    envDuration("CYODA_DISPATCH_WAIT_TIMEOUT", 5*time.Second),
			DispatchForwardTimeout: envDuration("CYODA_DISPATCH_FORWARD_TIMEOUT", 30*time.Second),
		},
	}
}

func envString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// envPEM reads a PEM key from an environment variable. If the value starts with
// "-----BEGIN", it is used as-is. Otherwise it is treated as base64-encoded PEM
// (single-line friendly for .env files and docker env_file).
func envPEM(key string) string {
	v := os.Getenv(key)
	if v == "" || strings.HasPrefix(v, "-----BEGIN") {
		return v
	}
	decoded, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return v // not base64, return as-is
	}
	return string(decoded)
}

func envBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func envHex(key string) []byte {
	v := envString(key, "")
	if v == "" {
		return nil
	}
	b, err := hex.DecodeString(v)
	if err != nil {
		// Fall back to raw bytes if not valid hex
		return []byte(v)
	}
	return b
}

// mockRolesFromEnv parses CYODA_IAM_MOCK_ROLES and falls back to the
// given defaults if unset. If the variable is *set but resolves to zero
// entries* (empty string, only whitespace, only commas), we emit a WARN:
// silently granting the admin default in that case would mask an operator
// misconfiguration — they clearly intended to restrict the mock user.
func mockRolesFromEnv(fallback []string) []string {
	const key = "CYODA_IAM_MOCK_ROLES"
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	parts := splitCSV(raw)
	if len(parts) == 0 {
		slog.Warn("ignored empty role override, using defaults",
			"pkg", "app",
			"key", key,
			"rawValue", raw,
			"defaults", fallback,
		)
		return fallback
	}
	return parts
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// ValidateIAM enforces the CYODA_REQUIRE_JWT contract: when set, the binary
// refuses to run with mock auth or a missing signing key. Intended for
// production provisioning (Helm) where silent mock-auth fallback would be
// a security hazard. Callers must invoke this before wiring auth in New().
func ValidateIAM(iam IAMConfig) error {
	if !iam.RequireJWT {
		return nil
	}
	if iam.Mode != "jwt" {
		return fmt.Errorf("CYODA_REQUIRE_JWT=true but CYODA_IAM_MODE=%q (expected \"jwt\")", iam.Mode)
	}
	if iam.JWTSigningKey == "" {
		return fmt.Errorf("CYODA_REQUIRE_JWT=true but CYODA_JWT_SIGNING_KEY is empty")
	}
	return nil
}
