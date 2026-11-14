package vehicletracker

import (
	"fmt"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestJourneyTrackCacheSharesImmutableTrackAndFreshness(t *testing.T) {
	cache := journeyTrackCache{}
	now := time.Now()
	firstTrack := ctdf.JourneyTrack{
		PrimaryIdentifier:    "track",
		Track:                []ctdf.Location{{Type: "Point", Coordinates: []float64{-0.1, 51.5}}},
		ModificationDateTime: now,
	}
	cache.put("track", firstTrack, false, now)

	got, found, missing := cache.get("track", now.Add(time.Second))
	if !found || missing {
		t.Fatalf("track lookup = found %v, missing %v; want positive hit", found, missing)
	}
	if &got.Track[0] != &firstTrack.Track[0] {
		t.Fatal("cached track should reuse the immutable track slice")
	}

	newerTrack := ctdf.JourneyTrack{
		PrimaryIdentifier:    "track",
		Track:                []ctdf.Location{{Type: "Point", Coordinates: []float64{-0.2, 51.6}}},
		ModificationDateTime: now.Add(time.Minute),
	}
	cache.put("track", newerTrack, false, now.Add(2*time.Second))
	got, found, missing = cache.get("track", now.Add(3*time.Second))
	if !found || missing || &got.Track[0] != &newerTrack.Track[0] {
		t.Fatal("a newer fetched track should replace an older concurrent cache value")
	}
}

func TestJourneyTrackCacheBoundsEntriesAndExpiresNegativeMisses(t *testing.T) {
	cache := journeyTrackCache{}
	now := time.Now()
	for index := 0; index < journeyTrackCacheMaxEntries+1; index++ {
		reference := fmt.Sprintf("track-%d", index)
		cache.put(reference, ctdf.JourneyTrack{PrimaryIdentifier: reference}, false, now)
	}
	if len(cache.entries) != journeyTrackCacheMaxEntries {
		t.Fatalf("cache entries = %d, want %d", len(cache.entries), journeyTrackCacheMaxEntries)
	}
	if _, found, _ := cache.get("track-0", now); found {
		t.Fatal("expected the least recently used track to be evicted")
	}

	cache.put("missing", ctdf.JourneyTrack{}, true, now)
	if _, found, missing := cache.get("missing", now.Add(time.Second)); !found || !missing {
		t.Fatal("expected the recent missing reference to be negatively cached")
	}
	if _, found, _ := cache.get("missing", now.Add(journeyTrackMissCacheTTL+time.Second)); found {
		t.Fatal("expected the negative cache entry to expire")
	}
}

func TestCloneJourneyPathCopiesPathsWithoutChangingBase(t *testing.T) {
	base := &ctdf.Journey{
		PrimaryIdentifier: "journey",
		Calls: ctdf.JourneyCallsFromLegs([]*ctdf.JourneyPathItem{{
			OriginStopRef:      "origin",
			DestinationStopRef: "destination", TrackRef: "leg",
		}}...),
	}
	clone := cloneJourneyPath(base)
	clone.Calls[1].StopRef = "changed"
	clone.Calls[0].Outgoing.Track = []ctdf.Location{{Type: "Point"}}

	if base.Calls[1].StopRef != "destination" || len(base.Calls[0].Outgoing.Track) != 0 {
		t.Fatalf("event view mutation changed cached schedule: %#v", base.Calls)
	}
}
