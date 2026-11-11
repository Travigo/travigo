package datalinker

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestBuildNearbyWalkTransfersCapsNearestPerStop(t *testing.T) {
	config := StopTransferBuildConfig{
		MaxDistanceMetres:         100,
		WalkSpeedMetresPerSec:     1,
		MaxNearbyTransfersPerStop: 2,
	}
	stops := []*transferStop{
		testTransferStop(0, "A", 51.5, 0),
		testTransferStop(1, "B", 51.5+metresToLatitudeDegrees(10), 0),
		testTransferStop(2, "C", 51.5+metresToLatitudeDegrees(20), 0),
		testTransferStop(3, "D", 51.5+metresToLatitudeDegrees(30), 0),
	}
	transfers := map[transferKey]transferCandidate{}

	buildNearbyWalkTransfers(stops, transfers, config)

	assertTransferType(t, transfers, "A", "B", ctdf.StopTransferTypeNearbyWalk)
	assertTransferType(t, transfers, "A", "C", ctdf.StopTransferTypeNearbyWalk)
	assertNoTransfer(t, transfers, "A", "D")
}

func TestBuildNearbyWalkTransfersPreservesExistingTransferType(t *testing.T) {
	config := StopTransferBuildConfig{
		MaxDistanceMetres:         100,
		WalkSpeedMetresPerSec:     1,
		MaxNearbyTransfersPerStop: 24,
	}
	stops := []*transferStop{
		testTransferStop(0, "A", 51.5, 0),
		testTransferStop(1, "B", 51.5+metresToLatitudeDegrees(10), 0),
	}
	transfers := map[transferKey]transferCandidate{}
	distance := roundedStopDistanceMetres(stops[0], stops[1])
	addTransfer(transfers, "A", "B", ctdf.StopTransferTypeSameStopGroup, distance, config.MaxDistanceMetres, config)
	addTransfer(transfers, "B", "A", ctdf.StopTransferTypeSameStopGroup, distance, config.MaxDistanceMetres, config)

	buildNearbyWalkTransfers(stops, transfers, config)

	assertTransferType(t, transfers, "A", "B", ctdf.StopTransferTypeSameStopGroup)
	assertTransferType(t, transfers, "B", "A", ctdf.StopTransferTypeSameStopGroup)
}

func TestStopTransferWorkerCountUsesAtMostHalfCPUs(t *testing.T) {
	tests := map[int]int{
		0:  1,
		1:  1,
		2:  1,
		3:  1,
		4:  2,
		8:  4,
		16: 8,
	}

	for cpuCount, expectedWorkers := range tests {
		if workers := stopTransferWorkerCount(cpuCount); workers != expectedWorkers {
			t.Fatalf("expected %d workers for %d CPUs, got %d", expectedWorkers, cpuCount, workers)
		}
	}
}

func TestStaleStopTransferDeletesAreBatched(t *testing.T) {
	changedStopIDs := make(map[string]struct{}, stopTransferStaleDeleteBatchSize+1)
	for i := 0; i <= stopTransferStaleDeleteBatchSize; i++ {
		changedStopIDs[fmt.Sprintf("stop-%05d", i)] = struct{}{}
	}

	batches := staleStopTransferDeleteBatches(changedStopIDs)
	if len(batches) != 2 {
		t.Fatalf("expected 2 stale-delete batches, got %d", len(batches))
	}
	if len(batches[0]) != stopTransferStaleDeleteBatchSize {
		t.Fatalf("first batch contains %d stop IDs, want %d", len(batches[0]), stopTransferStaleDeleteBatchSize)
	}
	if len(batches[1]) != 1 {
		t.Fatalf("second batch contains %d stop IDs, want 1", len(batches[1]))
	}
	if batches[0][0] != "stop-00000" || batches[1][0] != fmt.Sprintf("stop-%05d", stopTransferStaleDeleteBatchSize) {
		t.Fatalf("stale-delete batches are not deterministic: first=%q last=%q", batches[0][0], batches[1][0])
	}
}

