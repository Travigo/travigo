package datalinker

import (
	"context"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func setupTransferWriterMock(mt *mtest.T) func() {
	previous := database.Instance
	database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
	return func() { database.Instance = previous }
}

func TestReconcileTransferEdgesUpdatesByEndpointsAndPreservesCreation(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("update", func(mt *mtest.T) {
		defer setupTransferWriterMock(mt)()
		created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".stop_transfers", mtest.FirstBatch, bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "fromstopref", Value: "A"}, {Key: "tostopref", Value: "B"}, {Key: "creationdatetime", Value: created}, {Key: "type", Value: string(ctdf.StopTransferTypeNearbyWalk)}, {Key: "distancemetres", Value: 10}, {Key: "walkdurationseconds", Value: 10}, {Key: "minchangedurationseconds", Value: 60}, {Key: "totaldurationseconds", Value: 70}, {Key: "generatedradiusmetres", Value: 500}}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}),
		)
		candidate := transferCandidate{key: transferKey{"A", "B"}, transferType: ctdf.StopTransferTypeNearbyWalk, distanceMetres: 20, walkDurationSeconds: 20, minChangeDurationSeconds: 60, totalDurationSeconds: 80, generatedRadiusMetres: 500}
		ins, upd, del, err := reconcileTransferEdges(context.Background(), []string{"A"}, map[transferKey]transferCandidate{candidate.key: candidate}, StopTransferBuildConfig{})
		if err != nil || ins != 0 || upd != 1 || del != 0 {
			mt.Fatalf("counts/error = %d/%d/%d, %v", ins, upd, del, err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 2 || events[1].CommandName != "update" {
			mt.Fatalf("expected find and update, got %v", events)
		}
		u := events[1].Command.Lookup("updates", "0", "q").Document()
		if u.Lookup("fromstopref").StringValue() != "A" || u.Lookup("tostopref").StringValue() != "B" {
			mt.Fatalf("update did not use endpoint identity: %s", u)
		}
		set := events[1].Command.Lookup("updates", "0", "u", "$set").Document()
		if set.Lookup("creationdatetime").Type != 0 {
			mt.Fatalf("update must preserve creation time: %s", set)
		}
	})
}

func TestReconcileTransferEdgesSkipsUnchangedWrites(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("unchanged", func(mt *mtest.T) {
		defer setupTransferWriterMock(mt)()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".stop_transfers", mtest.FirstBatch, bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "fromstopref", Value: "A"}, {Key: "tostopref", Value: "B"}, {Key: "type", Value: string(ctdf.StopTransferTypeNearbyWalk)}, {Key: "distancemetres", Value: 10}, {Key: "walkdurationseconds", Value: 10}, {Key: "minchangedurationseconds", Value: 60}, {Key: "totaldurationseconds", Value: 70}, {Key: "generatedradiusmetres", Value: 500}}))
		candidate := transferCandidate{key: transferKey{"A", "B"}, transferType: ctdf.StopTransferTypeNearbyWalk, distanceMetres: 10, walkDurationSeconds: 10, minChangeDurationSeconds: 60, totalDurationSeconds: 70, generatedRadiusMetres: 500}
		_, upd, _, err := reconcileTransferEdges(context.Background(), []string{"A"}, map[transferKey]transferCandidate{candidate.key: candidate}, StopTransferBuildConfig{})
		if err != nil || upd != 0 {
			mt.Fatalf("updates/error = %d, %v", upd, err)
		}
		if events := mt.GetAllStartedEvents(); len(events) != 1 || events[0].CommandName != "find" {
			mt.Fatalf("unexpected writes for unchanged edge: %v", events)
		}
	})
}

func TestReconcileTransferEdgesRetainsAndUpdatesOneDuplicate(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("duplicate endpoint", func(mt *mtest.T) {
		defer setupTransferWriterMock(mt)()
		id1, id2 := primitive.NewObjectID(), primitive.NewObjectID()
		row := func(id primitive.ObjectID) bson.D {
			return bson.D{{Key: "_id", Value: id}, {Key: "fromstopref", Value: "A"}, {Key: "tostopref", Value: "B"}, {Key: "type", Value: string(ctdf.StopTransferTypeNearbyWalk)}, {Key: "distancemetres", Value: 10}, {Key: "walkdurationseconds", Value: 10}, {Key: "minchangedurationseconds", Value: 60}, {Key: "totaldurationseconds", Value: 70}, {Key: "generatedradiusmetres", Value: 500}}
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".stop_transfers", mtest.FirstBatch, row(id1), row(id2)),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
		)
		candidate := transferCandidate{key: transferKey{"A", "B"}, transferType: ctdf.StopTransferTypeNearbyWalk, distanceMetres: 11, walkDurationSeconds: 11, minChangeDurationSeconds: 60, totalDurationSeconds: 71, generatedRadiusMetres: 500}
		ins, upd, del, err := reconcileTransferEdges(context.Background(), []string{"A"}, map[transferKey]transferCandidate{candidate.key: candidate}, StopTransferBuildConfig{})
		if err != nil || ins != 0 || upd != 1 || del != 1 {
			mt.Fatalf("counts/error = %d/%d/%d, %v", ins, upd, del, err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 3 {
			mt.Fatalf("expected find, update and delete, got %v", events)
		}
		filter := events[1].Command.Lookup("updates", "0", "q").Document()
		if filter.Lookup("_id").ObjectID() != id1 {
			mt.Fatalf("update did not target retained row %s: %s", id1, filter)
		}
		query := events[0].Command.Lookup("filter").Document()
		if query.Lookup("datasource.originalformat").StringValue() != stopTransferGeneratorFormat || query.Lookup("primaryidentifier").Type != 0 {
			mt.Fatalf("query must select only generated rows: %s", query)
		}
	})
}

func TestReconcileTransferEdgesFlushesDeletionOnlyBatches(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("batch size one", func(mt *mtest.T) {
		defer setupTransferWriterMock(mt)()
		row := func(from, to string) bson.D {
			return bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "fromstopref", Value: from}, {Key: "tostopref", Value: to}}
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".stop_transfers", mtest.FirstBatch, row("A", "B"), row("A", "C")),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nDeleted", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nDeleted", Value: 1}),
		)
		_, _, deleted, err := reconcileTransferEdges(context.Background(), []string{"A"}, map[transferKey]transferCandidate{}, StopTransferBuildConfig{BatchSize: 1})
		if err != nil || deleted != 2 {
			mt.Fatalf("deleted/error = %d, %v", deleted, err)
		}
		events := mt.GetAllStartedEvents()
		if len(events) != 3 {
			mt.Fatalf("expected find plus two bounded deletes, got %v", events)
		}
		for _, event := range events[1:] {
			if event.CommandName != "delete" {
				mt.Fatalf("expected delete command, got %s", event.CommandName)
			}
		}
	})
}

func TestReconcileTransferEdgesEmptyOriginsDoesNoDatabaseWork(t *testing.T) {
	inserted, updated, deleted, err := reconcileTransferEdges(context.Background(), nil, nil, StopTransferBuildConfig{})
	if err != nil || inserted != 0 || updated != 0 || deleted != 0 {
		t.Fatalf("counts/error = %d/%d/%d, %v", inserted, updated, deleted, err)
	}
}
