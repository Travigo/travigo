package identifiers

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSiriCacheSharesSuccessfulLookupsAndRetriesDatabaseErrors(t *testing.T) {
	cache := siriLookupCache[string]{limit: 2, ttl: time.Minute}
	databaseError := errors.New("database unavailable")
	if _, err := cache.get("run", func() (string, error) { return "", databaseError }); !errors.Is(err, databaseError) {
		t.Fatalf("got %v, want database error", err)
	}

	var loads atomic.Int32
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			got, err := cache.get("run", func() (string, error) {
				loads.Add(1)
				return "journey", nil
			})
			if err != nil || got != "journey" {
				t.Errorf("got %q, %v, want journey", got, err)
			}
		}()
	}
	close(start)
	workers.Wait()
	if got := loads.Load(); got != 1 {
		t.Fatalf("loaded %d times, want once", got)
	}
}

func TestSiriCacheReusesMissesAndKeepsRecentlyUsedEntries(t *testing.T) {
	cache := siriLookupCache[string]{limit: 2, ttl: time.Minute}
	cache.store("miss", "", errSiriJourney)
	cache.store("old", "journey", nil)

	got, err := cache.get("miss", func() (string, error) {
		t.Fatal("cached miss triggered another lookup")
		return "", nil
	})
	if got != "" || err != errSiriJourney {
		t.Fatalf("got %q, %v, want cached miss", got, err)
	}
	cache.store("new", "another journey", nil)
	if _, ok := cache.lookup("old"); ok {
		t.Fatal("least recently used entry was retained")
	}
	if _, ok := cache.lookup("miss"); !ok {
		t.Fatal("recently used entry was evicted")
	}
}
