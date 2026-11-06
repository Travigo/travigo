package nrod

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/railtd"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestTDLoadsMongoMappings(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, evidence := range []string{"test-only source", ""} {
		name := "valid"
		if evidence == "" {
			name = "missing evidence"
		}
		mt.Run(name, func(mt *mtest.T) {
			oldDB := database.Instance
			database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
			mt.Cleanup(func() { database.Instance = oldDB })
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+database.TDMappingsCollectionName, mtest.FirstBatch,
				bson.D{{Key: "area", Value: "ZZ"}, {Key: "from", Value: "0100"}, {Key: "to", Value: "0101"},
					{Key: "origin", Value: "a"}, {Key: "destination", Value: "b"}, {Key: "evidence", Value: evidence},
					{Key: "confidence", Value: "mapped"}, {Key: "point", Value: bson.M{"type": "Point", "coordinates": bson.A{0.0, 52.0}}}},
			))
			tracker, err := loadTD(context.Background())
			if evidence == "" {
				if err != nil || len(tracker.mappings) != 0 {
					mt.Fatalf("invalid mapping was not skipped: %v", err)
				}
				return
			}
			if err != nil {
				mt.Fatal(err)
			}
			mapping := tracker.mappings[tdKey{"ZZ", "0100", "0101"}][tdLeg{"a", "b"}]
			if !tracker.areas["ZZ"] || mapping.Point.Coordinates[1] != 52 {
				mt.Fatalf("unexpected loaded mapping: %+v", mapping)
			}
		})
	}
}

func TestTDTrackProgress(t *testing.T) {
	point := func(lon, lat float64) ctdf.Location {
		return ctdf.Location{Type: "Point", Coordinates: []float64{lon, lat}}
	}
	track := []ctdf.Location{point(0, 52), point(0.01, 52), point(0.02, 52)}
	first := railtd.TrackProgress(point(0.005, 52), track)
	second := railtd.TrackProgress(point(0.015, 52), track)
	if first <= 0 || second <= first {
		t.Fatalf("incorrect progress: %f, %f", first, second)
	}
	if railtd.TrackProgress(point(0.005, 52.001), track) != -1 {
		t.Fatal("accepted off-route point")
	}
}

func TestTDTrustAnchorRejectsDelayedDeparture(t *testing.T) {
	tracker := newTDTracker()
	journey := &ctdf.RealtimeJourney{PrimaryIdentifier: "rail-test", JourneyRunDate: time.Now(), ActivelyTracked: true}
	movement := &TrustMovement{TrainID: "011A010106"}
	now := time.Now()
	tracker.trust(journey, movement, now, -1, nil)
	tracker.trust(journey, movement, now.Add(-time.Minute), 0, nil)
	if tracker.anchors[journey.PrimaryIdentifier].leg != -1 {
		t.Fatal("delayed departure reopened the leg")
	}
}

