package memory_test

import (
	"context"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	spitest "github.com/cyoda-platform/cyoda-go-spi/spitest"

	_ "github.com/cyoda-platform/cyoda-go/plugins/memory"
)

// TestConformance runs the SPI conformance harness against the memory plugin.
// The factory is created via the plugin registry so that initTransactionManager
// is called — matching production wiring exactly.
func TestConformance(t *testing.T) {
	p, ok := spi.GetPlugin("memory")
	if !ok {
		t.Fatal(`spi.GetPlugin("memory") returned ok=false — blank import missing`)
	}
	factory, err := p.NewFactory(context.Background(), func(string) string { return "" })
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	spitest.StoreFactoryConformance(t, spitest.Harness{
		Factory:      factory,
		AdvanceClock: func(d time.Duration) { time.Sleep(d) }, // placeholder; replaced in Task 11
	})
}
