package stopservices

import (
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestNormalizeBuildOptions(t *testing.T) {
	if got := normalizeBuildOptions(BuildOptions{}).BatchSize; got != DefaultBatchSize {
		t.Fatalf("default batch size = %d, want %d", got, DefaultBatchSize)
	}
	if got := normalizeBuildOptions(BuildOptions{BatchSize: 17}).BatchSize; got != 17 {
		t.Fatalf("configured batch size = %d, want 17", got)
	}
}

func TestStopServiceAggregationGroupsRawRelationsAndExcludesDynamicDatasets(t *testing.T) {
	pipeline := stopServiceAggregation("")
	if len(pipeline) != 5 {
		t.Fatalf("pipeline has %d stages, want 5", len(pipeline))
	}
	match, ok := pipeline[0][0].Value.(bson.M)
	if !ok {
		t.Fatalf("first stage filter has type %T, want bson.M", pipeline[0][0].Value)
	}
	datasetFilter, ok := match["datasource.datasetid"].(bson.M)
	if !ok {
		t.Fatalf("dataset filter has type %T, want bson.M", match["datasource.datasetid"])
	}
	if got := datasetFilter["$nin"]; !reflect.DeepEqual(got, bson.A{
		"gb-networkrail-vstp", "gb-nationalrail-darwin-amendment",
	}) {
		t.Fatalf("dynamic exclusion = %#v", got)
	}
	group, ok := pipeline[4][0].Value.(bson.M)
	if !ok {
		t.Fatalf("group stage has type %T, want bson.M", pipeline[4][0].Value)
	}
	groupKey, ok := group["_id"].(bson.M)
	if !ok {
		t.Fatalf("group key has type %T, want bson.M", group["_id"])
	}
	want := bson.M{
		"dataset": "$dataset", "stopref": "$stopRef", "serviceref": "$serviceRef",
	}
	if !reflect.DeepEqual(groupKey, want) {
		t.Fatalf("group key = %#v, want %#v", groupKey, want)
	}
	projectedStopRef, ok := pipeline[2][0].Value.(bson.M)["stopRef"]
	if !ok || projectedStopRef != "$calls.stopref" {
		t.Fatalf("projected stop ref = %#v", projectedStopRef)
	}
}

func TestStopServiceDatasetAggregationLimitsInput(t *testing.T) {
	pipeline := stopServiceAggregation("feed-a")
	match := pipeline[0][0].Value.(bson.M)
	if got := match["datasource.datasetid"]; got != "feed-a" {
		t.Fatalf("dataset selector = %#v, want feed-a", got)
	}
}

func TestFullBuildDiscoveryFenceDetectsWritesDuringDatasetListing(t *testing.T) {
	before := stateDocument{
		EventRevision:   7,
		FullRevision:    3,
		CatalogRevision: 2,
		Sources: map[string]datasetSourceState{
			sourceKey("existing"): {Dataset: "existing"},
		},
	}
	discovered := []string{"existing", "new-feed", "gb-networkrail-vstp"}
	registrations := newStaticDatasetRegistrations(before, discovered)
	if registrations != 1 {
		t.Fatalf("new registrations = %d, want 1", registrations)
	}

	afterRegistration := before
	afterRegistration.EventRevision++
	afterRegistration.CatalogRevision++
	if !stableAfterDiscovery(before, afterRegistration, registrations) {
		t.Fatal("expected only this build's new source registration to be stable")
	}

	// A scoped writer may start and finish after Distinct returns but before the
	// builder captures its source state. Its two revisions must prevent the
	// manifest from publishing a dataset list based on the earlier read.
	afterScopedWrite := afterRegistration
	afterScopedWrite.EventRevision += 2
	if stableAfterDiscovery(before, afterScopedWrite, registrations) {
		t.Fatal("scoped publish during discovery must invalidate the full build")
	}

	afterFullWrite := afterRegistration
	afterFullWrite.EventRevision += 2
	afterFullWrite.FullRevision += 2
	if stableAfterDiscovery(before, afterFullWrite, registrations) {
		t.Fatal("full publish during discovery must invalidate the full build")
	}
}

func TestWriteGenerationUsesBoundedBulkBatches(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("groups are inserted in configured batches", func(mt *mtest.T) {
		group := func(stop, service string) bson.D {
			return bson.D{{Key: "_id", Value: bson.D{
				{Key: "dataset", Value: "feed-a"},
				{Key: "stopref", Value: stop},
				{Key: "serviceref", Value: service},
			}}}
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch,
				group("stop-a", "service-a"),
				group("stop-b", "service-a"),
				group("stop-c", "service-b"),
			),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 2}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
		)

		count, err := writeGeneration(mt.Context(), mt.DB, "generation-1", "feed-a", 2)
		if err != nil {
			mt.Fatal(err)
		}
		if count != 3 {
			mt.Fatalf("pair count = %d, want 3", count)
		}

		var insertSizes []int
		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName != "insert" || event.Command.Lookup("insert").StringValue() != RefsCollectionName {
				continue
			}
			documents, err := event.Command.Lookup("documents").Array().Values()
			if err != nil {
				mt.Fatalf("read insert documents: %v", err)
			}
			insertSizes = append(insertSizes, len(documents))
		}
		if want := []int{2, 1}; !reflect.DeepEqual(insertSizes, want) {
			mt.Fatalf("insert batch sizes = %v, want %v", insertSizes, want)
		}
	})
}

func TestWriteGenerationRejectsUnownedStaticRows(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("missing dataset ID fails before any reference write", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch,
			bson.D{{Key: "_id", Value: bson.D{
				{Key: "stopref", Value: "stop-a"},
				{Key: "serviceref", Value: "service-a"},
			}}},
		))
		_, err := writeGeneration(mt.Context(), mt.DB, "generation-1", "", 100)
		if err == nil || errors.Is(err, ErrUnbuilt) {
			mt.Fatalf("expected missing datasource error, got %v", err)
		}
		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName == "insert" {
				mt.Fatal("wrote a stop-service relation before validating the source dataset")
			}
		}
	})
}
