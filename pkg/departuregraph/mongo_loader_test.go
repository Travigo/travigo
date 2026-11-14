package departuregraph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestMongoJourneyRepairResolvesStoredZeroTimes(t *testing.T) {
	journey := &ctdf.Journey{PrimaryIdentifier: "journey-1", Calls: ctdf.JourneyCallsFromLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "stop-a", DestinationStopRef: "stop-b", OriginArrivalTime: serviceTime(3600), OriginDepartureTime: serviceTime(3600), DestinationArrivalTime: serviceTime(4200)},
		&ctdf.JourneyPathItem{OriginStopRef: "stop-b", DestinationStopRef: "stop-c", OriginArrivalTime: serviceTime(0), OriginDepartureTime: time.Time{}, DestinationArrivalTime: serviceTime(5400)},
	)}
	journey.Calls[0].Outgoing = nil
	journey.Calls[1].Arrival = time.Time{}
	journey.Calls[1].Departure = time.Time{}
	journey.Calls[1].IncomingArrival = nil
	if err := repairMissingJourneyTimes(journey); err != nil {
		t.Fatal(err)
	}
	if journey.Calls[1].Departure.IsZero() || !journey.Calls[1].ArrivalTime().Equal(journey.Calls[1].Departure) {
		t.Fatal("stored intermediate zero times were not repaired")
	}
	data := newGraphData()
	if !data.addJourney(20261102, journey) {
		t.Fatalf("repaired journey was rejected: %#v", data.stats())
	}
	if len(data.materializeStop(20261102, []string{"stop-b"})) != 1 {
		t.Fatal("repaired call was not indexed")
	}
	if err := repairMissingJourneyTimes(&ctdf.Journey{Calls: []ctdf.JourneyCall{{StopRef: "only", Start: true, End: true}}}); err == nil {
		t.Fatal("unresolvable stored journey was accepted")
	}
}

func TestMongoJourneyRecordDecodesIdentifierAndProjectedJourney(t *testing.T) {
	identifier := primitive.NewObjectID()
	departure := time.Date(0, 1, 2, 1, 0, 0, 0, time.UTC)
	encoded, err := bson.Marshal(bson.M{
		"_id":               identifier,
		"primaryidentifier": "journey-1",
		"serviceref":        "service-1",
		"calls": bson.A{
			bson.M{"stopref": "stop-a", "start": true, "arrival": departure, "departure": departure},
			bson.M{"stopref": "stop-b", "end": true, "arrival": departure.Add(time.Minute)},
		},
	})
	if err != nil {
		t.Fatalf("marshal projected journey: %v", err)
	}

	var record mongoJourneyRecord
	if err := bson.Unmarshal(encoded, &record); err != nil {
		t.Fatalf("decode projected journey: %v", err)
	}
	journey := record.Journey
	if record.ID != identifier || journey.PrimaryIdentifier != "journey-1" || journey.ServiceRef != "service-1" ||
		len(journey.Calls) != 2 || journey.Calls[0].StopRef != "stop-a" || journey.Calls[1].StopRef != "stop-b" ||
		!journey.Calls[0].Departure.Equal(departure) {
		t.Fatalf("unexpected projected journey: %#v", record)
	}
}

func TestMongoLoaderScanProjectsAndDecodesOrderedCalls(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("journey scan", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previous })

		id := primitive.NewObjectID()
		departure := time.Date(0, 1, 2, 1, 0, 0, 0, time.UTC)
		document := bson.D{
			{Key: "_id", Value: id},
			{Key: "primaryidentifier", Value: "journey-1"},
			{Key: "serviceref", Value: "service-1"},
			{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": ctdf.AvailabilityMatchAll}}}},
			{Key: "calls", Value: bson.A{
				bson.M{"stopref": "stop-a", "start": true, "arrival": departure, "departure": departure, "platform": "1"},
				bson.M{"stopref": "stop-b", "end": true, "arrival": departure.Add(time.Minute), "platform": "2"},
			}},
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, document))

		var got *ctdf.Journey
		if err := (MongoLoader{}).ScanJourneys(context.Background(), []time.Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, "", func(journey *ctdf.Journey, cursor string) error {
			got = journey
			if cursor != id.Hex() {
				t.Fatalf("scan cursor = %q, want %q", cursor, id.Hex())
			}
			return nil
		}); err != nil {
			mt.Fatal(err)
		}
		if got == nil || len(got.Calls) != 2 || got.Calls[0].StopRef != "stop-a" || got.Calls[1].StopRef != "stop-b" ||
			got.Calls[0].Platform != "1" || got.Calls[1].ArrivalPlatform() != "2" {
			mt.Fatalf("scan did not reconstruct the projected calls: %#v", got)
		}
		command := mt.GetStartedEvent().Command
		projection := command.Lookup("projection").Document()
		for _, field := range []string{"calls.stopref", "calls.start", "calls.end", "calls.arrival", "calls.departure", "calls.platform"} {
			if projection.Lookup(field).Int32() != 1 {
				mt.Fatalf("departure graph projection omits %s", field)
			}
		}
		if _, err := projection.LookupErr("track"); err == nil {
			mt.Fatalf("departure graph fetched journey geometry: %v", projection)
		}
	})
}

func TestJourneyServiceDateFilterScopesPrimaryAvailabilityRules(t *testing.T) {
	filter := journeyServiceDateFilter([]time.Time{
		time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC),
	})
	clauses, ok := filter["$or"].(bson.A)
	if !ok || len(clauses) != 4 {
		t.Fatalf("availability clauses = %#v", filter["$or"])
	}
	rendered := fmt.Sprint(filter)
	for _, expected := range []string{"MatchAll", "DateRange", "2026-08-10", "2026-08-11", "Monday", "Tuesday"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("filter %q does not contain %q", rendered, expected)
		}
	}
}

func TestJourneyServiceDateFilterRejectsEmptyWindow(t *testing.T) {
	filter := journeyServiceDateFilter(nil)
	if _, exists := filter["_id"]; !exists {
		t.Fatalf("empty window filter = %#v", filter)
	}
}
