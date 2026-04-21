package gentree

import "testing"

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
