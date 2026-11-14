package journeytracks

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const journeyPathUpdateBatchSize = 1000

type serviceJourneyStats struct {
	Scanned int
	Updated int
	Batches int
}

// ApplyDataset replaces track references previously owned by datasetID and
// applies the dataset's current route tracks to matching scheduled journeys.
// Existing geometry from any other dataset remains authoritative.
func ApplyDataset(ctx context.Context, datasetID string, timestamp string) error {
	return applyDataset(ctx, datasetID, timestamp, "", database.JourneysCollectionName, false, []string{
		database.JourneysCollectionName,
		database.JourneysRawCollectionName,
	})
}

// ApplyDatasetForOperator applies a dataset's route tracks only to services
// operated by operatorRef.
func ApplyDatasetForOperator(ctx context.Context, datasetID string, timestamp string, operatorRef string) error {
	return applyDataset(ctx, datasetID, timestamp, operatorRef, database.JourneysCollectionName, false, []string{
		database.JourneysCollectionName,
		database.JourneysRawCollectionName,
	})
}

// ApplyOSMRailDataset applies tracks to raw journeys, keeping previous tracks as a fallback.
func ApplyOSMRailDataset(ctx context.Context, datasetID string, timestamp string) error {
	return applyDataset(ctx, datasetID, timestamp, "", database.JourneysRawCollectionName, true, []string{database.JourneysRawCollectionName})
}

func applyDataset(ctx context.Context, datasetID string, timestamp string, operatorRef string, readCollectionName string, keepFallback bool, writeCollectionNames []string) error {
	routes, err := Load(ctx, Filter{DatasetID: datasetID, Timestamp: timestamp})
	if err != nil {
		return err
	}
	ownedTrackRefs, err := datasetTrackRefs(ctx, datasetID)
	if err != nil {
		return err
	}
	// Performance Review [Set O] [PR099] [P2] (batch): Refresh clears every owned path reference,
	// writes those paths, then reads and writes matching journeys again to restore
	// tracks, even where geometry is unchanged. Compute the replacement diff in one
	// pass, clearing only obsolete references while preserving other datasets' tracks.
	// Stage/fence publication so failure after clearing cannot leave live journeys
	// without usable tracks; measure paths rewritten versus paths actually changed.
	if !keepFallback {
		if err := clearOwnedJourneyTrackRefs(ctx, ownedTrackRefs, writeCollectionNames); err != nil {
			return err
		}
		ownedTrackRefs = nil
	}

	routesByServiceName := map[string][]Route{}
	routesByServiceRef := map[string][]Route{}
	for _, route := range routes {
		for _, serviceNameKey := range route.Metadata.ServiceNameKeys {
			key := serviceKey(route.Metadata.TransportType, serviceNameKey)
			routesByServiceName[key] = append(routesByServiceName[key], route)
		}
		for _, serviceRef := range route.Metadata.ServiceRefs {
			routesByServiceRef[serviceRef] = append(routesByServiceRef[serviceRef], route)
		}
	}

	services, err := loadServices(ctx, operatorRef)
	if err != nil {
		return err
	}
	log.Info().Int("services", len(services)).Int("routes", len(routes)).Msg("Loaded journey track enrichment inputs")
	totalScanned := 0
	totalUpdated := 0
	changedRawDatasets := map[string]struct{}{}
	for serviceIndex, service := range services {
		candidates := append([]Route{}, routesByServiceName[serviceKey(service.TransportType, service.LineName())]...)
		serviceRefs := append([]string{service.PrimaryIdentifier}, service.OtherIdentifiers...)
		for _, serviceRef := range serviceRefs {
			candidates = appendUniqueRoutes(candidates, routesByServiceRef[serviceRef]...)
		}
		if len(candidates) == 0 {
			continue
		}
		startedAt := time.Now()
		stats, err := applyServiceJourneys(ctx, serviceRefs, candidates, readCollectionName, keepFallback, ownedTrackRefs, writeCollectionNames, changedRawDatasets)
		if err != nil {
			return err
		}
		totalScanned += stats.Scanned
		totalUpdated += stats.Updated
		log.Info().
			Int("service", serviceIndex+1).
			Int("services", len(services)).
			Str("service_ref", service.PrimaryIdentifier).
			Int("journeys_scanned", stats.Scanned).
			Int("journeys_updated", stats.Updated).
			Int("write_batches", stats.Batches).
			Dur("elapsed", time.Since(startedAt)).
			Msg("Applied journey tracks for service")
	}
	// OSM track enrichment changes raw journeys without reimporting their
	// source datasets. Make those datasets eligible for publication again.
	if len(changedRawDatasets) > 0 {
		datasetIDs := make([]string, 0, len(changedRawDatasets))
		for datasetID := range changedRawDatasets {
			datasetIDs = append(datasetIDs, datasetID)
		}
		_, err := database.GetCollection("dataset_versions").UpdateMany(ctx,
			bson.M{"dataset": bson.M{"$in": datasetIDs}},
			bson.M{"$max": bson.M{"rawjourneysmodified": time.Now()}},
		)
		if err != nil {
			return fmt.Errorf("mark enriched raw journey datasets for publication: %w", err)
		}
	}
	log.Info().Int("services", len(services)).Int("journeys_scanned", totalScanned).Int("journeys_updated", totalUpdated).Msg("Completed journey track enrichment")
	return nil
}

