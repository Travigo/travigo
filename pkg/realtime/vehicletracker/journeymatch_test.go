package vehicletracker

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
)

func TestMatchJourneyPositionUsesDistanceAlongLeg(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{
		Track: []ctdf.Location{
			{Type: "Point", Coordinates: []float64{0, 51}},
			{Type: "Point", Coordinates: []float64{0.01, 51}},
			{Type: "Point", Coordinates: []float64{0.02, 51}},
		},
	}}}

	match, ok := matchJourneyPositionWithStopFallback(journey, ctdf.Location{Type: "Point", Coordinates: []float64{0.015, 51}})
	if !ok {
		t.Fatal("expected match")
	}
	if match.PathIndex != 0 || match.LegProgress < 0.74 || match.LegProgress > 0.76 {
		t.Fatalf("got %#v", match)
	}
}

func TestMatchJourneyPositionFallsBackToStopPairWithoutTrack(t *testing.T) {
	firstStop := &ctdf.Stop{Location: &ctdf.Location{Type: "Point", Coordinates: []float64{0, 51}}}
	secondStop := &ctdf.Stop{Location: &ctdf.Location{Type: "Point", Coordinates: []float64{0.01, 51}}}
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{DestinationStop: firstStop},
		{DestinationStop: secondStop},
	}}

	match, ok := matchJourneyPositionWithStopFallback(journey, ctdf.Location{Type: "Point", Coordinates: []float64{0.0075, 51}})
	if !ok {
		t.Fatal("expected stop fallback match")
	}
	if match.PathIndex != 1 || !match.UsedGlobalTrack || match.HasTrackMatch || match.LegProgress < 0.7 || match.LegProgress > 0.8 {
		t.Fatalf("got %#v", match)
	}
}

func TestMatchJourneyProgressSkipsGlobalStopMapping(t *testing.T) {
	resetStopProjectionCacheForTest()
	journey := &ctdf.Journey{
		TrackRef: "shared-shape",
		Track: []ctdf.Location{
			{Type: "Point", Coordinates: []float64{0, 51}},
			{Type: "Point", Coordinates: []float64{0.01, 51}},
			{Type: "Point", Coordinates: []float64{0.02, 51}},
		},
		Path: []*ctdf.JourneyPathItem{{
			DestinationStop: &ctdf.Stop{Location: &ctdf.Location{Type: "Point", Coordinates: []float64{0.005, 51}}},
		}, {
			DestinationStop: &ctdf.Stop{Location: &ctdf.Location{Type: "Point", Coordinates: []float64{0.015, 51}}},
		}},
	}
	vehicle := ctdf.Location{Type: "Point", Coordinates: []float64{0.01, 51}}

	match, ok := matchJourneyProgress(journey, vehicle)
	if !ok || !match.HasTrackMatch || !match.UsedGlobalTrack {
		t.Fatalf("progress match = %#v, ok %t", match, ok)
	}
	if match.PathIndex != -1 || match.JourneyProgress < 0.49 || match.JourneyProgress > 0.51 {
		t.Fatalf("got %#v; want global progress without path mapping", match)
	}
	if got := stopProjectionCacheLenForTest(); got != 0 {
		t.Fatalf("progress-only matching populated %d stop projections", got)
	}

	fullMatch, ok := matchJourneyTrack(journey, vehicle, true)
	if !ok || !fullMatch.HasTrackMatch || fullMatch.PathIndex < 0 {
		t.Fatalf("full position match = %#v, ok %t", fullMatch, ok)
	}
	if got := stopProjectionCacheLenForTest(); got != 2 {
		t.Fatalf("full path mapping cached %d stop projections; want 2", got)
	}
}

