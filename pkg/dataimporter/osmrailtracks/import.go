package osmrailtracks

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/journeytracks"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const defaultLinkedDataset = "gb-nationalrail-timetable"

type Format struct {
	graph *railGraph
}

type routePattern struct {
	key         string
	stopRefs    []string
	serviceRefs []string
}

type snappedStop struct {
	graphNode int
}

type cachedTrack struct {
	locations []ctdf.Location
	err       error
}

func (format *Format) ParseFile(reader io.Reader) error {
	graph, err := parseRailGraph(reader)
	if err != nil {
		return err
	}
	format.graph = graph
	log.Info().Int("nodes", len(graph.nodes)).Msg("Built OpenStreetMap National Rail routing graph")
	return nil
}

func (format *Format) Import(dataset datasets.DataSet, source *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	if format.graph == nil || len(format.graph.nodes) == 0 {
		return datasets.DataImportReport{}, errors.New("OpenStreetMap rail graph has not been parsed")
	}
	if !dataset.SupportedObjects.JourneyTracks {
		return datasets.DataImportReport{}, errors.New("OSM rail track format requires journeytracks support")
	}

	linkedDataset := strings.TrimSpace(dataset.LinkedDataset)
	if linkedDataset == "" {
		linkedDataset = defaultLinkedDataset
	}
	maximumSnapDistance := float64(defaultSnapDistanceM)
	if configured := strings.TrimSpace(dataset.CustomConfig["maximumsnapdistancemetres"]); configured != "" {
		parsed, err := strconv.ParseFloat(configured, 64)
		if err != nil || parsed <= 0 {
			return datasets.DataImportReport{}, fmt.Errorf("invalid maximumsnapdistancemetres %q", configured)
		}
		maximumSnapDistance = parsed
	}

	ctx := context.Background()
	patterns, err := loadRoutePatterns(ctx, linkedDataset)
	if err != nil {
		return datasets.DataImportReport{}, err
	}
	if len(patterns) == 0 {
		return datasets.DataImportReport{}, fmt.Errorf("no journey patterns found for linked dataset %q", linkedDataset)
	}
	stops, err := loadPatternStops(ctx, patterns)
	if err != nil {
		return datasets.DataImportReport{}, err
	}

	snapped := make(map[string]snappedStop, len(stops))
	for stopRef, stop := range stops {
		if stop.Location == nil || len(stop.Location.Coordinates) < 2 {
			continue
		}
		index, _, found := format.graph.nearestNode(locationPoint(*stop.Location), maximumSnapDistance)
		if found {
			snapped[stopRef] = snappedStop{graphNode: index}
		}
	}

	trackCache := map[[2]int]cachedTrack{}
	trackDocuments := map[string]ctdf.JourneyTrack{}
	routeDocuments := []journeytracks.RouteLeg{}
	failedPatterns := 0
	now := time.Now()
	for _, pattern := range patterns {
		patternTracks := make([]ctdf.JourneyTrack, 0, len(pattern.stopRefs)-1)
		patternFailed := false
		failureReason := ""
		for index := 1; index < len(pattern.stopRefs); index++ {
			origin, originOK := snapped[pattern.stopRefs[index-1]]
			destination, destinationOK := snapped[pattern.stopRefs[index]]
			if !originOK || !destinationOK {
				patternFailed = true
				failureReason = fmt.Sprintf("could not snap %s or %s to rail graph", pattern.stopRefs[index-1], pattern.stopRefs[index])
				break
			}
			cacheKey := [2]int{origin.graphNode, destination.graphNode}
			cached, exists := trackCache[cacheKey]
			if !exists {
				reverseKey := [2]int{destination.graphNode, origin.graphNode}
				if reverse, reverseExists := trackCache[reverseKey]; reverseExists && reverse.err == nil {
					cached.locations = reverseTrack(reverse.locations)
				} else {
					cached.locations, cached.err = format.graph.route(origin.graphNode, destination.graphNode)
				}
				trackCache[cacheKey] = cached
			}
			if cached.err != nil || len(cached.locations) < 2 {
				patternFailed = true
				if cached.err != nil {
					failureReason = cached.err.Error()
				} else {
					failureReason = fmt.Sprintf("route %s to %s has fewer than two points", pattern.stopRefs[index-1], pattern.stopRefs[index])
				}
				break
			}
			trackID := trackIdentifier(pattern.stopRefs[index-1], pattern.stopRefs[index])
			patternTracks = append(patternTracks, ctdf.JourneyTrack{
				PrimaryIdentifier: trackID,
				Track:             cached.locations,
				DataSource:        source,
			})
		}

		if patternFailed {
			failedPatterns++
			if err := preserveExistingPattern(ctx, pattern.key, source); err != nil {
				return datasets.DataImportReport{}, fmt.Errorf("preserve OSM rail pattern %s: %w", pattern.key, err)
			}
			log.Warn().Str("route_key", pattern.key).Str("reason", failureReason).Strs("stops", pattern.stopRefs).Msg("Skipped OSM rail journey pattern; retained previous tracks when available")
			continue
		}

		stopIdentifiers := make([][]string, len(pattern.stopRefs))
		for index, stopRef := range pattern.stopRefs {
			stopIdentifiers[index] = stops[stopRef].GetAllStopIDs()
		}
		for legIndex, track := range patternTracks {
			track.ModificationDateTime = now
			trackDocuments[track.PrimaryIdentifier] = track
			routeDocuments = append(routeDocuments, journeytracks.RouteLeg{
				PrimaryIdentifier:    fmt.Sprintf("osm-rail-route-leg:%s:%d", pattern.key, legIndex),
				RouteKey:             pattern.key,
				ServiceRefs:          pattern.serviceRefs,
				TransportType:        ctdf.TransportTypeRail,
				RouteStopRefs:        pattern.stopRefs,
				RouteStopIdentifiers: stopIdentifiers,
				LegIndex:             legIndex,
				OriginStopRefs:       stopIdentifiers[legIndex],
				DestinationStopRefs:  stopIdentifiers[legIndex+1],
				TrackRef:             track.PrimaryIdentifier,
				DataSource:           source,
				Attributes: map[string]string{
					"source":         "openstreetmap",
					"license":        "ODbL-1.0",
					"linked_dataset": linkedDataset,
				},
			})
		}
	}

	if len(routeDocuments) == 0 {
		return datasets.DataImportReport{}, fmt.Errorf("none of %d National Rail journey patterns could be routed over OSM", len(patterns))
	}
	if err := storeTracks(ctx, trackDocuments, now); err != nil {
		return datasets.DataImportReport{}, err
	}
	if err := storeRouteLegs(ctx, routeDocuments); err != nil {
		return datasets.DataImportReport{}, err
	}
	log.Info().
		Int("patterns", len(patterns)-failedPatterns).
		Int("failed_patterns", failedPatterns).
		Int("tracks", len(trackDocuments)).
		Msg("Imported OpenStreetMap National Rail journey tracks")
	return datasets.DataImportReport{ImportedJourneyTracks: len(trackDocuments)}, nil
}

