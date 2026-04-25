// Package driver provides the HTTPDriver abstraction used by
// e2e/parity/externalapi scenarios. It has two constructors:
//
//   - NewInProcess(t, fixture) — wraps a parity BackendFixture, minting
//     a fresh tenant per driver. Used by parity Run* tests.
//   - NewRemote(t, baseURL, jwtToken) — takes an arbitrary base URL and
//     pre-minted JWT. Used by the remote-mode smoke test and (later)
//     live cyoda-cloud runs.
//
// Both constructors return the same *Driver type; test code is identical
// regardless of provenance. This is what makes "point it at cyoda-cloud"
// trivial.
package driver

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity"
	parityclient "github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// Driver drives cyoda's HTTP API through the dictionary vocabulary.
type Driver struct {
	t      *testing.T
	client *parityclient.Client
}

// NewInProcess wires up a driver against a parity BackendFixture,
// minting one fresh tenant via fixture.NewTenant(t). The tenant's JWT
// is used as the Authorization bearer for every call.
func NewInProcess(t *testing.T, fixture parity.BackendFixture) *Driver {
	t.Helper()
	tenant := fixture.NewTenant(t)
	return &Driver{
		t:      t,
		client: parityclient.NewClient(fixture.BaseURL(), tenant.Token),
	}
}

// NewRemote wires up a driver against an arbitrary base URL using the
// provided JWT. No tenant is minted — the caller is responsible for the
// JWT's tenant identity.
func NewRemote(t *testing.T, baseURL, jwtToken string) *Driver {
	t.Helper()
	return &Driver{
		t:      t,
		client: parityclient.NewClient(baseURL, jwtToken),
	}
}

// ListModelsDiscard lists models and discards the result. It exists only
// to give the driver_test suite a trivial round-trip for wiring checks.
// (Real dictionary helpers follow — create_model_from_sample, etc.)
func (d *Driver) ListModelsDiscard() error {
	_, err := d.client.ListModels(d.t)
	return err
}
