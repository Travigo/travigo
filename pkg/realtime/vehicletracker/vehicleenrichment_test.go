package vehicletracker

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/realtime/vehicledataprovider"
	"github.com/travigo/travigo/pkg/realtime/vehicledataprovider/dvlaves"
	"github.com/travigo/travigo/pkg/redis_client"
)

func TestVehicleEnrichmentRunsOnlyAfterJourneyCreation(t *testing.T) {
	setupIdentificationTestRedis(t)
	ctx := context.Background()
	schedule := &ctdf.Journey{PrimaryIdentifier: "schedule", DepartureTimezone: "UTC", Calls: []ctdf.JourneyCall{
		{StopRef: "origin", Start: true}, {StopRef: "destination", End: true},
	}}
	consumer := &BatchConsumer{
		vehicleEnrichmentEnabled: true,
		trackedJourneyCache: &trackedJourneyCache{journeyCache: map[string]*cachedTrackedJourney{
			schedule.PrimaryIdentifier: {Journey: schedule, journeyID: schedule.PrimaryIdentifier},
		}},
	}
	event := &VehicleUpdateEvent{
		SourceType: "GTFS-RT",
		RecordedAt: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC),
		VehicleLocationUpdate: &VehicleLocationUpdate{
			Timeframe: "2026-11-21", VehicleRegistration: "AA19AAA", VehicleDataProvider: dvlaves.ProviderID,
			StopUpdates: []VehicleLocationEventStopUpdate{{StopID: "destination"}},
		},
	}
	update := func() {
		t.Helper()
		if err := consumer.updateRealtimeJourney(schedule.PrimaryIdentifier, event); err != nil {
			t.Fatal(err)
		}
		event.RecordedAt = event.RecordedAt.Add(time.Second)
	}
	created := make(chan error, 2)
	for range 2 {
		go func() {
			created <- consumer.updateRealtimeJourney(schedule.PrimaryIdentifier, event)
		}()
	}
	for range 2 {
		if err := <-created; err != nil {
			t.Fatal(err)
		}
	}
	event.RecordedAt = event.RecordedAt.Add(time.Second)
	jobs, err := redis_client.Client.XRange(ctx, realtimestore.VehicleEnrichmentStream, "-", "+").Result()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("concurrent creation queued %d jobs, want one: %v", len(jobs), err)
	}
	job := jobs[0]
	identifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, event.VehicleLocationUpdate.Timeframe, schedule.PrimaryIdentifier)
	if job.Values["journey"] != identifier || job.Values["registration"] != "AA19AAA" {
		t.Fatalf("unexpected queued job: %v", job.Values)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, job.Values["created"].(string))
	if err != nil {
		t.Fatal(err)
	}

	details := ctdf.JourneyDetailedBus{RegistrationNumber: "AA19AAA", VehicleMake: "VOLVO"}
	attach := func() {
		t.Helper()
		if err := realtimestore.UpdateBusDetails(ctx, identifier, createdAt, details); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want ctdf.JourneyDetailedBus) {
		t.Helper()
		if count, err := redis_client.Client.XLen(ctx, realtimestore.VehicleEnrichmentStream).Result(); err != nil || count != 1 {
			t.Fatalf("existing journey changed queue length to %d: %v", count, err)
		}
		got, err := realtimestore.FindByIdentifier(ctx, identifier)
		if err != nil {
			t.Fatal(err)
		}
		if got.DetailedMotorvehicleInformation != want || !got.ModificationDateTime.Equal(event.RecordedAt.Add(-time.Second)) {
			t.Fatalf("unexpected detail %+v at %v", got.DetailedMotorvehicleInformation, got.ModificationDateTime)
		}
	}

	event.VehicleLocationUpdate.VehicleRegistration = ""
	update()
	attach()
	check(details)
	update()
	check(details)

	event.VehicleLocationUpdate.VehicleRegistration = "AB20XYZ"
	update()
	attach() // A delayed result from the original vehicle is no longer applicable.
	check(ctdf.JourneyDetailedBus{})
}

type testVehicleDataProvider struct {
	calls atomic.Int32
}

func (provider *testVehicleDataProvider) GetVehicleDataBus(_ context.Context, registration string) (*ctdf.JourneyDetailedBus, error) {
	if provider.calls.Add(1) == 1 {
		return nil, vehicledataprovider.ErrLookupDeferred
	}
	return &ctdf.JourneyDetailedBus{RegistrationNumber: registration, VehicleMake: "VOLVO"}, nil
}

func TestVehicleEnrichmentRecoversAbandonedDelivery(t *testing.T) {
	server := setupIdentificationTestRedis(t)
	ctx := context.Background()
	client := redis_client.Client
	stream := realtimestore.VehicleEnrichmentStream
	createdAt := time.Now().UTC()
	server.SetTime(createdAt)
	for i := range 260 {
		id := fmt.Sprintf("journey-%d", i)
		snapshot, err := realtimestore.FindForTrackerUpdate(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Journey = &ctdf.RealtimeJourney{
			PrimaryIdentifier: id, CreationDateTime: createdAt, VehicleRegistration: "AA19AAA",
		}
		if i == 259 {
			snapshot.Journey.TimeoutDurationMinutes = 1
		}
		if err := realtimestore.SaveVehicleTrackerSnapshot(ctx, snapshot, nil); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := client.XLen(ctx, stream).Result(); err != nil || count != 260 {
		t.Fatalf("queued %d of 260 journey creations: %v", count, err)
	}
	if err := client.XGroupCreate(ctx, stream, vehicleEnrichmentGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	batches, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: vehicleEnrichmentGroup, Consumer: "stopped-tracker", Streams: []string{stream, ">"}, Count: 2, Block: -1,
	}).Result()
	if err != nil || len(batches) != 1 || len(batches[0].Messages) != 2 {
		t.Fatalf("interrupted tracker couldn't claim a delivery: %v, %v", batches, err)
	}
	message := batches[0].Messages[1]
	if removed, err := acknowledgeVehicleEnrichmentScript.Run(ctx, client, []string{stream},
		vehicleEnrichmentGroup, message.ID, "another-tracker").Int(); err != nil || removed != 0 {
		t.Fatalf("a worker removed a delivery it did not own: %d, %v", removed, err)
	}
	provider := new(testVehicleDataProvider)
	if err := processVehicleEnrichment(ctx, provider, "stopped-tracker", batches[0].Messages[0]); err != nil {
		t.Fatal(err)
	}
	// One lookup was deferred and the other was never processed by this tracker.
	if pending, err := client.XPending(ctx, stream, vehicleEnrichmentGroup).Result(); err != nil || pending.Count != 2 {
		t.Fatalf("expected both jobs to remain pending, got %v: %v", pending, err)
	}
	server.FastForward(time.Minute)
	server.SetTime(createdAt.Add(time.Minute))

	workerCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		consumeVehicleEnrichment(workerCtx, provider, "replacement-tracker")
	}()
	defer func() {
		cancel()
		<-stopped
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if count, err := client.XLen(ctx, stream).Result(); err == nil && count == 0 {
			if calls := provider.calls.Load(); calls != 260 {
				t.Fatalf("provider made %d calls for 259 live journeys and one deferred lookup", calls)
			}
			for _, id := range []string{"journey-0", "journey-1"} {
				snapshot, err := realtimestore.FindForTrackerUpdate(ctx, id)
				if err != nil || snapshot.Journey == nil || snapshot.Journey.DetailedMotorvehicleInformation.VehicleMake != "VOLVO" {
					t.Fatalf("pending job for %s was not recovered: %v", id, err)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("replacement tracker did not drain the durable queue")
}
