package datalinker

import (
	"context"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

// These tests exercise the production incremental pipeline through its normal
// Mongo read path. A seeded checkpoint makes the cases incremental, rather
// than testing helper predicates in isolation.
func TestBuildIncrementalTransfersNoopAndStaleServices(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("unchanged inputs do not write", func(mt *mtest.T) {
		withTransferMockDatabase(mt, func(db *mongo.Database) {
			config := StopTransferBuildConfig{MaxDistanceMetres: 100, MinChangeSeconds: 60, WalkSpeedMetresPerSec: 1.3, BatchSize: 100, MaxNearbyTransfersPerStop: 1, SameStopGroupMultiplier: 1}.withDefaults()
			checkpoint := transferBuildCheckpoint{Rules: transferRules(config), Linked: "r1", ServiceRevision: 7}
			mt.AddMockResponses(
				findOneResponse(mt, "dataset_versions", checkpoint),
				findOneResponse(mt, "dataset_versions", bson.M{"dataset": datasetversion.StopTransfersDataset, "lastmodified": time.Now()}),
				findOneResponse(mt, "linked_stop_change_state", bson.M{"_id": linkedStopChangeStateID, "revision": "r1", "building": false, "livetoken": "live-1"}),
				findOneResponse(mt, "stops", bson.M{"_linkerpublicationtoken": "live-1"}),
				findOneResponse(mt, "stop_service_state", currentServiceState(7)),
				mtest.CreateCursorResponse(0, mt.DB.Name()+"."+transferDirtyCollection, mtest.FirstBatch),
			)
			lease := &transferBuildLease{db: db, token: "test-token"}
			if err := buildIncrementalTransfers(context.Background(), lease, config, nil); err != nil {
				mt.Fatalf("buildIncrementalTransfers: %v", err)
			}
			assertNoPipelineWrites(mt)
		})
	})
	mt.Run("stale service view defers checkpoint", func(mt *mtest.T) {
		withTransferMockDatabase(mt, func(db *mongo.Database) {
			config := StopTransferBuildConfig{MaxDistanceMetres: 100, MinChangeSeconds: 60, WalkSpeedMetresPerSec: 1.3, BatchSize: 100, MaxNearbyTransfersPerStop: 1, SameStopGroupMultiplier: 1}.withDefaults()
			checkpoint := transferBuildCheckpoint{Rules: transferRules(config), Linked: "r1", ServiceRevision: 7}
			mt.AddMockResponses(
				findOneResponse(mt, "dataset_versions", checkpoint),
				findOneResponse(mt, "dataset_versions", bson.M{"dataset": datasetversion.StopTransfersDataset, "lastmodified": time.Now()}),
				findOneResponse(mt, "linked_stop_change_state", bson.M{"_id": linkedStopChangeStateID, "revision": "r1", "building": false, "livetoken": "live-1"}),
				findOneResponse(mt, "stops", bson.M{"_linkerpublicationtoken": "live-1"}),
				findOneResponse(mt, "stop_service_state", bson.M{"_id": "static", "initialized": true, "eventRevision": int64(8), "fullRevision": int64(8), "fullPublishedRevision": int64(7), "fullActiveWriters": int64(0), "sources": bson.M{}}),
			)
			lease := &transferBuildLease{db: db, token: "test-token"}
			if err := buildIncrementalTransfers(context.Background(), lease, config, nil); err != nil {
				mt.Fatalf("stale service view should defer without error: %v", err)
			}
			assertNoPipelineWrites(mt)
		})
	})
}

func withTransferMockDatabase(mt *mtest.T, run func(*mongo.Database)) {
	previous := database.Instance
	database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
	defer func() { database.Instance = previous }()
	run(mt.DB)
}

func findOneResponse(mt *mtest.T, collection string, doc interface{}) bson.D {
	encoded, err := bson.Marshal(doc)
	if err != nil {
		mt.Fatal(err)
	}
	var document bson.D
	if err := bson.Unmarshal(encoded, &document); err != nil {
		mt.Fatal(err)
	}
	return mtest.CreateCursorResponse(0, mt.DB.Name()+"."+collection, mtest.FirstBatch, document)
}

func currentServiceState(revision int64) bson.M {
	return bson.M{"_id": "static", "initialized": true, "eventRevision": revision, "fullRevision": revision, "fullPublishedRevision": revision, "fullActiveWriters": int64(0), "sources": bson.M{}, "retired": bson.A{}}
}

func assertNoPipelineWrites(mt *mtest.T) {
	mt.Helper()
	for _, event := range mt.GetAllStartedEvents() {
		switch event.CommandName {
		case "insert", "update", "delete", "findAndModify", "bulkWrite":
			mt.Fatalf("no-op/deferred build issued Mongo write command %q: %s", event.CommandName, event.Command)
		}
	}
}
