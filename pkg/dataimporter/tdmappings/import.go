package tdmappings

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/journeytracks"
	"github.com/travigo/travigo/pkg/railtd"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mappingKey struct{ Area, From, To, Origin, Destination string }

type reviewedMapping struct {
	key                            mappingKey
	point                          ctdf.Location
	confidence, evidence, trackRef string
}

func Command() *cli.Command {
	return &cli.Command{
		Name: "td-mappings", Usage: "Import Network Rail SMART reporting points",
		Flags: []cli.Flag{
			&cli.PathFlag{Name: "smart", Usage: "Use a local SMART snapshot instead of downloading it"},
			&cli.StringFlag{Name: "reviewed", Usage: "CSV of verified CA steps"},
			&cli.BoolFlag{Name: "apply", Usage: "Write validated mappings to MongoDB"},
		},
		Subcommands: []*cli.Command{candidatesCommand()},
		Action: func(c *cli.Context) error {
			if c.String("reviewed") == "" {
				return importSMART(c)
			}
			file, err := os.Open(c.String("reviewed"))
			if err != nil {
				return err
			}
			defer file.Close()
			rows, err := readReviewed(file)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				_, err := fmt.Fprintln(c.App.Writer, "No reviewed TD mappings to import")
				return err
			}
			if err := database.Connect(); err != nil {
				return err
			}
			if err := validateTracks(c.Context, rows); err != nil {
				return err
			}
			if c.Bool("apply") {
				if err := upsertMappings(c.Context, rows); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(c.App.Writer, "Validated %d TD mappings (write enabled: %t)\n", len(rows), c.Bool("apply"))
			return err
		},
	}
}

func readReviewed(input io.Reader) ([]reviewedMapping, error) {
	reader := csv.NewReader(input)
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	expected := strings.Split("area,from,to,origin,destination,track_ref,longitude,latitude,confidence,evidence", ",")
	if !slices.Equal(header, expected) {
		return nil, fmt.Errorf("expected CSV header %s", strings.Join(expected, ","))
	}
	var rows []reviewedMapping
	seen := map[mappingKey]bool{}
	for line := 2; ; line++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		for i := range record {
			record[i] = strings.TrimSpace(record[i])
		}
		key := mappingKey{record[0], record[1], record[2], record[3], record[4]}
		lon, lonErr := strconv.ParseFloat(record[6], 64)
		lat, latErr := strconv.ParseFloat(record[7], 64)
		point := ctdf.Location{Type: "Point", Coordinates: []float64{lon, lat}}
		if key.Area == "" || key.From == "" || key.To == "" || key.From == key.To ||
			key.Origin == "" || key.Destination == "" || key.Origin == key.Destination ||
			record[5] == "" || lonErr != nil || latErr != nil || !railtd.ValidPoint(&point) ||
			(record[8] != "mapped" && record[8] != "approximate") || record[9] == "" {
			return nil, fmt.Errorf("invalid reviewed TD mapping at row %d", line)
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate reviewed TD mapping at row %d", line)
		}
		seen[key] = true
		rows = append(rows, reviewedMapping{key: key, point: point, trackRef: record[5], confidence: record[8], evidence: record[9]})
	}
	return rows, nil
}

func validateTracks(ctx context.Context, rows []reviewedMapping) error {
	type legKey struct{ origin, destination, track string }
	type verifiedLeg struct {
		origin, destination string
		track               []ctdf.Location
	}
	legs := map[legKey]verifiedLeg{}
	seen := map[mappingKey]bool{}
	for i := range rows {
		row := &rows[i]
		key := legKey{row.key.Origin, row.key.Destination, row.trackRef}
		leg, cached := legs[key]
		if !cached {
			var route journeytracks.RouteLeg
			err := database.GetCollection(journeytracks.RouteCollectionName).FindOne(ctx, bson.M{
				"trackref": key.track, "attributes.source": "openstreetmap", "originstoprefs": key.origin, "destinationstoprefs": key.destination,
			}).Decode(&route)
			if err != nil {
				return fmt.Errorf("verify OSM leg %s to %s (%s): %w", key.origin, key.destination, key.track, err)
			}
			if route.LegIndex < 0 || route.LegIndex >= len(route.RouteStopRefs)-1 {
				return fmt.Errorf("OSM leg %s to %s (%s) has invalid stop references", key.origin, key.destination, key.track)
			}
			var record ctdf.JourneyTrack
			if err := database.GetCollection("journey_tracks").FindOne(ctx, bson.M{"primaryidentifier": key.track}).Decode(&record); err != nil {
				return fmt.Errorf("load track %s: %w", key.track, err)
			}
			leg = verifiedLeg{route.RouteStopRefs[route.LegIndex], route.RouteStopRefs[route.LegIndex+1], record.Track}
			legs[key] = leg
		}
		if railtd.TrackProgress(row.point, leg.track) < 0 {
			return fmt.Errorf("TD step %s %s to %s is outside its 50 m OSM leg tolerance", row.key.Area, row.key.From, row.key.To)
		}
		row.key.Origin, row.key.Destination = leg.origin, leg.destination
		if seen[row.key] {
			return fmt.Errorf("duplicate reviewed TD mapping for %s %s to %s on leg %s to %s",
				row.key.Area, row.key.From, row.key.To, leg.origin, leg.destination)
		}
		seen[row.key] = true
	}
	return nil
}

func upsertMappings(ctx context.Context, rows []reviewedMapping) error {
	if err := database.EnsureTDMappingsIndex(ctx); err != nil {
		return err
	}
	writes := make([]mongo.WriteModel, 0, len(rows))
	for _, row := range rows {
		key := row.key
		writes = append(writes, mongo.NewUpdateOneModel().SetFilter(bson.M{
			"area": key.Area, "from": key.From, "to": key.To, "origin": key.Origin, "destination": key.Destination,
		}).SetUpdate(bson.M{"$set": bson.M{"point": row.point, "confidence": row.confidence, "evidence": row.evidence}}).SetUpsert(true))
	}
	_, err := database.GetCollection(database.TDMappingsCollectionName).BulkWrite(ctx, writes, options.BulkWrite().SetOrdered(false))
	return err
}
