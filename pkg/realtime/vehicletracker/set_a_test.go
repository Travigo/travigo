package vehicletracker

import (
	"fmt"
	"github.com/travigo/travigo/pkg/ctdf"
	"testing"
	"time"
)

func predictionFixture(n int) *ctdf.RealtimeJourney {
	base := time.Date(0, 1, 1, 10, 0, 0, 0, time.UTC)
	j := &ctdf.RealtimeJourney{Journey: &ctdf.Journey{}, Stops: map[string]*ctdf.RealtimeJourneyStops{}}
	for i := 0; i < n; i++ {
		j.Journey.Path = append(j.Journey.Path, &ctdf.JourneyPathItem{OriginStopRef: fmt.Sprint(i % 5), DestinationStopRef: fmt.Sprint((i + 1) % 5), OriginDepartureTime: base.Add(time.Duration(i) * time.Minute), DestinationArrivalTime: base.Add(time.Duration(i+1) * time.Minute)})
	}
	for _, s := range estimateFutureStops(j.Journey.Path, 0, 0) {
		j.Stops[ctdf.RealtimeJourneyStopKey(s.StopRef, s.JourneyStopIndex)] = s
	}
	return j
}

func TestFutureStopsNeedRebuild(t *testing.T) {
	j := predictionFixture(20)
	if futureStopsNeedRebuild(j, journeyDestinationKeys(j.Journey), 0, 0, time.UTC) {
		t.Fatal("unchanged predictions rebuilt")
	}
	delete(j.Stops, ctdf.RealtimeJourneyStopKey(j.Journey.Path[5].DestinationStopRef, 6))
	if !futureStopsNeedRebuild(j, journeyDestinationKeys(j.Journey), 0, 0, time.UTC) {
		t.Fatal("missing prediction ignored")
	}
	j = predictionFixture(20)
	j.Journey.Path[5].DestinationArrivalTime = j.Journey.Path[5].DestinationArrivalTime.Add(2 * time.Minute)
	if !futureStopsNeedRebuild(j, journeyDestinationKeys(j.Journey), 0, 0, time.UTC) {
		t.Fatal("timetable edit ignored")
	}
	j = predictionFixture(20)
	if !futureStopsNeedRebuild(j, journeyDestinationKeys(j.Journey), 0, time.Minute, time.UTC) {
		t.Fatal("offset change ignored")
	}
}

func TestJourneyCacheUsesAccessOrder(t *testing.T) {
	c := &BatchConsumer{journeyCache: map[string]*cachedTrackedJourney{}}
	for i := 0; i < trackedJourneyCacheMaxEntries; i++ {
		e := &cachedTrackedJourney{journeyID: fmt.Sprint(i)}
		c.journeyCache[e.journeyID] = e
		c.touchJourneyCacheLocked(e)
	}
	// A feed timestamp older than every entry must still refresh access order.
	if _, err := c.getCachedTrackedJourney("0", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	e := &cachedTrackedJourney{journeyID: "new"}
	c.journeyCache[e.journeyID] = e
	c.touchJourneyCacheLocked(e)
	c.pruneJourneyCacheLocked()
	if c.journeyCache["0"] == nil || c.journeyCache["1"] != nil {
		t.Fatal("eviction did not follow access order")
	}
}

var benchmarkPredictionSink []*ctdf.RealtimeJourneyStops
var benchmarkRebuildSink bool

func BenchmarkUnchangedPredictions(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		j := predictionFixture(n)
		keys := journeyDestinationKeys(j.Journey)
		b.Run(fmt.Sprintf("%d/before", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkPredictionSink = estimateFutureStops(j.Journey.Path, 0, 0)
			}
		})
		b.Run(fmt.Sprintf("%d/after", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkRebuildSink = futureStopsNeedRebuild(j, keys, 0, 0, time.UTC)
			}
		})
	}
}
