package stopservices

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestBatchForStopsDistinguishesUnbuiltFromEmpty(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("missing state is unbuilt", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch))
		_, err := BatchForStops(mt.Context(), mt.DB, []string{"stop-1"})
		if !errors.Is(err, ErrUnbuilt) {
			mt.Fatalf("error = %v, want ErrUnbuilt", err)
		}
	})
	mt.Run("initialized empty catalog returns present empty slices", func(mt *mtest.T) {
		state := bson.D{{Key: "_id", Value: "static"}, {Key: "initialized", Value: true}, {Key: "sources", Value: bson.D{}}}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch, state))
		got, err := BatchForStops(mt.Context(), mt.DB, []string{"stop-1", "stop-2"})
		if err != nil {
			mt.Fatal(err)
		}
		want := map[string][]string{"stop-1": {}, "stop-2": {}}
		if !reflect.DeepEqual(got, want) {
			mt.Fatalf("references = %#v, want %#v", got, want)
		}
		if events := mt.GetAllStartedEvents(); len(events) != 1 || events[0].CommandName != "find" {
			mt.Fatalf("expected only the manifest read, got %#v", events)
		}
	})
	mt.Run("registered source without a published generation is unbuilt", func(mt *mtest.T) {
		state := bson.D{{Key: "_id", Value: "static"}, {Key: "initialized", Value: true}, {Key: "sources", Value: bson.D{
			{Key: "new-source", Value: bson.D{{Key: "dataset", Value: "new-static-dataset"}, {Key: "publishedRevision", Value: int64(-1)}}},
		}}}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch, state))
		_, err := BatchForStops(mt.Context(), mt.DB, []string{"stop-1"})
		if !errors.Is(err, ErrUnbuilt) {
			mt.Fatalf("error = %v, want ErrUnbuilt", err)
		}
	})
}

func TestBatchForStopsReadsPublishedStaticGenerationsAndMergesDatasets(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("read uses published generations and excludes dynamic overlays", func(mt *mtest.T) {
		state := bson.D{
			{Key: "_id", Value: "static"},
			{Key: "initialized", Value: true},
			{Key: "sources", Value: bson.D{
				{Key: "a", Value: bson.D{{Key: "dataset", Value: "static-a"}, {Key: "revision", Value: int64(2)}, {Key: "publishedRevision", Value: int64(1)}, {Key: "activeWriters", Value: int64(1)}, {Key: "generation", Value: "generation-a"}}},
				{Key: "b", Value: bson.D{{Key: "dataset", Value: "static-b"}, {Key: "revision", Value: int64(1)}, {Key: "publishedRevision", Value: int64(1)}, {Key: "activeWriters", Value: int64(0)}, {Key: "generation", Value: "generation-b"}}},
				{Key: "dynamic", Value: bson.D{{Key: "dataset", Value: "gb-networkrail-vstp"}, {Key: "generation", Value: "dynamic-generation"}}},
			}},
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch, state),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+RefsCollectionName, mtest.FirstBatch,
				bson.D{{Key: "stopref", Value: "stop-1"}, {Key: "serviceref", Value: "service-b"}},
				bson.D{{Key: "stopref", Value: "stop-1"}, {Key: "serviceref", Value: "service-a"}},
				bson.D{{Key: "stopref", Value: "stop-1"}, {Key: "serviceref", Value: "service-a"}},
				bson.D{{Key: "stopref", Value: "stop-2"}, {Key: "serviceref", Value: "service-c"}},
			),
		)

		got, err := BatchForStops(mt.Context(), mt.DB, []string{"stop-1", "stop-2", "stop-3", "stop-1"})
		if err != nil {
			mt.Fatal(err)
		}
		want := map[string][]string{
			"stop-1": {"service-a", "service-b"},
			"stop-2": {"service-c"},
			"stop-3": {},
		}
		if !reflect.DeepEqual(got, want) {
			mt.Fatalf("references = %#v, want %#v", got, want)
		}

		var sourcePairs []string
		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName != "find" || event.Command.Lookup("find").StringValue() != RefsCollectionName {
				continue
			}
			values, err := event.Command.Lookup("filter", "$or").Array().Values()
			if err != nil {
				mt.Fatalf("read source filter: %v", err)
			}
			for _, value := range values {
				document := value.Document()
				sourcePairs = append(sourcePairs, document.Lookup("dataset").StringValue()+"/"+document.Lookup("generation").StringValue())
			}
		}
		wantPairs := []string{"static-a/generation-a", "static-b/generation-b"}
		sort.Strings(sourcePairs)
		if !reflect.DeepEqual(sourcePairs, wantPairs) {
			mt.Fatalf("queried generations = %v, want %v", sourcePairs, wantPairs)
		}
	})
}
