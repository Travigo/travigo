package stopservices

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// This test executes the aggregation and publication protocol against MongoDB.
// It never uses the application's connection settings or an existing database.
func TestMongoStaticDatasetContributions(t *testing.T) {
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
	db := client.Database("travigo_stopservices_test_" + primitive.NewObjectID().Hex())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := db.Drop(cleanupCtx); err != nil {
			t.Errorf("drop isolated test database: %v", err)
		}
		_ = client.Disconnect(cleanupCtx)
	})
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := BatchForStops(ctx, db, []string{"shared"}); !errors.Is(err, ErrUnbuilt) {
		t.Fatalf("initial coverage: got %v, want ErrUnbuilt", err)
	}
	journey := func(dataset, service, from, to string) bson.M {
		return bson.M{
			"datasource": bson.M{"datasetid": dataset},
			"serviceref": service,
			"path":       bson.A{bson.M{"originstopref": from, "destinationstopref": to}},
		}
	}
	_, err = db.Collection("journeys").InsertMany(ctx, []any{
		journey("feed-a", "shared-service", "shared", "terminal"),
		journey("feed-a", "shared-service", "shared", "terminal"),
		journey("feed-a", "a-only", "shared", "a-stop"),
		journey("feed-b", "shared-service", "shared", "terminal"),
		journey("feed-b", "b-only", "shared", "b-stop"),
		journey("gb-networkrail-vstp", "dynamic-vstp", "shared", "terminal"),
		journey("gb-nationalrail-darwin-amendment", "dynamic-darwin", "shared", "terminal"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Build(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertRefs := func(want map[string][]string) {
		t.Helper()
		refs := make([]string, 0, len(want))
		for ref := range want {
			refs = append(refs, ref)
		}
		got, err := BatchForStops(ctx, db, refs)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stop services: got %#v, want %#v", got, want)
		}
	}
	assertRefs(map[string][]string{
		"shared":   {"a-only", "b-only", "shared-service"},
		"terminal": {"shared-service"},
		"empty":    {},
	})
	finish, err := BeginSourceWrite(ctx, db, "feed-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("journeys").DeleteMany(ctx, bson.M{"datasource.datasetid": "feed-a"}); err != nil {
		t.Fatal(err)
	}
	// A writer in progress must not replace the last completed contribution.
	if err := BuildDataset(ctx, db, "feed-a"); !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("build during publication: got %v, want ErrSourceBusy", err)
	}
	assertRefs(map[string][]string{"shared": {"a-only", "b-only", "shared-service"}})
	if err := finish(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPending(ctx, db); err != nil {
		t.Fatal(err)
	}
	// Removing one feed must retain services contributed by the other feed.
	assertRefs(map[string][]string{
		"shared":   {"b-only", "shared-service"},
		"terminal": {"shared-service"},
		"a-stop":   {},
	})
	if count, err := BuildPending(ctx, db); err != nil || count != 0 {
		t.Fatalf("unchanged refresh: got count=%d err=%v", count, err)
	}
}
