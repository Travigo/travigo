package datalinker

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/database"

	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
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
	datasetFilter := bson.M{"datasource.datasetid": "dataset-a"}
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

func TestStopServicesWriterRecoveryRequiresExplicitAcknowledgement(t *testing.T) {
	run := func(args ...string) error {
		app := cli.NewApp()
		app.Commands = []*cli.Command{RegisterCLI()}
		return app.Run(append([]string{"travigo", "data-linker", "stop-services", "recover-writers"}, args...))
	}

	if err := run(); err == nil || !strings.Contains(err.Error(), "confirm-no-publishers") {
		t.Fatalf("error = %v, expected required no-publishers acknowledgement", err)
	}
	if err := run("--confirm-no-publishers=false"); err == nil || !strings.Contains(err.Error(), "must be true") {
		t.Fatalf("error = %v, expected explicit true acknowledgement", err)
	}
}

func TestJourneyPublishDoesNotRunWhenStopServicesMarkerCannotBegin(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("begin marker failure prevents journey mutation", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 2, Message: "state unavailable"}))
		publishCalled := false
		err := runJourneySourceWrite(t.Context(), func() error {
			publishCalled = true
			return nil
		}, "dataset-a")
		if err == nil || !strings.Contains(err.Error(), "begin stop-services source write") {
			mt.Fatalf("error = %v, expected source marker begin failure", err)
		}
		if publishCalled {
			mt.Fatal("journey mutation ran without a durable source marker")
		}
		if events := mt.GetAllStartedEvents(); len(events) != 1 || events[0].CommandName != "update" {
			mt.Fatalf("MongoDB commands = %#v, expected only the failed state update", events)
		}
	})
}

func TestJourneyPublishingResumesAfterDatasetCleanupFailure(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("retry without a previous publish record", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		importedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		replyVersions := func(versions ...bson.D) {
			namespace := mt.DB.Name() + ".dataset_versions"
			mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, versions...))
		}
		replySourceWriteStart := func() {
			mt.AddMockResponses(
				mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}), // ensure state document
				mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}), // register dataset
				mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}), // mark source write active
			)
		}
		replySourceWriteFinish := func() {
			mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}))
		}
		replyMerge := func() {
			mt.AddMockResponses(
				mtest.CreateSuccessResponse(bson.E{Key: "values", Value: bson.A{"100"}}),
				mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys_raw", mtest.FirstBatch),
			)
		}
		replyCleanup := func() {
			mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 3}))
		}
		replyCheckpoint := func() {
			mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}))
		}
		checkCheckpoint := func(dataset string) {
			mt.Helper()
			for _, event := range mt.GetAllStartedEvents() {
				if event.CommandName != "update" {
					continue
				}
				updates, err := event.Command.Lookup("updates").Array().Values()
				if err != nil || len(updates) != 1 {
					mt.Fatalf("expected one checkpoint update, got %v, %v", updates, err)
				}
				update := updates[0].Document()
				gotDataset, ok := update.Lookup("q", "dataset").StringValueOK()
				if !ok || gotDataset == "" {
					continue
				}
				if gotDataset != dataset {
					mt.Fatalf("checkpointed %s, expected %s", gotDataset, dataset)
				}
				if got := update.Lookup("u", "$max", "publishedjourneysversion").Time(); !got.Equal(importedAt) {
					mt.Fatalf("checkpointed version %s, expected %s", got, importedAt)
				}
			}
		}
		checkCommands := func(want ...string) {
			mt.Helper()
			var got []string
			for _, event := range mt.GetAllStartedEvents() {
				got = append(got, event.CommandName)
			}
			if !reflect.DeepEqual(got, want) {
				mt.Fatalf("MongoDB commands = %v, expected %v", got, want)
			}
		}

		olderImport := importedAt.Add(-72 * time.Hour)
		enriched := func(checkpoint time.Time) bson.D {
			return bson.D{
				{Key: "dataset", Value: "a"},
				{Key: "lastmodified", Value: olderImport},
				{Key: "rawjourneysmodified", Value: importedAt},
				{Key: "publishedjourneysversion", Value: checkpoint},
			}
		}
		second := bson.D{{Key: "dataset", Value: "b"}, {Key: "lastmodified", Value: importedAt}}
		replyVersions(second, enriched(olderImport))
		replySourceWriteStart()
		replyMerge()
		replyCleanup()
		replySourceWriteFinish()
		replyCheckpoint()
		replySourceWriteStart()
		replyMerge()
		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 2, Message: "cleanup failed"}))
		replySourceWriteFinish()

		err := publishUpdatedDatasets(t.Context(), "journeys_raw", "journeys", "linker-journeys")
		if err == nil || !strings.Contains(err.Error(), "publish journey dataset b: delete stale journeys") {
			mt.Fatalf("expected cleanup failure for b, got %v", err)
		}
		checkCommands("find", "update", "update", "update", "distinct", "aggregate", "delete", "update", "update", "update", "update", "update", "distinct", "aggregate", "delete", "update")
		checkCheckpoint("a")

		mt.ClearEvents()
		replyVersions(enriched(importedAt), second)
		replySourceWriteStart()
		replyMerge()
		replyCleanup()
		replySourceWriteFinish()
		replyCheckpoint()

		err = publishUpdatedDatasets(t.Context(), "journeys_raw", "journeys", "linker-journeys")
		if err != nil {
			mt.Fatal(err)
		}
		checkCommands("find", "update", "update", "update", "distinct", "aggregate", "delete", "update", "update")
		checkCheckpoint("b")
	})
}

func TestJourneyPublishDoesNotCheckpointWhenStopServicesMarkerFinishFails(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("marker finish failure remains retryable", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		importedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".dataset_versions", mtest.FirstBatch,
				bson.D{{Key: "dataset", Value: "dataset-a"}, {Key: "lastmodified", Value: importedAt}},
			),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "values", Value: bson.A{"100"}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys_raw", mtest.FirstBatch),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
			mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 2, Message: "marker finish failed"}),
		)

		err := publishUpdatedDatasets(t.Context(), "journeys_raw", "journeys", "linker-journeys")
		if err == nil || !strings.Contains(err.Error(), "finish stop-services source write") {
			mt.Fatalf("error = %v, expected stop-services marker finish failure", err)
		}

		var commands []string
		for _, event := range mt.GetAllStartedEvents() {
			commands = append(commands, event.CommandName)
			if event.CommandName != "update" {
				continue
			}
			updates, err := event.Command.Lookup("updates").Array().Values()
			if err != nil || len(updates) != 1 {
				mt.Fatalf("expected one update command operation, got %v, %v", updates, err)
			}
			update := updates[0].Document()
			if datasetID, ok := update.Lookup("q", "dataset").StringValueOK(); ok && datasetID == "dataset-a" {
				mt.Fatal("journey dataset was checkpointed after source marker finish failed")
			}
		}
		wantCommands := []string{"find", "update", "update", "update", "distinct", "aggregate", "delete", "update"}
		if !reflect.DeepEqual(commands, wantCommands) {
			mt.Fatalf("MongoDB commands = %v, expected %v", commands, wantCommands)
		}
	})
}
