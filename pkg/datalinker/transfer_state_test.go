package datalinker

import (
	"context"
	"testing"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestDirtyOriginRowsDeduplicatesAndDropsEmptyIDs(t *testing.T) {
	rows := dirtyOriginRows([]string{"a", "", "b", "a", "b", "c"})
	if len(rows) != 3 {
		t.Fatalf("got %d dirty rows, want 3", len(rows))
	}
	want := []string{"a", "b", "c"}
	for i, row := range rows {
		id := row.(bson.M)["_id"]
		if id != want[i] {
			t.Fatalf("dirty row %d ID = %v, want %q", i, id, want[i])
		}
	}
}

func TestTransferBuildBatchRejectsLostFence(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("lease token no longer matches", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		defer func() { database.Instance = previous }()
		mt.AddMockResponses(
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 0}, bson.E{Key: "nModified", Value: 0}),
			mtest.CreateSuccessResponse(bson.E{Key: "ok", Value: 1}), // abortTransaction
		)
		lease := &transferBuildLease{db: mt.DB, token: "stale-token"}
		called := false
		err := lease.WithBatch(context.Background(), func(context.Context) error { called = true; return nil })
		if err == nil || called {
			mt.Fatalf("lost lease must reject callback, err=%v, called=%t", err, called)
		}
		events := mt.GetAllStartedEvents()
		if len(events) < 1 || events[0].CommandName != "update" {
			mt.Fatalf("fence assertion was not the first transaction operation: %#v", events)
		}
		filter := events[0].Command.Lookup("updates", "0", "q").Document()
		if filter.Lookup("token").StringValue() != "stale-token" {
			mt.Fatalf("lease assertion did not include token: %s", filter)
		}
	})
}

func TestDirtyOriginBatchSizeStaysBounded(t *testing.T) {
	if transferDirtyBatchSize < 1 || transferDirtyBatchSize > 1000 {
		t.Fatalf("dirty origin transaction batch size = %d, want 1..1000", transferDirtyBatchSize)
	}
}
