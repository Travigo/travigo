package tflarrivals

import (
	"errors"
	"github.com/travigo/travigo/pkg/ctdf"
	"testing"
	"time"
)

func TestValidatedStopTimezone(t *testing.T) {
	if got := validatedStopTimezone("invalid/timezone"); got != time.UTC {
		t.Fatal("invalid zone must safely fall back to UTC")
	}
	first := validatedStopTimezone("Europe/London")
	if first != validatedStopTimezone("Europe/London") {
		t.Fatal("valid timezone not reused")
	}
}
func TestTfLPrimaryStopCacheHit(t *testing.T) {
	// A warm cache must resolve without any database connection.
	stopTflStopCacheMutex.Lock()
	old, expiry := stopTflStopCache["test"], stopTflStopCacheExpires["test"]
	stopTflStopCache["test"] = &ctdf.Stop{PrimaryIdentifier: "test"}
	want := stopTflStopCache["test"]
	stopTflStopCacheExpires["test"] = time.Now().Add(time.Minute)
	stopTflStopCacheMutex.Unlock()
	defer func() {
		stopTflStopCacheMutex.Lock()
		stopTflStopCache["test"] = old
		stopTflStopCacheExpires["test"] = expiry
		stopTflStopCacheMutex.Unlock()
	}()
	if getStopFromTfLStop("test") != want {
		t.Fatal("warm stop not reused")
	}
}

func TestExpiredTfLStopCacheRefresh(t *testing.T) {
	id := "expired-test"
	old := &ctdf.Stop{PrimaryIdentifier: "last-good"}
	expired := time.Now().Add(-time.Minute)
	stopTflStopCacheMutex.Lock()
	stopTflStopCache[id], stopTflStopCacheExpires[id] = old, expired
	stopTflStopCacheMutex.Unlock()
	defer func() {
		stopTflStopCacheMutex.Lock()
		delete(stopTflStopCache, id)
		delete(stopTflStopCacheExpires, id)
		stopTflStopCacheMutex.Unlock()
	}()
	calls := 0
	fail := func(string) (*ctdf.Stop, error) { calls++; return nil, errors.New("database unavailable") }
	for i := 0; i < 2; i++ {
		if got := getStopFromTfLStopWithLookup(id, fail); got != old {
			t.Fatal("lost last-good stop on refresh failure")
		}
	}
	if calls != 2 {
		t.Fatal("failed refresh must remain retryable")
	}
	if got := getStopFromTfLStopWithLookup(id, func(string) (*ctdf.Stop, error) { return nil, nil }); got != nil {
		t.Fatal("confirmed absence returned stale stop")
	}
	if got := getStopFromTfLStopWithLookup(id, fail); got != nil {
		t.Fatal("confirmed absence resurrected stale stop")
	}
	replacement := &ctdf.Stop{PrimaryIdentifier: "new"}
	if got := getStopFromTfLStopWithLookup(id, func(string) (*ctdf.Stop, error) { return replacement, nil }); got != replacement {
		t.Fatal("successful refresh not used")
	}
	if got := getStopFromTfLStopWithLookup(id, fail); got != replacement || calls != 3 {
		t.Fatal("successful refresh not cached")
	}
}
