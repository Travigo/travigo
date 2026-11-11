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
				trackedJourneyCache: &trackedJourneyCache{journeyCache: map[string]*cachedTrackedJourney{
					journey.PrimaryIdentifier: {
						Journey:   journey,
						StopKeys:  journeyDestinationKeys(journey),
						journeyID: journey.PrimaryIdentifier,
					},
				}},
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
			initial, err := realtimestore.FindForTrackerUpdate(ctx, fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, event.VehicleLocationUpdate.Timeframe, journey.PrimaryIdentifier))
			if err != nil {
				t.Fatal(err)
			}
			if futureStopsNeedRebuild(initial.Journey, journeyDestinationKeys(journey), 0, initial.Journey.Offset, time.UTC) {
				t.Fatal("unchanged GPS predictions would be rebuilt after date normalization")
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

func TestTrackerSavesPredictionsWithoutRequiringPastStops(t *testing.T) {
	for _, tc := range []struct {
		name         string
		observedHour int
		observedMin  int
		clockShift   time.Duration
		delay        int
		arrivalExtra int
		gps          bool
		missingTimes bool
		previousNext int
		wantNext     int
	}{
		{name: "before departure", observedHour: 9, observedMin: 50},
		{name: "GPS overrides timing", observedHour: 9, observedMin: 50, gps: true, wantNext: 1},
		{name: "dwelling at repeated origin", observedHour: 10, observedMin: 21, wantNext: 2},
		{name: "dwelling at intermediate stop", observedHour: 10, observedMin: 11, wantNext: 1},
		{name: "delayed arrival without departure prediction", observedHour: 10, observedMin: 13, arrivalExtra: 300, wantNext: 1},
		{name: "departed intermediate stop", observedHour: 10, observedMin: 12, wantNext: 2},
		{name: "between repeated calls", observedHour: 10, observedMin: 15, wantNext: 2},
		{name: "approaching terminal", observedHour: 10, observedMin: 25, wantNext: 3},
		{name: "all predictions past", observedHour: 11, wantNext: -1},
		{name: "terminal progress retained", observedHour: 11, previousNext: 3, wantNext: 3},
		{name: "uncertain progress retained", observedHour: 9, observedMin: 50, previousNext: 2, wantNext: 2},
		{name: "missing times", observedHour: 10, missingTimes: true, wantNext: -1},
		{name: "overnight delay only", observedHour: 24, observedMin: 50, clockShift: 15 * time.Hour, delay: 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupIdentificationTestRedis(t)
			ctx := context.Background()
			day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
			clock := time.Date(0, 1, 1, 10, 0, 0, 0, time.UTC).Add(tc.clockShift)
			journey := &ctdf.Journey{
				PrimaryIdentifier: "schedule",
				DepartureTimezone: "UTC",
				Path: []*ctdf.JourneyPathItem{
					{OriginStopRef: "a", DestinationStopRef: "b", OriginArrivalTime: clock, OriginDepartureTime: clock, DestinationArrivalTime: clock.Add(10 * time.Minute)},
					{OriginStopRef: "b", DestinationStopRef: "a", OriginArrivalTime: clock.Add(10 * time.Minute), OriginDepartureTime: clock.Add(12 * time.Minute), DestinationArrivalTime: clock.Add(20 * time.Minute)},
					{OriginStopRef: "a", DestinationStopRef: "c", OriginArrivalTime: clock.Add(20 * time.Minute), OriginDepartureTime: clock.Add(22 * time.Minute), DestinationArrivalTime: clock.Add(30 * time.Minute)},
				},
			}
			if tc.missingTimes {
				for _, path := range journey.Path {
					path.OriginArrivalTime = time.Time{}
					path.OriginDepartureTime = time.Time{}
					path.DestinationArrivalTime = time.Time{}
				}
			}
			if tc.gps {
				point := ctdf.Location{Type: "Point", Coordinates: []float64{0.01, 51}}
				for _, path := range journey.Path {
					path.DestinationStop = &ctdf.Stop{Location: &point}
				}
				journey.Path[0].Track = []ctdf.Location{
					{Type: "Point", Coordinates: []float64{0, 51}},
					point,
				}
			}
			consumer := &BatchConsumer{trackedJourneyCache: &trackedJourneyCache{journeyCache: map[string]*cachedTrackedJourney{
				journey.PrimaryIdentifier: {
					Journey: journey, journeyID: journey.PrimaryIdentifier,
					StopOccurrences: journeyStopOccurrences(journey),
					StopKeys:        journeyDestinationKeys(journey),
				},
			}}}
			event := &VehicleUpdateEvent{
				RecordedAt:            day.Add(time.Duration(tc.observedHour)*time.Hour + time.Duration(tc.observedMin)*time.Minute),
				VehicleLocationUpdate: &VehicleLocationUpdate{Timeframe: day.Format("2006-01-02")},
			}
			if tc.gps {
				event.VehicleLocationUpdate.Location = ctdf.Location{Type: "Point", Coordinates: []float64{0.005, 51}}
			}
			for _, ref := range []string{"a", "b", "a", "c"} {
				event.VehicleLocationUpdate.StopUpdates = append(event.VehicleLocationUpdate.StopUpdates, VehicleLocationEventStopUpdate{
					StopID: ref, ArrivalTime: time.Unix(0, 0), DepartureTime: time.Unix(0, 0),
					ArrivalOffset: tc.delay + tc.arrivalExtra, DepartureOffset: tc.delay,
				})
			}
			id := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, event.VehicleLocationUpdate.Timeframe, journey.PrimaryIdentifier)
			calls := []string{"a", "b", "a", "c"}
			if tc.previousNext > 0 {
				if err := realtimestore.SaveRealtimeJourney(ctx, &ctdf.RealtimeJourney{
					PrimaryIdentifier: id, Journey: journey, JourneyRunDate: day,
					NextStopRef: calls[tc.previousNext], NextStopIndex: tc.previousNext,
					DepartedStopRef: calls[tc.previousNext-1],
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := consumer.updateRealtimeJourney(journey.PrimaryIdentifier, event); err != nil {
				t.Fatal(err)
			}
			saved, err := realtimestore.FindByIdentifier(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(saved.Stops) != len(calls) {
				t.Fatalf("saved %d predictions, want %d", len(saved.Stops), len(calls))
			}
			if tc.wantNext < 0 {
				if saved.NextStopRef != "" || saved.DepartedStopRef != "" {
					t.Fatalf("uncertain progress was guessed: next=%s departed=%s", saved.NextStopRef, saved.DepartedStopRef)
				}
			} else if saved.NextStopIndex != tc.wantNext || saved.NextStopRef != calls[tc.wantNext] {
				t.Fatalf("next stop = %s@%d, want %s@%d", saved.NextStopRef, saved.NextStopIndex, calls[tc.wantNext], tc.wantNext)
			}
			if tc.wantNext == 0 && saved.DepartedStopRef != "" {
				t.Fatalf("pre-departure run claims to have departed %q", saved.DepartedStopRef)
			}
			last := saved.RealtimeStop("c", 3)
			if tc.missingTimes {
				if !last.ArrivalTime.IsZero() || !last.DepartureTime.IsZero() {
					t.Fatalf("missing times became dated predictions: %+v", last)
				}
			} else {
				want := day.Add(10*time.Hour + tc.clockShift + 30*time.Minute + time.Duration(tc.delay+tc.arrivalExtra)*time.Second)
				if !last.ArrivalTime.Equal(want) {
					t.Fatalf("terminal prediction = %v, want %v", last.ArrivalTime, want)
				}
				stop := saved.RealtimeStop("b", 1)
				if stop.DepartureTime.Before(stop.ArrivalTime) {
					t.Fatalf("fallback departure %v precedes predicted arrival %v", stop.DepartureTime, stop.ArrivalTime)
				}
			}
		})
	}
}

func TestTrackerRejectsInvalidTimeframeAndEmptyPath(t *testing.T) {
	for _, timeframe := range []string{"invalid", "2026-11-10"} {
		t.Run(timeframe, func(t *testing.T) {
			setupIdentificationTestRedis(t)
			consumer := &BatchConsumer{trackedJourneyCache: &trackedJourneyCache{journeyCache: map[string]*cachedTrackedJourney{
				"schedule": {Journey: &ctdf.Journey{}, journeyID: "schedule"},
			}}}
			event := &VehicleUpdateEvent{VehicleLocationUpdate: &VehicleLocationUpdate{
				Timeframe:   timeframe,
				StopUpdates: []VehicleLocationEventStopUpdate{{StopID: "a"}},
			}}
			if err := consumer.updateRealtimeJourney("schedule", event); err == nil {
				t.Fatal("invalid update was accepted")
			}
			if count := redis_client.Client.DBSize(context.Background()).Val(); count != 0 {
				t.Fatalf("invalid update wrote %d Redis keys", count)
			}
		})
	}
}

func TestStopPredictionTimeUsesServiceDateAndTimezone(t *testing.T) {
	location, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	clock := time.Date(0, 1, 1, 1, 30, 0, 0, time.UTC)
	want := time.Date(2026, 3, 29, 0, 35, 0, 0, time.UTC)
	got := stopPredictionTime(time.Unix(0, 0), clock, 300, day, location)
	if !got.Equal(want) {
		t.Fatalf("DST delay prediction = %v, want %v", got, want)
	}
	absolute := time.Date(2026, 3, 29, 4, 0, 0, 0, time.UTC)
	if got := stopPredictionTime(absolute, clock, 300, day, location); !got.Equal(absolute) {
		t.Fatalf("absolute prediction changed to %v", got)
	}
}
