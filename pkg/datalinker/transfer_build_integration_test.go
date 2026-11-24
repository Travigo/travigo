package datalinker

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// These tests need a replica set because transfer output, checkpoints, and
// dirty-origin cleanup are committed in fenced Mongo transactions.
func TestBuildIncrementalTransfersMongoRecovery(t *testing.T) {
	uri := os.Getenv("TRAVIGO_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set TRAVIGO_TEST_MONGODB_URI to run isolated MongoDB integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("travigo_transfer_test_" + primitive.NewObjectID().Hex())
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		t.Fatal(err)
	}
	if hello.SetName == "" && hello.Msg != "isdbgrid" {
		_ = client.Disconnect(context.Background())
		t.Skip("TRAVIGO_TEST_MONGODB_URI must point to a replica set or mongos")
	}
	previousDB := database.Instance
	database.Instance = &database.MongoInstance{Client: client, Database: db}
	t.Cleanup(func() {
		database.Instance = previousDB
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := db.Drop(cleanupCtx); err != nil {
			t.Errorf("drop isolated transfer test database: %v", err)
		}
		_ = client.Disconnect(cleanupCtx)
	})

	// An initialized empty stop-service view means the optional static-service
	// filter is disabled. No datasource YAML or global dataset manager is used.
	_, err = db.Collection(stopservices.StateCollectionName).InsertOne(ctx, bson.M{
		"_id": "static", "initialized": true, "eventRevision": int64(0), "fullRevision": int64(0),
		"fullPublishedRevision": int64(0), "fullActiveWriters": int64(0), "catalogRevision": int64(0), "sources": bson.M{}, "retired": bson.A{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTransferLocalityIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("linked_stop_change_state").InsertOne(ctx, bson.M{"_id": linkedStopChangeStateID, "building": false, "revision": "r1", "livetoken": "t-r1"}); err != nil {
		t.Fatal(err)
	}
	for _, stop := range []ctdf.Stop{integrationStop("A", 0), integrationStop("B", metresForLongitude(10, 51)), integrationStop("C", metresForLongitude(20, 51))} {
		if _, err := db.Collection("stops").InsertOne(ctx, stop); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Collection("stops").UpdateMany(ctx, bson.M{}, bson.M{"$set": bson.M{"_linkerpublicationtoken": "t-r1"}}); err != nil {
		t.Fatal(err)
	}
	legacy := bson.M{"primaryidentifier": "legacy-orphan", "fromstopref": "gone", "tostopref": "ghost", "type": ctdf.StopTransferTypeNearbyWalk,
		"datasource": bson.M{"originalformat": stopTransferGeneratorFormat}}
	if _, err := db.Collection("stop_transfers").InsertOne(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	config := StopTransferBuildConfig{MaxDistanceMetres: 100, MinChangeSeconds: 60, WalkSpeedMetresPerSec: 1.3, BatchSize: 100, MaxNearbyTransfersPerStop: 1, SameStopGroupMultiplier: 1}
	build := func() {
		t.Helper()
		lease, err := acquireTransferBuild(ctx)
		if err != nil {
			t.Fatal(err)
		}
		buildErr := buildIncrementalTransfers(ctx, lease, config.withDefaults(), nil)
		releaseErr := lease.Release(ctx)
		if buildErr != nil {
			t.Fatalf("build incremental transfers: %v", buildErr)
		}
		if releaseErr != nil {
			t.Fatalf("release transfer build lease: %v", releaseErr)
		}
	}
	assertEdge := func(from, to string, exists bool) {
		t.Helper()
		count, err := db.Collection("stop_transfers").CountDocuments(ctx, bson.M{"fromstopref": from, "tostopref": to, "datasource.originalformat": stopTransferGeneratorFormat})
		if err != nil {
			t.Fatal(err)
		}
		if (count > 0) != exists {
			t.Fatalf("generated edge %s -> %s exists=%t, want %t", from, to, count > 0, exists)
		}
	}

	build() // Initial snapshot, including cleanup of an orphan legacy edge.
	assertEdge("gone", "ghost", false)
	assertEdge("A", "B", true)
	assertEdge("A", "C", false) // nearest-one rule
	var before ctdf.StopTransfer
	if err := db.Collection("stop_transfers").FindOne(ctx, bson.M{"fromstopref": "A", "tostopref": "B", "datasource.originalformat": stopTransferGeneratorFormat}).Decode(&before); err != nil {
		t.Fatal(err)
	}
	build() // No source or config changes should leave output untouched.
	var unchanged ctdf.StopTransfer
	if err := db.Collection("stop_transfers").FindOne(ctx, bson.M{"fromstopref": "A", "tostopref": "B", "datasource.originalformat": stopTransferGeneratorFormat}).Decode(&unchanged); err != nil {
		t.Fatal(err)
	}
	if !before.ModificationDateTime.Equal(unchanged.ModificationDateTime) {
		t.Fatal("unchanged incremental build rewrote an edge")
	}

	// Moving B makes C the replacement nearest neighbor of A.
	if _, err := db.Collection("stops").UpdateOne(ctx, bson.M{"primaryidentifier": "B"}, bson.M{"$set": bson.M{"location": ctdf.Location{Type: "Point", Coordinates: []float64{metresForLongitude(1000, 51), 51}}}}); err != nil {
		t.Fatal(err)
	}
	publishStopChange(t, ctx, db, "r1", "r2", []string{"B"}, false)
	build()
	assertEdge("A", "B", false)
	assertEdge("A", "C", true)

	// A retirement requests a rebuild only after the stop linker publishes a
	// newer version; its full-scan journal then removes the retired stop.
	if _, err := db.Collection("stops").DeleteOne(ctx, bson.M{"primaryidentifier": "C"}); err != nil {
		t.Fatal(err)
	}
	if err := requestStopTransferRebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if err := datasetversion.Upsert(ctx, ctdf.DatasetVersion{Dataset: datasetversion.LinkerDataset("stop"), LastModified: time.Now().Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	publishStopChange(t, ctx, db, "r2", "r3", nil, true)
	build()
	assertEdge("A", "C", false)
	var checkpoint transferBuildCheckpoint
	if err := db.Collection("dataset_versions").FindOne(ctx, bson.M{"dataset": datasetversion.StopTransfersDataset}).Decode(&checkpoint); err != nil {
		t.Fatal(err)
	}
	if !checkpoint.RebuildAfter.IsZero() {
		t.Fatal("retirement rebuild request was not consumed")
	}

	// Expanding and then shrinking the radius must update an existing
	// outgoing edge even though the input revisions have stayed constant.
	config.MaxDistanceMetres = 1100
	build()
	assertEdge("A", "B", true)
	config.MaxDistanceMetres = 15
	build()
	assertEdge("A", "B", false)

	// Simulate a crashed writer after persisting its dirty-origin marker and a
	// partial output write. The next build must replay it and clear the marker.
	if _, err := db.Collection(transferDirtyCollection).InsertOne(ctx, bson.M{"_id": "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("stop_transfers").InsertOne(ctx, bson.M{"primaryidentifier": "partial-a-ghost", "fromstopref": "A", "tostopref": "ghost", "type": ctdf.StopTransferTypeNearbyWalk, "datasource": bson.M{"originalformat": stopTransferGeneratorFormat}}); err != nil {
		t.Fatal(err)
	}
	build()
	assertEdge("A", "ghost", false)
	if err := db.Collection(transferDirtyCollection).FindOne(ctx, bson.M{"_id": "A"}).Err(); err != mongo.ErrNoDocuments {
		t.Fatalf("dirty origin survived recovery: %v", err)
	}
}

func integrationStop(id string, longitude float64) ctdf.Stop {
	return ctdf.Stop{PrimaryIdentifier: id, Active: true, Location: &ctdf.Location{Type: "Point", Coordinates: []float64{longitude, 51}}}
}

func metresForLongitude(metres, latitude float64) float64 {
	return metres / (111320 * math.Cos(latitude*math.Pi/180))
}

func publishStopChange(t *testing.T, ctx context.Context, db *mongo.Database, previous, next string, ids []string, full bool) {
	t.Helper()
	if _, err := db.Collection(linkedStopChangeHistory).InsertOne(ctx, linkedStopChangeRecord{ID: next, Previous: previous, IDs: ids, FullScan: full}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("stops").UpdateMany(ctx, bson.M{}, bson.M{"$set": bson.M{"_linkerpublicationtoken": "t-" + next}}); err != nil {
		t.Fatal(err)
	}
	result, err := db.Collection("linked_stop_change_state").UpdateOne(ctx, bson.M{"_id": linkedStopChangeStateID}, bson.M{"$set": bson.M{"revision": next, "building": false, "livetoken": "t-" + next}})
	if err != nil {
		t.Fatal(err)
	}
	if result.MatchedCount != 1 {
		t.Fatalf("publish stop change revision %s: no state document", next)
	}
}
