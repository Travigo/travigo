package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

func TestTrackerSnapshotCASRejectsStaleWriteAndCommitsLocation(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	identifier := "tracker-cas"
	modified := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	initial := &ctdf.RealtimeJourney{
		PrimaryIdentifier:    identifier,
		Journey:              &ctdf.Journey{PrimaryIdentifier: "scheduled-journey"},
		OtherIdentifiers:     map[string]string{"provider": "provider-id"},
		ModificationDateTime: modified,
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"stop-a": {StopRef: "stop-a", JourneyStopIndex: 1, Platform: "1"},
		},
	}
	if err := SaveRealtimeJourney(ctx, initial); err != nil {
		t.Fatalf("save initial journey: %v", err)
	}

	first, err := FindForTrackerUpdate(ctx, identifier)
	if err != nil {
		t.Fatalf("find first tracker snapshot: %v", err)
	}
	stale, err := FindForTrackerUpdate(ctx, identifier)
	if err != nil {
		t.Fatalf("find stale tracker snapshot: %v", err)
	}
	first.Journey.ModificationDateTime = modified.Add(time.Minute)
	first.Journey.NextStopRef = "stop-b"
	first.Journey.Stops["stop-a"] = &ctdf.RealtimeJourneyStops{StopRef: "stop-a", JourneyStopIndex: 1, Platform: "2"}
	if first.previous.Stops["stop-a"].Platform != "1" {
		t.Fatalf("working stop replacement mutated previous event snapshot: %#v", first.previous.Stops["stop-a"])
	}
	observedAt := modified.Add(time.Second)
	if err := first.SetLocation(ctdf.Location{Type: "Point", Coordinates: []float64{-0.1, 51.5}}, 90, observedAt); err != nil {
		t.Fatalf("set snapshot location: %v", err)
	}
	if err := SaveRealtimeJourneySnapshot(ctx, first); err != nil {
		t.Fatalf("save first tracker snapshot: %v", err)
	}

	stale.Journey.ModificationDateTime = modified.Add(30 * time.Second)
	stale.Journey.NextStopRef = "stale-stop"
	if err := SaveRealtimeJourneySnapshot(ctx, stale); !errors.Is(err, ErrRealtimeJourneyConflict) {
		t.Fatalf("stale snapshot error = %v, want conflict", err)
	}

	latest, err := FindByIdentifier(ctx, identifier)
	if err != nil {
		t.Fatalf("find saved journey: %v", err)
	}
	if latest.NextStopRef != "stop-b" {
		t.Fatalf("stale snapshot overwrote latest next stop: %q", latest.NextStopRef)
	}
	if latest.VehicleLocationObservedAt == nil || !latest.VehicleLocationObservedAt.Equal(observedAt) || len(latest.VehicleLocation.Coordinates) < 2 || latest.VehicleLocation.Coordinates[0] != -0.1 {
		t.Fatalf("tracker location overlay was not committed with journey: %#v", latest)
	}
	if ttl := server.TTL(realtimeJourneyDetailsKey(identifier)); ttl != defaultRealtimeJourneyTTL {
		t.Fatalf("details TTL = %s, want %s", ttl, defaultRealtimeJourneyTTL)
	}
	if ttl := server.TTL(realtimeJourneyMappingKey("travigo-journeyid", "scheduled-journey")); ttl != defaultRealtimeJourneyTTL {
		t.Fatalf("mapping TTL = %s, want %s", ttl, defaultRealtimeJourneyTTL)
	}
}

