//go:build cyoda_recon

package recon

import (
	"bufio"
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type CloudConfig struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
}

func loadCloudConfig() CloudConfig {
	// Load .env file as fallback values (does not pollute the process environment).
	dotenv := parseDotEnv(".env")

	lookup := func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return dotenv[key]
	}

	baseURL := lookup("CYODA_CLOUD_BASE_URL")
	if baseURL == "" {
		baseURL = "https://localhost:8443/api"
	}
	tokenURL := lookup("CYODA_CLOUD_TOKEN_URL")
	if tokenURL == "" {
		tokenURL = baseURL + "/oauth/token"
	}
	return CloudConfig{
		BaseURL:      baseURL,
		TokenURL:     tokenURL,
		ClientID:     lookup("CYODA_CLOUD_CLIENT_ID"),
		ClientSecret: lookup("CYODA_CLOUD_CLIENT_SECRET"),
	}
}

// parseDotEnv reads a .env file and returns key-value pairs.
// Does NOT set environment variables — values stay in the returned map.
// Lines must be KEY=VALUE or export KEY=VALUE. Comments (#) and blank lines are skipped.
func parseDotEnv(path string) map[string]string {
	result := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return result
}

func newCyodaCloudClient(cfg CloudConfig) *http.Client {
	// Allow self-signed certs for local Cyoda Cloud instances.
	insecureTransport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	insecureHTTPClient := &http.Client{Transport: insecureTransport}

	// oauth2.HTTPClient is the context key the oauth2 package uses
	// to pick up a custom HTTP client for token exchange.
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, insecureHTTPClient)

	ccCfg := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
	}
	return ccCfg.Client(ctx)
}