func TestStopTransferRetirementRebuild(t *testing.T) {
	requestedAt := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, linked := range []bool{false, true} {
		mt.Run(fmt.Sprintf("relinked=%t", linked), func(mt *mtest.T) {
			previousDB := database.Instance
			database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
			mt.Cleanup(func() { database.Instance = previousDB })

			linkedAt := requestedAt.Add(-time.Minute)
			if linked {
				linkedAt = requestedAt.Add(time.Minute)
			}
			namespace := mt.DB.Name() + ".dataset_versions"
			mt.AddMockResponses(
				mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, bson.D{{Key: "rebuildafter", Value: requestedAt}}),
				mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, bson.D{{Key: "lastmodified", Value: linkedAt}}),
			)
			if linked {
				success := mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1})
				stop := func(id string, latitude float64) bson.D {
					return bson.D{
						{Key: "primaryidentifier", Value: id},
						{Key: "location", Value: bson.M{"type": "Point", "coordinates": bson.A{0.0, latitude}}},
					}
				}
				mt.AddMockResponses(
					mtest.CreateCursorResponse(0, mt.DB.Name()+".stops", mtest.FirstBatch,
						stop("retained-a", 51.5), stop("retained-b", 51.5001)),
					success, success, success,
				)
			}
			err := BuildStopTransfers(StopTransferBuildConfig{})
			events := mt.GetAllStartedEvents()
			if !linked {
				if err == nil || len(events) != 2 {
					mt.Fatalf("expected rebuild to wait for relinking, got %v and %d commands", err, len(events))
				}
				return
			}
			if err != nil {
				mt.Fatal(err)
			}
			if len(events) != 6 || events[3].CommandName != "update" || events[4].CommandName != "delete" || events[5].CommandName != "update" {
				mt.Fatalf("expected transfer upsert, stale-transfer delete, then checkpoint, got %v", events)
			}
			transfer := events[3].Command.Lookup("updates", "0", "u").Document()
			filter := events[4].Command.Lookup("deletes", "0", "q").Document()
			if filter.Lookup("datasource.originalformat").StringValue() != stopTransferGeneratorFormat ||
				filter.Lookup("modificationdatetime", "$lt").DateTime() != transfer.Lookup("modificationdatetime").DateTime() ||
				filter.Lookup("$or").Type != 0 {
				mt.Fatalf("expected age-based pruning of all generated transfers, got %s", filter)
			}
			checkpoint := events[5].Command.Lookup("updates", "0").Document()
			if checkpoint.Lookup("q", "dataset").StringValue() != datasetversion.StopTransfersDataset ||
				!checkpoint.Lookup("q", "rebuildafter").Time().Equal(requestedAt) ||
				checkpoint.Lookup("u", "$unset", "rebuildafter").Type == 0 {
				mt.Fatalf("expected checkpoint for the completed request, got %s", checkpoint)
			}
		})
	}
}

func testTransferStop(index int, ref string, latitude float64, longitude float64) *transferStop {
	return &transferStop{
		PrimaryIdentifier: ref,
		Index:             index,
		Location: &ctdf.Location{
			Type:        "Point",
			Coordinates: []float64{longitude, latitude},
		},
		Latitude:    latitude,
		Longitude:   longitude,
		CosLatitude: math.Cos(latitude * math.Pi / 180),
	}
}

func metresToLatitudeDegrees(metres float64) float64 {
	return metres / metresPerDegree
}

func assertTransferType(t *testing.T, transfers map[transferKey]transferCandidate, from string, to string, transferType ctdf.StopTransferType) {
	t.Helper()

	transfer, ok := transfers[transferKey{from: from, to: to}]
	if !ok {
		t.Fatalf("expected transfer from %s to %s", from, to)
	}
	if transfer.transferType != transferType {
		t.Fatalf("expected transfer from %s to %s to have type %s, got %s", from, to, transferType, transfer.transferType)
	}
}

func assertNoTransfer(t *testing.T, transfers map[transferKey]transferCandidate, from string, to string) {
	t.Helper()

	if _, ok := transfers[transferKey{from: from, to: to}]; ok {
		t.Fatalf("expected no transfer from %s to %s", from, to)
	}
}
