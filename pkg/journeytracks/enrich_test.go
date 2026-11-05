package journeytracks

import (
	"context"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestApplyBestRouteAssignsTrackReferencesToScheduledJourneyLegs(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "a", DestinationStopRef: "b"},
		{OriginStopRef: "b", DestinationStopRef: "c", TrackRef: "timetable-track"},
	}}
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
	if !changed || journey.Path[0].TrackRef != "imported-a-b" {
		t.Fatalf("first leg track ref = %q", journey.Path[0].TrackRef)
	}
	if journey.Path[1].TrackRef != "timetable-track" {
		t.Fatalf("timetable track was overwritten: %q", journey.Path[1].TrackRef)
	}
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
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "a", DestinationStopRef: "b", PassingPath: []string{"via"}},
	}}
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
	if err != nil || !changed || journey.Path[0].TrackRef != "c-matching" {
		t.Fatalf("changed = %t, track = %q, err = %v", changed, journey.Path[0].TrackRef, err)
	}
	journey.Path[0].TrackRef = ""
	journey.Path[0].PassingPath = nil
	changed, err = ApplyBestRoute(context.Background(), journey, candidates)
	if err != nil || !changed || journey.Path[0].TrackRef != "a-legacy" {
		t.Fatalf("legacy journey: changed = %t, track = %q, err = %v", changed, journey.Path[0].TrackRef, err)
	}
}

func TestApplyOSMRailRoutesKeepsFallbackAcrossPathGap(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "a", DestinationStopRef: "b", PassingPath: []string{"via"}, TrackRef: "old-a-b"},
		{OriginStopRef: "c", DestinationStopRef: "d", PassingPath: []string{"unroutable"}, TrackRef: "old-c-d"},
	}}
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
	if journey.Path[0].TrackRef != "new-a-b" || journey.Path[1].TrackRef != "old-c-d" {
		t.Fatalf("track refs = %q, %q", journey.Path[0].TrackRef, journey.Path[1].TrackRef)
	}
}
