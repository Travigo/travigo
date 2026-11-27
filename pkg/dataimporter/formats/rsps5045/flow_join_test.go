package rsps5045

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestWriteFlowFaresRejectsMissingFlow(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("missing flow", func(mt *mtest.T) {
		old := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = old })
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+fareStageCollection, mtest.FirstBatch, bson.D{{Key: "n", Value: int32(1)}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+flowStageCollection, mtest.FirstBatch, bson.D{{Key: "n", Value: int32(0)}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+flowStageCollection, mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+fareStageCollection, mtest.FirstBatch, bson.D{{Key: "runid", Value: "v1"}, {Key: "flowid", Value: "missing"}}),
		)
		source := &ctdf.DataSourceReference{DatasetID: "rail", FareVersionID: "v1", ImportContext: context.Background()}
		var report datasets.DataImportReport
		err := (&Fares{}).writeFlowFares(datasets.DataSet{Identifier: "rail"}, source, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, &report)
		if err == nil || !strings.Contains(err.Error(), "only 0 join") {
			mt.Fatalf("expected missing flow error, got %v", err)
		}
	})
}

func TestWriteFlowFaresConvertsCachedFaresAndDeduplicatesAreas(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("cached flow", func(mt *mtest.T) {
		old := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = old })
		flow := bson.D{{Key: "flowid", Value: "flow"}, {Key: "origin", Value: "0123"}, {Key: "destination", Value: "4567"}, {Key: "statuscode", Value: "000"}, {Key: "direction", Value: "R"}}
		firstFare := bson.D{{Key: "flowid", Value: "flow"}, {Key: "ticketcode", Value: "AAA"}, {Key: "amountpence", Value: int64(100)}}
		secondFare := bson.D{{Key: "flowid", Value: "flow"}, {Key: "ticketcode", Value: "BBB"}, {Key: "amountpence", Value: int64(200)}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+fareStageCollection, mtest.FirstBatch, bson.D{{Key: "n", Value: int32(2)}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+flowStageCollection, mtest.FirstBatch, bson.D{{Key: "n", Value: int32(1)}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+flowStageCollection, mtest.FirstBatch, flow),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+fareStageCollection, mtest.FirstBatch, firstFare, secondFare),
			mtest.CreateSuccessResponse(),
			mtest.CreateSuccessResponse(),
			mtest.CreateSuccessResponse(),
			mtest.CreateSuccessResponse(),
		)
		source := &ctdf.DataSourceReference{DatasetID: "rail", FareVersionID: "v1", ImportContext: context.Background()}
		record := func(_ string, id string, from, to time.Time) ctdf.FareRecord {
			return ctdf.FareRecord{DatasetID: source.DatasetID, FareVersionID: source.FareVersionID, SourceIdentifier: id, EffectiveFrom: from, EffectiveTo: to, DataSource: source}
		}
		tickets := map[string][]ticketType{"AAA": {{ProductRef: "product-a"}}, "BBB": {{ProductRef: "product-b"}}}
		var report datasets.DataImportReport
		if err := (&Fares{}).writeFlowFares(datasets.DataSet{Identifier: "rail"}, source, record, tickets, "adult", nil, nil, nil, nil, nil, nil, nil, &report); err != nil {
			mt.Fatal(err)
		}
		if report.ImportedFareOffers != 2 || report.ImportedFareRules != 4 || report.ImportedFareLocations != 2 {
			mt.Fatalf("unexpected converted counts: %+v", report)
		}
		written := make(map[string]int)
		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName == "update" {
				updates, err := event.Command.Lookup("updates").Array().Values()
				if err != nil {
					mt.Fatal(err)
				}
				written[event.Command.Lookup("update").StringValue()] += len(updates)
			}
		}
		if written["fare_locations"] != 2 || written["fare_offers"] != 2 || written["fare_leg_rules"] != 4 {
			mt.Fatalf("unexpected writes: %v", written)
		}
	})
}

func TestWriteFlowFaresFallsBackBeforeLoadingOversizedCache(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("oversized flow count", func(mt *mtest.T) {
		old := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = old })
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+fareStageCollection, mtest.FirstBatch, bson.D{{Key: "n", Value: int32(1)}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+flowStageCollection, mtest.FirstBatch, bson.D{{Key: "n", Value: int32(1_000_001)}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+fareStageCollection, mtest.FirstBatch),
		)
		source := &ctdf.DataSourceReference{DatasetID: "rail", FareVersionID: "v1", ImportContext: context.Background()}
		var report datasets.DataImportReport
		err := (&Fares{}).writeFlowFares(datasets.DataSet{Identifier: "rail"}, source, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, &report)
		if err == nil || !strings.Contains(err.Error(), "only 0 join") {
			mt.Fatalf("expected joined count mismatch, got %v", err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 3 || events[2].CommandName != "aggregate" || events[2].Command.Lookup("aggregate").StringValue() != fareStageCollection {
			mt.Fatalf("expected a database join immediately after the flow count, got %v", events)
		}
	})
}
