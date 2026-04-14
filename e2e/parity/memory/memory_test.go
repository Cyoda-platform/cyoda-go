package memory

import (
	"flag"
	"os"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

var sharedFixture *memoryFixture

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

	fix, teardown, err := setup()
	if err != nil {
		// Cannot use t.Fatal in TestMain — print and exit.
		println("FATAL: fixture setup failed:", err.Error())
		os.Exit(1)
	}
	defer teardown()

	sharedFixture = fix
	os.Exit(m.Run())
}

func TestParity(t *testing.T) {
	for _, nt := range parity.AllTests() {
		t.Run(nt.Name, func(t *testing.T) {
			nt.Fn(t, sharedFixture)
		})
	}
}