// loadServices fully consumes and closes the Mongo cursor before per-service
// journey processing starts. Individual services can take hours to enrich, so
// retaining this outer cursor made it expire while it sat idle.
func loadServices(ctx context.Context, operatorRef string) ([]ctdf.Service, error) {
	filter := bson.M{}
	if operatorRef != "" {
		filter["operatorref"] = operatorRef
	}
	cursor, err := database.GetCollection("services").Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	services := []ctdf.Service{}
	for cursor.Next(ctx) {
		var service ctdf.Service
		if err := cursor.Decode(&service); err != nil {
			cursor.Close(ctx)
			return nil, err
		}
		services = append(services, service)
	}
	cursorErr := cursor.Err()
	closeErr := cursor.Close(ctx)
	if cursorErr != nil {
		return nil, cursorErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return services, nil
}

func appendUniqueRoutes(destination []Route, additions ...Route) []Route {
	seen := map[string]struct{}{}
	for _, route := range destination {
		seen[route.Metadata.RouteKey] = struct{}{}
	}
	for _, route := range additions {
		if _, exists := seen[route.Metadata.RouteKey]; exists {
			continue
		}
		seen[route.Metadata.RouteKey] = struct{}{}
		destination = append(destination, route)
	}
	return destination
}

func serviceKey(transportType ctdf.TransportType, serviceName string) string {
	return string(transportType) + "\x00" + strings.ToLower(strings.TrimSpace(serviceName))
}

func datasetTrackRefs(ctx context.Context, datasetID string) (map[string]struct{}, error) {
	return loadTrackRefs(ctx, bson.M{"datasource.datasetid": datasetID})
}

func loadTrackRefs(ctx context.Context, filter bson.M) (map[string]struct{}, error) {
	cursor, err := database.GetCollection("journey_tracks").Find(ctx, filter, options.Find().SetProjection(bson.M{"primaryidentifier": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	refs := map[string]struct{}{}
	for cursor.Next(ctx) {
		var track ctdf.JourneyTrack
		if err := cursor.Decode(&track); err != nil {
			return nil, err
		}
		refs[track.PrimaryIdentifier] = struct{}{}
	}
	return refs, cursor.Err()
}

func clearOwnedJourneyTrackRefs(ctx context.Context, owned map[string]struct{}, writeCollectionNames []string) error {
	if len(owned) == 0 {
		return nil
	}
	refs := make([]string, 0, len(owned))
	for ref := range owned {
		refs = append(refs, ref)
	}
	for start := 0; start < len(refs); start += 5000 {
		end := min(start+5000, len(refs))
		trackRefs := bson.M{"$in": refs[start:end]}
		for _, collectionName := range writeCollectionNames {
			_, err := database.GetCollection(collectionName).UpdateMany(
				ctx,
				bson.M{"calls.outgoing.trackref": trackRefs},
				bson.M{"$unset": bson.M{"calls.$[call].outgoing.trackref": ""}},
				options.Update().SetArrayFilters(options.ArrayFilters{Filters: []interface{}{
					bson.M{"call.outgoing.trackref": trackRefs},
				}}),
			)
			if err != nil {
				return fmt.Errorf("clear owned journey track references in %s: %w", collectionName, err)
			}
		}
	}
	return nil
}

func applyServiceJourneys(ctx context.Context, serviceRefs []string, candidates []Route, readCollectionName string, keepFallback bool, ownedTrackRefs map[string]struct{}, writeCollectionNames []string, changedRawDatasets map[string]struct{}) (serviceJourneyStats, error) {
	stats := serviceJourneyStats{}
	candidatesByEndpoints := indexRoutesByEndpoints(candidates)
	cursor, err := database.GetCollection(readCollectionName).Find(ctx,
		bson.M{"serviceref": bson.M{"$in": serviceRefs}},
		options.Find().SetProjection(enrichmentJourneyProjection),
	)
	if err != nil {
		return stats, err
	}
	defer cursor.Close(ctx)
	models := []mongo.WriteModel{}
	flush := func() error {
		if len(models) == 0 {
			return nil
		}
		if err := bulkWriteJourneyPaths(ctx, models, writeCollectionNames); err != nil {
			return err
		}
		stats.Batches++
		models = models[:0]
		return nil
	}
	startedAt := time.Now()
	for cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return stats, err
		}
		stats.Scanned++
		var changed bool
		var err error
		if keepFallback {
			changed, err = applyOSMRailRoutes(ctx, &journey, candidatesByEndpoints, ownedTrackRefs)
		} else {
			changed, err = applyIndexedRoutes(ctx, &journey, candidatesByEndpoints)
		}
		if err != nil {
			return stats, err
		}
		if changed {
			if keepFallback && journey.DataSource != nil && journey.DataSource.DatasetID != "" {
				changedRawDatasets[journey.DataSource.DatasetID] = struct{}{}
			}
			model, err := journeyPathUpdate(&journey)
			if err != nil {
				return stats, err
			}
			models = append(models, model)
			stats.Updated++
			if len(models) == journeyPathUpdateBatchSize {
				if err := flush(); err != nil {
					return stats, err
				}
			}
		}
		if stats.Scanned%100000 == 0 {
			log.Info().Int("journeys_scanned", stats.Scanned).Int("journeys_updated", stats.Updated).Int("write_batches", stats.Batches).Dur("elapsed", time.Since(startedAt)).Msg("Journey track service progress")
		}
	}
	if err := cursor.Err(); err != nil {
		return stats, err
	}
	if err := flush(); err != nil {
		return stats, err
	}
	return stats, nil
}

func applyIndexedRoutes(ctx context.Context, journey *ctdf.Journey, candidates map[string][]Route) (bool, error) {
	changed := false
	for calls := range ContiguousCallPaths(journey) {
		stops := JourneyCallStops(calls)
		if len(stops) < 2 {
			continue
		}
		updated, err := applyBestRouteToCalls(ctx, calls, candidates[routeEndpointKey(stops[0], stops[len(stops)-1])])
		if err != nil {
			return false, err
		}
		changed = changed || updated
	}
	return changed, nil
}

func applyOSMRailRoutes(ctx context.Context, journey *ctdf.Journey, candidates map[string][]Route, owned map[string]struct{}) (bool, error) {
	changed := false
	for calls := range ContiguousCallPaths(journey) {
		fallback := map[*ctdf.JourneyCall]string{}
		for _, call := range calls[:len(calls)-1] {
			if call.Outgoing != nil {
				if _, ok := owned[call.Outgoing.TrackRef]; ok {
					fallback[call] = call.Outgoing.TrackRef
					call.Outgoing.TrackRef = ""
					call.Outgoing.Track = nil
				}
			}
		}
		stops := JourneyCallStops(calls)
		updated := false
		if len(stops) >= 2 {
			var err error
			updated, err = applyBestRouteToCalls(ctx, calls, candidates[routeEndpointKey(stops[0], stops[len(stops)-1])])
			if err != nil {
				for call, ref := range fallback {
					call.Outgoing.TrackRef = ref
				}
				return false, err
			}
		}
		for call, ref := range fallback {
			if call.Outgoing == nil {
				call.Outgoing = &ctdf.JourneyLeg{}
			}
			if call.Outgoing.TrackRef == "" {
				call.Outgoing.TrackRef = ref
			}
		}
		changed = changed || updated
	}
	return changed, nil
}

func bulkWriteJourneyPaths(ctx context.Context, models []mongo.WriteModel, collectionNames []string) error {
	for _, collectionName := range collectionNames {
		if _, err := database.GetCollection(collectionName).BulkWrite(ctx, models); err != nil {
			return fmt.Errorf("update journey paths in %s: %w", collectionName, err)
		}
	}
	return nil
}

var enrichmentJourneyProjection = bson.M{
	"primaryidentifier":    1,
	"modificationdatetime": 1,
	"datasource":           1,
	"trackref":             1,
	"calls":                1,
	"serviceref":           1,
}

func indexRoutesByEndpoints(routes []Route) map[string][]Route {
	// Performance Review [Set O] [PR100] [P2] (batch): This enumerates every ordered stop pair
	// and alias combination, then deduplicates route entries per pair. Work and possible
	// index size grow quadratically in route length and with the product of alias counts.
	// Index routes by individual canonical stops and intersect endpoint candidates, or
	// build/cache only endpoint pairs requested by journeys. Preserve ordered subsequence
	// and repeated-stop matching downstream.
	index := map[string][]Route{}
	seen := map[string]map[string]struct{}{}
	for _, route := range routes {
		stops := route.Metadata.RouteStopIdentifiers
		for originIndex := 0; originIndex+1 < len(stops); originIndex++ {
			for destinationIndex := originIndex + 1; destinationIndex < len(stops); destinationIndex++ {
				for _, originRef := range stops[originIndex] {
					for _, destinationRef := range stops[destinationIndex] {
						key := routeEndpointKey(originRef, destinationRef)
						if seen[key] == nil {
							seen[key] = map[string]struct{}{}
						}
						if _, exists := seen[key][route.Metadata.RouteKey]; exists {
							continue
						}
						seen[key][route.Metadata.RouteKey] = struct{}{}
						index[key] = append(index[key], route)
					}
				}
			}
		}
	}
	return index
}

func routeEndpointKey(originStopRef, destinationStopRef string) string {
	return originStopRef + "\x00" + destinationStopRef
}

func journeyPathUpdate(journey *ctdf.Journey) (mongo.WriteModel, error) {
	if err := journey.ValidatePath(); err != nil {
		return nil, fmt.Errorf("validate updated journey %s: %w", journey.PrimaryIdentifier, err)
	}
	filter := bson.M{"primaryidentifier": journey.PrimaryIdentifier}
	if journey.DataSource != nil {
		if journey.DataSource.DatasetID != "" {
			filter["datasource.datasetid"] = journey.DataSource.DatasetID
		}
		if journey.DataSource.Timestamp != "" {
			filter["datasource.timestamp"] = journey.DataSource.Timestamp
		}
	}
	if !journey.ModificationDateTime.IsZero() {
		filter["modificationdatetime"] = journey.ModificationDateTime
	}
	return mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(bson.M{"$set": bson.M{"calls": journey.Calls}}), nil
}

func ApplyBestRoute(ctx context.Context, journey *ctdf.Journey, candidates []Route) (bool, error) {
	if journey.TrackRef != "" || len(journey.Track) > 0 {
		return false, nil
	}
	changed := false
	for calls := range ContiguousCallPaths(journey) {
		updated, err := applyBestRouteToCalls(ctx, calls, candidates)
		if err != nil {
			return false, err
		}
		changed = changed || updated
	}
	return changed, nil
}

func applyBestRouteToCalls(ctx context.Context, calls []*ctdf.JourneyCall, candidates []Route) (bool, error) {
	journeyStops := JourneyCallStops(calls)
	if len(journeyStops) < 2 {
		return false, nil
	}
	type routeMatch struct {
		route     Route
		positions []int
	}
	matches := []routeMatch{}
	hasPassingPath := false
	for _, call := range calls[:len(calls)-1] {
		if call.Outgoing != nil && len(call.Outgoing.PassingPath) > 0 {
			hasPassingPath = true
			break
		}
	}
	for _, candidate := range candidates {
		if positions, ok := MatchStops(journeyStops, candidate.Metadata.RouteStopIdentifiers); ok {
			if (hasPassingPath || len(candidate.Metadata.PassingPaths) > 0) &&
				!matchesPassingCallPaths(calls, positions, candidate.Metadata.PassingPaths) {
				continue
			}
			matches = append(matches, routeMatch{route: candidate, positions: positions})
		}
	}
	if len(matches) == 0 {
		return false, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		iExtra := len(matches[i].route.Metadata.RouteStopIdentifiers) - len(journeyStops)
		jExtra := len(matches[j].route.Metadata.RouteStopIdentifiers) - len(journeyStops)
		if iExtra != jExtra {
			return iExtra < jExtra
		}
		return matches[i].route.Metadata.RouteKey < matches[j].route.Metadata.RouteKey
	})
	selected := matches[0]
	changed := false
	for pathIndex, call := range calls[:len(calls)-1] {
		if call.Outgoing != nil && (call.Outgoing.TrackRef != "" || len(call.Outgoing.Track) > 0) || pathIndex+1 >= len(selected.positions) {
			continue
		}
		start, end := selected.positions[pathIndex], selected.positions[pathIndex+1]
		if end <= start {
			continue
		}
		if end == start+1 {
			if leg, ok := selected.route.Legs[start]; ok {
				if call.Outgoing == nil {
					call.Outgoing = &ctdf.JourneyLeg{}
				}
				call.Outgoing.TrackRef = leg.PrimaryIdentifier
				changed = true
			}
			continue
		}
		trackRef, err := storeCombinedTrack(ctx, selected.route, start, end)
		if err != nil {
			return false, err
		}
		if trackRef != "" {
			if call.Outgoing == nil {
				call.Outgoing = &ctdf.JourneyLeg{}
			}
			call.Outgoing.TrackRef = trackRef
			changed = true
		}
	}
	return changed, nil
}

func matchesPassingCallPaths(calls []*ctdf.JourneyCall, positions []int, passingPaths [][]string) bool {
	if len(calls)-1 != len(passingPaths) || len(calls) != len(positions) {
		return false
	}
	for i, call := range calls[:len(calls)-1] {
		var passingPath []string
		if call.Outgoing != nil {
			passingPath = call.Outgoing.PassingPath
		}
		if positions[i] != i || !slices.Equal(passingPath, passingPaths[i]) {
			return false
		}
	}
	return positions[len(calls)-1] == len(calls)-1
}

func storeCombinedTrack(ctx context.Context, route Route, start, end int) (string, error) {
	// Performance Review [Set F] [PR101] [P2] (batch): Journeys needing the same express segment
	// each concatenate its geometry and synchronously upsert the same track again.
	// Deduplicate by route generation/start/end within the enrichment run and batch unique
	// tracks; persist them successfully before publishing journey refs. Bound the cache
	// and include geometry/version changes so reuse cannot retain a previous route shape.
	combined := []ctdf.Location{}
	for legIndex := start; legIndex < end; legIndex++ {
		leg, ok := route.Legs[legIndex]
		if !ok || len(leg.Track) < 2 {
			return "", nil
		}
		for pointIndex, point := range leg.Track {
			if pointIndex == 0 && len(combined) > 0 && samePoint(combined[len(combined)-1], point) {
				continue
			}
			combined = append(combined, point)
		}
	}
	trackRef := fmt.Sprintf("journey-track-composite:%s:%d-%d", route.Metadata.RouteKey, start, end)
	now := time.Now()
	_, err := database.GetCollection("journey_tracks").UpdateOne(ctx, bson.M{"primaryidentifier": trackRef}, bson.M{
		"$set":         bson.M{"track": combined, "datasource": route.Metadata.DataSource, "modificationdatetime": now},
		"$setOnInsert": bson.M{"primaryidentifier": trackRef, "creationdatetime": now},
	}, options.Update().SetUpsert(true))
	return trackRef, err
}

func samePoint(a, b ctdf.Location) bool {
	return len(a.Coordinates) >= 2 && len(b.Coordinates) >= 2 && a.Coordinates[0] == b.Coordinates[0] && a.Coordinates[1] == b.Coordinates[1]
}
