package tdmappings

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/railtd"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const smartURL = "https://publicdatafeeds.networkrail.co.uk/ntrod/SupportingFileAuthenticate?type=SMART"

func importSMART(c *cli.Context) error {
	raw, err := readSMARTSource(c.Context, c.Path("smart"))
	if err != nil {
		return err
	}
	steps, err := readSMART(raw)
	if err != nil {
		return err
	}
	if err := database.Connect(); err != nil {
		return err
	}
	snapshot, err := resolveSMART(c.Context, steps)
	if err != nil {
		return err
	}
	_, err = database.GetCollection(railtd.SMARTCollection).ReplaceOne(c.Context, bson.M{"_id": railtd.SMARTSnapshotID}, snapshot, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("save SMART snapshot: %w", err)
	}
	_, err = fmt.Fprintf(c.App.Writer, "Imported %d SMART rows and %d reporting points\n", len(steps), len(snapshot.Locations))
	return err
}

func readSMARTSource(ctx context.Context, path string) ([]byte, error) {
	var source io.ReadCloser
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		source = file
	} else {
		username, password := os.Getenv("TRAVIGO_NETWORKRAIL_USERNAME"), os.Getenv("TRAVIGO_NETWORKRAIL_PASSWORD")
		if username == "" || password == "" {
			return nil, fmt.Errorf("set TRAVIGO_NETWORKRAIL_USERNAME and TRAVIGO_NETWORKRAIL_PASSWORD or pass --smart")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, smartURL, nil)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(username, password)
		client := &http.Client{Timeout: time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("SMART download: %s", resp.Status)
		}
		source = resp.Body
	}
	defer source.Close()
	const limit = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("SMART source exceeds 32 MiB")
	}
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		raw, err = io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > limit {
			return nil, fmt.Errorf("decompressed SMART source exceeds 32 MiB")
		}
	}
	return raw, nil
}

func resolveSMART(ctx context.Context, steps []railtd.SMARTStep) (railtd.SMARTSnapshot, error) {
	snapshot := railtd.SMARTSnapshot{Steps: steps, Locations: map[string]railtd.SMARTLocation{}}
	const prefix = "gb-stanox-"
	wanted := map[string]bool{}
	var aliases []string
	for _, step := range steps {
		stanox := strings.TrimSpace(step.STANOX)
		if stanox != "" && !wanted[stanox] {
			wanted[stanox] = true
			aliases = append(aliases, prefix+stanox)
		}
	}
	collection := database.GetCollection("stops")
	ambiguous := map[string]bool{}
	owners := map[string]string{}
	for start := 0; start < len(aliases); start += 500 {
		batch := aliases[start:min(start+500, len(aliases))]
		cursor, err := collection.Find(ctx, bson.M{"otheridentifiers": bson.M{"$in": batch}},
			options.Find().SetProjection(bson.M{"primaryidentifier": 1, "otheridentifiers": 1, "location": 1}))
		if err != nil {
			return snapshot, err
		}
		var stops []ctdf.Stop
		if err := cursor.All(ctx, &stops); err != nil {
			return snapshot, err
		}
		inBatch := map[string]bool{}
		for _, alias := range batch {
			inBatch[alias] = true
		}
		for _, stop := range stops {
			if stop.PrimaryIdentifier == "" {
				continue
			}
			refs := append([]string{stop.PrimaryIdentifier}, stop.OtherIdentifiers...)
			// A stop can be returned again through another batch; only resolve aliases in this batch.
			for _, alias := range stop.OtherIdentifiers {
				if !inBatch[alias] {
					continue
				}
				stanox := strings.TrimPrefix(alias, prefix)
				if previous, ok := owners[stanox]; ok && previous != stop.PrimaryIdentifier {
					ambiguous[stanox] = true
				}
				owners[stanox] = stop.PrimaryIdentifier
				if railtd.ValidPoint(stop.Location) {
					snapshot.Locations[stanox] = railtd.SMARTLocation{Refs: refs, Point: *stop.Location}
				}
			}
		}
	}
	for stanox := range ambiguous {
		delete(snapshot.Locations, stanox)
	}
	return snapshot, nil
}
