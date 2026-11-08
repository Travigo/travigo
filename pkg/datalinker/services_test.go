package datalinker

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestEnrichStagedServicesWritesPassengerMetadataByDocumentID(t *testing.T) {
	t.Chdir("../..")
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("service ready for publication", func(mt *mtest.T) {
		id := primitive.NewObjectID()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
				{Key: "_id", Value: id},
				{Key: "primaryidentifier", Value: "existing-linked-id"},
				{Key: "servicename", Value: "GRN"},
				{Key: "operatorref", Value: "gb-noc-NXMT"},
				{Key: "transporttype", Value: "Metro"},
				{Key: "datasource", Value: bson.M{"datasetid": "gb-dft-bods-gtfs-schedule-north-east"}},
			}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
		)
		if err := enrichStagedServices(t.Context(), mt.Coll); err != nil {
			mt.Fatal(err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 2 || events[0].CommandName != "find" || events[1].CommandName != "update" {
			mt.Fatalf("expected a scan and an update, got %v", events)
		}
		updates, err := events[1].Command.Lookup("updates").Array().Values()
		if err != nil || len(updates) != 1 {
			mt.Fatalf("expected one service update, got %v, %v", updates, err)
		}
		update := updates[0].Document()
		if got := update.Lookup("q", "_id").ObjectID(); got != id {
			mt.Fatalf("updated document %v, want %v", got, id)
		}
		fields := update.Lookup("u", "$set").Document()
		if fields.Lookup("servicename").StringValue() != "Green Line" ||
			fields.Lookup("publisherservicename").StringValue() != "GRN" ||
			fields.Lookup("website").StringValue() == "" || fields.Lookup("primaryidentifier").Type != 0 {
			mt.Fatalf("wrong staged fields: %v", fields)
		}
	})
}