func loadRoutePatterns(ctx context.Context, datasetID string) ([]routePattern, error) {
	cursor, err := database.GetCollection("journeys").Find(
		ctx,
		bson.M{"datasource.datasetid": datasetID},
		options.Find().SetProjection(bson.M{"path.originstopref": 1, "path.destinationstopref": 1, "serviceref": 1}),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	type patternAccumulator struct {
		stopRefs    []string
		serviceRefs map[string]struct{}
	}
	byKey := map[string]*patternAccumulator{}
	for cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return nil, err
		}
		stopRefs := journeytracks.JourneyStops(journey.Path)
		if len(stopRefs) < 2 || journey.ServiceRef == "" {
			continue
		}
		key := routeIdentifier(stopRefs)
		pattern := byKey[key]
		if pattern == nil {
			pattern = &patternAccumulator{stopRefs: stopRefs, serviceRefs: map[string]struct{}{}}
			byKey[key] = pattern
		}
		pattern.serviceRefs[journey.ServiceRef] = struct{}{}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	patterns := make([]routePattern, 0, len(keys))
	for _, key := range keys {
		accumulator := byKey[key]
		serviceRefs := make([]string, 0, len(accumulator.serviceRefs))
		for serviceRef := range accumulator.serviceRefs {
			serviceRefs = append(serviceRefs, serviceRef)
		}
		sort.Strings(serviceRefs)
		patterns = append(patterns, routePattern{key: key, stopRefs: accumulator.stopRefs, serviceRefs: serviceRefs})
	}
	return patterns, nil
}

