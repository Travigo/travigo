package datalinker

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"testing"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestRetiredDatasetLookupProtectsConfiguredAndInternalIDs(t *testing.T) {
	t.Chdir("../..")
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("no retired IDs", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		empty := mtest.CreateSuccessResponse(bson.E{Key: "values", Value: bson.A{}})
		mt.AddMockResponses(empty, empty)
		if err := removeRetiredDatasets(mt.Context()); err != nil {
			mt.Fatal(err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 2 {
			mt.Fatalf("got %d Mongo commands, want two lookups and no writes", len(events))
		}
		for i, field := range []string{"dataset", "datasetidentifier"} {
			event := events[i]
			if event.CommandName != "distinct" {
				mt.Fatalf("got %s, want distinct", event.CommandName)
			}
			keep, err := event.Command.Lookup("query", field, "$nin").Array().Values()
			if err != nil {
				mt.Fatal(err)
			}
			for _, id := range []string{
				"gb-traveline-noc", "global-openstreetmap-osm-data",
				"linker-stops", "linker-services", "linker-journeys", "stop-transfers",
			} {
				if !slices.ContainsFunc(keep, func(value bson.RawValue) bool { return value.StringValue() == id }) {
					mt.Errorf("%s lookup failed to protect %q", field, id)
				}
			}
		}
	})
}

func TestRetiredDatasetDeletionStopsOnWriteFailure(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("first collection fails", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 2, Message: "delete failed"}))
		if err := deleteRetiredDatasetData(mt.Context(), []string{"retired-feed"}); err == nil {
			mt.Fatal("expected deletion to return the write failure")
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 1 || events[0].Command.Lookup("delete").StringValue() != "stops_raw" {
			mt.Fatalf("expected only the failed stops_raw deletion, got %v", events)
		}
		filter := events[0].Command.Lookup("deletes", "0", "q").Document()
		ids, err := filter.Lookup("datasource.datasetid", "$in").Array().Values()
		if err != nil || len(ids) != 1 || ids[0].StringValue() != "retired-feed" {
			mt.Fatalf("unexpected deletion filter %s", filter)
		}
	})
}

func TestRetiredDatasetRetryWithdrawsStopServicesAfterJourneyDeletion(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("live journeys already gone", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		const retiredID = "retired-feed"
		retiredKey := fmt.Sprintf("%x", sha256.Sum256([]byte(retiredID)))
		retainedKey := fmt.Sprintf("%x", sha256.Sum256([]byte("retained-feed")))
		success := mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1})
		nothingDeleted := mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 0})
		mt.AddMockResponses(
			nothingDeleted, nothingDeleted, nothingDeleted, nothingDeleted, nothingDeleted,
			success,                        // imported GTFS transfers
			nothingDeleted, nothingDeleted, // raw and published journeys
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+stopservices.StateCollectionName, mtest.FirstBatch,
				bson.D{{Key: "eventRevision", Value: int64(5)}, {Key: "sources", Value: bson.M{
					retiredKey:  bson.M{"dataset": retiredID, "generation": "old", "activeWriters": 1},
					retainedKey: bson.M{"dataset": "retained-feed", "generation": "current", "activeWriters": 1},
				}}}),
			success,
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journey_tracks", mtest.FirstBatch),
			nothingDeleted, nothingDeleted,
			success, success, // transfer and stop index rebuild checkpoints
		)
		if err := deleteRetiredDatasetData(mt.Context(), []string{retiredID}); err != nil {
			mt.Fatal(err)
		}
		var deletedTransfers, withdrewGeneration bool
		events := mt.GetAllStartedEvents()
		for _, event := range events {
			switch {
			case event.CommandName == "delete" && event.Command.Lookup("delete").StringValue() == "stop_transfers":
				filter := event.Command.Lookup("deletes", "0", "q").Document()
				id := filter.Lookup("datasource.datasetid", "$in", "0").StringValue()
				deletedTransfers = id == retiredID
			case event.CommandName == "update" && event.Command.Lookup("update").StringValue() == stopservices.StateCollectionName:
				update := event.Command.Lookup("updates", "0", "u").Document()
				fields, err := update.Lookup("$unset").Document().Elements()
				if err != nil || len(fields) != 1 || fields[0].Key() != "sources."+retiredKey {
					mt.Fatalf("unexpected manifest retirement: %s", update)
				}
				if update.Lookup("$push", "retired", "$each", "0", "generation").StringValue() != "old" {
					mt.Fatalf("retirement did not preserve the prior generation for readers: %s", update)
				}
				withdrewGeneration = true
			}
		}
		if !deletedTransfers || !withdrewGeneration {
			mt.Fatalf("retirement missed imported transfers or stop-services: transfers=%t stop-services=%t", deletedTransfers, withdrewGeneration)
		}
		checkpoint := events[len(events)-2].Command.Lookup("updates", "0").Document()
		if checkpoint.Lookup("q", "dataset").StringValue() != datasetversion.StopTransfersDataset ||
			checkpoint.Lookup("u", "$max", "rebuildafter").DateTime() == 0 {
			mt.Fatalf("expected a durable stop-transfer rebuild request, got %s", checkpoint)
		}
		checkpoint = events[len(events)-1].Command.Lookup("updates", "0").Document()
		if checkpoint.Lookup("q", "dataset").StringValue() != datasetversion.LinkerDataset("stop") ||
			checkpoint.Lookup("u", "$max", "stopindexrebuildafter").DateTime() == 0 {
			mt.Fatalf("expected a durable stop index rebuild request, got %s", checkpoint)
		}
	})
}
