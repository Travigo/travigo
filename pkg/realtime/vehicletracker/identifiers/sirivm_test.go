package identifiers

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestSameServiceNameNormalizesCommonPresentationDifferences(t *testing.T) {
	for _, test := range []struct{ left, right string }{
		{" X 1 ", "x-1"},
		{"A/12", "a12"},
		{"25", "Route 25"},
	} {
		if !sameServiceName(test.left, test.right) {
			t.Fatalf("%q and %q should match", test.left, test.right)
		}
	}
}

func TestSiriReportsCandidatesForEachAttemptedFallback(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("all fallbacks", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previous })
		operatorRef := mt.Name() + time.Now().String()
		available := bson.D{
			{Key: "primaryidentifier", Value: "available"},
			{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
		}
		unavailable := bson.D{{Key: "primaryidentifier", Value: "unavailable"}}
		wrongDirection := bson.D{
			{Key: "primaryidentifier", Value: "wrong-direction"},
			{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
			{Key: "direction", Value: "inbound"},
			{Key: "departuretime", Value: time.Date(0, 1, 2, 1, 30, 0, 0, time.UTC)},
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".operators", mtest.FirstBatch,
				bson.D{{Key: "primaryidentifier", Value: operatorRef}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".services", mtest.FirstBatch,
				bson.D{{Key: "primaryidentifier", Value: "service"}, {Key: "servicename", Value: "1"}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, unavailable),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, wrongDirection),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, available, unavailable),
		)

		before := telemetry.Snapshot().SiriMatching
		identifier := SiriVM{IdentifyingInformation: map[string]string{
			"OperatorRef": operatorRef, "PublishedLineName": "1", "VehicleJourneyRef": "trip",
			"BlockRef": "block", "OriginRef": "origin", "FramedVehicleJourneyDate": "2026-11-10",
			"DirectionRef": "outbound", "OriginAimedDepartureTime": "2026-11-10T01:30:00+00:00",
		}}
		if got, err := identifier.IdentifyJourney(); err != nil || got != "available" {
			mt.Fatalf("stops fallback returned %q, %v", got, err)
		}
		after := telemetry.Snapshot().SiriMatching
		for name, want := range map[string][3]int64{
			"journey_ref":      {1, 0, 0},
			"block_ref":        {0, 0, 0},
			"stops":            {2, 1, 1},
			"stops_exact_time": {1, 1, 0},
		} {
			a, b := before[name], after[name]
			if b.Lookup.Count-a.Lookup.Count != 1 || b.Narrowing.Count-a.Narrowing.Count != 1 {
				mt.Errorf("%s did not record one lookup and narrowing attempt", name)
			}
			if got := [3]int64{b.Candidates - a.Candidates, b.Available - a.Available, b.Matches - a.Matches}; got != want {
				mt.Errorf("%s candidates, available and matches = %v, want %v", name, got, want)
			}
		}
	})
}

func TestSameServiceNameDoesNotMatchDifferentRoutes(t *testing.T) {
	if sameServiceName("25", "26") {
		t.Fatal("different service names matched")
	}
}

func TestSiriExactDepartureAcceptsOvernightClockAndRejectsAmbiguity(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("overnight departure", func(mt *mtest.T) {
		day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
		identifier := SiriVM{CurrentTime: day, IdentifyingInformation: map[string]string{
			"OriginAimedDepartureTime": "2026-11-11T01:30:00+00:00", "DirectionRef": "OUTBOUND",
		}}
		candidate := func(id string) bson.D {
			return bson.D{
				{Key: "primaryidentifier", Value: id},
				{Key: "direction", Value: "outbound"},
				{Key: "departuretime", Value: time.Date(0, 1, 2, 1, 30, 0, 0, time.UTC)},
				{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
			}
		}
		namespace := mt.DB.Name() + "." + mt.Coll.Name()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, candidate("trip")),
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, candidate("trip"), candidate("other")),
		)
		query := bson.M{"serviceref": "route", "path.originstopref": "stop"}
		if got := identifier.findExactDeparture(mt.Coll, day, query); got != "trip" {
			mt.Fatalf("exact departure got %q, want overnight trip", got)
		}
		if got := identifier.findExactDeparture(mt.Coll, day, query); got != "" {
			mt.Fatalf("ambiguous departure selected %q instead of allowing the broad fallback", got)
		}
		filter := mt.GetAllStartedEvents()[0].Command.Lookup("filter").Document()
		clock := filter.Lookup("$and", "1", "$expr", "$eq", "1").StringValue()
		if clock != "01:30" {
			mt.Fatalf("lookup clock = %q, want 01:30", clock)
		}
	})
}

func TestSiriCandidateProjectionRetainsMatchingFields(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("candidate narrowing", func(mt *mtest.T) {
		day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
		candidate := func(id string, hour int) bson.D {
			departure := time.Date(0, 1, 1, hour, 0, 0, 0, time.UTC)
			return bson.D{
				{Key: "primaryidentifier", Value: id},
				{Key: "serviceref", Value: "service"},
				{Key: "direction", Value: "outbound"},
				{Key: "departuretime", Value: departure},
				{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
				{Key: "path", Value: bson.A{bson.M{
					"originstopref": "origin", "destinationstopref": "dest",
					"originarrivaltime": departure, "origindeparturetime": departure,
					"destinationarrivaltime": departure.Add(10 * time.Minute),
				}}},
			}
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, candidate("wanted", 10), candidate("other", 11)))
		candidates, _, err := getAvailableJourneys(mt.Coll, day, bson.M{"serviceref": "service"}, 0)
		if err != nil {
			mt.Fatal(err)
		}
		projection := mt.GetStartedEvent().Command.Lookup("projection").Document()
		for _, field := range []string{
			"primaryidentifier", "serviceref", "direction", "departuretime", "availability",
			"path.originstopref", "path.destinationstopref", "path.originarrivaltime",
			"path.origindeparturetime", "path.destinationarrivaltime",
		} {
			if projection.Lookup(field).Int32() != 1 {
				mt.Fatalf("candidate projection omits required field %s", field)
			}
		}
		if _, err := projection.LookupErr("track"); err == nil {
			mt.Fatal("candidate projection includes whole-journey geometry")
		}
		identifier := SiriVM{
			CurrentTime: day,
			IdentifyingInformation: map[string]string{
				"DirectionRef": "outbound", "OriginRef": "origin", "DestinationRef": "dest",
				"OriginAimedDepartureTime": "2026-11-10T10:00:00+00:00",
			},
		}
		selected, err := identifier.narrowJourneys(candidates, true)
		if err != nil || selected.PrimaryIdentifier != "wanted" {
			mt.Fatalf("lightweight candidates changed matching: %+v (%v)", selected, err)
		}
		if len(candidates) != 2 || candidates[0].Availability == nil || candidates[0].Path[0].OriginStopRef != "origin" {
			mt.Fatal("candidate availability or ordered path was lost")
		}
		if candidates[0].GenerateFunctionalHash(true) == candidates[1].GenerateFunctionalHash(true) {
			mt.Fatal("candidate projection lost functional hash inputs")
		}
	})
}
