package vehicletracker

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestStopLocationsAreReusedAcrossJourneys(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("reuse alias across different journey paths", func(mt *mtest.T) {
		old := database.Instance
		database.Instance = &database.MongoInstance{Database: mt.DB, Client: mt.Client}
		mt.Cleanup(func() { database.Instance = old })
		ref := mt.DB.Name() + "-alias"
		id := mt.DB.Name() + "-stop"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".stops", mtest.FirstBatch, bson.D{
			{Key: "primaryidentifier", Value: id},
			{Key: "otheridentifiers", Value: bson.A{ref}},
			{Key: "location", Value: bson.M{"type": "Point", "coordinates": bson.A{-0.1, 51.5}}},
		}))
		for _, origin := range []string{"first", "second"} {
			journey := &ctdf.Journey{Calls: ctdf.JourneyCallsFromLegs(&ctdf.JourneyPathItem{
				OriginStopRef: origin, DestinationStopRef: ref,
			})}
			complete, err := hydrateDestinationStops(journey)
			stop := journey.Calls[1].Stop
			if err != nil || !complete || stop == nil || stop.PrimaryIdentifier != id || stop.Location.Coordinates[1] != 51.5 {
				mt.Fatalf("location was not hydrated: stop=%+v, complete=%t, error=%v", stop, complete, err)
			}
		}
		if got := len(mt.GetAllStartedEvents()); got != 1 {
			mt.Fatalf("got %d MongoDB commands, want one", got)
		}
	})
}
