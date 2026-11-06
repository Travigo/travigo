package realtimestore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

func TestRailObservationOnlyStoresNewerMappedPositions(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{PrimaryIdentifier: "rail-test"}
	now := time.Now().UTC().Truncate(time.Millisecond)
	trust := ctdf.Location{Type: "Point", Coordinates: []float64{0, 52}}
	td := ctdf.Location{Type: "Point", Coordinates: []float64{0.01, 52}}
	for _, update := range []struct {
		offset  int
		source  string
		point   *ctdf.Location
		changed bool
	}{
		{0, "TRUST", &trust, true},
		{3, "TD", nil, false},
		{2, "TD", &td, true},
		{2, "TRUST", &trust, false},
		{1, "TRUST", &trust, false},
	} {
		key := realtimeJourneyLocationKey(journey.PrimaryIdentifier)
		before, _ := server.Get(key)
		changed, err := RecordRailObservation(ctx, journey, RailObservation{
			ObservedAt: now.Add(time.Duration(update.offset) * time.Second), Source: update.source,
		}, update.point)
		if err != nil || changed != update.changed {
			t.Fatalf("offset %d: changed=%t, err=%v", update.offset, changed, err)
		}
		if after, _ := server.Get(key); !update.changed && after != before {
			t.Fatalf("offset %d changed the stored location", update.offset)
		}
	}
	stored, err := getStoredLocation(ctx, journey.PrimaryIdentifier)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Position.ObservedAt.Equal(now.Add(2*time.Second)) || stored.Location.Coordinates[0] != td.Coordinates[0] {
		t.Fatalf("unexpected observations: %+v", stored)
	}
	ApplyLocation(ctx, journey)
	if journey.VehicleLocationObservedAt == nil || journey.VehicleLocationSource != "TD" {
		t.Fatalf("missing TD metadata: %+v", journey.VehicleLocationSource)
	}
}

func TestMapOmitsFinishedRailRunWithPosition(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	now := time.Now()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier:    "finished-rail",
		ModificationDateTime: now,
		Journey:              &ctdf.Journey{PrimaryIdentifier: "schedule"},
	}
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatal(err)
	}
	point := ctdf.Location{Type: "Point", Coordinates: []float64{0, 52}}
	if _, err := RecordRailObservation(ctx, journey, RailObservation{ObservedAt: now, Source: "TRUST", Confidence: "mapped"}, &point); err != nil {
		t.Fatal(err)
	}
	bounds := realtimeJourneyBounds{bottomLeftLon: -1, bottomLeftLat: 51, topRightLon: 1, topRightLat: 53}
	result, err := findActiveWithinBoundsForIDs(ctx, bounds, []string{journey.PrimaryIdentifier})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatal("finished rail run appeared on the map")
	}
}

func TestRailMapRejectsExpiredPosition(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	now := time.Now()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "rail-replay", ModificationDateTime: now, ActivelyTracked: true,
		Journey: &ctdf.Journey{PrimaryIdentifier: "schedule"},
	}
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatal(err)
	}
	point := ctdf.Location{Type: "Point", Coordinates: []float64{0, 52}}
	stale := RailObservation{ObservedAt: now.Add(-railPositionMaxAge - time.Minute), Source: "TRUST"}
	if changed, err := RecordRailObservation(ctx, journey, stale, &point); changed || err != nil {
		t.Fatalf("accepted stale replay, changed=%t, err=%v", changed, err)
	}
	raw, err := json.Marshal(storedVehicleLocation{Location: point, Position: &stale})
	if err != nil {
		t.Fatal(err)
	}
	if err := redis_client.Client.Set(ctx, realtimeJourneyLocationKey(journey.PrimaryIdentifier), raw, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	bounds := realtimeJourneyBounds{bottomLeftLon: -1, bottomLeftLat: 51, topRightLon: 1, topRightLat: 53}
	result, err := findActiveWithinBoundsForIDs(ctx, bounds, []string{journey.PrimaryIdentifier})
	if err != nil || len(result) != 0 {
		t.Fatalf("map returned stale positions: %v, %v", result, err)
	}
}
