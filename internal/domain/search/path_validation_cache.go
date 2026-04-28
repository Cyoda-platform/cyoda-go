package search

import (
	"sync"

	"github.com/maypok86/otter/v2"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/modelcache"
)

// pathValidationCacheTopic is the gossip topic the path-validation
// negative cache subscribes to. It is the same topic used by the
// model-descriptor cache (internal/cluster/modelcache) so a single
// schema-change event drops both the descriptor and the negative
// path entries derived from it.
const pathValidationCacheTopic = "model.invalidate"

// pathValidationCacheCapacity bounds the negative cache size. Otter's
// S3-FIFO eviction handles overflow automatically — this bound is the
// memory ceiling, not a TTL: a hot unknown path stays cached as long as
// it keeps being queried (or until an invalidation event fires for its
// model).
const pathValidationCacheCapacity = 10000

// pathCacheKey identifies a single (tenant, modelRef, fieldPath) tuple
// in the negative cache. Otter requires comparable keys; the struct is
// comparable by value so it works directly without a hash function.
type pathCacheKey struct {
	tenant       string
	entityName   string
	modelVersion string
	fieldPath    string
}

// modelRefKey is the (tenant, modelRef) generation-bucket key. A single
// schema-change event for one model bumps its generation, which causes
// every cached path under that model to be treated as stale on the next
// lookup. Otter's S3-FIFO eviction reaps the stale entries naturally.
type modelRefKey struct {
	tenant       string
	entityName   string
	modelVersion string
}

// PathValidationCache is a loading-cache-style negative cache for the
// search-service's pre-execution field-path validation. It records
// "this path was confirmed absent from this (tenant, modelRef)'s
// schema FieldsMap as of generation N". A serial flood of validation
// requests for the same unknown path collapses into a single inner-
// store Get + RefreshAndGet pair instead of one pair per request.
//
// Invalidation is event-driven: the cache subscribes to the cluster
// broadcaster's "model.invalidate" topic. When an event arrives for
// (tenant, modelRef), the generation for that bucket is bumped so
// subsequent lookups treat any prior negative entry as stale. This
// preserves the issue #77 contract — a peer extending the schema
// must not be hidden behind a stale negative entry.
//
// The cache is bounded (10000 entries by default); otter's S3-FIFO
// policy evicts cold entries automatically, so unbounded memory growth
// is impossible even under adversarial traffic.
//
// The zero value is not safe — use NewPathValidationCache.
type PathValidationCache struct {
	cache *otter.Cache[pathCacheKey, uint64]

	mu          sync.RWMutex
	generations map[modelRefKey]uint64
}

// NewPathValidationCache constructs a PathValidationCache. broadcaster
// may be nil for single-node deployments; when non-nil the cache
// subscribes to the cluster invalidation topic and drops affected
// entries on every received event.
func NewPathValidationCache(broadcaster spi.ClusterBroadcaster) *PathValidationCache {
	c := otter.Must(&otter.Options[pathCacheKey, uint64]{
		MaximumSize: pathValidationCacheCapacity,
	})
	pvc := &PathValidationCache{
		cache:       c,
		generations: make(map[modelRefKey]uint64),
	}
	if broadcaster != nil {
		broadcaster.Subscribe(pathValidationCacheTopic, pvc.handleInvalidation)
	}
	return pvc
}

// IsAbsent reports whether the (tenant, ref, path) tuple has a current
// negative-cache entry — i.e. the path was previously confirmed absent
// from the model's FieldsMap and no schema-change event has fired for
// that model since. A cached entry whose generation is below the
// current bucket generation is treated as a miss (and will be reaped
// by S3-FIFO eviction in time).
func (c *PathValidationCache) IsAbsent(tenant string, ref spi.ModelRef, path string) bool {
	if c == nil {
		return false
	}
	key := pathCacheKey{
		tenant:       tenant,
		entityName:   ref.EntityName,
		modelVersion: ref.ModelVersion,
		fieldPath:    path,
	}
	cached, ok := c.cache.GetIfPresent(key)
	if !ok {
		return false
	}
	current := c.currentGeneration(modelRefKey{
		tenant:       tenant,
		entityName:   ref.EntityName,
		modelVersion: ref.ModelVersion,
	})
	return cached == current
}

// MarkAbsent records the (tenant, ref, path) tuple as confirmed absent
// at the current generation. Subsequent IsAbsent calls return true
// until the next invalidation event for that (tenant, ref).
func (c *PathValidationCache) MarkAbsent(tenant string, ref spi.ModelRef, path string) {
	if c == nil {
		return
	}
	gen := c.currentGeneration(modelRefKey{
		tenant:       tenant,
		entityName:   ref.EntityName,
		modelVersion: ref.ModelVersion,
	})
	c.cache.Set(pathCacheKey{
		tenant:       tenant,
		entityName:   ref.EntityName,
		modelVersion: ref.ModelVersion,
		fieldPath:    path,
	}, gen)
}

// MarkPresent removes any negative cache entry for the (tenant, ref,
// path) tuple. Defensive: callers invoke this when a path has been
// confirmed present in the schema, ensuring a follow-up rename or
// schema fix is reflected immediately rather than waiting for an
// invalidation event.
func (c *PathValidationCache) MarkPresent(tenant string, ref spi.ModelRef, path string) {
	if c == nil {
		return
	}
	c.cache.Invalidate(pathCacheKey{
		tenant:       tenant,
		entityName:   ref.EntityName,
		modelVersion: ref.ModelVersion,
		fieldPath:    path,
	})
}

// InvalidateRef bumps the generation for (tenant, ref) so every
// previously-cached negative entry under that model is treated as
// stale. The entries themselves are not eagerly removed; otter's S3-
// FIFO eviction handles cold reaping as new traffic arrives. This
// design avoids holding a per-bucket index of paths solely for the
// invalidation path, at the cost of a small amount of dead-but-stale
// space in the cache between events.
func (c *PathValidationCache) InvalidateRef(tenant string, ref spi.ModelRef) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generations[modelRefKey{
		tenant:       tenant,
		entityName:   ref.EntityName,
		modelVersion: ref.ModelVersion,
	}]++
}

func (c *PathValidationCache) currentGeneration(k modelRefKey) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generations[k]
}

// handleInvalidation is the gossip-topic subscriber. Decoded payloads
// reuse the modelcache encoding so the descriptor cache and the
// negative cache react to the same schema-change broadcasts in lock
// step.
func (c *PathValidationCache) handleInvalidation(payload []byte) {
	tenant, ref, ok := modelcache.DecodeInvalidation(payload)
	if !ok {
		return
	}
	c.InvalidateRef(tenant, ref)
}
