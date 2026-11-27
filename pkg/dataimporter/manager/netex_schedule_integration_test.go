package manager

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestNetExScheduleZipUsesNormalCleanupAndVersionFlow(t *testing.T) {
	uri := os.Getenv("TRAVIGO_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set TRAVIGO_TEST_MONGODB_URI to run the NeTEx schedule import integration test")
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
	dbName := fmt.Sprintf("travigo_netex_schedule_%d", time.Now().UnixNano())
	db := client.Database(dbName)
	previous := database.Instance
	database.Instance = &database.MongoInstance{Client: client, Database: db}
	t.Cleanup(func() {
		database.Instance = previous
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})

	const datasetID = "netex-fixture"
	staleSource := &ctdf.DataSourceReference{OriginalFormat: string(datasets.DataSetFormatNetExSchedule), DatasetID: datasetID, Timestamp: "old"}
	if _, err := db.Collection("stops_raw").InsertOne(ctx, &ctdf.Stop{
		PrimaryIdentifier: "stale-stop", DataSource: staleSource,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("stop_groups").InsertOne(ctx, &ctdf.StopGroup{
		PrimaryIdentifier: "stale-group", DataSource: staleSource,
	}); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "schedule.zip")
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(archive)
	member, err := zipWriter.Create("stops.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = member.Write([]byte(strings.TrimSpace(`<PublicationDelivery xmlns="http://www.netex.org.uk/netex"><dataObjects><StopPlace id="place"><Name>Central</Name><StopPlaceType>busStation</StopPlaceType><Centroid><Location><Longitude>-1.2</Longitude><Latitude>52.1</Latitude></Location></Centroid></StopPlace><Quay id="quay"><Name>Bay 4</Name><PublicCode>4</PublicCode><ParentZoneRef ref="place"/></Quay><ScheduledStopPoint id="point"><Name>Central</Name></ScheduledStopPoint><PassengerStopAssignment id="assignment"><ScheduledStopPointRef ref="point"/><QuayRef ref="quay"/></PassengerStopAssignment></dataObjects></PublicationDelivery>`)))
	if err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	dataset := &datasets.DataSet{
		Identifier:   datasetID,
		Format:       datasets.DataSetFormatNetExSchedule,
		Source:       archivePath,
		UnpackBundle: datasets.BundleFormatZIP,
		SupportedObjects: datasets.SupportedObjects{
			Stops: true, StopGroups: true,
		},
	}
	if err := importDataset(dataset, true, false, "", ""); err != nil {
		t.Fatalf("import NeTEx archive: %v", err)
	}

	if count, err := db.Collection("stops_raw").CountDocuments(ctx, bson.M{"datasource.datasetid": datasetID}); err != nil || count != 2 {
		t.Fatalf("stored stop count = %d, %v; want 2", count, err)
	}
	if count, err := db.Collection("stops_raw").CountDocuments(ctx, bson.M{"primaryidentifier": "stale-stop"}); err != nil || count != 0 {
		t.Fatalf("stale stop count = %d, %v; want 0", count, err)
	}
	if count, err := db.Collection("stop_groups").CountDocuments(ctx, bson.M{"datasource.datasetid": datasetID}); err != nil || count != 1 {
		t.Fatalf("stored stop group count = %d, %v; want 1", count, err)
	}
	if count, err := db.Collection("stop_groups").CountDocuments(ctx, bson.M{"primaryidentifier": "stale-group"}); err != nil || count != 0 {
		t.Fatalf("stale stop group count = %d, %v; want 0", count, err)
	}
	if err := db.Collection("dataset_versions").FindOne(ctx, bson.M{"dataset": datasetID, "hash": bson.M{"$ne": ""}}).Err(); err != nil {
		t.Fatalf("dataset version not published after normal archive flow: %v", err)
	}
	var report datasets.DataImportReport
	if err := db.Collection("dataset_import_reports").FindOne(ctx, bson.M{"datasetidentifier": datasetID}).Decode(&report); err != nil {
		t.Fatalf("normal import report not published: %v", err)
	}
	if report.ImportedStops != 2 || report.ImportedStopGroups != 1 {
		t.Fatalf("import report counts = stops %d, groups %d; want 2 and 1", report.ImportedStops, report.ImportedStopGroups)
	}
}
