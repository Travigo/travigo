package identifiers

import (
	"regexp"
	"testing"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs/nyct"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestSubwayJourneyCalendarMatching(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, tt := range []struct {
		name string
		trip string
		docs []bson.D
		want string
		fail bool
	}{
		{name: "inactive candidates do not hide valid trip", docs: []bson.D{
			subwayJourney("old1", "2026-11-03", false), subwayJourney("old2", "2026-11-02", false), subwayJourney("valid", "2026-11-04", false),
		}, want: "valid"},
		{name: "calendar exception", docs: []bson.D{subwayJourney("excluded", "2026-11-04", true)}, fail: true},
		{name: "ambiguous", docs: []bson.D{subwayJourney("one", "2026-11-04", false), subwayJourney("two", "2026-11-04", false)}, fail: true},
		{name: "unmatched", fail: true},
		{name: "full path resolves collision", trip: "095950_2..N14R", docs: []bson.D{
			subwayPathJourney("other", "095950_2..N06R"), subwayPathJourney("wanted", "095950_2..N14R"),
		}, want: "wanted"},
		{name: "missing path refuses collision", trip: "095950_2..N", docs: []bson.D{
			subwayPathJourney("other", "095950_2..N06R"), subwayPathJourney("wanted", "095950_2..N14R"),
		}, fail: true},
		{name: "pathless unique fallback", trip: "095950_2..N", docs: []bson.D{subwayPathJourney("wanted", "095950_2..N14R")}, want: "wanted"},
		{name: "unknown path unique fallback", trip: "095950_2..N99R", docs: []bson.D{subwayPathJourney("wanted", "095950_2..N14R")}, want: "wanted"},
		{name: "unknown path refuses collision", trip: "095950_2..N99R", docs: []bson.D{subwayPathJourney("one", "095950_2..N06R"), subwayPathJourney("two", "095950_2..N14R")}, fail: true},
		{name: "shuttle exact match", trip: "021150_GS.N01R", docs: []bson.D{subwayPathJourney("shuttle", "021150_GS.N01R")}, want: "shuttle"},
	} {
		mt.Run(tt.name, func(mt *mtest.T) {
			previous := database.Instance
			database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
			defer func() { database.Instance = previous }()
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, tt.docs...))
			identifier := GTFSRT{IdentifyingInformation: map[string]string{"LinkedDataset": nyct.SubwayDataset, "TripID": "021150_2..N", "TripStartDate": "20261104"}}
			if tt.trip != "" {
				identifier.IdentifyingInformation["TripID"] = tt.trip
			}
			got, err := identifier.IdentifyJourney()
			if (err != nil) != tt.fail || got != tt.want {
				mt.Fatalf("got %q, %v; want %q, failure %v", got, err, tt.want, tt.fail)
			}
			command := mt.GetStartedEvent().Command
			filter := command.Lookup("filter").Document()
			key := nyct.TripKey(identifier.IdentifyingInformation["TripID"])
			if filter.Lookup("datasource.datasetid").StringValue() != nyct.SubwayDataset || filter.Lookup("otheridentifiers.GTFS-TripID").Document().Lookup("$regex").StringValue() != "^"+regexp.QuoteMeta(key)+"[A-Za-z0-9]*$" {
				mt.Fatal("incorrect query", filter)
			}
			if _, err := command.LookupErr("limit"); err == nil {
				mt.Fatal("query limits candidates before date filtering")
			}
		})
	}
}

func subwayJourney(id, date string, exclude bool) bson.D {
	availability := bson.M{"match": bson.A{bson.M{"type": "Date", "value": date}}}
	if exclude {
		availability["exclude"] = bson.A{bson.M{"type": "Date", "value": date}}
	}
	return bson.D{{Key: "primaryidentifier", Value: id}, {Key: "availability", Value: availability}}
}

func TestSubwayRejectsInvalidIdentity(t *testing.T) {
	for _, tt := range []struct{ trip, date string }{
		{"021150_2..N", ""}, {"021150_2..N", "20260230"}, {"bad", "20261104"},
	} {
		identifier := GTFSRT{IdentifyingInformation: map[string]string{"LinkedDataset": nyct.SubwayDataset, "TripID": tt.trip, "TripStartDate": tt.date}}
		if _, err := identifier.IdentifyJourney(); err == nil {
			t.Fatalf("accepted invalid identity %+v", tt)
		}
	}
}

func TestSubwayDoesNotReturnPartialResultAfterCursorError(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("getMore error", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		defer func() { database.Instance = previous }()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(123, mt.DB.Name()+".journeys", mtest.FirstBatch, subwayJourney("valid", "2026-11-04", false)),
			mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 1, Message: "cursor failed"}),
			mtest.CreateSuccessResponse(),
		)
		identifier := GTFSRT{IdentifyingInformation: map[string]string{"LinkedDataset": nyct.SubwayDataset, "TripID": "021150_2..N", "TripStartDate": "20261104"}}
		if got, err := identifier.IdentifyJourney(); err == nil || got != "" {
			mt.Fatalf("returned partial result %q, %v", got, err)
		}
	})
}

func subwayPathJourney(id, trip string) bson.D {
	return append(subwayJourney(id, "2026-11-04", false), bson.E{Key: "otheridentifiers", Value: bson.M{"GTFS-TripID": trip}})
}
