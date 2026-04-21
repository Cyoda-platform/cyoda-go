// Package gentree produces random ModelNode trees and JSON-like values
// for property-based testing of the schema transformation pipeline.
// Determinism: use only ordered data structures when emitting tree
// shape. Never `range` over maps in generator paths — see
// TestGeneratorIsMapFree.
package gentree

import (
	"math/rand/v2"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// GenConfig holds all configurable knobs for the random tree generator.
type GenConfig struct {
	Seed        int64
	MaxDepth    int
	MaxWidth    int
	KindWeights KindWeights
	// PrimitiveWeights maps each DataType to a relative weight; the generator
	// normalises them — values need not sum to 1.0.
	PrimitiveWeights map[schema.DataType]float64
	AllowNulls       bool
	TargetLevel      spi.ChangeLevel
}

// KindWeights controls the relative probability of generating a leaf,
// object, or array node at each position in the tree.
// KindWeights are relative; the generator normalises them — they need not sum to 1.0.
type KindWeights struct {
	Leaf, Object, Array float64
}

// DefaultConfig returns a GenConfig with sensible defaults for
// property-based tests.
func DefaultConfig() GenConfig {
	return GenConfig{
		Seed:        1,
		MaxDepth:    5,
		MaxWidth:    6,
		KindWeights: KindWeights{Leaf: 0.5, Object: 0.3, Array: 0.2},
		PrimitiveWeights: map[schema.DataType]float64{
			schema.Integer:        5,
			schema.Long:           3,
			schema.BigInteger:     1,
			schema.UnboundInteger: 1,
			schema.Double:         3,
			schema.BigDecimal:     2,
			schema.UnboundDecimal: 1,
			schema.String:         5,
			schema.Boolean:        2,
			schema.Null:           1,
		},
		AllowNulls:  true,
		TargetLevel: spi.ChangeLevelStructural,
	}
}

// NewRNG returns a PCG-seeded *rand.Rand; same seed produces same
// sequence across Go versions.
func NewRNG(seed int64) *rand.Rand {
	// Split int64 into two uint64s for PCG's two-word seed.
	s1 := uint64(seed)
	s2 := uint64(seed) ^ 0x9E3779B97F4A7C15
	return rand.New(rand.NewPCG(s1, s2))
}