func TestPathForGlobalProgressReusesSharedShapeAndKeysStopCoordinates(t *testing.T) {
	resetStopProjectionCacheForTest()
	track := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{0, 51}},
		{Type: "Point", Coordinates: []float64{0.01, 51}},
		{Type: "Point", Coordinates: []float64{0.02, 51}},
	}
	makeJourney := func(stops ...float64) *ctdf.Journey {
		paths := make([]*ctdf.JourneyPathItem, len(stops))
		for i, longitude := range stops {
			paths[i] = &ctdf.JourneyPathItem{DestinationStop: &ctdf.Stop{Location: &ctdf.Location{
				Type: "Point", Coordinates: []float64{longitude, 51},
			}}}
		}
		return &ctdf.Journey{TrackRef: "shared-shape", Track: track, Path: paths}
	}

	firstJourney := makeJourney(0.005, 0.015)
	if gotIndex, gotProgress := pathForGlobalProgress(firstJourney, 0.5); gotIndex != 1 || math.Abs(gotProgress-0.5) > 0.01 {
		t.Fatalf("first mapping = (%d, %f); want (1, about 0.5)", gotIndex, gotProgress)
	}
	if got := stopProjectionCacheLenForTest(); got != 2 {
		t.Fatalf("first journey cached %d stop projections; want 2", got)
	}

	secondJourney := makeJourney(0.006, 0.018)
	if gotIndex, gotProgress := pathForGlobalProgress(secondJourney, 0.6); gotIndex != 1 || math.Abs(gotProgress-0.5) > 0.01 {
		t.Fatalf("second mapping = (%d, %f); want (1, about 0.5)", gotIndex, gotProgress)
	}
	if got := stopProjectionCacheLenForTest(); got != 4 {
		t.Fatalf("different schedule stop coordinates produced %d entries; want 4", got)
	}

	if gotIndex, gotProgress := pathForGlobalProgress(firstJourney, 0.5); gotIndex != 1 || math.Abs(gotProgress-0.5) > 0.01 {
		t.Fatalf("reused mapping = (%d, %f); want (1, about 0.5)", gotIndex, gotProgress)
	}
	if got := stopProjectionCacheLenForTest(); got != 4 {
		t.Fatalf("reusing a journey added cache entries: got %d; want 4", got)
	}

	// The same ref can be rehydrated with replacement geometry. A distinct
	// backing slice is a new weak identity and must not reuse old projections.
	reloadedTrack := append([]ctdf.Location(nil), track...)
	reloadedTrack[1] = ctdf.Location{Type: "Point", Coordinates: []float64{0.01, 51.001}}
	reloaded := *firstJourney
	reloaded.Track = reloadedTrack
	pathForGlobalProgress(&reloaded, 0.5)
	if got := stopProjectionCacheLenForTest(); got != 6 {
		t.Fatalf("reloaded track reused stale projections: got %d entries; want 6", got)
	}
}

func TestPathForGlobalProgressPreservesProjectionSemantics(t *testing.T) {
	resetStopProjectionCacheForTest()
	loopTrack := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{0, 51}},
		{Type: "Point", Coordinates: []float64{0.01, 51}},
		{Type: "Point", Coordinates: []float64{0, 51}},
	}
	malformedTrack := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{0, 51}},
		{Type: "Point", Coordinates: []float64{0.005}},
		{Type: "Point", Coordinates: []float64{0.01, 51}},
		{Type: "Point", Coordinates: []float64{0.02, 51}},
	}
	validStop := func(longitude float64) *ctdf.JourneyPathItem {
		return &ctdf.JourneyPathItem{DestinationStop: &ctdf.Stop{Location: &ctdf.Location{Type: "Point", Coordinates: []float64{longitude, 51}}}}
	}
	invalidStop := &ctdf.JourneyPathItem{DestinationStop: &ctdf.Stop{Location: &ctdf.Location{Type: "Point", Coordinates: []float64{0}}}}

	cases := []struct {
		name           string
		track          []ctdf.Location
		path           []*ctdf.JourneyPathItem
		globalProgress float64
	}{
		{name: "loop tie keeps earliest segment", track: loopTrack, path: []*ctdf.JourneyPathItem{validStop(0.005), validStop(0.009)}, globalProgress: 0.2},
		{name: "malformed vertices and stops are skipped", track: malformedTrack, path: []*ctdf.JourneyPathItem{nil, invalidStop, {DestinationStop: &ctdf.Stop{}}, validStop(0.005), validStop(0.018)}, globalProgress: 0.6},
		{name: "decreasing loop progress preserves order", track: loopTrack, path: []*ctdf.JourneyPathItem{validStop(0.009), validStop(0.002)}, globalProgress: 0.8},
		{name: "zero usable segments falls back to last path", track: []ctdf.Location{{Type: "Point", Coordinates: []float64{0, 51}}}, path: []*ctdf.JourneyPathItem{validStop(0), validStop(0.005)}, globalProgress: 0.5},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			journey := &ctdf.Journey{TrackRef: "shape-" + test.name, Track: test.track, Path: test.path}
			wantIndex, wantProgress := pathForGlobalProgressUncached(journey, test.globalProgress)
			gotIndex, gotProgress := pathForGlobalProgress(journey, test.globalProgress)
			if gotIndex != wantIndex || math.Abs(gotProgress-wantProgress) > 1e-12 || (math.IsNaN(gotProgress) != math.IsNaN(wantProgress)) {
				t.Fatalf("got (%d, %.15f), want (%d, %.15f)", gotIndex, gotProgress, wantIndex, wantProgress)
			}
		})
	}
}

