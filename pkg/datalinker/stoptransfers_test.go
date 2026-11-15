package datalinker

import (
	"context"
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

	buildNearbyWalkTransfers(stops, transfers, config, false)

	assertTransferType(t, transfers, "A", "B", ctdf.StopTransferTypeNearbyWalk)
	assertTransferType(t, transfers, "B", "A", ctdf.StopTransferTypeNearbyWalk)
	assertTransferType(t, transfers, "A", "C", ctdf.StopTransferTypeNearbyWalk)
	assertNoTransfer(t, transfers, "A", "D")
}

func TestBuildSameStopGroupTransfersKeepsBothDirections(t *testing.T) {
	config := StopTransferBuildConfig{
		MaxDistanceMetres:     100,
		WalkSpeedMetresPerSec: 1,
	}
	from := testTransferStop(0, "from", 51.5, 0)
	to := testTransferStop(1, "to", 51.5+metresToLatitudeDegrees(10), 0)
	from.Associations = []*ctdf.Association{{Type: "stop_group", AssociatedIdentifier: "group"}}
	to.Associations = []*ctdf.Association{{Type: "stop_group", AssociatedIdentifier: "group"}}
	transfers := map[transferKey]transferCandidate{}

	buildSameStopGroupTransfers([]*transferStop{from, to}, transfers, config)

	assertTransferType(t, transfers, "from", "to", ctdf.StopTransferTypeSameStopGroup)
	assertTransferType(t, transfers, "to", "from", ctdf.StopTransferTypeSameStopGroup)
}

func TestBuildPlatformAliasTransfersKeepsBothDirections(t *testing.T) {
	config := StopTransferBuildConfig{WalkSpeedMetresPerSec: 1}
	parent := testTransferStop(0, "parent", 51.5, 0)
	platform := testTransferStop(1, "platform", 51.5+metresToLatitudeDegrees(10), 0)
	parent.Platforms = []*ctdf.StopPlatform{{PrimaryIdentifier: platform.PrimaryIdentifier}}
	transfers := map[transferKey]transferCandidate{}

	buildPlatformAliasTransfers([]*transferStop{parent, platform}, transfers, config)

	assertTransferType(t, transfers, "parent", "platform", ctdf.StopTransferTypePlatformAlias)
	assertTransferType(t, transfers, "platform", "parent", ctdf.StopTransferTypePlatformAlias)
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

	buildNearbyWalkTransfers(stops, transfers, config, false)

	assertTransferType(t, transfers, "A", "B", ctdf.StopTransferTypeSameStopGroup)
	assertTransferType(t, transfers, "B", "A", ctdf.StopTransferTypeSameStopGroup)
}

func TestBuildNearbyWalkTransfersFiltersUnservedStopsButKeepsEquivalenceLinks(t *testing.T) {
	config := StopTransferBuildConfig{
		MaxDistanceMetres:         100,
		WalkSpeedMetresPerSec:     1,
		MaxNearbyTransfersPerStop: 24,
	}
	served := testTransferStop(0, "served", 51.5, 0)
	unserved := testTransferStop(1, "unserved", 51.5+metresToLatitudeDegrees(10), 0)
	unservedNeighbour := testTransferStop(2, "unserved-neighbour", 51.5+metresToLatitudeDegrees(20), 0)
	served.HasService = true
	served.Associations = []*ctdf.Association{{Type: "stop_group", AssociatedIdentifier: "group"}}
	unserved.Associations = []*ctdf.Association{{Type: "stop_group", AssociatedIdentifier: "group"}}
	served.Platforms = []*ctdf.StopPlatform{{PrimaryIdentifier: "unserved"}}
	transfers := map[transferKey]transferCandidate{}

	stops := []*transferStop{served, unserved, unservedNeighbour}
	buildSameStopGroupTransfers(stops, transfers, config)
	buildPlatformAliasTransfers(stops, transfers, config)
	buildNearbyWalkTransfers(stops, transfers, config, true)

	assertTransferType(t, transfers, "served", "unserved", ctdf.StopTransferTypePlatformAlias)
	assertTransferType(t, transfers, "unserved", "served", ctdf.StopTransferTypePlatformAlias)
	assertNoTransfer(t, transfers, "served", "unserved-neighbour")
	assertNoTransfer(t, transfers, "unserved-neighbour", "served")
}

func TestStopHasServiceMatchesPrimaryAliasAndPlatformRefs(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "primary",
		OtherIdentifiers:  []string{"alias"},
		Platforms:         []*ctdf.StopPlatform{{PrimaryIdentifier: "platform"}},
	}
	for _, stopRef := range []string{"primary", "alias", "platform"} {
		if !stopHasService(stop, map[string]struct{}{stopRef: {}}) {
			t.Errorf("service ref %q did not match stop identifiers", stopRef)
		}
	}
	if stopHasService(stop, map[string]struct{}{"unrelated": {}}) {
		t.Fatal("unrelated service ref matched stop")
	}
	if !stopHasService(stop, nil) {
		t.Fatal("empty service view should keep stops when filtering is disabled")
	}
	railStop := &ctdf.Stop{TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	if !stopHasService(railStop, map[string]struct{}{"static-stop": {}}) {
		t.Fatal("rail stop should be retained because dynamic rail journeys are not indexed")
	}
}

