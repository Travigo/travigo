package journeytracks

import (
	"context"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestCleanupOSMRailTracksProtectsJourneyReferences(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("only delete unreferenced tracks", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previous })

		reply := func(collection string, documents ...bson.D) {
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+collection, mtest.FirstBatch, documents...))
		}
		reply("journey_tracks",
			bson.D{{Key: "primaryidentifier", Value: "raw-fallback"}},
			bson.D{{Key: "primaryidentifier", Value: "staged"}},
			bson.D{{Key: "primaryidentifier", Value: "live"}},
			bson.D{{Key: "primaryidentifier", Value: "unused"}},
		)
		reply(database.JourneysRawCollectionName, bson.D{{Key: "path", Value: bson.A{
			bson.D{{Key: "trackref", Value: "raw-fallback"}},
		}}})
		reply(database.JourneysStagingCollectionName, bson.D{{Key: "trackref", Value: "staged"}})
		reply(database.JourneysCollectionName, bson.D{{Key: "trackref", Value: "live"}})
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}))

		source := &ctdf.DataSourceReference{DatasetID: "osm", OriginalFormat: "gb-osm-rail-tracks", Timestamp: "123"}
		if err := CleanupOSMRailTracks(context.Background(), source); err != nil {
			mt.Fatal(err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 5 || events[4].CommandName != "delete" {
			mt.Fatalf("expected four reads and one delete, got %v", events)
		}
		deletions, err := events[4].Command.Lookup("deletes").Array().Values()
		if err != nil || len(deletions) != 1 {
			mt.Fatalf("delete requests = %v, %v", deletions, err)
		}
		refs, err := deletions[0].Document().Lookup("q", "primaryidentifier", "$in").Array().Values()
		if err != nil || len(refs) != 1 || refs[0].StringValue() != "unused" {
			mt.Fatalf("deleted track refs = %v, %v", refs, err)
		}
	})
}
