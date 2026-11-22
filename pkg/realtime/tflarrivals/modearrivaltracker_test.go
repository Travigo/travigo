package tflarrivals

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/realtime/vehicledataprovider/dvlaves"
	"github.com/travigo/travigo/pkg/redis_client"
)

func TestModeArrivalsURLRequestsAllPredictionsPerStop(t *testing.T) {
	requestURL, err := url.Parse(modeArrivalsURL("tube", "test-key"))
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}

	if got, want := requestURL.Path, "/mode/tube/arrivals"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got, want := requestURL.Query().Get("count"), "-1"; got != want {
		t.Errorf("count = %q, want %q", got, want)
	}
	if got, want := requestURL.Query().Get("app_key"), "test-key"; got != want {
		t.Errorf("app_key = %q, want %q", got, want)
	}
}

func TestModeArrivalsURLLimitsBusPredictionsPerStop(t *testing.T) {
	requestURL, err := url.Parse(modeArrivalsURL("bus", "test-key"))
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}

	if got, want := requestURL.Query().Get("count"), "8"; got != want {
		t.Errorf("count = %q, want %q", got, want)
	}
}

func TestTFLTrackerRuntimeJourneyFilter(t *testing.T) {
	filter := newTFLTrackerRuntimeJourneyFilter([]TflTracker{
		{Line: "1", TripID: "trip-a"},
		{Line: "1", TripID: "trip-a"},
		{Line: "2", TripID: "trip-b"},
	})

	tests := []struct {
		name   string
		line   string
		tripID string
		want   bool
	}{
		{name: "tracked pair", line: "1", tripID: "trip-a", want: true},
		{name: "second tracked pair", line: "2", tripID: "trip-b", want: true},
		{name: "line does not imply trip", line: "1", tripID: "trip-b", want: false},
		{name: "trip does not imply line", line: "2", tripID: "trip-a", want: false},
		{name: "unknown pair", line: "3", tripID: "trip-c", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := filter(test.line, test.tripID); got != test.want {
				t.Errorf("filter(%q, %q) = %t, want %t", test.line, test.tripID, got, test.want)
			}
		})
	}
}

func TestBusArrivalLookupIdentifiesUniqueVehicleJourney(t *testing.T) {
	lookup := buildBusArrivalLookup([]ArrivalPrediction{
		{ModeName: "bus", LineID: "1", Direction: "inbound", VehicleID: "vehicle-1", DestinationNaptanID: "stop-a", TripID: "trip-a"},
		{ModeName: "bus", LineID: "1", Direction: "inbound", VehicleID: "vehicle-1", DestinationNaptanID: "stop-a", TripID: "trip-a"},
		{ModeName: "bus", LineID: "2", Direction: "outbound", VehicleID: "vehicle-2", DestinationNaptanID: "stop-b", TripID: "trip-b"},
		{ModeName: "bus", LineID: "2", Direction: "outbound", VehicleID: "vehicle-2", DestinationNaptanID: "stop-c", TripID: "trip-c"},
	})

	tripID, err := identifyBusFromLookup(BusMonitorEvent{NumberPlate: "vehicle-1"}, lookup)
	if err != nil {
		t.Fatalf("identify unique vehicle: %v", err)
	}
	if got, want := tripID, "trip-a"; got != want {
		t.Errorf("trip ID = %q, want %q", got, want)
	}

	if _, err := identifyBusFromLookup(BusMonitorEvent{NumberPlate: "vehicle-2"}, lookup); err == nil {
		t.Error("expected ambiguous vehicle to fail identification")
	}
	if _, err := identifyBusFromLookup(BusMonitorEvent{NumberPlate: "unknown"}, lookup); err == nil {
		t.Error("expected unknown vehicle to fail identification")
	}
}

