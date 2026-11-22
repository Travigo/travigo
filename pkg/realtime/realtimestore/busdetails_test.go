package realtimestore

import (
	"context"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestUpdateBusDetailsPreservesExpiryAndRejectsRecreatedJourney(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	created := time.Now().UTC()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "recreated-bus-journey", CreationDateTime: created, VehicleRegistration: "AB19XYZ",
	}
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatal(err)
	}
	key := realtimeJourneyDetailsKey(journey.PrimaryIdentifier)
	server.FastForward(time.Minute)
	ttl := server.TTL(key)
	details := ctdf.JourneyDetailedBus{RegistrationNumber: journey.VehicleRegistration, VehicleMake: "VOLVO"}
	if err := UpdateBusDetails(ctx, journey.PrimaryIdentifier, created, details); err != nil {
		t.Fatal(err)
	}
	if server.TTL(key) != ttl {
		t.Fatal("bus details write changed the journey expiry")
	}
	server.FastForward(ttl)
	if err := UpdateBusDetails(ctx, journey.PrimaryIdentifier, created, details); err != nil || server.Exists(key) {
		t.Fatalf("update recreated an expired journey: %v", err)
	}
	journey.CreationDateTime = created.Add(time.Minute)
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBusDetails(ctx, journey.PrimaryIdentifier, created, details); err != nil {
		t.Fatal(err)
	}
	got, err := FindByIdentifier(ctx, journey.PrimaryIdentifier)
	if err != nil {
		t.Fatal(err)
	}
	if got.DetailedMotorvehicleInformation != (ctdf.JourneyDetailedBus{}) {
		t.Fatal("stale bus details applied to a recreated journey")
	}
}

func TestVehicleEnrichmentQueueFailurePreventsJourneyCreation(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	id := "queue-write-failure"
	if err := server.Set(VehicleEnrichmentStream, "wrong Redis key type"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := FindForTrackerUpdate(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Journey = &ctdf.RealtimeJourney{
		PrimaryIdentifier: id, CreationDateTime: time.Now().UTC(), VehicleRegistration: "AB19XYZ",
	}
	if err := SaveVehicleTrackerSnapshot(ctx, snapshot, nil); err == nil {
		t.Fatal("journey creation succeeded despite a failed enrichment enqueue")
	}
	if server.Exists(realtimeJourneyDetailsKey(id)) {
		t.Fatal("journey was created without its enrichment job")
	}
}
