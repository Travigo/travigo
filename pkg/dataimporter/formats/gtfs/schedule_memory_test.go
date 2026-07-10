package gtfs

import (
	"crypto/sha256"
	"testing"
)

func TestBoundedPathPatternCacheEvictsOldestEntry(t *testing.T) {
	cache := newBoundedPathPatternCache(2)
	first := sha256.Sum256([]byte("first"))
	second := sha256.Sum256([]byte("second"))
	third := sha256.Sum256([]byte("third"))

	cache.Put(first, true)
	cache.Put(second, false)
	cache.Put(third, true)

	if _, exists := cache.Get(first); exists {
		t.Fatal("expected oldest pattern to be evicted")
	}
	if split, exists := cache.Get(second); !exists || split {
		t.Fatal("expected second pattern and its failed-split result to remain cached")
	}
	if split, exists := cache.Get(third); !exists || !split {
		t.Fatal("expected newest successful pattern to remain cached")
	}
	if cache.evictions != 1 {
		t.Fatalf("expected one eviction, got %d", cache.evictions)
	}
}

func TestJourneyPathPatternHashRemainsCompatible(t *testing.T) {
	stopTimes := []StopTime{{StopID: "stop-a"}, {StopID: "stop-b"}}
	want := sha256.Sum256([]byte("shape-1\x00stop-a\x00stop-b"))

	if got := journeyPathPatternHash("shape-1", stopTimes); got != want {
		t.Fatalf("pattern hash changed: got %x want %x", got, want)
	}
}
