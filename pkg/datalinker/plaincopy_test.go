package datalinker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func TestPlainCopyLinkerUsesRawStagingAndLiveCollections(t *testing.T) {
	linker := NewPlainCopyLinker("journey")
	live, raw, staging := linker.collectionNames()

	if live != "journeys" || raw != "journeys_raw" || staging != "journeys_staging" {
		t.Fatalf("collections = %q, %q, %q", live, raw, staging)
	}
}

func TestStaleDatasetRecordsFilterKeepsCurrentRawTimestamps(t *testing.T) {
	filter := staleDatasetRecordsFilter("dataset-a", []string{"100", "101"})
	expected := bson.M{
		"datasource.datasetid": "dataset-a",
		"datasource.timestamp": bson.M{"$nin": []string{"100", "101"}},
	}

	if !reflect.DeepEqual(filter, expected) {
		t.Fatalf("filter = %#v, expected %#v", filter, expected)
	}
}

func TestStaleDatasetRecordsFilterDeletesDatasetWhenRawIsEmpty(t *testing.T) {
	filter := staleDatasetRecordsFilter("dataset-a", nil)
	expected := bson.M{"datasource.datasetid": "dataset-a"}

	if !reflect.DeepEqual(filter, expected) {
		t.Fatalf("filter = %#v, expected %#v", filter, expected)
	}
}

func TestIncrementalMergePipelineDoesNotReplaceTheLiveCollection(t *testing.T) {
	datasetFilter := bson.M{"datasource.datasetid": bson.M{"$in": []string{"dataset-a"}}}
	pipeline := incrementalMergePipeline(datasetFilter, "journeys")
	if len(pipeline) != 2 {
		t.Fatalf("pipeline has %d stages, expected 2", len(pipeline))
	}
	if !reflect.DeepEqual(pipeline[0], bson.D{{Key: "$match", Value: datasetFilter}}) {
		t.Fatalf("match stage = %#v", pipeline[0])
	}

	mergeStage, ok := pipeline[1].Map()["$merge"].(bson.D)
	if !ok {
		t.Fatalf("final stage is not $merge: %#v", pipeline[1])
	}
	mergeOptions := mergeStage.Map()
	if mergeOptions["into"] != "journeys" || mergeOptions["on"] != "_id" || mergeOptions["whenMatched"] != "replace" || mergeOptions["whenNotMatched"] != "insert" {
		t.Fatalf("merge options = %#v", mergeOptions)
	}
}

func TestSkipStagingIsRejectedForOtherLinkers(t *testing.T) {
	app := cli.NewApp()
	app.Commands = []*cli.Command{RegisterCLI()}

	err := app.Run([]string{"travigo", "data-linker", "run", "--type", "services", "--skip-staging"})
	if err == nil || !strings.Contains(err.Error(), "only supported for journeys") {
		t.Fatalf("error = %v, expected journeys-only validation", err)
	}
}