func loadPatternStops(ctx context.Context, patterns []routePattern) (map[string]*ctdf.Stop, error) {
	refSet := map[string]struct{}{}
	for _, pattern := range patterns {
		for _, stopRef := range pattern.stopRefs {
			refSet[stopRef] = struct{}{}
		}
	}
	refs := make([]string, 0, len(refSet))
	for ref := range refSet {
		refs = append(refs, ref)
	}

	cursor, err := database.GetCollection("stops").Find(ctx, bson.M{"$or": bson.A{
		bson.M{"primaryidentifier": bson.M{"$in": refs}},
		bson.M{"otheridentifiers": bson.M{"$in": refs}},
	}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	stops := map[string]*ctdf.Stop{}
	for cursor.Next(ctx) {
		stop := &ctdf.Stop{}
		if err := cursor.Decode(stop); err != nil {
			return nil, err
		}
		for _, identifier := range stop.GetAllStopIDs() {
			if _, wanted := refSet[identifier]; wanted {
				stops[identifier] = stop
			}
		}
	}
	return stops, cursor.Err()
}

func storeTracks(ctx context.Context, tracks map[string]ctdf.JourneyTrack, now time.Time) error {
	models := make([]mongo.WriteModel, 0, 500)
	flush := func() error {
		if len(models) == 0 {
			return nil
		}
		_, err := database.GetCollection("journey_tracks").BulkWrite(ctx, models)
		models = models[:0]
		return err
	}
	for _, track := range tracks {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"primaryidentifier": track.PrimaryIdentifier}).
			SetUpdate(bson.M{
				"$set":         bson.M{"track": track.Track, "datasource": track.DataSource, "modificationdatetime": now},
				"$setOnInsert": bson.M{"primaryidentifier": track.PrimaryIdentifier, "creationdatetime": now},
			}).
			SetUpsert(true))
		if len(models) == cap(models) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func storeRouteLegs(ctx context.Context, legs []journeytracks.RouteLeg) error {
	models := make([]mongo.WriteModel, 0, 500)
	flush := func() error {
		if len(models) == 0 {
			return nil
		}
		_, err := database.GetCollection(journeytracks.RouteCollectionName).BulkWrite(ctx, models)
		models = models[:0]
		return err
	}
	for _, leg := range legs {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"primaryidentifier": leg.PrimaryIdentifier}).
			SetUpdate(bson.M{"$set": leg}).
			SetUpsert(true))
		if len(models) == cap(models) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func preserveExistingPattern(ctx context.Context, routeKey string, source *ctdf.DataSourceReference) error {
	filter := bson.M{"datasource.datasetid": source.DatasetID, "routekey": routeKey}
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
	if len(trackRefs) == 0 {
		return nil
	}
	if _, err := database.GetCollection(journeytracks.RouteCollectionName).UpdateMany(ctx, filter, bson.M{"$set": bson.M{"datasource.timestamp": source.Timestamp}}); err != nil {
		return err
	}
	_, err = database.GetCollection("journey_tracks").UpdateMany(
		ctx,
		bson.M{"primaryidentifier": bson.M{"$in": trackRefs}},
		bson.M{"$set": bson.M{"datasource.timestamp": source.Timestamp}},
	)
	return err
}

func routeIdentifier(stopRefs []string) string {
	hash := sha256.Sum256([]byte(strings.Join(stopRefs, "\x00")))
	return fmt.Sprintf("osm-national-rail:%x", hash[:12])
}

func trackIdentifier(originStopRef, destinationStopRef string) string {
	hash := sha256.Sum256([]byte(originStopRef + "\x00" + destinationStopRef))
	return fmt.Sprintf("osm-rail-track:%x", hash[:12])
}

func reverseTrack(track []ctdf.Location) []ctdf.Location {
	reversed := make([]ctdf.Location, len(track))
	for index := range track {
		reversed[len(track)-1-index] = track[index]
	}
	return reversed
}
