package vehicletracker

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func setupIdentificationTestRedis(t *testing.T) *miniredis.Miniredis {
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

func TestIdentificationMappingAcceptsOnlyNewerUpdates(t *testing.T) {
	setupIdentificationTestRedis(t)
	ctx := context.Background()
	initial := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	newer := initial.Add(time.Minute)

	storeIdentificationMapping(ctx, "vehicle-1", "journey-1", initial, identificationCacheTTL)

	journeyID, found, err := touchExistingIdentificationMapping(ctx, "vehicle-1", newer)
	if err != nil {
		t.Fatalf("touch newer mapping: %v", err)
	}
	if !found || journeyID != "journey-1" {
		t.Fatalf("expected journey mapping, got id=%q found=%t", journeyID, found)
	}

	journeyID, found, err = touchExistingIdentificationMapping(ctx, "vehicle-1", initial)
	if err != nil {
		t.Fatalf("touch stale mapping: %v", err)
	}
	if !found || journeyID != "" {
		t.Fatalf("expected stale update to be ignored, got id=%q found=%t", journeyID, found)
	}
}

func TestIdentificationMappingReturnsMissAndSuppressesFailures(t *testing.T) {
	setupIdentificationTestRedis(t)
	ctx := context.Background()
	now := time.Now()

	journeyID, found, err := touchExistingIdentificationMapping(ctx, "missing", now)
	if err != nil {
		t.Fatalf("touch missing mapping: %v", err)
	}
	if found || journeyID != "" {
		t.Fatalf("expected cache miss, got id=%q found=%t", journeyID, found)
	}

	storeIdentificationMapping(ctx, "failed", "N/A", now, identificationCacheTTL)
	journeyID, found, err = touchExistingIdentificationMapping(ctx, "failed", now.Add(time.Second))
	if err != nil {
		t.Fatalf("touch suppressed mapping: %v", err)
	}
	if !found || journeyID != "" {
		t.Fatalf("expected suppressed mapping, got id=%q found=%t", journeyID, found)
	}
}

func TestSiriReusesMatchWhileCheckingGTFSOwnership(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("fixed expiry", func(mt *mtest.T) {
		server := setupIdentificationTestRedis(mt.T)
		oldDatabase := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = oldDatabase })

		mockMatch := func(journeyID string) {
			mt.AddMockResponses(
				mtest.CreateCursorResponse(0, mt.DB.Name()+".operators", mtest.FirstBatch,
					bson.D{{Key: "primaryidentifier", Value: "operator"}}),
				mtest.CreateCursorResponse(0, mt.DB.Name()+".services", mtest.FirstBatch,
					bson.D{{Key: "primaryidentifier", Value: "service"}, {Key: "servicename", Value: "1"}}),
				mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, bson.D{
					{Key: "primaryidentifier", Value: journeyID},
					{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
				}),
			)
		}

		ctx := context.Background()
		consumer := &BatchConsumer{}
		info := map[string]string{
			"OperatorRef": "operator", "PublishedLineName": "1", "VehicleJourneyRef": "trip-a",
			"FramedVehicleJourneyDate": "2026-11-10", "LinkedDataset": "schedule",
		}
		event := &VehicleUpdateEvent{
			LocalID: "siri-trip-a", SourceType: "siri-vm",
			RecordedAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
			DataSource: &ctdf.DataSourceReference{DatasetID: "siri"},
			VehicleLocationUpdate: &VehicleLocationUpdate{
				VehicleIdentifier: "vehicle", Timeframe: "2026-11-10",
			},
		}
		identify := func(want string) {
			mt.Helper()
			if got := consumer.identifyVehicle(event, event.SourceType, info); got != want {
				mt.Fatalf("identify %s: got %q, want %q", event.LocalID, got, want)
			}
			event.RecordedAt = event.RecordedAt.Add(time.Second)
		}

		storeGTFSJourneyOwnership(ctx, event, info, "journey-a")
		mockMatch("journey-a")
		identify("")
		server.FastForward(4 * time.Minute)
		identify("")
		if got := len(mt.GetAllStartedEvents()); got != 3 {
			mt.Fatalf("cached suppression ran another timetable search: %d Mongo commands", got)
		}

		// A late result from a concurrent miss must not restart the cache window.
		storeIdentificationMapping(ctx, event.LocalID, "journey-a", event.RecordedAt, suppressedSiriMatchTTL)
		storeGTFSJourneyOwnership(ctx, event, info, "journey-b")
		identify("journey-a")

		event.LocalID = "siri-trip-b"
		info["VehicleJourneyRef"] = "trip-b"
		mockMatch("journey-b")
		identify("")

		server.FastForward(time.Minute + time.Second)
		event.LocalID = "siri-trip-a"
		info["VehicleJourneyRef"] = "trip-a"
		mockMatch("journey-a")
		identify("journey-a")
		if got := len(mt.GetAllStartedEvents()); got != 9 {
			mt.Fatalf("expected three timetable searches across key change and expiry, got %d Mongo commands", got)
		}
	})
}

func TestGTFSJourneyOwnershipIsJourneySpecificAndExpiresAfterEightHours(t *testing.T) {
	setupIdentificationTestRedis(t)
	ctx := context.Background()
	consumer := &BatchConsumer{}
	event := &VehicleUpdateEvent{VehicleLocationUpdate: &VehicleLocationUpdate{
		VehicleIdentifier: "vehicle-1",
		Timeframe:         "2026-07-21",
	}}
	information := map[string]string{"LinkedDataset": "gb-dft-bods-gtfs-schedule-*"}

	storeGTFSJourneyOwnership(ctx, event, information, "journey-a")

	if !consumer.gtfsOwnsJourney(event, information, "journey-a") {
		t.Fatal("expected GTFS to own the same journey")
	}
	if consumer.gtfsOwnsJourney(event, information, "journey-b") {
		t.Fatal("GTFS ownership must not suppress a different journey after vehicle turn-over")
	}

	ttl, err := redis_client.Client.TTL(ctx, gtfsJourneyOwnershipKey(information["LinkedDataset"], "vehicle-1", "2026-07-21")).Result()
	if err != nil {
		t.Fatalf("get ownership TTL: %v", err)
	}
	if ttl > gtfsJourneyOwnershipTTL || ttl < gtfsJourneyOwnershipTTL-time.Minute {
		t.Fatalf("expected approximately %s TTL, got %s", gtfsJourneyOwnershipTTL, ttl)
	}
}
