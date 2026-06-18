package vehicletracker

import (
	"strconv"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestBuildPathByOriginStopRef(t *testing.T) {
	path := []*ctdf.JourneyPathItem{
		{OriginStopRef: "stop-a", DestinationStopRef: "stop-b"},
		{OriginStopRef: "stop-b", DestinationStopRef: "stop-c"},
	}

	pathByOrigin := buildPathByOriginStopRef(path)

	if pathByOrigin["stop-a"] != path[0] {
		t.Fatal("expected stop-a to map to first path item")
	}
	if pathByOrigin["stop-b"] != path[1] {
		t.Fatal("expected stop-b to map to second path item")
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