func TestReconcileTFLRealtimeStopsReplacesRevisedPrediction(t *testing.T) {
	oldArrival := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	newArrival := oldArrival.Add(time.Minute)
	previous := map[string]*ctdf.RealtimeJourneyStops{
		"waterloo@0": realtimeStop("waterloo", oldArrival, ctdf.RealtimeJourneyStopTimeEstimatedFuture),
	}
	current := []*ctdf.RealtimeJourneyStops{
		realtimeStop("waterloo", newArrival, ctdf.RealtimeJourneyStopTimeEstimatedFuture),
	}

	stops := reconcileTFLRealtimeStops(previous, current, map[string]int{"waterloo": 1})
	if len(stops) != 1 {
		t.Fatalf("stop count = %d, want 1", len(stops))
	}
	if !stops[0].ArrivalTime.Equal(newArrival) {
		t.Errorf("arrival = %s, want revised arrival %s", stops[0].ArrivalTime, newArrival)
	}
	if stops[0].TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture {
		t.Errorf("time type = %q, want future estimate", stops[0].TimeType)
	}
}

func TestReconcileTFLRealtimeStopsMovesDisappearedPredictionToHistory(t *testing.T) {
	waterlooArrival := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	lambethArrival := waterlooArrival.Add(2 * time.Minute)
	previous := map[string]*ctdf.RealtimeJourneyStops{
		"waterloo@0": realtimeStop("waterloo", waterlooArrival, ctdf.RealtimeJourneyStopTimeEstimatedFuture),
	}
	current := []*ctdf.RealtimeJourneyStops{
		realtimeStop("lambeth", lambethArrival, ctdf.RealtimeJourneyStopTimeEstimatedFuture),
	}

	stops := reconcileTFLRealtimeStops(previous, current, map[string]int{"waterloo": 1, "lambeth": 1})
	if len(stops) != 2 {
		t.Fatalf("stop count = %d, want 2", len(stops))
	}
	if stops[0].StopRef != "waterloo" || stops[0].TimeType != ctdf.RealtimeJourneyStopTimeHistorical {
		t.Errorf("first stop = %#v, want historical Waterloo", stops[0])
	}
	if stops[1].StopRef != "lambeth" || stops[1].TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture {
		t.Errorf("second stop = %#v, want future Lambeth", stops[1])
	}
}

func TestReconcileTFLRealtimeStopsRepairsDuplicateHistory(t *testing.T) {
	start := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	previous := map[string]*ctdf.RealtimeJourneyStops{
		"waterloo@0": realtimeStop("waterloo", start, ctdf.RealtimeJourneyStopTimeHistorical),
		"waterloo@1": realtimeStop("waterloo", start.Add(time.Minute), ctdf.RealtimeJourneyStopTimeHistorical),
		"waterloo@2": realtimeStop("waterloo", start.Add(2*time.Minute), ctdf.RealtimeJourneyStopTimeHistorical),
	}
	current := []*ctdf.RealtimeJourneyStops{
		realtimeStop("lambeth", start.Add(3*time.Minute), ctdf.RealtimeJourneyStopTimeEstimatedFuture),
	}

	stops := reconcileTFLRealtimeStops(previous, current, map[string]int{"waterloo": 1, "lambeth": 1})
	waterlooCount := 0
	for _, stop := range stops {
		if stop.StopRef == "waterloo" {
			waterlooCount++
		}
	}
	if waterlooCount != 1 {
		t.Errorf("Waterloo count = %d, want 1", waterlooCount)
	}
}

func TestReconcileTFLRealtimeStopsPreservesGenuineRepeatedCall(t *testing.T) {
	start := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	previous := map[string]*ctdf.RealtimeJourneyStops{
		"loop@0": realtimeStop("loop", start, ctdf.RealtimeJourneyStopTimeHistorical),
	}
	current := []*ctdf.RealtimeJourneyStops{
		realtimeStop("loop", start.Add(time.Hour), ctdf.RealtimeJourneyStopTimeEstimatedFuture),
	}

	stops := reconcileTFLRealtimeStops(previous, current, map[string]int{"loop": 2})
	if len(stops) != 2 {
		t.Fatalf("stop count = %d, want 2 genuine calls", len(stops))
	}
	if stops[0].TimeType != ctdf.RealtimeJourneyStopTimeHistorical || stops[1].TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture {
		t.Errorf("calls = %#v, want historical then future", stops)
	}
}

func realtimeStop(stopRef string, arrival time.Time, timeType ctdf.RealtimeJourneyStopTimeType) *ctdf.RealtimeJourneyStops {
	return &ctdf.RealtimeJourneyStops{
		StopRef:       stopRef,
		ArrivalTime:   arrival,
		DepartureTime: arrival,
		TimeType:      timeType,
	}
}

