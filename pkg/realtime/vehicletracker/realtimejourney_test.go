package vehicletracker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/redis_client"
)

func TestTrackerUpdatePersistsLocationAndUsesTrackForHistory(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hasTrack bool
	}{
		{name: "track", hasTrack: true},
		{name: "stop fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupIdentificationTestRedis(t)
			ctx := context.Background()
			recordedAt := time.Date(2026, 11, 10, 12, 5, 0, 0, time.UTC)
			origin := ctdf.Location{Type: "Point", Coordinates: []float64{0, 51}}
			destination := ctdf.Location{Type: "Point", Coordinates: []float64{0.004, 51}}
			leg := &ctdf.JourneyPathItem{
				OriginStopRef:          "origin",
				DestinationStopRef:     "destination",
				DestinationStop:        &ctdf.Stop{Location: &destination},
				OriginDepartureTime:    time.Date(0, 1, 1, 12, 0, 0, 0, time.UTC),
				DestinationArrivalTime: time.Date(0, 1, 1, 12, 10, 0, 0, time.UTC),
			}
			if tc.hasTrack {
				leg.Track = []ctdf.Location{origin, destination}
			}
			journey := &ctdf.Journey{
				PrimaryIdentifier: "schedule",
				DepartureTimezone: "UTC",
				Path:              []*ctdf.JourneyPathItem{leg},
			}
			consumer := &BatchConsumer{
				journeyCache: map[string]*cachedTrackedJourney{
					journey.PrimaryIdentifier: {
						Journey:   journey,
						StopKeys:  journeyDestinationKeys(journey),
						journeyID: journey.PrimaryIdentifier,
					},
				},
			}
			event := &VehicleUpdateEvent{
				RecordedAt: recordedAt,
				DataSource: &ctdf.DataSourceReference{DatasetID: "siri"},
				VehicleLocationUpdate: &VehicleLocationUpdate{
					Timeframe:              "2026-11-10",
					IdentifyingInformation: map[string]string{"LinkedDataset": "test"},
					VehicleIdentifier:      "vehicle",
					Location:               ctdf.Location{Type: "Point", Coordinates: []float64{0.002, 51}},
				},
			}
			if err := consumer.updateRealtimeJourney(journey.PrimaryIdentifier, event); err != nil {
				t.Fatalf("update journey: %v", err)
			}

			// A handoff may select a feed with an older observation timestamp.
			handoffAt := recordedAt.Add(-time.Minute)
			event.RecordedAt = handoffAt
			event.DataSource = &ctdf.DataSourceReference{DatasetID: "gtfs"}
			event.VehicleLocationUpdate.Location = origin
			if err := consumer.updateRealtimeJourney(journey.PrimaryIdentifier, event); err != nil {
				t.Fatalf("handle older update: %v", err)
			}

			id := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, event.VehicleLocationUpdate.Timeframe, journey.PrimaryIdentifier)
			saved, err := realtimestore.FindByIdentifier(ctx, id)
			if err != nil {
				t.Fatalf("load journey: %v", err)
			}
			if !saved.ModificationDateTime.Equal(handoffAt) || saved.NextStopRef != "destination" || saved.DataSource.DatasetID != "gtfs" {
				t.Fatalf("unexpected saved journey timestamp %v and next stop %q", saved.ModificationDateTime, saved.NextStopRef)
			}
			if saved.VehicleLocationObservedAt == nil || !saved.VehicleLocationObservedAt.Equal(handoffAt) || saved.VehicleLocation.Coordinates[0] != 0 {
				t.Fatalf("unexpected saved position %+v at %v", saved.VehicleLocation, saved.VehicleLocationObservedAt)
			}
			history := loadVehicleJourneyHistory(ctx, event)
			if tc.hasTrack {
				if history == nil || !history.RecordedAt.Equal(recordedAt) || math.Abs(history.Progress-0.5) > 0.0001 {
					t.Fatalf("unexpected track history %+v", history)
				}
			} else if history != nil {
				t.Fatalf("stop fallback recorded vehicle history %+v", history)
			}
		})
	}
}

func TestVehicleHistoryOrdersLegacyAndUTCObservations(t *testing.T) {
	setupIdentificationTestRedis(t)
	ctx := context.Background()
	key := "history"
	if err := redis_client.Client.Set(ctx, key, `{"journey_id":"legacy","recorded_at":"2026-11-10T13:00:00+01:00"}`, vehicleHistoryTTL).Err(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	storeVehicleJourneyHistory(ctx, key, at.Add(-time.Second), "older", 0)
	legacy, err := redis_client.Client.Get(ctx, key).Result()
	if err != nil || legacy != `{"journey_id":"legacy","recorded_at":"2026-11-10T13:00:00+01:00"}` {
		t.Fatalf("older observation replaced offset timestamp: %s (%v)", legacy, err)
	}
	storeVehicleJourneyHistory(ctx, key, at.Add(time.Nanosecond), "newer", 0)
	storeVehicleJourneyHistory(ctx, key, at, "older", 0)
	var history vehicleJourneyHistory
	value, err := redis_client.Client.Get(ctx, key).Bytes()
	if err != nil || json.Unmarshal(value, &history) != nil || history.JourneyID != "newer" {
		t.Fatalf("expected latest history, got %s (%v)", value, err)
	}
}
