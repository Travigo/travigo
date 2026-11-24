package datasetversion

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestChangedDatasetsBatchesSourceVersionsAndPreservesOrder(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("batch lookup", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		derivedAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		sourceAt := derivedAt.Add(time.Second)
		namespace := mt.DB.Name() + ".dataset_versions"
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, bson.D{{Key: "dataset", Value: "derived"}, {Key: "lastmodified", Value: derivedAt}}),
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch,
				bson.D{{Key: "dataset", Value: "newer"}, {Key: "lastmodified", Value: sourceAt}},
				bson.D{{Key: "dataset", Value: "same"}, {Key: "lastmodified", Value: derivedAt}},
			),
		)

		got, err := ChangedDatasets(context.Background(), "derived", []string{"newer", "missing", "same", "newer"})
		if err != nil {
			mt.Fatal(err)
		}
		if want := []string{"newer", "newer"}; !reflect.DeepEqual(got, want) {
			mt.Fatalf("ChangedDatasets() = %v, want %v", got, want)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 2 || events[1].CommandName != "find" {
			mt.Fatalf("expected derived lookup and one batched source lookup, got %v", events)
		}
		ids, err := events[1].Command.Lookup("filter", "dataset", "$in").Array().Values()
		if err != nil || len(ids) != 4 {
			mt.Fatalf("source lookup did not include source IDs: %v, %v", ids, err)
		}
	})
}

func TestChangedDatasetsFirstRunReturnsSourcesWithoutBatchLookup(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("missing derived version", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".dataset_versions", mtest.FirstBatch))

		sources := []string{"one", "two"}
		got, err := ChangedDatasets(context.Background(), "derived", sources)
		if err != nil {
			mt.Fatal(err)
		}
		if !reflect.DeepEqual(got, sources) {
			mt.Fatalf("ChangedDatasets() = %v, want %v", got, sources)
		}
		if events := mt.GetAllStartedEvents(); len(events) != 1 {
			mt.Fatalf("first run should only look up derived version, got %d commands", len(events))
		}
	})
}
