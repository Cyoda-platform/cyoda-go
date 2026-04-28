package multinode

import (
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

// MultiNodeFixture is the cluster-capable counterpart to
// parity.BackendFixture. Implementations launch N cyoda-go
// subprocesses sharing the same backing storage and expose one
// HTTP base URL per node. Tenants are minted once and used across
// all nodes (the cluster shares state, including auth).
type MultiNodeFixture interface {
	// BaseURLs returns one HTTP base URL per node, in stable order.
	// Length equals NodeCount(). Each URL has no trailing slash.
	BaseURLs() []string

	// NodeCount returns the number of nodes in the cluster.
	NodeCount() int

	// NewTenant mints a fresh tenant for the test. The returned JWT
	// is valid against every node in the cluster.
	NewTenant(t *testing.T) parity.Tenant
}
