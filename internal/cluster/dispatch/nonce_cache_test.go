package dispatch

import (
	"testing"
	"time"
)

func TestNonceCache_FirstObservationNotSeen(t *testing.T) {
	c := newNonceCache(60*time.Second, 100, time.Now)
	nonce := []byte("abcdefghijkl")
	if c.checkAndRecord(nonce, time.Now()) {
		t.Fatal("first observation reported as seen")
	}
}

func TestNonceCache_DuplicateIsSeen(t *testing.T) {
	c := newNonceCache(60*time.Second, 100, time.Now)
	nonce := []byte("abcdefghijkl")
	now := time.Now()
	if c.checkAndRecord(nonce, now) {
		t.Fatal("first observation reported as seen")
	}
	if !c.checkAndRecord(nonce, now) {
		t.Fatal("duplicate observation not detected")
	}
}

func TestNonceCache_EvictsAfterTTL(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time { return base }
	c := newNonceCache(60*time.Second, 100, nowFn)
	nonce := []byte("abcdefghijkl")

	c.checkAndRecord(nonce, base)

	// Advance clock past TTL.
	base = base.Add(120 * time.Second)

	// After TTL has elapsed, the same nonce is no longer considered seen.
	if c.checkAndRecord(nonce, base) {
		t.Fatal("nonce still seen after TTL expired")
	}
}

func TestNonceCache_BoundedSize_RejectsWhenFull(t *testing.T) {
	c := newNonceCache(60*time.Second, 3, time.Now)

	now := time.Now()
	n1 := []byte("aaaaaaaaaaaa")
	n2 := []byte("bbbbbbbbbbbb")
	n3 := []byte("cccccccccccc")
	n4 := []byte("dddddddddddd")

	if c.checkAndRecord(n1, now) {
		t.Fatal("n1 seen on first observation")
	}
	if c.checkAndRecord(n2, now) {
		t.Fatal("n2 seen on first observation")
	}
	if c.checkAndRecord(n3, now) {
		t.Fatal("n3 seen on first observation")
	}

	// Cache is at capacity. A new nonce should be rejected (fail-closed).
	// Returning "seen" is the conservative signal: the caller treats it as
	// a replay-reject and surfaces an error.
	if !c.checkAndRecord(n4, now) {
		t.Fatal("expected capacity-exceeded to fail closed (report seen), got not-seen")
	}
}

func TestNonceCache_CapacityRecoversAfterEviction(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time { return base }
	c := newNonceCache(60*time.Second, 2, nowFn)

	n1 := []byte("aaaaaaaaaaaa")
	n2 := []byte("bbbbbbbbbbbb")
	n3 := []byte("cccccccccccc")

	c.checkAndRecord(n1, base)
	c.checkAndRecord(n2, base)

	// Capacity full; n3 rejected.
	if !c.checkAndRecord(n3, base) {
		t.Fatal("expected n3 to be fail-closed rejected at capacity")
	}

	// Advance past TTL — both n1 and n2 should evict.
	base = base.Add(120 * time.Second)

	// Now there's room; n3 is fresh.
	if c.checkAndRecord(n3, base) {
		t.Fatal("n3 reported seen even after eviction freed space")
	}
}

func TestNonceCache_DifferentNonceLengthsDistinct(t *testing.T) {
	c := newNonceCache(60*time.Second, 100, time.Now)
	now := time.Now()

	a := []byte("aaaaaaaaaaaa")
	b := []byte("aaaaaaaaaaab")
	if c.checkAndRecord(a, now) {
		t.Fatal("a seen on first observation")
	}
	if c.checkAndRecord(b, now) {
		t.Fatal("b treated as seen; cache is not byte-distinct")
	}
}
