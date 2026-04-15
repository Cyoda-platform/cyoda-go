package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/cyoda-platform/cyoda-go/app"
	"github.com/cyoda-platform/cyoda-go/internal/testing/localproc"

	// Register stock storage plugins so spi.GetPlugin("postgres") resolves.
	_ "github.com/cyoda-platform/cyoda-go/plugins/memory"
	_ "github.com/cyoda-platform/cyoda-go/plugins/postgres"
)

var (
	serverURL string                            // base URL of the test server (e.g., "http://127.0.0.1:12345")
	dbPool    *pgxpool.Pool                     // direct DB access for verification queries
	procSvc   *localproc.LocalProcessingService // in-process processor/criteria for workflow tests
)

func TestMain(m *testing.M) {
	// flag.Parse must be called before testing.Short() is valid.
	flag.Parse()
	if testing.Short() {
		os.Exit(0) // skip E2E in short mode
	}

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:17-alpine",
		tcpostgres.WithDatabase("minicyoda_test"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("failed to start postgres container: %v", err)
	}
	defer pgContainer.Terminate(ctx)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("failed to get connection string: %v", err)
	}

	// Create a direct pool for verification queries
	dbPool, err = pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatalf("failed to create verification pool: %v", err)
	}
	defer dbPool.Close()

	// Generate JWT signing key
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("failed to generate RSA key: %v", err)
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		log.Fatalf("failed to marshal key: %v", err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}))

	// Configure postgres plugin via env vars — the plugin reads its own
	// config from CYODA_POSTGRES_* through the getenv function passed to
	// NewFactory. Unset after app.New captures them.
	os.Setenv("CYODA_POSTGRES_URL", connStr)
	os.Setenv("CYODA_POSTGRES_MAX_CONNS", "5")
	os.Setenv("CYODA_POSTGRES_MIN_CONNS", "1")
	os.Setenv("CYODA_POSTGRES_MAX_CONN_IDLE_TIME", "1m")
	os.Setenv("CYODA_POSTGRES_AUTO_MIGRATE", "true")
	defer func() {
		os.Unsetenv("CYODA_POSTGRES_URL")
		os.Unsetenv("CYODA_POSTGRES_MAX_CONNS")
		os.Unsetenv("CYODA_POSTGRES_MIN_CONNS")
		os.Unsetenv("CYODA_POSTGRES_MAX_CONN_IDLE_TIME")
		os.Unsetenv("CYODA_POSTGRES_AUTO_MIGRATE")
	}()

	cfg := app.DefaultConfig()
	cfg.ContextPath = "/api"
	cfg.StorageBackend = "postgres"
	cfg.IAM = app.IAMConfig{
		Mode:          "jwt",
		JWTSigningKey: keyPEM,
		JWTIssuer:     "cyoda-go-test",
		JWTExpiry:     3600,
	}
	cfg.Bootstrap = app.BootstrapConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		TenantID:     "test-tenant",
		UserID:       "test-admin",
		Roles:        "ROLE_ADMIN,ROLE_M2M",
	}

	// In-process processor/criteria service for workflow E2E tests.
	procSvc = localproc.New()
	cfg.ExternalProcessing = procSvc

	// Create an unstarted server to discover the port BEFORE constructing the app.
	// The app constructs the JWKS validator URL using cfg.HTTPPort — it must match
	// the actual server port for JWT validation to work.
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	serverURL = srv.URL
	defer srv.Close()

	// Extract the port from the httptest server URL and set it in the config
	// so the JWKS validator URL points to the right place.
	srvPort := srv.Listener.Addr().(*net.TCPAddr).Port
	cfg.HTTPPort = srvPort

	a := app.New(cfg)
	srv.Config.Handler = a.Handler()

	os.Exit(m.Run())
}

func TestHealth(t *testing.T) {
	resp, err := http.Get(serverURL + "/api/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
