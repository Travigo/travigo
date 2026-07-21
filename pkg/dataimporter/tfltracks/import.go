package tfltracks

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/journeytracks"
	"github.com/travigo/travigo/pkg/tflapi"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var DefaultModes = []string{"tube", "dlr", "tram", "river-bus", "cable-car", "overground", "elizabeth-line", "national-rail", "bus"}

type Format struct{}

func (*Format) ParseFile(io.Reader) error { return nil }

func (*Format) Import(datasets.DataSet, *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	return datasets.DataImportReport{}, errors.New("TfL route tracks must be imported as an API dataset")
}

func (*Format) ImportAPI(dataset datasets.DataSet, source *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	modes := DefaultModes
	if configured := strings.TrimSpace(dataset.CustomConfig["modes"]); configured != "" {
		modes = strings.Split(configured, ",")
	}
	count, err := ImportRoutes(context.Background(), Options{
		AppKey: os.Getenv("TRAVIGO_TFL_API_KEY"), Modes: modes,
		MaxConcurrency: 8, RequestInterval: 160 * time.Millisecond,
	}, source)
	return datasets.DataImportReport{ImportedJourneyTracks: count}, err
}

type Options struct {
	AppKey          string
	Modes           []string
	MaxConcurrency  int
	RequestInterval time.Duration
}

type modeDefinition struct {
	ID            string
	TransportType ctdf.TransportType
}

var modeDefinitions = map[string]modeDefinition{
	"bus":            {ID: "bus", TransportType: ctdf.TransportTypeBus},
	"dlr":            {ID: "dlr", TransportType: ctdf.TransportTypeRail},
	"river-bus":      {ID: "river-bus", TransportType: ctdf.TransportTypeFerry},
	"tram":           {ID: "tram", TransportType: ctdf.TransportTypeTram},
	"tube":           {ID: "tube", TransportType: ctdf.TransportTypeMetro},
	"cable-car":      {ID: "cable-car", TransportType: ctdf.TransportTypeCableCar},
	"overground":     {ID: "overground", TransportType: ctdf.TransportTypeRail},
	"elizabeth-line": {ID: "elizabeth-line", TransportType: ctdf.TransportTypeRail},
	"national-rail":  {ID: "national-rail", TransportType: ctdf.TransportTypeRail},
}

type routeTask struct {
	mode      modeDefinition
	line      tflapi.Line
	direction string
}

type routeImportFailure struct {
	task        routeTask
	err         error
	preserveErr error
}

type stopResolver struct {
	mu    sync.RWMutex
	cache map[string]*ctdf.Stop
}

func ImportRoutes(ctx context.Context, options Options, source *ctdf.DataSourceReference) (int, error) {
	if options.AppKey == "" {
		return 0, errors.New("TRAVIGO_TFL_API_KEY is required")
	}
	if len(options.Modes) == 0 {
		options.Modes = DefaultModes
	}
	if options.MaxConcurrency < 1 {
		options.MaxConcurrency = 8
	}
	if options.RequestInterval <= 0 {
		options.RequestInterval = 160 * time.Millisecond
	}

	ticker := time.NewTicker(options.RequestInterval)
	defer ticker.Stop()
	client := &tflapi.Client{AppKey: options.AppKey, HTTPClient: &http.Client{Timeout: 30 * time.Second}, Requests: ticker.C}
	resolver := &stopResolver{cache: map[string]*ctdf.Stop{}}
	runTimestamp := fmt.Sprint(time.Now().UnixNano())
	if source == nil {
		source = &ctdf.DataSourceReference{OriginalFormat: string(datasets.DataSetFormatTfLRouteTracks), ProviderName: "Transport for London", ProviderID: "gb-tfl", DatasetID: "gb-tfl-route-tracks"}
	}
	source.Timestamp = runTimestamp
	var importedTracks atomic.Int64

	var importErrors []error
	for _, requestedMode := range options.Modes {
		modeID := strings.TrimSpace(requestedMode)
		mode, exists := modeDefinitions[modeID]
		if !exists {
			importErrors = append(importErrors, fmt.Errorf("unsupported TfL mode %q", modeID))
			continue
		}

		lines, err := client.Lines(ctx, modeID)
		if err != nil {
			importErrors = append(importErrors, fmt.Errorf("list %s lines: %w", modeID, err))
			continue
		}
		log.Info().Str("mode", modeID).Int("lines", len(lines)).Msg("Importing TfL route tracks")

		tasks := make(chan routeTask)
		errCh := make(chan routeImportFailure, len(lines)*2)
		var workers sync.WaitGroup
		for range options.MaxConcurrency {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for task := range tasks {
					count, err := importRouteSequence(ctx, client, resolver, task, source)
					if err != nil {
						errCh <- routeImportFailure{
							task: task, err: err,
							preserveErr: preserveExistingRouteTracks(ctx, task, source),
						}
					} else {
						importedTracks.Add(int64(count))
					}
				}
			}()
		}
		for _, line := range lines {
			for _, direction := range []string{"inbound", "outbound"} {
				tasks <- routeTask{mode: mode, line: line, direction: direction}
			}
		}
		close(tasks)
		workers.Wait()
		close(errCh)

		for failure := range errCh {
			if failure.preserveErr != nil {
				importErrors = append(importErrors, fmt.Errorf("%w; preserve previous tracks: %v", failure.err, failure.preserveErr))
				log.Error().Err(failure.err).Str("mode", modeID).Str("line", failure.task.line.ID).Str("direction", failure.task.direction).Msg("Failed importing TfL route sequence and preserving previous tracks")
				continue
			}
			log.Warn().Err(failure.err).Str("mode", modeID).Str("line", failure.task.line.ID).Str("direction", failure.task.direction).Msg("Skipped TfL route sequence; retained previous tracks when available")
		}
	}
	if err := errors.Join(importErrors...); err != nil {
		return int(importedTracks.Load()), err
	}
	return int(importedTracks.Load()), nil
}