func TestTfLBusArrivalsQueueDVLAEnrichmentOnlyWhenCreated(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	old := redis_client.Client
	redis_client.Client = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = redis_client.Client.Close()
		redis_client.Client = old
	})
	t.Setenv(dvlaves.APIKeyEnvironmentVariable, "test-key")

	now := time.Now().UTC()
	stopIDs := []string{"dvla-test-stop-a", "dvla-test-stop-b"}
	stopTflStopCacheMutex.Lock()
	for _, id := range stopIDs {
		stopTflStopCache[id] = &ctdf.Stop{PrimaryIdentifier: id, Timezone: "Europe/London"}
		stopTflStopCacheExpires[id] = now.Add(time.Minute)
	}
	stopTflStopCacheMutex.Unlock()
	t.Cleanup(func() {
		stopTflStopCacheMutex.Lock()
		defer stopTflStopCacheMutex.Unlock()
		for _, id := range stopIDs {
			delete(stopTflStopCache, id)
			delete(stopTflStopCacheExpires, id)
		}
	})
	const dataset = "gb-tfl-mode/bus/arrivals"
	source := &ctdf.DataSourceReference{DatasetID: dataset}
	tracker := &ModeArrivalTracker{Mode: &TfLMode{ModeID: "bus", Lines: map[string]*TfLLine{
		"1": {Service: &ctdf.Service{PrimaryIdentifier: "test-line", TransportType: ctdf.TransportTypeBus}},
	}}}
	update := func(journeyID, vehicleID string) {
		t.Helper()
		predictions := []ArrivalPrediction{
			{LineID: "1", NaptanID: stopIDs[0], VehicleID: vehicleID, ExpectedArrival: now.Add(time.Minute).Format(time.RFC3339)},
			{LineID: "1", NaptanID: stopIDs[1], VehicleID: vehicleID, ExpectedArrival: now.Add(2 * time.Minute).Format(time.RFC3339)},
		}
		if err := tracker.parseGroupedArrivals(journeyID, predictions, source); err != nil {
			t.Fatal(err)
		}
	}
	const identifier = "test-tfl-bus-plate"
	update(identifier, "AB24XYZ")
	update("test-tfl-bus-fleet-id", "15327")

	jobs, err := redis_client.Client.XRange(ctx, realtimestore.VehicleEnrichmentStream, "-", "+").Result()
	if err != nil || len(jobs) != 1 || jobs[0].Values["journey"] != identifier || jobs[0].Values["registration"] != "AB24XYZ" {
		t.Fatalf("expected only the plate-backed journey to be queued: %v, %v", jobs, err)
	}
	fleetJourney, err := realtimestore.FindForTrackerUpdate(ctx, "test-tfl-bus-fleet-id")
	if err != nil || fleetJourney.Journey == nil || fleetJourney.Journey.VehicleRefs[dataset] != "15327" {
		t.Fatalf("expected the opaque TfL vehicle reference to be retained: %v", err)
	}

	initial, err := realtimestore.FindForTrackerUpdate(ctx, identifier)
	if err != nil || initial.Journey == nil {
		t.Fatalf("read initial bus journey: %v", err)
	}
	details := ctdf.JourneyDetailedBus{RegistrationNumber: "AB24XYZ", VehicleMake: "VOLVO"}
	if err := realtimestore.UpdateBusDetails(ctx, identifier, initial.Journey.CreationDateTime, details); err != nil {
		t.Fatal(err)
	}
	update(identifier, "AB24XYZ")
	refreshed, err := realtimestore.FindForTrackerUpdate(ctx, identifier)
	if err != nil || refreshed.Journey == nil || refreshed.Journey.DetailedMotorvehicleInformation != details {
		t.Fatalf("TfL refresh lost vehicle details: %v", err)
	}
	if count, err := redis_client.Client.XLen(ctx, realtimestore.VehicleEnrichmentStream).Result(); err != nil || count != 1 {
		t.Fatalf("TfL refresh changed the enrichment queue length to %d: %v", count, err)
	}
}