func TestTrackerSnapshotCASRejectsConcurrentCreation(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	first, err := FindForTrackerUpdate(ctx, "tracker-create-race")
	if err != nil {
		t.Fatal(err)
	}
	second, err := FindForTrackerUpdate(ctx, "tracker-create-race")
	if err != nil {
		t.Fatal(err)
	}
	first.Journey = &ctdf.RealtimeJourney{PrimaryIdentifier: "tracker-create-race", NextStopRef: "first"}
	if err := SaveRealtimeJourneySnapshot(ctx, first); err != nil {
		t.Fatalf("save initial creation: %v", err)
	}
	second.Journey = &ctdf.RealtimeJourney{PrimaryIdentifier: "tracker-create-race", NextStopRef: "second"}
	if err := SaveRealtimeJourneySnapshot(ctx, second); !errors.Is(err, ErrRealtimeJourneyConflict) {
		t.Fatalf("concurrent creation error = %v, want conflict", err)
	}
	stored, err := FindByIdentifier(ctx, "tracker-create-race")
	if err != nil || stored.NextStopRef != "first" {
		t.Fatalf("concurrent creation replaced winner: journey=%#v err=%v", stored, err)
	}
}

func setupMemoryTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousClient := redis_client.Client
	redis_client.Client = client

	t.Cleanup(func() {
		redis_client.Client = previousClient
		_ = client.Close()
	})

	return server
}

func TestSaveRealtimeJourneyDefaultsZeroExpiration(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{PrimaryIdentifier: "zero-timeout"}

	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}

	ttl := server.TTL(realtimeJourneyDetailsKey(journey.PrimaryIdentifier))
	if ttl != defaultRealtimeJourneyTTL {
		t.Fatalf("expected TTL %s, got %s", defaultRealtimeJourneyTTL, ttl)
	}
}

func TestSaveRealtimeJourneyPreservesPositiveExpiration(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "rail-journey",
		TimeoutDurationMinutes: 181,
	}

	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}

	expected := 181 * time.Minute
	if ttl := server.TTL(realtimeJourneyDetailsKey(journey.PrimaryIdentifier)); ttl != expected {
		t.Fatalf("expected TTL %s, got %s", expected, ttl)
	}
}

func TestBoardRealtimeRetainsUnexpiredLegacyVSTPCancellation(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	modified := time.Now().Add(-5 * time.Hour)
	for _, journey := range []*ctdf.RealtimeJourney{
		{PrimaryIdentifier: "gb-nationalrail-vstp-cancellation-2026-11-01:2026-11-05:train", Journey: &ctdf.Journey{PrimaryIdentifier: "train"}, Cancelled: true, ModificationDateTime: modified, TimeoutDurationMinutes: 24 * 60},
		{PrimaryIdentifier: "other", Journey: &ctdf.Journey{PrimaryIdentifier: "other"}, Cancelled: true, ModificationDateTime: modified, TimeoutDurationMinutes: 24 * 60},
		{PrimaryIdentifier: "gb-nationalrail-vstp-cancellation-2026-11-01:2026-11-05:expired", Journey: &ctdf.Journey{PrimaryIdentifier: "expired"}, Cancelled: true, ModificationDateTime: modified, TimeoutDurationMinutes: 60},
	} {
		if err := SaveRealtimeJourney(ctx, journey); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{"train", "other", "expired"}
	standard, err := FindCurrentForJourneyIDs(ctx, ids)
	if err != nil || len(standard) != 0 {
		t.Fatalf("ordinary lookup = %#v, %v", standard, err)
	}
	board, err := FindBoardRealtimeForJourneyIDs(ctx, ids)
	if err != nil || len(board) != 1 || board["train"] == nil {
		t.Fatalf("board lookup = %#v, %v", board, err)
	}
}

func TestSaveRealtimeJourneyPreservesOverlayReplacementFields(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier:          "overlay-suppression",
		SuppressFromDepartures:     true,
		SuppressFromDepartureDates: []string{"2026-07-10"},
		ReplacedByJourneyRef:       "overlay-journey",
		Journey: &ctdf.Journey{
			PrimaryIdentifier:   "base-journey",
			ReplacesJourneyRefs: []string{"older-journey"},
		},
	}

	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}

	stored, err := FindByIdentifier(ctx, journey.PrimaryIdentifier)
	if err != nil {
		t.Fatalf("find realtime journey: %v", err)
	}
	if !stored.SuppressFromDepartures || stored.ReplacedByJourneyRef != "overlay-journey" {
		t.Fatalf("overlay replacement fields were not preserved: %#v", stored)
	}
	if stored.Journey == nil || len(stored.Journey.ReplacesJourneyRefs) != 1 || stored.Journey.ReplacesJourneyRefs[0] != "older-journey" {
		t.Fatalf("journey replacement references were not preserved: %#v", stored.Journey)
	}
}

