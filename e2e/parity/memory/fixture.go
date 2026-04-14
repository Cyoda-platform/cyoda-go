// Package memory provides a BackendFixture that runs cyoda-go with
// the in-memory storage backend and a compute-test-client subprocess
// connected via gRPC.
package memory

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity"
	"github.com/cyoda-platform/cyoda-go/e2e/parity/fixtureutil"
)

// memoryFixture implements parity.BackendFixture for the memory backend.
type memoryFixture struct {
	baseURL      string
	grpcEndpoint string
	keySet       *fixtureutil.JWTKeySet
}

// BaseURL implements parity.BackendFixture.
func (f *memoryFixture) BaseURL() string { return f.baseURL }

// GRPCEndpoint implements parity.BackendFixture.
func (f *memoryFixture) GRPCEndpoint() string { return f.grpcEndpoint }

// NewTenant implements parity.BackendFixture — mints a fresh JWT with
// a unique tenant for each test.
func (f *memoryFixture) NewTenant(t *testing.T) parity.Tenant {
	t.Helper()
	return fixtureutil.MintTenantJWT(t, f.keySet)
}

// ComputeTenant implements parity.BackendFixture.
func (f *memoryFixture) ComputeTenant(t *testing.T) parity.Tenant {
	t.Helper()
	return fixtureutil.MintComputeTenantJWT(t, f.keySet)
}

// setup builds binaries, launches subprocesses, and waits for readiness.
// It returns a teardown function that kills the subprocesses.
func setup() (*memoryFixture, func(), error) {
	ks, err := fixtureutil.GenerateJWTKeySet()
	if err != nil {
		return nil, nil, err
	}

	result, cleanup, err := fixtureutil.LaunchCyodaAndCompute(ks, []string{
		"CYODA_STORAGE_BACKEND=memory",
	})
	if err != nil {
		return nil, nil, err
	}

	fix := &memoryFixture{
		baseURL:      result.BaseURL,
		grpcEndpoint: result.GRPCEndpoint,
		keySet:       ks,
	}

	return fix, cleanup, nil
}