func preserveExistingRouteTracks(ctx context.Context, task routeTask, source *ctdf.DataSourceReference) error {
	filter := bson.M{
		"datasource.datasetid": source.DatasetID,
		"attributes.mode":      task.mode.ID,
		"attributes.line":      task.line.ID,
		"direction":            task.direction,
	}
	cursor, err := database.GetCollection(journeytracks.RouteCollectionName).Find(ctx, filter)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	trackRefs := []string{}
	for cursor.Next(ctx) {
		var leg journeytracks.RouteLeg
		if err := cursor.Decode(&leg); err != nil {
			return err
		}
		trackRefs = append(trackRefs, leg.TrackRef)
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	if _, err := database.GetCollection(journeytracks.RouteCollectionName).UpdateMany(ctx, filter, bson.M{"$set": bson.M{"datasource.timestamp": source.Timestamp}}); err != nil {
		return err
	}
	if len(trackRefs) > 0 {
		_, err = database.GetCollection("journey_tracks").UpdateMany(ctx, bson.M{"primaryidentifier": bson.M{"$in": trackRefs}}, bson.M{"$set": bson.M{"datasource.timestamp": source.Timestamp}})
	}
	return err
}

func importRouteSequence(ctx context.Context, client *tflapi.Client, resolver *stopResolver, task routeTask, source *ctdf.DataSourceReference) (int, error) {
	sequence, err := client.RouteSequence(ctx, task.line.ID, task.direction)
	if err != nil {
		return 0, fmt.Errorf("%s %s %s: %w", task.mode.ID, task.line.ID, task.direction, err)
	}
	decodedTracks := make([][]ctdf.Location, 0, len(sequence.LineStrings))
	for lineStringIndex, encodedTrack := range sequence.LineStrings {
		track, err := tflapi.DecodeLineString(encodedTrack)
		if err != nil {
			return 0, fmt.Errorf("%s %s %s line string %d: %w", task.mode.ID, task.line.ID, task.direction, lineStringIndex, err)
		}
		decodedTracks = append(decodedTracks, track)
	}
	stationLocations := tflStationLocations(sequence.Stations)

	imported := 0
	for routeIndex, route := range sequence.OrderedLineRoutes {
		locations := make([]ctdf.Location, len(route.NaptanIDs))
		routeStopRefs := make([]string, len(route.NaptanIDs))
		routeStopIdentifiers := make([][]string, len(route.NaptanIDs))
		for index, naptanID := range route.NaptanIDs {
			formattedID := fmt.Sprintf(ctdf.GBStopIDFormat, naptanID)
			stop, err := resolver.resolve(ctx, naptanID)
			if err != nil {
				return imported, fmt.Errorf("%s %s %s route %d stop %s: %w", task.mode.ID, task.line.ID, task.direction, routeIndex, naptanID, err)
			}
			identifiers := []string{formattedID}
			routeStopRefs[index] = formattedID
			if stop != nil {
				identifiers = append(identifiers, stop.GetAllStopIDs()...)
				routeStopRefs[index] = stop.PrimaryIdentifier
			}
			location, found := stationLocations[naptanID]
			if !found && stop != nil && stop.Location != nil && len(stop.Location.Coordinates) >= 2 {
				location, found = *stop.Location, true
			}
			if !found {
				return imported, fmt.Errorf("%s %s %s route %d stop %s has no TfL or CTDF location", task.mode.ID, task.line.ID, task.direction, routeIndex, naptanID)
			}
			locations[index] = location
			routeStopIdentifiers[index] = identifiers
		}
		legTracks, _, err := tflapi.SplitBestTrack(locations, decodedTracks)
		if err != nil {
			return imported, fmt.Errorf("%s %s %s route %d: %w", task.mode.ID, task.line.ID, task.direction, routeIndex, err)
		}

		routeKey := routeIdentifier(task.mode.ID, task.line.ID, task.direction, route.NaptanIDs)
		trackModels := make([]mongo.WriteModel, 0, len(legTracks))
		metadataModels := make([]mongo.WriteModel, 0, len(legTracks))
		now := time.Now()
		for legIndex, legTrack := range legTracks {
			trackID := fmt.Sprintf("tfl-route-track:%s:%d", routeKey, legIndex)
			journeyTrack := ctdf.JourneyTrack{
				PrimaryIdentifier:    trackID,
				Track:                legTrack,
				DataSource:           source,
				ModificationDateTime: now,
			}
			metadata := journeytracks.RouteLeg{
				PrimaryIdentifier: trackID, RouteKey: routeKey,
				ServiceNameKeys: lineKeys(task.line), TransportType: task.mode.TransportType,
				Direction: task.direction, RouteName: route.Name,
				ExternalStopRefs: route.NaptanIDs, RouteStopRefs: routeStopRefs, RouteStopIdentifiers: routeStopIdentifiers, LegIndex: legIndex,
				OriginStopRefs: routeStopIdentifiers[legIndex], DestinationStopRefs: routeStopIdentifiers[legIndex+1],
				TrackRef: trackID, DataSource: source,
				Attributes: map[string]string{"mode": task.mode.ID, "line": task.line.ID, "line_name": task.line.Name},
			}
			trackModels = append(trackModels, mongo.NewUpdateOneModel().SetFilter(bson.M{"primaryidentifier": trackID}).SetUpdate(bson.M{
				"$set":         bson.M{"track": journeyTrack.Track, "datasource": journeyTrack.DataSource, "modificationdatetime": now},
				"$setOnInsert": bson.M{"primaryidentifier": trackID, "creationdatetime": now},
			}).SetUpsert(true))
			metadataModels = append(metadataModels, mongo.NewUpdateOneModel().SetFilter(bson.M{"primaryidentifier": trackID}).SetUpdate(bson.M{"$set": metadata}).SetUpsert(true))
		}
		if len(trackModels) > 0 {
			if _, err := database.GetCollection("journey_tracks").BulkWrite(ctx, trackModels); err != nil {
				return imported, fmt.Errorf("store %s: %w", routeKey, err)
			}
			if _, err := database.GetCollection(journeytracks.RouteCollectionName).BulkWrite(ctx, metadataModels); err != nil {
				return imported, fmt.Errorf("store %s metadata: %w", routeKey, err)
			}
			imported += len(trackModels)
		}
	}
	return imported, nil
}

func tflStationLocations(stations []tflapi.Station) map[string]ctdf.Location {
	locations := make(map[string]ctdf.Location, len(stations))
	for _, station := range stations {
		if station.Latitude < -90 || station.Latitude > 90 || station.Longitude < -180 || station.Longitude > 180 {
			continue
		}
		locations[station.ID] = ctdf.Location{Type: "Point", Coordinates: []float64{station.Longitude, station.Latitude}}
	}
	return locations
}

func routeIdentifier(modeID, lineID, direction string, naptanIDs []string) string {
	hash := sha256.Sum256([]byte(strings.Join(naptanIDs, "\x00")))
	return fmt.Sprintf("%s:%s:%s:%x", modeID, lineID, direction, hash[:8])
}

func lineKeys(line tflapi.Line) []string {
	keys := []string{strings.ToLower(strings.TrimSpace(line.ID)), strings.ToLower(strings.TrimSpace(line.Name))}
	sort.Strings(keys)
	if len(keys) == 2 && keys[0] == keys[1] {
		keys = keys[:1]
	}
	return keys
}

func (resolver *stopResolver) resolve(ctx context.Context, naptanID string) (*ctdf.Stop, error) {
	resolver.mu.RLock()
	stop := resolver.cache[naptanID]
	resolver.mu.RUnlock()
	if stop != nil {
		return stop, nil
	}

	identifier := fmt.Sprintf(ctdf.GBStopIDFormat, naptanID)
	if err := database.GetCollection("stops").FindOne(ctx, bson.M{"$or": bson.A{bson.M{"primaryidentifier": identifier}, bson.M{"otheridentifiers": identifier}}}).Decode(&stop); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}
	if stop == nil {
		var group *ctdf.StopGroup
		if err := database.GetCollection("stop_groups").FindOne(ctx, bson.M{"otheridentifiers": identifier}).Decode(&group); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, err
		}
		if group != nil {
			if err := database.GetCollection("stops").FindOne(ctx, bson.M{"associations.associatedidentifier": group.PrimaryIdentifier}).Decode(&stop); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
				return nil, err
			}
		}
	}
	if stop == nil {
		return nil, nil
	}
	resolver.mu.Lock()
	resolver.cache[naptanID] = stop
	resolver.mu.Unlock()
	return stop, nil
}
