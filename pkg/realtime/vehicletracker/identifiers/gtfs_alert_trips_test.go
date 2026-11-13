package identifiers

import (
	"slices"
	"testing"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestResolveAlertTripsBatchesAndCachesFrequencyMatches(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("frequency and ordinary trips", func(mt *mtest.T) {
		old := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = old })
		row := func(trip, clock, id string) bson.D {
			return bson.D{
				{Key: "primaryidentifier", Value: id},
				{Key: "otheridentifiers", Value: bson.M{"GTFS-TripID": trip, "GTFS-TripStartTime": clock}},
			}
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch,
			row("frequency", "24:30:00", "overnight"), row("frequency", "25:30:00", "later"),
			row("ordinary", "", "scheduled"),
			row("ambiguous", "08:00:00", "one"), row("ambiguous", "08:00:00", "two"),
		))
		ref := func(trip, clock string) map[string]string {
			return map[string]string{"LinkedDataset": mt.DB.Name(), "TripID": trip, "TripStartTime": clock}
		}
		refs := []map[string]string{
			ref("frequency", "24:30:00"), ref("frequency", "25:30:00"),
			ref("ordinary", "14:10:00"), ref("ambiguous", "08:00:00"), ref("missing", ""),
			ref("frequency", "24:30:00"), {"StopID": "stop"},
		}
		want := []string{"overnight", "later", "scheduled", "", "", "overnight", ""}
		for range 2 {
			got, err := ResolveAlertTrips(refs)
			if err != nil || !slices.Equal(got, want) {
				mt.Fatalf("results %v, want %v, error %v", got, want, err)
			}
		}
		if got := len(mt.GetAllStartedEvents()); got != 1 {
			mt.Fatalf("got %d MongoDB commands across two alerts, want 1", got)
		}
	})
}
