package tdmappings

import (
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/journeytracks"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestCandidatesCommandScopesSMARTToPilotStations(t *testing.T) {
	dir := t.TempDir()
	smart := filepath.Join(dir, "smart.json")
	corpus := filepath.Join(dir, "corpus.json")
	for path, content := range map[string]string{
		smart: `{"BERTHDATA":[
   {"TD":"ZZ","FROMBERTH":"0001","TOBERTH":"0002","STANOX":"00123","STEPTYPE":"B","EVENT":"A","BERTHOFFSET":-15},
   {"TD":"ZZ","FROMBERTH":"0003","TOBERTH":"0004","STANOX":"00456","STEPTYPE":"B"},
   {"TD":"ZZ","FROMBERTH":"0005","TOBERTH":"0006","STANOX":"00123","STEPTYPE":"I"}
  ]}`,
		corpus: `{"TIPLOCDATA":[{"STANOX":"00123","TIPLOC":"CAMBDGE","3ALPHA":"CBG"},{"STANOX":"00456","TIPLOC":"OTHER","3ALPHA":"ABC"}]}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	app := &cli.App{Writer: &output, ErrWriter: io.Discard, Commands: []*cli.Command{Command()}}
	if err := app.Run([]string{"travigo", "td-mappings", "candidates", "--smart", smart, "--corpus", corpus}); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&output).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || strings.Join(rows[1], ",") != "ZZ,0001,0002,00123,CAMBDGE,CBG,A,-15" {
		t.Fatalf("unexpected candidates: %v", rows)
	}
}

func TestReadReviewedMappings(t *testing.T) {
	header := "area,from,to,origin,destination,track_ref,longitude,latitude,confidence,evidence\n"
	valid := "ZZ,0001,0002,origin,dest,track,0.1,52,mapped,reviewed signal\n"
	rows, err := readReviewed(strings.NewReader(header + valid))
	if err != nil || len(rows) != 1 || rows[0].key.From != "0001" {
		t.Fatalf("unexpected rows: %v (%v)", rows, err)
	}
	for _, input := range []string{valid + valid, strings.Replace(valid, "reviewed signal", "", 1), strings.Replace(valid, "52", "NaN", 1)} {
		if _, err := readReviewed(strings.NewReader(header + input)); err == nil {
			t.Fatalf("accepted invalid rows: %s", input)
		}
	}
}

func TestReviewedMappingValidatesTrackAndUpserts(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("approved point on ordered leg", func(mt *mtest.T) {
		oldDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = oldDB })
		ctx := context.Background()
		rows, err := readReviewed(strings.NewReader("area,from,to,origin,destination,track_ref,longitude,latitude,confidence,evidence\nZZ,0001,0002,alias-a,alias-b,track,0.01,52,mapped,reviewed signal\n"))
		if err != nil {
			mt.Fatal(err)
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+journeytracks.RouteCollectionName, mtest.FirstBatch, bson.D{{Key: "trackref", Value: "track"}, {Key: "routestoprefs", Value: bson.A{"a", "b"}}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journey_tracks", mtest.FirstBatch, bson.D{{Key: "track", Value: bson.A{
				bson.M{"type": "Point", "coordinates": bson.A{0.0, 52.0}}, bson.M{"type": "Point", "coordinates": bson.A{0.02, 52.0}},
			}}}),
			mtest.CreateSuccessResponse(),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
		)
		if err := validateTracks(ctx, rows); err != nil {
			mt.Fatal(err)
		}
		if rows[0].key.Origin != "a" || rows[0].key.Destination != "b" {
			mt.Fatalf("expected canonical endpoint IDs, got %+v", rows[0].key)
		}
		if err := upsertMappings(ctx, rows); err != nil {
			mt.Fatal(err)
		}
		commands := mt.GetAllStartedEvents()
		if len(commands) != 4 || commands[3].Command.Lookup("update").StringValue() != database.TDMappingsCollectionName {
			mt.Fatalf("unexpected Mongo commands: %v", commands)
		}
	})
}

func TestReviewedCSVCanRunWithoutMappings(t *testing.T) {
	var output bytes.Buffer
	app := &cli.App{Writer: &output, ErrWriter: io.Discard, Commands: []*cli.Command{Command()}}
	path := filepath.Join(t.TempDir(), "reviewed.csv")
	if err := os.WriteFile(path, []byte("area,from,to,origin,destination,track_ref,longitude,latitude,confidence,evidence\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.Run([]string{"travigo", "td-mappings", "--reviewed", path, "--apply"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No reviewed TD mappings to import") {
		t.Fatalf("unexpected output: %q", output.String())
	}
}
