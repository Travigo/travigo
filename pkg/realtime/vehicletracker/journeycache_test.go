package vehicletracker

import (
	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"strconv"
	"sync"
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
		trackedJourneyCache: &trackedJourneyCache{journeyCache: map[string]*cachedTrackedJourney{}},
	}

	for index := 0; index < trackedJourneyCacheMaxEntries+2; index += 1 {
		entry := &cachedTrackedJourney{journeyID: strconv.Itoa(index), LastUsed: time.Unix(int64(index), 0)}
		consumer.journeyCache[entry.journeyID] = entry
		consumer.touchJourneyCacheLocked(entry)
	}

	consumer.pruneJourneyCacheLocked()

	if len(consumer.journeyCache) != trackedJourneyCacheMaxEntries {
		t.Fatalf("expected %d entries, got %d", trackedJourneyCacheMaxEntries, len(consumer.journeyCache))
	}
	if consumer.journeyCache["0"] != nil || consumer.journeyCache["1"] != nil {
		t.Fatal("expected oldest cache entries to be pruned")
	}
}

func TestConsumersShareJourneyLoadAndHydration(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("concurrent consumers", func(mt *mtest.T) {
		oldDatabase, oldConnection := database.Instance, redis_client.QueueConnection
		oldCache := processTrackedJourneyCache
		processTrackedJourneyCache = &trackedJourneyCache{}
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		redis_client.QueueConnection = rmq.NewTestConnection()
		mt.Cleanup(func() {
			database.Instance, redis_client.QueueConnection = oldDatabase, oldConnection
			processTrackedJourneyCache = oldCache
		})
		identifier := mt.Name()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, bson.D{
				{Key: "primaryidentifier", Value: identifier},
				{Key: "path", Value: bson.A{bson.D{{Key: "originstopref", Value: "origin"}, {Key: "destinationstopref", Value: "dest"}}}},
			}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".services", mtest.FirstBatch, bson.D{{Key: "primaryidentifier", Value: "service"}}),
		)
		consumers := make([]*BatchConsumer, numConsumers)
		for i := range consumers {
			consumers[i] = NewBatchConsumer(i)
			if consumers[i].trackedJourneyCache != processTrackedJourneyCache {
				mt.Fatal("consumer does not use the process cache")
			}
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		entries := make([]*cachedTrackedJourney, 50)
		errs := make([]error, len(entries))
		for i := range entries {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				entries[i], errs[i] = consumers[i%numConsumers].getCachedTrackedJourney(identifier, time.Now())
				if errs[i] == nil {
					entries[i].journeyForProgress()
				}
			}(i)
		}
		close(start)
		wg.Wait()
		for i, entry := range entries {
			if errs[i] != nil {
				mt.Fatalf("lookup %d: %v", i, errs[i])
			}
			if entry != entries[0] {
				mt.Fatal("consumers received separate cache entries")
			}
		}
		if got := len(mt.GetAllStartedEvents()); got != 2 {
			mt.Fatalf("got %d database commands, want one journey and one service load", got)
		}
		if got := entries[0].StopOccurrences["dest"]; len(got) != 1 || got[0] != 1 {
			mt.Fatalf("shared stop occurrences = %v", got)
		}
	})
}