func TestSaveRealtimeJourneyPreservesJourneyPath(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "tfl-journey",
		Journey: &ctdf.Journey{
			PrimaryIdentifier: "tfl-journey",
			Calls: ctdf.JourneyCallsFromLegs([]*ctdf.JourneyPathItem{{
				OriginStopRef:          "gb-atco-origin",
				DestinationStopRef:     "gb-atco-destination",
				OriginPlatform:         "1 - Northbound",
				DestinationPlatform:    "2 - Northbound",
				OriginArrivalTime:      time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
				DestinationArrivalTime: time.Date(2026, 7, 9, 12, 2, 0, 0, time.UTC),
				OriginDepartureTime:    time.Date(2026, 7, 9, 12, 0, 30, 0, time.UTC),
				OriginActivity:         []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup},
				DestinationActivity:    []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown},
				TrackRef:               "realtime-tfl-route-track:test",
			}}...),
		},
	}

	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}

	stored, err := FindByIdentifier(ctx, journey.PrimaryIdentifier)
	if err != nil {
		t.Fatalf("find realtime journey: %v", err)
	}
	if stored.Journey == nil || len(stored.Journey.Calls) != 2 {
		t.Fatalf("call count = %d, want 2", len(stored.Journey.Calls))
	}

	pathItem := stored.Journey.Leg(0)
	if got, want := pathItem.OriginStopRef, "gb-atco-origin"; got != want {
		t.Errorf("origin stop = %q, want %q", got, want)
	}
	if got, want := pathItem.DestinationStopRef, "gb-atco-destination"; got != want {
		t.Errorf("destination stop = %q, want %q", got, want)
	}
	if got, want := pathItem.OriginPlatform, "1 - Northbound"; got != want {
		t.Errorf("origin platform = %q, want %q", got, want)
	}
	if got, want := pathItem.DestinationArrivalTime, time.Date(2026, 7, 9, 12, 2, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("destination arrival = %s, want %s", got, want)
	}
	if got, want := pathItem.OriginActivity, []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("origin activity = %v, want %v", got, want)
	}
	if got, want := pathItem.TrackRef, "realtime-tfl-route-track:test"; got != want {
		t.Errorf("track reference = %q, want %q", got, want)
	}
}

func TestRailAllocationRemainsAvailableForDay(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	detailed := ctdf.JourneyDetailedRail{
		Trains: []ctdf.RailTrain{{ID: "unit-1", FleetID: "156", Carriages: []ctdf.RailCarriage{{ID: "A"}, {ID: "B"}}}},
	}

	if err := UpdateRailDetailedAllocation(ctx, "rail-journey", detailed); err != nil {
		t.Fatalf("update rail allocation: %v", err)
	}

	key := realtimeJourneyRailDetailedKey("allocation", "rail-journey")
	if ttl := server.TTL(key); ttl != railAllocationTTL {
		t.Fatalf("expected TTL %s, got %s", railAllocationTTL, ttl)
	}
	raw, err := server.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	var stored storedRailAllocation
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Enriched || stored.Trains[0].VehicleTypeName != "Class 156" ||
		len(stored.Trains[0].Carriages[0].Toilets) == 0 || stored.Trains[0].Carriages[0].Occupancy != -1 {
		t.Fatalf("allocation was not enriched before writing: %+v", stored)
	}
	if detailed.Trains[0].Carriages[0].Occupancy != 0 || detailed.Trains[0].VehicleType != "" {
		t.Fatal("allocation write mutated the caller's train")
	}
}

