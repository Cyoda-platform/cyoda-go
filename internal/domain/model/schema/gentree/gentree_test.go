package gentree

import (
	"encoding/json"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
)

func TestDefaultConfigSane(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxDepth < 3 {
		t.Fatalf("DefaultConfig MaxDepth=%d, want >=3", cfg.MaxDepth)
	}
	if cfg.MaxWidth < 3 {
		t.Fatalf("DefaultConfig MaxWidth=%d, want >=3", cfg.MaxWidth)
	}
	if cfg.Seed == 0 {
		t.Fatalf("DefaultConfig.Seed=0, want non-zero default")
	}
	if cfg.TargetLevel == "" {
		t.Fatalf("DefaultConfig.TargetLevel is empty; want a valid level (got %q, want e.g. %q)", cfg.TargetLevel, spi.ChangeLevelStructural)
	}
}

func TestNewRNGSameSeedSameSequence(t *testing.T) {
	r1 := NewRNG(42)
	r2 := NewRNG(42)
	for i := 0; i < 16; i++ {
		a := r1.Uint64()
		b := r2.Uint64()
		if a != b {
			t.Fatalf("seed 42, step %d: %d != %d", i, a, b)
		}
	}
}

func TestGenValueProducesWalkableOutput(t *testing.T) {
	cfg := DefaultConfig()
	r := NewRNG(7)
	for i := 0; i < 50; i++ {
		v := GenValue(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
		// GenValue output must be accepted by importer.Walk.
		if _, err := importer.Walk(v); err != nil {
			t.Fatalf("sample %d: Walk failed: %v (value: %#v)", i, err, v)
		}
	}
}

func TestGenValueSameSeedSameOutput(t *testing.T) {
	cfg := DefaultConfig()
	v1 := GenValue(NewRNG(99), cfg.MaxDepth, cfg.MaxWidth, cfg)
	v2 := GenValue(NewRNG(99), cfg.MaxDepth, cfg.MaxWidth, cfg)
	b1, _ := json.Marshal(v1)
	b2, _ := json.Marshal(v2)
	if string(b1) != string(b2) {
		t.Fatalf("seed 99 produced divergent output:\n  v1=%s\n  v2=%s", b1, b2)
	}
}
