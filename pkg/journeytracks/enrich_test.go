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