func TestRailLoadingUsesRealtimeJourneyExpiration(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "rail-journey",
		TimeoutDurationMinutes: 181,
	}
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}

	detailed := ctdf.JourneyDetailedRail{
		Trains: []ctdf.RailTrain{{ID: "unit-1", Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: 42}}}},
	}
	if err := UpdateRailDetailedLoading(ctx, journey.PrimaryIdentifier, detailed); err != nil {
		t.Fatalf("update rail loading: %v", err)
	}

	expected := 181 * time.Minute
	key := realtimeJourneyRailDetailedKey("loading", journey.PrimaryIdentifier)
	if ttl := server.TTL(key); ttl != expected {
		t.Fatalf("expected TTL %s, got %s", expected, ttl)
	}
}

func TestRailDetailedRedisRoundTripKeepsLoadingScopedToTrain(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	allocation := ctdf.JourneyDetailedRail{
		Trains: []ctdf.RailTrain{
			{ID: "front", Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: -1}}},
			{ID: "rear", Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: -1}}},
		},
	}
	loading := ctdf.JourneyDetailedRail{
		Trains: []ctdf.RailTrain{
			{ID: "rear", Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: 64}}},
		},
	}

	if err := UpdateRailDetailedAllocation(ctx, "coupled-service", allocation); err != nil {
		t.Fatalf("update rail allocation: %v", err)
	}
	if err := UpdateRailDetailedLoading(ctx, "coupled-service", loading); err != nil {
		t.Fatalf("update rail loading: %v", err)
	}

	detailed, err := GetRailDetailed(ctx, "coupled-service")
	if err != nil {
		t.Fatalf("get rail detailed: %v", err)
	}
	if detailed.Trains[0].Carriages[0].Occupancy != -1 {
		t.Fatalf("expected front coach A to remain unknown")
	}
	if detailed.Trains[1].Carriages[0].Occupancy != 64 {
		t.Fatalf("expected rear coach A loading to survive Redis round trip")
	}
}

func TestCleanupStaleLocationIndexKeepsLiveLocations(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	indexKey := realtimeJourneyLocationGeoIndexKey()

	if err := redis_client.Client.GeoAdd(ctx, indexKey,
		&redis.GeoLocation{Name: "live", Longitude: -0.1, Latitude: 51.5},
		&redis.GeoLocation{Name: "expired", Longitude: -0.2, Latitude: 51.6},
	).Err(); err != nil {
		t.Fatalf("seed geo index: %v", err)
	}
	if err := redis_client.Client.Set(
		ctx,
		realtimeJourneyLocationKey("live"),
		`{"location":{"type":"Point","coordinates":[-0.1,51.5]}}`,
		time.Minute,
	).Err(); err != nil {
		t.Fatalf("seed live location: %v", err)
	}

	removed, err := CleanupStaleLocationIndex(ctx)
	if err != nil {
		t.Fatalf("clean location index: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected one removal, got %d", removed)
	}

	members, err := redis_client.Client.ZRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("read geo index: %v", err)
	}
	if len(members) != 1 || members[0] != "live" {
		t.Fatalf("expected only live member, got %v", members)
	}
}

func TestFindActiveWithinBoundsBatchesDetailsAndLocations(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "active",
		ActivelyTracked:        true,
		ModificationDateTime:   time.Now(),
		TimeoutDurationMinutes: 10,
		Journey:                &ctdf.Journey{PrimaryIdentifier: "journey"},
	}
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}
	if err := UpdateLocation(ctx, journey.PrimaryIdentifier, ctdf.Location{
		Type:        "Point",
		Coordinates: []float64{-0.1, 51.5},
	}, 90); err != nil {
		t.Fatalf("save realtime journey location: %v", err)
	}
	if err := redis_client.Client.GeoAdd(ctx, realtimeJourneyLocationGeoIndexKey(), &redis.GeoLocation{
		Name:      "missing-details",
		Longitude: -0.1,
		Latitude:  51.5,
	}).Err(); err != nil {
		t.Fatalf("seed stale location index entry: %v", err)
	}

	journeys, err := findActiveWithinBoundsForIDs(ctx, realtimeJourneyBounds{
		bottomLeftLon: -0.2,
		bottomLeftLat: 51.4,
		topRightLon:   0.0,
		topRightLat:   51.6,
	}, []string{journey.PrimaryIdentifier, "missing-details"})
	if err != nil {
		t.Fatalf("find active realtime journeys: %v", err)
	}
	if len(journeys) != 1 || journeys[0].PrimaryIdentifier != journey.PrimaryIdentifier {
		t.Fatalf("expected active journey only, got %#v", journeys)
	}
	if journeys[0].VehicleBearing != 90 || len(journeys[0].VehicleLocation.Coordinates) != 2 {
		t.Fatalf("expected location overlay from batched read, got %#v", journeys[0].VehicleLocation)
	}

	members, err := redis_client.Client.ZRange(ctx, realtimeJourneyLocationGeoIndexKey(), 0, -1).Result()
	if err != nil {
		t.Fatalf("read location index: %v", err)
	}
	if len(members) != 1 || members[0] != journey.PrimaryIdentifier {
		t.Fatalf("expected stale member removal, got %v", members)
	}
}

