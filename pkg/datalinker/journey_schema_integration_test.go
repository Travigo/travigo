package datalinker

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestJourneyPublicationRejectsInlineGeometry(t *testing.T) {
	uri := os.Getenv("TRAVIGO_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set TRAVIGO_TEST_MONGODB_URI to run isolated MongoDB integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("travigo_journey_test_" + primitive.NewObjectID().Hex())
	previous := database.Instance
	database.Instance = &database.MongoInstance{Client: client, Database: db}
	t.Cleanup(func() {
		database.Instance = previous
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := db.Drop(cleanupCtx); err != nil {
			t.Errorf("drop isolated test database: %v", err)
		}
		_ = client.Disconnect(cleanupCtx)
	})

	for _, name := range []string{database.JourneysRawCollectionName, database.JourneysStagingCollectionName, database.JourneysCollectionName} {
		if err := database.EnsureJourneyCollection(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	raw := db.Collection(database.JourneysRawCollectionName)
	live := db.Collection(database.JourneysCollectionName)
	journey := &ctdf.Journey{
		PrimaryIdentifier: "one", ServiceRef: "service",
		TrackRef: "route-track",
		Calls:    ctdf.JourneyCallsFromLegs(&ctdf.JourneyPathItem{OriginStopRef: "from", DestinationStopRef: "to", TrackRef: "leg-track"}),
	}
	journey.RefreshStopRefs()
	if err := journey.ValidatePath(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("journey_tracks").InsertMany(ctx, []any{
		ctdf.JourneyTrack{PrimaryIdentifier: "route-track", Track: []ctdf.Location{{Coordinates: []float64{0, 51}}, {Coordinates: []float64{1, 51}}}},
		ctdf.JourneyTrack{PrimaryIdentifier: "leg-track", Track: []ctdf.Location{{Coordinates: []float64{0, 51}}, {Coordinates: []float64{1, 51}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.InsertOne(ctx, journey); err != nil {
		t.Fatal(err)
	}

	invalid := bson.M{"calls": bson.A{bson.M{"stopref": "from", "outgoing": bson.M{"track": bson.A{1, 2}}}}}
	assertValidationError := func(err error) {
		t.Helper()
		var serverErr mongo.ServerError
		if !errors.As(err, &serverErr) || !serverErr.HasErrorCode(121) {
			t.Fatalf("expected document validation error, got %v", err)
		}
	}
	for _, name := range []string{database.JourneysRawCollectionName, database.JourneysStagingCollectionName, database.JourneysCollectionName} {
		collection := db.Collection(name)
		_, err := collection.InsertOne(ctx, invalid)
		assertValidationError(err)
		_, err = collection.InsertOne(ctx, bson.M{"calls": bson.A{}, "track": bson.A{}})
		assertValidationError(err)
		_, err = collection.InsertOne(ctx, bson.M{"path": bson.A{}})
		assertValidationError(err)
	}

	if err := NewPlainCopyLinker("journey").Run(PlainCopyConfig{}); err != nil {
		t.Fatal(err)
	}
	_, err = live.InsertOne(ctx, invalid)
	assertValidationError(err)

	var published ctdf.Journey
	if err := live.FindOne(ctx, bson.M{"originstoprefs": "from", "destinationstoprefs": "to"}).Decode(&published); err != nil {
		t.Fatal(err)
	}
	if stats := published.GetTracks(); stats.QueryError || stats.Hydrated != 2 {
		t.Fatalf("published tracks could not hydrate: %+v", stats)
	}

	journey.PrimaryIdentifier = "incremental"
	journey.DataSource = &ctdf.DataSourceReference{DatasetID: "fixture", Timestamp: "one"}
	if _, err := raw.InsertOne(ctx, journey); err != nil {
		t.Fatal(err)
	}
	if err := datasetversion.Upsert(ctx, ctdf.DatasetVersion{Dataset: "fixture", LastModified: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := NewPlainCopyLinker("journey").Run(PlainCopyConfig{SkipStaging: true}); err != nil {
		t.Fatal(err)
	}
	if err := live.FindOne(ctx, bson.M{"primaryidentifier": journey.PrimaryIdentifier}).Err(); err != nil {
		t.Fatal(err)
	}

	badSource := db.Collection("invalid_source")
	if _, err := badSource.InsertOne(ctx, invalid); err != nil {
		t.Fatal(err)
	}
	assertValidationError(copyCollection(badSource.Name(), live.Name()))
	cursor, err := badSource.Aggregate(ctx, incrementalMergePipeline(bson.M{}, live.Name()))
	if cursor != nil {
		defer cursor.Close(ctx)
	}
	assertValidationError(err)
}