func TestTDStepsOnlyPositionUniqueRunOnDirectedLeg(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("recent TRUST then TD", func(mt *mtest.T) {
		redisServer := miniredis.RunT(mt.T)
		oldRedis, oldDB := redis_client.Client, database.Instance
		redis_client.Client = redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() {
			_ = redis_client.Client.Close()
			redis_client.Client, database.Instance = oldRedis, oldDB
		})
		ctx := context.Background()
		now := time.Now().UTC().Truncate(time.Second)
		point := func(lon float64) ctdf.Location { return ctdf.Location{Type: "Point", Coordinates: []float64{lon, 52}} }
		tracker := newTDTracker()
		tracker.areas["ZZ"] = true
		key := tdKey{"ZZ", "0100", "0101"}
		legKey := tdLeg{"a", "b"}
		tracker.mappings[key] = map[tdLeg]tdMapping{legKey: {Area: key.Area, From: key.From, To: key.To, Origin: legKey.Origin, Destination: legKey.Destination, Confidence: "mapped", Point: point(0.01)}}
		leg := &ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", TrackRef: "track"}
		journey := &ctdf.RealtimeJourney{
			PrimaryIdentifier: "rail-run", JourneyRunDate: now.Truncate(24 * time.Hour), ActivelyTracked: true,
			OtherIdentifiers: map[string]string{"TrainID": "011A010106"},
			Journey:          &ctdf.Journey{PrimaryIdentifier: "rail-schedule", Path: []*ctdf.JourneyPathItem{leg}},
		}
		save := func(run *ctdf.RealtimeJourney) {
			if err := realtimestore.SaveRealtimeJourney(ctx, run); err != nil {
				mt.Fatal(err)
			}
			tracker.trust(run, &TrustMovement{TrainID: run.OtherIdentifiers["TrainID"]}, now.Add(-time.Minute), 0, nil)
		}
		save(journey)
		trust := point(0)
		if _, err := realtimestore.RecordRailObservation(ctx, journey, realtimestore.RailObservation{ObservedAt: now.Add(-time.Minute), Source: "TRUST", Confidence: "mapped"}, &trust); err != nil {
			mt.Fatal(err)
		}

		second := *journey
		second.PrimaryIdentifier = "rail-run-reused-code"
		second.OtherIdentifiers = map[string]string{"TrainID": "021A010106"}
		save(&second)
		response := mtest.CreateCursorResponse(0, mt.DB.Name()+".journey_tracks", mtest.FirstBatch,
			bson.D{{Key: "track", Value: []ctdf.Location{point(0), point(0.02)}}})
		mt.AddMockResponses(response, response)
		frame := func(from, to string, at time.Time) {
			tracker.processFrame(ctx, []byte(fmt.Sprintf(`[{"CA_MSG":{"time":"%d","area_id":"ZZ","from":"%s","to":"%s","descr":"1A01"}}]`, at.UnixMilli(), from, to)))
		}
		assertPosition := func(source string, lon float64) {
			current, err := realtimestore.FindByIdentifier(ctx, journey.PrimaryIdentifier)
			if err != nil {
				mt.Fatal(err)
			}
			if current.VehicleLocationObservedAt == nil || current.VehicleLocationSource != source || current.VehicleLocation.Coordinates[0] != lon {
				mt.Fatalf("unexpected position: %+v %+v", current.VehicleLocationSource, current.VehicleLocation)
			}
		}
		frame("0100", "0101", now)
		assertPosition("TRUST", 0)
		tracker.invalidateTrain(second.OtherIdentifiers["TrainID"], now)
		mt.AddMockResponses(response)
		frame("0100", "0101", now.Add(time.Second))
		assertPosition("TD", 0.01)
		frame("0101", "0102", now.Add(2*time.Second))
		assertPosition("TD", 0.01)
		tracker.trust(&second, &TrustMovement{TrainID: second.OtherIdentifiers["TrainID"]}, now.Add(2*time.Second), 0, nil)
		tracker.mappings[tdKey{"ZZ", "0102", "0103"}] = map[tdLeg]tdMapping{
			legKey: {From: "0102", Confidence: "mapped", Point: point(0.005)},
		}
		mt.AddMockResponses(response, response)
		frame("0102", "0103", now.Add(3*time.Second))
		assertPosition("TD", 0.01)
		other, err := realtimestore.FindByIdentifier(ctx, second.PrimaryIdentifier)
		if err != nil {
			mt.Fatal(err)
		}
		if other.VehicleLocationObservedAt != nil {
			mt.Fatal("a backward step for the bound run was assigned to a different run")
		}
		snapshot := railtd.SMARTSnapshot{
			Steps: []railtd.SMARTStep{
				{TD: "ZZ", FROMBERTH: "0102", TOBERTH: "0104", STANOX: "00123", STEPTYPE: "B", EVENT: "A"},
				{TD: "ZZ", FROMBERTH: "0102", TOBERTH: "0105", STANOX: "00123", STEPTYPE: "B", EVENT: "A"},
				{TD: "ZZ", FROMBERTH: "0102", TOBERTH: "0105", STANOX: "00456", STEPTYPE: "B", EVENT: "A"},
			},
			Locations: map[string]railtd.SMARTLocation{"00123": {Refs: []string{"b"}, Point: point(0.02)}},
		}
		raw, err := bson.Marshal(snapshot)
		if err != nil {
			mt.Fatal(err)
		}
		var document bson.D
		if err := bson.Unmarshal(raw, &document); err != nil {
			mt.Fatal(err)
		}
		// The previous rejected step queried only the bound train's track.
		mt.ClearMockResponses()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+railtd.SMARTCollection, mtest.FirstBatch, document))
		if err := tracker.loadSMART(ctx); err != nil {
			mt.Fatal(err)
		}
		if len(tracker.smart) != 1 {
			mt.Fatalf("unexpected resolved SMART steps: %v", tracker.smart)
		}
		mt.AddMockResponses(response)
		frame("0102", "0104", now.Add(4*time.Second))
		assertPosition("TD", 0.02)
		positioned, err := realtimestore.FindByIdentifier(ctx, journey.PrimaryIdentifier)
		if err != nil {
			mt.Fatal(err)
		}
		if positioned.VehicleLocationConfidence != "approximate" {
			mt.Fatalf("unexpected SMART confidence: %q", positioned.VehicleLocationConfidence)
		}
	})
}

func TestTrustPassingPointKeepsDepartureAnchor(t *testing.T) {
	tracker := newTDTracker()
	now := time.Now()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "rail-run", ActivelyTracked: true, JourneyRunDate: now,
		Journey: &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{PassingPath: []string{"gb-tiploc-PASS"}}}},
	}
	movement := &TrustMovement{TrainID: "021A010106"}
	departure := now.Add(-time.Minute)
	tracker.trust(journey, movement, departure, 0, nil)
	anchor := tracker.anchors[journey.PrimaryIdentifier]
	anchor.berth, anchor.progress = "0001", 0.25
	tracker.anchors[journey.PrimaryIdentifier] = anchor

	tracker.trust(journey, movement, now, -1, &ctdf.Stop{OtherIdentifiers: []string{"gb-tiploc-PASS"}})
	got := tracker.anchors[journey.PrimaryIdentifier]
	if got.leg != 0 || got.berth != "0001" || got.progress != 0.25 || !got.at.Equal(departure) || !got.trustAt.Equal(now) {
		t.Fatalf("passing point did not preserve departure and TD continuity: %+v", got)
	}
}

func TestCancellationBeforeDepartureRejectsOlderAnchor(t *testing.T) {
	tracker := newTDTracker()
	now := time.Now()
	journey := &ctdf.RealtimeJourney{PrimaryIdentifier: "rail-run", ActivelyTracked: true, JourneyRunDate: now}
	movement := &TrustMovement{TrainID: "021A010106"}
	tracker.invalidateTrain(movement.TrainID, now)
	tracker.trust(journey, movement, now.Add(-time.Second), 0, nil)
	if _, exists := tracker.anchors[journey.PrimaryIdentifier]; exists {
		t.Fatal("an older departure created an anchor after cancellation")
	}
}