func TestTFLDepartureBoardIndexReplacesStopsAndBatchesReads(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	now := time.Now().Round(time.Second)
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "tfl-journey",
		ModificationDateTime:   now,
		TimeoutDurationMinutes: 10,
		Journey:                &ctdf.Journey{PrimaryIdentifier: "journey"},
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"stop-a": {
				StopRef:       "stop-a",
				TimeType:      ctdf.RealtimeJourneyStopTimeEstimatedFuture,
				ArrivalTime:   now.Add(5 * time.Minute),
				DepartureTime: now.Add(5 * time.Minute),
			},
		},
	}
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}
	if err := IndexTFLDepartureBoardJourney(ctx, journey); err != nil {
		t.Fatalf("index TFL departure board journey: %v", err)
	}

	journey.Stops = map[string]*ctdf.RealtimeJourneyStops{
		"stop-b": {
			StopRef:       "stop-b",
			TimeType:      ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			ArrivalTime:   now.Add(10 * time.Minute),
			DepartureTime: now.Add(10 * time.Minute),
		},
	}
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("resave realtime journey: %v", err)
	}
	if err := IndexTFLDepartureBoardJourney(ctx, journey); err != nil {
		t.Fatalf("reindex TFL departure board journey: %v", err)
	}

	oldStopIDs, err := redis_client.Client.ZRange(ctx, tflDepartureBoardStopKey("stop-a"), 0, -1).Result()
	if err != nil {
		t.Fatalf("read old stop index: %v", err)
	}
	if len(oldStopIDs) != 0 {
		t.Fatalf("expected old stop index to be empty, got %v", oldStopIDs)
	}

	departures, err := FindTFLDepartureBoardJourneys(ctx, []string{"stop-b"}, now)
	if err != nil {
		t.Fatalf("find TFL departure board journeys: %v", err)
	}
	if len(departures) != 1 || departures[0].PrimaryIdentifier != journey.PrimaryIdentifier {
		t.Fatalf("expected indexed TFL journey, got %#v", departures)
	}

	futureJourney := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "tfl-future-journey",
		ModificationDateTime:   now,
		TimeoutDurationMinutes: 10,
		Journey:                &ctdf.Journey{PrimaryIdentifier: "future-journey"},
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"stop-b": {
				StopRef:       "stop-b",
				TimeType:      ctdf.RealtimeJourneyStopTimeEstimatedFuture,
				ArrivalTime:   now.Add(3 * time.Hour),
				DepartureTime: now.Add(3 * time.Hour),
			},
		},
	}
	if err := SaveRealtimeJourney(ctx, futureJourney); err != nil {
		t.Fatalf("save future realtime journey: %v", err)
	}
	if err := IndexTFLDepartureBoardJourney(ctx, futureJourney); err != nil {
		t.Fatalf("index future realtime journey: %v", err)
	}

	bounded, err := FindTFLDepartureBoardJourneysBounded(ctx, []string{"stop-b"}, now, now.Add(time.Hour), 1)
	if err != nil {
		t.Fatalf("find bounded TFL departure board journeys: %v", err)
	}
	if len(bounded) != 1 || bounded[0].PrimaryIdentifier != journey.PrimaryIdentifier {
		t.Fatalf("expected horizon and limit to keep earliest journey, got %#v", bounded)
	}
}
