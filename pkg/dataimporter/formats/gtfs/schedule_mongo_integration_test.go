package gtfs

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestGTFSJourneyBatchStoresReferencedTracksBeforeCalls(t *testing.T) {
	uri := os.Getenv("TRAVIGO_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set TRAVIGO_TEST_MONGODB_URI to run the MongoDB schedule writer integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("travigo_gtfs_fixture_%d", time.Now().UnixNano())
	db := client.Database(databaseName)
	previous := database.Instance
	database.Instance = &database.MongoInstance{Client: client, Database: db}
	t.Cleanup(func() {
		database.Instance = previous
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})

	datasource := &ctdf.DataSourceReference{OriginalFormat: "gtfs", DatasetID: "gtfs-fixture", Timestamp: "fixture-v1"}
	trackStore := &journeyTrackStore{
		dataset:    datasets.DataSet{Identifier: "gtfs-fixture"},
		datasource: datasource,
		pending:    make([]mongo.WriteModel, 0, journeyTrackBatchSize),
	}
	geometry := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{-0.1, 51.5}},
		{Type: "Point", Coordinates: []float64{-0.09, 51.51}},
	}
	wholeRef, err := trackStore.Reference("whole-shape", geometry)
	if err != nil {
		t.Fatal(err)
	}
	firstLegRef, err := trackStore.Reference("split-shape:0", geometry)
	if err != nil {
		t.Fatal(err)
	}
	secondLegRef, err := trackStore.Reference("split-shape:1", geometry)
	if err != nil {
		t.Fatal(err)
	}

	whole := &ctdf.Journey{
		PrimaryIdentifier: "whole-shape-trip",
		DataSource:        datasource,
		TrackRef:          wholeRef,
		Calls: ctdf.JourneyCallsFromLegs(&ctdf.JourneyPathItem{
			OriginStopRef: "stop-a", DestinationStopRef: "stop-b",
		}),
	}
	split := &ctdf.Journey{
		PrimaryIdentifier: "split-shape-trip",
		DataSource:        datasource,
		Calls: ctdf.JourneyCallsFromLegs(
			&ctdf.JourneyPathItem{OriginStopRef: "stop-a", DestinationStopRef: "stop-b", TrackRef: firstLegRef},
			&ctdf.JourneyPathItem{OriginStopRef: "stop-b", DestinationStopRef: "stop-c", TrackRef: secondLegRef},
		),
	}
	for _, journey := range []*ctdf.Journey{whole, split} {
		journey.RefreshStopRefs()
		if err := journey.ValidatePath(); err != nil {
			t.Fatalf("validate journey %s: %v", journey.PrimaryIdentifier, err)
		}
	}
	models := []mongo.WriteModel{}
	for _, journey := range []*ctdf.Journey{whole, split} {
		replacement, err := bson.Marshal(journey)
		if err != nil {
			t.Fatal(err)
		}
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(bson.M{"primaryidentifier": journey.PrimaryIdentifier}).
			SetReplacement(replacement).
			SetUpsert(true))
	}
	if err := flushGTFSJourneyBatch(trackStore, models); err != nil {
		t.Fatal(err)
	}

	if count, err := db.Collection("journey_tracks").CountDocuments(ctx, bson.M{}); err != nil || count != 3 {
		t.Fatalf("stored journey tracks = %d, %v; want 3", count, err)
	}
	for identifier, want := range map[string][]string{
		"whole-shape-trip": {},
		"split-shape-trip": {firstLegRef, secondLegRef},
	} {
		var raw bson.Raw
		if err := db.Collection(database.JourneysRawCollectionName).FindOne(ctx, bson.M{"primaryidentifier": identifier}).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if raw.Lookup("path").Type != 0 || raw.Lookup("track").Type != 0 {
			t.Fatalf("journey %s stored a legacy path or inline shape", identifier)
		}
		var stored ctdf.Journey
		if err := bson.Unmarshal(raw, &stored); err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0)
		for origin, _ := range stored.LegCalls() {
			if origin.Outgoing != nil && origin.Outgoing.TrackRef != "" {
				got = append(got, origin.Outgoing.TrackRef)
			}
		}
		if identifier == "whole-shape-trip" {
			if stored.TrackRef != wholeRef || len(got) != 0 {
				t.Fatalf("whole shape refs = root %q, leg %v", stored.TrackRef, got)
			}
		} else if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("split shape refs = %v, want %v", got, want)
		}
	}

	if err := db.Collection("journey_tracks").Drop(ctx); err != nil {
		t.Fatal(err)
	}
	validator := bson.M{"$jsonSchema": bson.M{"bsonType": "object", "required": bson.A{"intentional_rejection"}}}
	if err := db.CreateCollection(ctx, "journey_tracks", options.CreateCollection().SetValidator(validator)); err != nil {
		t.Fatal(err)
	}
	failingStore := &journeyTrackStore{
		dataset:    datasets.DataSet{Identifier: "gtfs-fixture"},
		datasource: datasource,
		pending:    make([]mongo.WriteModel, 0, journeyTrackBatchSize),
	}
	failingRef, err := failingStore.Reference("rejected-track", geometry)
	if err != nil {
		t.Fatal(err)
	}
	failingJourney := &ctdf.Journey{
		PrimaryIdentifier: "journey-must-not-reference-rejected-track",
		DataSource:        datasource,
		TrackRef:          failingRef,
		Calls: ctdf.JourneyCallsFromLegs(&ctdf.JourneyPathItem{
			OriginStopRef: "stop-a", DestinationStopRef: "stop-b",
		}),
	}
	journeyDocument, err := bson.Marshal(failingJourney)
	if err != nil {
		t.Fatal(err)
	}
	writeErr := flushGTFSJourneyBatch(failingStore, []mongo.WriteModel{mongo.NewReplaceOneModel().
		SetFilter(bson.M{"primaryidentifier": failingJourney.PrimaryIdentifier}).
		SetReplacement(journeyDocument).
		SetUpsert(true)})
	if writeErr == nil {
		t.Fatal("expected track validation failure")
	}
	if count, err := db.Collection(database.JourneysRawCollectionName).CountDocuments(ctx, bson.M{"primaryidentifier": failingJourney.PrimaryIdentifier}); err != nil || count != 0 {
		t.Fatalf("journey referencing rejected track was stored: count=%d err=%v", count, err)
	}
}