func TestStopProjectionCacheIsBoundedAndConcurrent(t *testing.T) {
	resetStopProjectionCacheForTest()
	track := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{0, 51}},
		{Type: "Point", Coordinates: []float64{0.01, 51}},
	}
	shape := newStopProjectionShape("many-stops", track)
	for i := 0; i < stopProjectionCacheMaxEntries+32; i++ {
		stop := ctdf.Location{Type: "Point", Coordinates: []float64{float64(i+1) / 100000, 51}}
		if _, ok := cachedStopProgress(shape, stop, track); !ok {
			t.Fatalf("stop %d did not project", i)
		}
	}
	if got := stopProjectionCacheLenForTest(); got != stopProjectionCacheMaxEntries {
		t.Fatalf("cache retained %d entries; want limit %d", got, stopProjectionCacheMaxEntries)
	}

	resetStopProjectionCacheForTest()
	stop := ctdf.Location{Type: "Point", Coordinates: []float64{0.005, 51}}
	const goroutines = 32
	var wait sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if progress, ok := cachedStopProgress(shape, stop, track); !ok || progress < 0.49 || progress > 0.51 {
				t.Errorf("progress = %f, ok %t", progress, ok)
			}
		}()
	}
	wait.Wait()
	if got := stopProjectionCacheLenForTest(); got != 1 {
		t.Fatalf("concurrent lookup left %d entries; want 1", got)
	}
}

func pathForGlobalProgressUncached(journey *ctdf.Journey, globalProgress float64) (int, float64) {
	previous := 0.0
	for index, path := range journey.Path {
		if path == nil || path.DestinationStop == nil || path.DestinationStop.Location == nil || len(path.DestinationStop.Location.Coordinates) < 2 {
			continue
		}
		_, destinationProgress, ok := closestTrackPosition(*path.DestinationStop.Location, journey.Track)
		if !ok {
			continue
		}
		if globalProgress <= destinationProgress || index == len(journey.Path)-1 {
			span := destinationProgress - previous
			if span <= 0 {
				return index, 0
			}
			return index, math.Max(0, math.Min(1, (globalProgress-previous)/span))
		}
		previous = destinationProgress
	}
	return len(journey.Path) - 1, 1
}

func resetStopProjectionCacheForTest() {
	globalStopProjectionCache.mu.Lock()
	globalStopProjectionCache.entries = nil
	globalStopProjectionCache.lru.Init()
	globalStopProjectionCache.mu.Unlock()
}

func stopProjectionCacheLenForTest() int {
	globalStopProjectionCache.mu.Lock()
	defer globalStopProjectionCache.mu.Unlock()
	return globalStopProjectionCache.lru.Len()
}

