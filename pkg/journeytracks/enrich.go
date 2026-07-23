package journeytracks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ApplyDataset replaces track references previously owned by datasetID and
// applies the dataset's current route tracks to matching scheduled journeys.
// Existing geometry from any other dataset remains authoritative.
func ApplyDataset(ctx context.Context, datasetID string, timestamp string) error {
	routes, err := Load(ctx, Filter{DatasetID: datasetID, Timestamp: timestamp})
	if err != nil {
		return err
	}
	ownedTrackRefs, err := datasetTrackRefs(ctx, datasetID)
	if err != nil {
		return err
	}
	if err := clearOwnedJourneyTrackRefs(ctx, ownedTrackRefs); err != nil {
		return err
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

	serviceCursor, err := database.GetCollection("services").Find(ctx, bson.M{})
	if err != nil {
		return err
	}
	defer serviceCursor.Close(ctx)
	for serviceCursor.Next(ctx) {
		var service ctdf.Service
		if err := serviceCursor.Decode(&service); err != nil {
			return err
		}
		candidates := append([]Route{}, routesByServiceName[serviceKey(service.TransportType, service.ServiceName)]...)
		serviceRefs := append([]string{service.PrimaryIdentifier}, service.OtherIdentifiers...)
		for _, serviceRef := range serviceRefs {
			candidates = appendUniqueRoutes(candidates, routesByServiceRef[serviceRef]...)
		}
		if len(candidates) == 0 {
			continue
		}
		if err := applyServiceJourneys(ctx, serviceRefs, candidates); err != nil {
			return err
		}
	}
	return serviceCursor.Err()
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
	cursor, err := database.GetCollection("journey_tracks").Find(ctx, bson.M{"datasource.datasetid": datasetID}, options.Find().SetProjection(bson.M{"primaryidentifier": 1}))
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

func clearOwnedJourneyTrackRefs(ctx context.Context, owned map[string]struct{}) error {
	if len(owned) == 0 {
		return nil
	}
	refs := make([]string, 0, len(owned))
	for ref := range owned {
		refs = append(refs, ref)
	}
	seenJourneys := map[string]struct{}{}
	models := []mongo.WriteModel{}
	flush := func() error {
		if len(models) == 0 {
			return nil
		}
		_, err := database.GetCollection("journeys").BulkWrite(ctx, models)
		models = models[:0]
		return err
	}
	for start := 0; start < len(refs); start += 5000 {
		end := min(start+5000, len(refs))
		cursor, err := database.GetCollection("journeys").Find(ctx, bson.M{"path.trackref": bson.M{"$in": refs[start:end]}})
		if err != nil {
			return err
		}
		for cursor.Next(ctx) {
			var journey ctdf.Journey
			if err := cursor.Decode(&journey); err != nil {
				cursor.Close(ctx)
				return err
			}
			if _, seen := seenJourneys[journey.PrimaryIdentifier]; seen {
				continue
			}
			seenJourneys[journey.PrimaryIdentifier] = struct{}{}
			changed := false
			for _, path := range journey.Path {
				if path == nil {
					continue
				}
				if _, isOwned := owned[path.TrackRef]; isOwned {
					path.TrackRef = ""
					path.Track = nil
					changed = true
				}
			}
			if changed {
				models = append(models, journeyPathUpdate(&journey))
				if len(models) == 1000 {
					if err := flush(); err != nil {
						cursor.Close(ctx)
						return err
					}
				}
			}
		}
		if err := cursor.Err(); err != nil {
			cursor.Close(ctx)
			return err
		}
		cursor.Close(ctx)
	}
	return flush()
}

func applyServiceJourneys(ctx context.Context, serviceRefs []string, candidates []Route) error {
	candidatesByEndpoints := indexRoutesByEndpoints(candidates)
	cursor, err := database.GetCollection("journeys").Find(ctx, bson.M{"serviceref": bson.M{"$in": serviceRefs}})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	models := []mongo.WriteModel{}
	for cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return err
		}
		journeyStops := JourneyStops(journey.Path)
		if len(journeyStops) < 2 {
			continue
		}
		journeyCandidates := candidatesByEndpoints[routeEndpointKey(journeyStops[0], journeyStops[len(journeyStops)-1])]
		changed, err := ApplyBestRoute(ctx, &journey, journeyCandidates)
		if err != nil {
			return err
		}
		if changed {
			models = append(models, journeyPathUpdate(&journey))
		}
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	if len(models) > 0 {
		_, err = database.GetCollection("journeys").BulkWrite(ctx, models)
	}
	return err
}

func indexRoutesByEndpoints(routes []Route) map[string][]Route {
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

func journeyPathUpdate(journey *ctdf.Journey) mongo.WriteModel {
	return mongo.NewUpdateOneModel().SetFilter(bson.M{"primaryidentifier": journey.PrimaryIdentifier}).SetUpdate(bson.M{"$set": bson.M{"path": journey.Path}})
}

func ApplyBestRoute(ctx context.Context, journey *ctdf.Journey, candidates []Route) (bool, error) {
	if journey.TrackRef != "" || len(journey.Track) > 0 {
		return false, nil
	}
	journeyStops := JourneyStops(journey.Path)
	type routeMatch struct {
		route     Route
		positions []int
	}
	matches := []routeMatch{}
	for _, candidate := range candidates {
		if positions, ok := MatchStops(journeyStops, candidate.Metadata.RouteStopIdentifiers); ok {
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
	for pathIndex, path := range journey.Path {
		if path == nil || path.TrackRef != "" || len(path.Track) > 0 || pathIndex+1 >= len(selected.positions) {
			continue
		}
		start, end := selected.positions[pathIndex], selected.positions[pathIndex+1]
		if end <= start {
			continue
		}
		if end == start+1 {
			if leg, ok := selected.route.Legs[start]; ok {
				path.TrackRef = leg.PrimaryIdentifier
				changed = true
			}
			continue
		}
		trackRef, err := storeCombinedTrack(ctx, selected.route, start, end)
		if err != nil {
			return false, err
		}
		if trackRef != "" {
			path.TrackRef = trackRef
			changed = true
		}
	}
	return changed, nil
}

func storeCombinedTrack(ctx context.Context, route Route, start, end int) (string, error) {
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
