package journeytracks

import (
	"context"
	"sort"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const RouteCollectionName = "journey_track_routes"

// RouteLeg describes how a generic journey track relates to a service and a
// pair of stops. Importers may add source-specific lookup values in Attributes.
type RouteLeg struct {
	PrimaryIdentifier    string                    `bson:",omitempty"`
	RouteKey             string                    `bson:"routekey"`
	ServiceRefs          []string                  `bson:"servicerefs,omitempty"`
	ServiceNameKeys      []string                  `bson:"servicenamekeys"`
	TransportType        ctdf.TransportType        `bson:"transporttype"`
	Direction            string                    `bson:"direction,omitempty"`
	RouteName            string                    `bson:"routename,omitempty"`
	ExternalStopRefs     []string                  `bson:"externalstoprefs,omitempty"`
	RouteStopRefs        []string                  `bson:"routestoprefs"`
	RouteStopIdentifiers [][]string                `bson:"routestopidentifiers"`
	LegIndex             int                       `bson:"legindex"`
	OriginStopRefs       []string                  `bson:"originstoprefs"`
	DestinationStopRefs  []string                  `bson:"destinationstoprefs"`
	TrackRef             string                    `bson:"trackref"`
	Attributes           map[string]string         `bson:"attributes,omitempty"`
	DataSource           *ctdf.DataSourceReference `bson:"datasource,omitempty"`
}

type Route struct {
	Metadata RouteLeg
	Legs     map[int]ctdf.JourneyTrack
}

type Filter struct {
	DatasetID  string
	Timestamp  string
	Attributes map[string]string
}

func Load(ctx context.Context, filter Filter) ([]Route, error) {
	query := bson.M{}
	if filter.DatasetID != "" {
		query["datasource.datasetid"] = filter.DatasetID
	}
	if filter.Timestamp != "" {
		query["datasource.timestamp"] = filter.Timestamp
	}
	for key, value := range filter.Attributes {
		query["attributes."+key] = value
	}
	cursor, err := database.GetCollection(RouteCollectionName).Find(ctx, query)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	legs := []RouteLeg{}
	trackRefs := []string{}
	for cursor.Next(ctx) {
		var leg RouteLeg
		if err := cursor.Decode(&leg); err != nil {
			return nil, err
		}
		legs = append(legs, leg)
		trackRefs = append(trackRefs, leg.TrackRef)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	tracks := map[string]ctdf.JourneyTrack{}
	if len(trackRefs) > 0 {
		trackCursor, err := database.GetCollection("journey_tracks").Find(ctx, bson.M{"primaryidentifier": bson.M{"$in": trackRefs}})
		if err != nil {
			return nil, err
		}
		defer trackCursor.Close(ctx)
		for trackCursor.Next(ctx) {
			var track ctdf.JourneyTrack
			if err := trackCursor.Decode(&track); err != nil {
				return nil, err
			}
			tracks[track.PrimaryIdentifier] = track
		}
		if err := trackCursor.Err(); err != nil {
			return nil, err
		}
	}

	byKey := map[string]*Route{}
	for _, leg := range legs {
		route := byKey[leg.RouteKey]
		if route == nil {
			route = &Route{Metadata: leg, Legs: map[int]ctdf.JourneyTrack{}}
			byKey[leg.RouteKey] = route
		}
		if track, ok := tracks[leg.TrackRef]; ok {
			route.Legs[leg.LegIndex] = track
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	routes := make([]Route, 0, len(keys))
	for _, key := range keys {
		routes = append(routes, *byKey[key])
	}
	return routes, nil
}

func MatchStops(journeyStops []string, routeStops [][]string) ([]int, bool) {
	if len(journeyStops) < 2 || len(routeStops) < len(journeyStops) {
		return nil, false
	}
	positions := make([]int, 0, len(journeyStops))
	nextRouteIndex := 0
	for _, journeyStop := range journeyStops {
		matched := false
		for nextRouteIndex < len(routeStops) {
			if contains(routeStops[nextRouteIndex], journeyStop) {
				positions = append(positions, nextRouteIndex)
				nextRouteIndex++
				matched = true
				break
			}
			nextRouteIndex++
		}
		if !matched {
			return nil, false
		}
	}
	return positions, true
}

func JourneyStops(path []*ctdf.JourneyPathItem) []string {
	stops := make([]string, 0, len(path)+1)
	for _, item := range path {
		if item == nil {
			continue
		}
		if len(stops) == 0 {
			stops = append(stops, item.OriginStopRef)
		}
		stops = append(stops, item.DestinationStopRef)
	}
	return stops
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
