package journeytracks

import (
	"context"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func journeyWithLegs(legs ...*ctdf.JourneyPathItem) *ctdf.Journey {
	return &ctdf.Journey{Calls: ctdf.JourneyCallsFromLegs(legs...)}
}

func outgoingTrackRef(journey *ctdf.Journey, index int) string {
	for call, _ := range journey.LegCalls() {
		if index == 0 {
			if call.Outgoing == nil {
				return ""
			}
			return call.Outgoing.TrackRef
		}
		index--
	}
	return ""
}

func TestApplyBestRouteAssignsTrackReferencesToScheduledJourneyLegs(t *testing.T) {
	journey := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b"},
		&ctdf.JourneyPathItem{OriginStopRef: "b", DestinationStopRef: "c", TrackRef: "timetable-track"},
	)
	route := Route{
		Metadata: RouteLeg{RouteKey: "route", RouteStopIdentifiers: [][]string{{"a"}, {"b"}, {"c"}}},
		Legs: map[int]ctdf.JourneyTrack{
			0: {PrimaryIdentifier: "imported-a-b", Track: []ctdf.Location{{Coordinates: []float64{0, 0}}, {Coordinates: []float64{1, 0}}}},
			1: {PrimaryIdentifier: "imported-b-c", Track: []ctdf.Location{{Coordinates: []float64{1, 0}}, {Coordinates: []float64{2, 0}}}},
		},
	}
	changed, err := ApplyBestRoute(context.Background(), journey, []Route{route})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || outgoingTrackRef(journey, 0) != "imported-a-b" {
		t.Fatalf("first leg track ref = %q", outgoingTrackRef(journey, 0))
	}
	if outgoingTrackRef(journey, 1) != "timetable-track" {
		t.Fatalf("timetable track was overwritten: %q", outgoingTrackRef(journey, 1))
	}
}

func TestTrackEnrichmentCountsActualWritesAcrossCollections(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("modified, unmatched and unchanged", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previous })

		calls := bson.A{bson.M{"stopref": "a", "start": true}, bson.M{"stopref": "b", "end": true}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch,
				bson.D{{Key: "primaryidentifier", Value: "first"}, {Key: "calls", Value: calls}},
				bson.D{{Key: "primaryidentifier", Value: "second"}, {Key: "calls", Value: calls}},
			),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 2}, bson.E{Key: "nModified", Value: 1}),
		)
		routes := []Route{{
			Metadata: RouteLeg{RouteStopIdentifiers: [][]string{{"a"}, {"b"}}},
			Legs:     map[int]ctdf.JourneyTrack{0: {PrimaryIdentifier: "a-b-track"}},
		}}
		stats, err := applyServiceJourneys(context.Background(), []string{"service"}, routes,
			database.JourneysCollectionName, false, nil,
			[]string{database.JourneysCollectionName, database.JourneysRawCollectionName}, nil)
		want := serviceJourneyStats{Scanned: 2, Batches: 1, Modified: 2, Unmatched: 1, Unchanged: 1}
		if err != nil || stats != want {
			mt.Fatalf("stats = %+v, err = %v, want %+v", stats, err, want)
		}
	})
}

func TestIndexRoutesByEndpointsIncludesSubsequencesAndAliases(t *testing.T) {
	route := Route{Metadata: RouteLeg{
		RouteKey: "route",
		RouteStopIdentifiers: [][]string{
			{"a", "alias-a"},
			{"b"},
			{"c", "alias-c"},
		},
	}}
	index := indexRoutesByEndpoints([]Route{route})

	for _, key := range []string{
		routeEndpointKey("a", "c"),
		routeEndpointKey("alias-a", "alias-c"),
		routeEndpointKey("b", "c"),
	} {
		if len(index[key]) != 1 || index[key][0].Metadata.RouteKey != "route" {
			t.Fatalf("route missing from endpoint index for %q", key)
		}
	}
	if len(index[routeEndpointKey("c", "a")]) != 0 {
		t.Fatal("route was indexed in the wrong direction")
	}
}

func TestApplyBestRouteMatchesPassingPath(t *testing.T) {
	journey := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", PassingPath: []string{"via"}},
	)
	candidate := func(key string, passingPaths [][]string) Route {
		return Route{
			Metadata: RouteLeg{
				RouteKey: key, RouteStopIdentifiers: [][]string{{"a"}, {"b"}}, PassingPaths: passingPaths,
			},
			Legs: map[int]ctdf.JourneyTrack{0: {PrimaryIdentifier: key}},
		}
	}
	candidates := []Route{candidate("a-legacy", nil), candidate("b-other", [][]string{{"other"}}), candidate("c-matching", [][]string{{"via"}})}
	changed, err := ApplyBestRoute(context.Background(), journey, candidates)
	if err != nil || !changed || outgoingTrackRef(journey, 0) != "c-matching" {
		t.Fatalf("changed = %t, track = %q, err = %v", changed, outgoingTrackRef(journey, 0), err)
	}
	journey.Calls[0].Outgoing.TrackRef = ""
	journey.Calls[0].Outgoing.PassingPath = nil
	changed, err = ApplyBestRoute(context.Background(), journey, candidates)
	if err != nil || !changed || outgoingTrackRef(journey, 0) != "a-legacy" {
		t.Fatalf("legacy journey: changed = %t, track = %q, err = %v", changed, outgoingTrackRef(journey, 0), err)
	}
}

func TestApplyOSMRailRoutesKeepsFallbackAcrossPathGap(t *testing.T) {
	journey := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", PassingPath: []string{"via"}, TrackRef: "old-a-b"},
		&ctdf.JourneyPathItem{OriginStopRef: "c", DestinationStopRef: "d", PassingPath: []string{"unroutable"}, TrackRef: "old-c-d"},
	)
	routes := []Route{{
		Metadata: RouteLeg{
			RouteKey: "a-b-via", RouteStopIdentifiers: [][]string{{"a"}, {"b"}}, PassingPaths: [][]string{{"via"}},
		},
		Legs: map[int]ctdf.JourneyTrack{0: {PrimaryIdentifier: "new-a-b"}},
	}}
	owned := map[string]struct{}{"old-a-b": {}, "old-c-d": {}}
	changed, err := applyOSMRailRoutes(context.Background(), journey, indexRoutesByEndpoints(routes), owned)
	if err != nil || !changed {
		t.Fatalf("changed = %t, err = %v", changed, err)
	}
	if outgoingTrackRef(journey, 0) != "new-a-b" || outgoingTrackRef(journey, 1) != "old-c-d" {
		t.Fatalf("track refs = %q, %q", outgoingTrackRef(journey, 0), outgoingTrackRef(journey, 1))
	}
}
