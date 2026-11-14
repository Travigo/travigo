package vehicletracker

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestCachedCallOffsetsMatchJourneyIterators(t *testing.T) {
	journey := &ctdf.Journey{Calls: []ctdf.JourneyCall{
		{StopRef: "A", Start: true}, {StopRef: "B", End: true},
		{StopRef: "A", Start: true}, {StopRef: "B"}, {StopRef: "A", End: true},
	}}
	entry := &cachedTrackedJourney{Journey: journey}
	// Hydrated views copy calls; offsets must point into the current view.
	view := *journey
	view.Calls = append([]ctdf.JourneyCall(nil), journey.Calls...)
	for index, call := range view.StopCalls() {
		if got := entry.stopCall(&view, index); got != call {
			t.Fatalf("stop %d: got %p want %p", index, got, call)
		}
	}
	index := 0
	for origin, destination := range view.LegCalls() {
		gotOrigin, gotDestination := entry.legEndpoints(&view, index)
		if gotOrigin != origin || gotDestination != destination {
			t.Fatalf("leg %d has wrong endpoints", index)
		}
		index++
	}
	if entry.stopCall(&view, -1) != nil || entry.stopCall(&view, 4) != nil {
		t.Fatal("invalid stop index accepted")
	}
	if from, to := entry.legEndpoints(&view, index); from != nil || to != nil {
		t.Fatal("invalid leg index accepted")
	}
}

func TestTrackedServiceCacheExpiryAndFailures(t *testing.T) {
	var cache trackedServiceCache
	service := &ctdf.Service{PrimaryIdentifier: "service"}
	calls := 0
	load := func() (*ctdf.Service, error) { calls++; return service, nil }
	first, firstErr := cache.get("service", load)
	second, secondErr := cache.get("service", load)
	if first != service || second != service || firstErr != nil || secondErr != nil || calls != 1 {
		t.Fatal("service was not cached")
	}
	cache.mu.Lock()
	cache.entries["service"] = trackedServiceEntry{service, time.Now().Add(-time.Second)}
	cache.mu.Unlock()
	refreshed, err := cache.get("service", load)
	if refreshed != service || err != nil || calls != 2 {
		t.Fatal("expired service was not reloaded")
	}
	fail := func() (*ctdf.Service, error) { calls++; return nil, errors.New("temporary failure") }
	_, firstErr = cache.get("failed", fail)
	_, secondErr = cache.get("failed", fail)
	if firstErr == nil || secondErr == nil || calls != 4 {
		t.Fatal("failed lookup was cached")
	}
}

func TestTrackedServiceCacheCoalescesLoads(t *testing.T) {
	var cache trackedServiceCache
	var loads atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	load := func() (*ctdf.Service, error) {
		if loads.Add(1) == 1 {
			close(entered)
		}
		<-release
		return &ctdf.Service{}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); cache.get("service", load) }()
	}
	<-entered
	close(release)
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatalf("got %d loads", loads.Load())
	}
}

func TestCachedTrackLengthsAndReplacement(t *testing.T) {
	point := func(lon float64) ctdf.Location { return ctdf.Location{Coordinates: []float64{lon, 51}} }
	track := []ctdf.Location{point(0), point(.01), {}, point(.03), point(.04)}
	lengths := cachedTrackLengths(track)
	if len(lengths) != len(track) || lengths[1] <= 0 || lengths[1] != lengths[2] || lengths[2] != lengths[3] || lengths[4] <= lengths[3] {
		t.Fatalf("invalid lengths: %v", lengths)
	}
	if again := cachedTrackLengths(track); &again[0] != &lengths[0] {
		t.Fatal("lengths were not reused")
	}
	replacement := []ctdf.Location{point(0), point(.02), {}, point(.03), point(.04)}
	if next := cachedTrackLengths(replacement); math.Abs(next[1]-lengths[1]) < 1 {
		t.Fatal("replacement geometry reused stale lengths")
	}
	if _, progress, ok := closestTrackPosition(point(.035), track); !ok || progress < .74 || progress > .76 {
		t.Fatalf("unexpected progress %v, %v", progress, ok)
	}
}
