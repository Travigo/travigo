package vehicletracker

import (
	"strconv"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestJourneyStopOccurrencesPreserveRepeatedAndTerminalStops(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "waterloo", DestinationStopRef: "vauxhall"},
		{OriginStopRef: "vauxhall", DestinationStopRef: "waterloo"},
		{OriginStopRef: "waterloo", DestinationStopRef: "clapham"},
	}}

	got := journeyStopOccurrences(journey)
	want := map[string][]int{
		"waterloo": {0, 2},
		"vauxhall": {1},
		"clapham":  {3},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v stop refs, want %v", got, want)
	}
	for stopRef, wantIndices := range want {
		gotIndices := got[stopRef]
		if len(gotIndices) != len(wantIndices) {
			t.Fatalf("%s occurrences = %v, want %v", stopRef, gotIndices, wantIndices)
		}
		for i := range wantIndices {
			if gotIndices[i] != wantIndices[i] {
				t.Fatalf("%s occurrences = %v, want %v", stopRef, gotIndices, wantIndices)
			}
		}
	}
}

func TestPruneJourneyCacheLockedRemovesOldestEntries(t *testing.T) {
	consumer := &BatchConsumer{
		journeyCache: map[string]*cachedTrackedJourney{},
	}

	for index := 0; index < trackedJourneyCacheMaxEntries+2; index += 1 {
		consumer.journeyCache[strconv.Itoa(index)] = &cachedTrackedJourney{
			LastUsed: time.Unix(int64(index), 0),
		}
	}

	consumer.pruneJourneyCacheLocked()

	if len(consumer.journeyCache) != trackedJourneyCacheMaxEntries {
		t.Fatalf("expected %d entries, got %d", trackedJourneyCacheMaxEntries, len(consumer.journeyCache))
	}
	if consumer.journeyCache["0"] != nil || consumer.journeyCache["1"] != nil {
		t.Fatal("expected oldest cache entries to be pruned")
	}
}
