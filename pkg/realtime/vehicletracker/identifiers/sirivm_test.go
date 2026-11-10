package identifiers

import (
	"testing"
	"time"

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

func TestSameServiceNameDoesNotMatchDifferentRoutes(t *testing.T) {
	if sameServiceName("25", "26") {
		t.Fatal("different service names matched")
	}
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
		candidates := getAvailableJourneys(mt.Coll, day, bson.M{"serviceref": "service"})
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