func TestBuildNearbyWalkTransfersKeepsDynamicRailStopsWithStaticServices(t *testing.T) {
	config := StopTransferBuildConfig{
		MaxDistanceMetres:         100,
		WalkSpeedMetresPerSec:     1,
		MaxNearbyTransfersPerStop: 24,
	}
	staticStop := testTransferStop(0, "static-stop", 51.5, 0)
	dynamicStop := testTransferStop(1, "dynamic-stop", 51.5+metresToLatitudeDegrees(10), 0)
	staticStop.HasService = true
	dynamicStop.HasService = stopHasService(&ctdf.Stop{
		PrimaryIdentifier: dynamicStop.PrimaryIdentifier,
		TransportTypes:    []ctdf.TransportType{ctdf.TransportTypeRail},
	}, map[string]struct{}{"static-stop": {}})
	transfers := map[transferKey]transferCandidate{}

	buildNearbyWalkTransfers([]*transferStop{staticStop, dynamicStop}, transfers, config, true)

	assertTransferType(t, transfers, "static-stop", "dynamic-stop", ctdf.StopTransferTypeNearbyWalk)
	assertTransferType(t, transfers, "dynamic-stop", "static-stop", ctdf.StopTransferTypeNearbyWalk)
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

func TestWriteStopTransfersPrunesStaleEdgesGlobally(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("global pruning", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nRemoved", Value: 1}))

		if err := writeStopTransfers(context.Background(), map[transferKey]transferCandidate{}, StopTransferBuildConfig{}); err != nil {
			mt.Fatal(err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 1 || events[0].CommandName != "delete" {
			mt.Fatalf("expected one global stale-transfer delete, got %v", events)
		}
		filter := events[0].Command.Lookup("deletes", "0", "q").Document()
		if filter.Lookup("datasource.originalformat").StringValue() != stopTransferGeneratorFormat ||
			filter.Lookup("modificationdatetime", "$lt").Type == 0 ||
			filter.Lookup("$or").Type != 0 {
			mt.Fatalf("expected global pruning of stale generated transfers, got %s", filter)
		}
	})
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
				mtest.CreateCursorResponse(0, mt.DB.Name()+".stop_service_state", mtest.FirstBatch),
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
			if len(events) != 7 || events[4].CommandName != "update" || events[5].CommandName != "delete" || events[6].CommandName != "update" {
				mt.Fatalf("expected transfer upsert, stale-transfer delete, then checkpoint, got %v", events)
			}
			transfer := events[4].Command.Lookup("updates", "0", "u").Document()
			filter := events[5].Command.Lookup("deletes", "0", "q").Document()
			if filter.Lookup("datasource.originalformat").StringValue() != stopTransferGeneratorFormat ||
				filter.Lookup("modificationdatetime", "$lt").DateTime() != transfer.Lookup("modificationdatetime").DateTime() ||
				filter.Lookup("$or").Type != 0 {
				mt.Fatalf("expected age-based pruning of all generated transfers, got %s", filter)
			}
			checkpoint := events[6].Command.Lookup("updates", "0").Document()
			if checkpoint.Lookup("q", "dataset").StringValue() != datasetversion.StopTransfersDataset ||
				!checkpoint.Lookup("q", "rebuildafter").Time().Equal(requestedAt) ||
				checkpoint.Lookup("u", "$unset", "rebuildafter").Type == 0 {
				mt.Fatalf("expected checkpoint for the completed request, got %s", checkpoint)
			}
		})
	}
}

func TestEnsureStopLinksCurrentWaitsForChangedStopSources(t *testing.T) {
	sourceModified := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, linked := range []bool{false, true} {
		mt.Run(fmt.Sprintf("linked=%t", linked), func(mt *mtest.T) {
			previousDB := database.Instance
			database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
			mt.Cleanup(func() { database.Instance = previousDB })

			linkedModified := sourceModified.Add(-time.Millisecond)
			if linked {
				linkedModified = sourceModified.Add(time.Millisecond)
			}
			namespace := mt.DB.Name() + ".dataset_versions"
			mt.AddMockResponses(
				mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch,
					bson.D{{Key: "lastmodified", Value: sourceModified}},
				),
				mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch,
					bson.D{{Key: "lastmodified", Value: linkedModified}},
				),
			)

			err := ensureStopLinksCurrent(context.Background(), []string{"changed-stop-source"})
			if linked && err != nil {
				mt.Fatal(err)
			}
			if !linked && err == nil {
				mt.Fatal("expected stale linked stops to block transfer rebuild")
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