func TestServiceTimeOnDateRetainsServiceDayOffset(t *testing.T) {
	serviceDate := time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)
	encoded := time.Date(0, time.January, 2, 1, 30, 0, 0, time.UTC)
	got := serviceTimeOnDate(serviceDate, encoded, time.UTC)
	want := time.Date(2026, time.July, 11, 1, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestServiceTimeOnDateUsesClockChangeServiceOrigin(t *testing.T) {
	location, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	clock := time.Date(0, 1, 1, 8, 0, 0, 0, time.UTC)
	want := time.Date(2026, 3, 29, 8, 0, 0, 0, location)
	if got := serviceTimeOnDate(date, clock, location); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEstimateFutureStopsAbsorbsDelayDuringScheduledDwell(t *testing.T) {
	base := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	paths := []*ctdf.JourneyPathItem{
		{DestinationStopRef: "timed-stop", DestinationArrivalTime: base.Add(5 * time.Minute)},
		{
			OriginStopRef:          "timed-stop",
			OriginDepartureTime:    base.Add(7 * time.Minute),
			DestinationStopRef:     "downstream-stop",
			DestinationArrivalTime: base.Add(12 * time.Minute),
		},
		{OriginStopRef: "downstream-stop", OriginDepartureTime: base.Add(12 * time.Minute), DestinationStopRef: "terminus", DestinationArrivalTime: base.Add(18 * time.Minute)},
	}

	tests := []struct {
		name                  string
		offset                time.Duration
		wantTimedArrival      time.Time
		wantTimedDeparture    time.Time
		wantDownstreamArrival time.Time
	}{
		{
			name:                  "delay is fully absorbed",
			offset:                time.Minute,
			wantTimedArrival:      base.Add(6 * time.Minute),
			wantTimedDeparture:    base.Add(7 * time.Minute),
			wantDownstreamArrival: base.Add(12 * time.Minute),
		},
		{
			name:                  "remaining delay is carried forward",
			offset:                3 * time.Minute,
			wantTimedArrival:      base.Add(8 * time.Minute),
			wantTimedDeparture:    base.Add(8 * time.Minute),
			wantDownstreamArrival: base.Add(13 * time.Minute),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stops := estimateFutureStops(paths, 0, test.offset)
			if !stops[0].ArrivalTime.Equal(test.wantTimedArrival) || !stops[0].DepartureTime.Equal(test.wantTimedDeparture) {
				t.Fatalf("timed stop = arrival %s, departure %s; want arrival %s, departure %s", stops[0].ArrivalTime, stops[0].DepartureTime, test.wantTimedArrival, test.wantTimedDeparture)
			}
			if !stops[1].ArrivalTime.Equal(test.wantDownstreamArrival) {
				t.Fatalf("downstream arrival = %s, want %s", stops[1].ArrivalTime, test.wantDownstreamArrival)
			}
		})
	}
}

func TestJourneyStopOccurrenceIndexHandlesRepeatedStop(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "waterloo", DestinationStopRef: "vauxhall"},
		{OriginStopRef: "vauxhall", DestinationStopRef: "waterloo"},
		{OriginStopRef: "waterloo", DestinationStopRef: "clapham"},
	}}
	if got := journeyStopOccurrenceIndex(journey, "waterloo", 0); got != 0 {
		t.Fatalf("expected first Waterloo call at 0, got %d", got)
	}
	if got := journeyStopOccurrenceIndex(journey, "waterloo", 1); got != 2 {
		t.Fatalf("expected second Waterloo call at 2, got %d", got)
	}
}

func TestSelectLocationCandidateRequiresClearWinner(t *testing.T) {
	if got := selectLocationCandidate([]locationJourneyCandidate{{journeyID: "a", distance: 10, score: 10}, {journeyID: "b", distance: 20, score: 20}}); got != "" {
		t.Fatalf("got %q, want no ambiguous match", got)
	}
	if got := selectLocationCandidate([]locationJourneyCandidate{{journeyID: "a", distance: 10, score: 10}, {journeyID: "b", distance: 50, score: 50}}); got != "a" {
		t.Fatalf("got %q, want a", got)
	}
}

func TestScoreLocationCandidatePrefersForwardProgressOnSameJourney(t *testing.T) {
	history := &vehicleJourneyHistory{JourneyID: "same", Progress: 0.5}
	if got := scoreLocationCandidate(100, 0.6, "same", history); got >= 100 {
		t.Fatalf("forward same-journey score = %f", got)
	}
	if got := scoreLocationCandidate(100, 0.2, "same", history); got <= 100 {
		t.Fatalf("backwards same-journey score = %f", got)
	}
}

func TestGeometryMetricsCoverBothMatchingPathsWithoutDoubleCounting(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{Track: []ctdf.Location{{Type: "Point", Coordinates: []float64{0, 51}}, {Type: "Point", Coordinates: []float64{0.01, 51}}}}}}
	vehicle := ctdf.Location{Type: "Point", Coordinates: []float64{0.005, 51}}
	for _, match := range []func(*ctdf.Journey, ctdf.Location) (journeyPositionMatch, bool){matchJourneyPositionWithStopFallback, matchJourneyProgress} {
		before := telemetry.Snapshot().Stages["geometry"].Count
		if _, ok := match(journey, vehicle); !ok {
			t.Fatal("expected match")
		}
		if got := telemetry.Snapshot().Stages["geometry"].Count - before; got != 1 {
			t.Fatalf("matching counted %d times", got)
		}
		before = telemetry.Snapshot().Stages["geometry"].Count
		if _, ok := match(nil, vehicle); ok {
			t.Fatal("nil journey matched")
		}
		if telemetry.Snapshot().Stages["geometry"].Count-before != 1 {
			t.Fatal("failed matching not timed")
		}
	}
}
