package memory_test

import (
	"testing"
	"time"

	spitest "github.com/cyoda-platform/cyoda-go-spi/spitest"

	"github.com/cyoda-platform/cyoda-go/plugins/memory"
)

// TestConformance runs the SPI conformance harness against the memory plugin.
// At this point, the harness suite functions are skip-stubs; this test
// validates the cross-module wiring. Task 11 wires in a real TestClock.
func TestConformance(t *testing.T) {
	factory := memory.NewStoreFactory()
	spitest.StoreFactoryConformance(t, spitest.Harness{
		Factory:      factory,
		AdvanceClock: func(d time.Duration) { time.Sleep(d) }, // placeholder; replaced in Task 11
	})
}
