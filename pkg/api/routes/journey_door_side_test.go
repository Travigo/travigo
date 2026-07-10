package routes

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestCalculateTrainDoorSideUsesJourneyDirection(t *testing.T) {
	osmStop := testDoorSideOSMStop()
	stop := testLocation(0, 0)
	south := testLocation(0, -0.01)
	north := testLocation(0, 0.01)

	northbound := calculateTrainDoorSide(osmStop, "Platform 1", stop, &south, &north)
	if northbound.side != trainDoorSideLeft {
		t.Fatalf("expected northbound platform to be on the left, got %s (%s)", northbound.side, northbound.reason)
	}
	if northbound.platformElement == nil || northbound.platformElement.ID != 101 {
		t.Fatalf("expected platform edge 101, got %#v", northbound.platformElement)
	}

	southbound := calculateTrainDoorSide(osmStop, "1", stop, &north, &south)
	if southbound.side != trainDoorSideRight {
		t.Fatalf("expected southbound platform to be on the right, got %s (%s)", southbound.side, southbound.reason)
	}
}

func TestCalculateTrainDoorSideReturnsUnknownForAmbiguousIslandPlatform(t *testing.T) {
	osmStop := &ctdf.OSMStop{Features: []ctdf.OSMStopFeature{
		{
			Type:     ctdf.OSMStopFeatureTypePlatform,
			Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 201},
			Ref:      "3",
			Geometry: []ctdf.Location{testLocation(-0.00001, -0.001), testLocation(-0.00001, 0.001)},
		},
		{
			Type:     ctdf.OSMStopFeatureTypeTrack,
			Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 301},
			Geometry: []ctdf.Location{testLocation(-0.00005, -0.001), testLocation(-0.00005, 0.001)},
		},
		{
			Type:     ctdf.OSMStopFeatureTypeTrack,
			Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 302},
			Geometry: []ctdf.Location{testLocation(0.00003, -0.001), testLocation(0.00003, 0.001)},
		},
	}}
	stop := testLocation(0, 0)
	south := testLocation(0, -0.01)
	north := testLocation(0, 0.01)

	result := calculateTrainDoorSide(osmStop, "3", stop, &south, &north)
	if result.side != trainDoorSideUnknown {
		t.Fatalf("expected ambiguous island platform to be unknown, got %s", result.side)
	}
}

func TestFindRealtimePlatformMatchesStopAliases(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "station",
		OtherIdentifiers:  []string{"station-alias"},
	}
	realtimeJourney := &ctdf.RealtimeJourney{Stops: map[string]*ctdf.RealtimeJourneyStops{
		"unrelated-map-key": {StopRef: "station-alias", Platform: "4"},
	}}

	if platform := findRealtimePlatform(realtimeJourney, stop, "station", 0); platform != "4" {
		t.Fatalf("expected realtime platform 4, got %q", platform)
	}
}

func TestFindRealtimePlatformSelectsRepeatedStopVisit(t *testing.T) {
	stop := &ctdf.Stop{PrimaryIdentifier: "station"}
	realtimeJourney := &ctdf.RealtimeJourney{}
	realtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "station", JourneyStopIndex: 0, Platform: "1"})
	realtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "station", JourneyStopIndex: 3, Platform: "4"})

	if platform := findRealtimePlatform(realtimeJourney, stop, "station", 3); platform != "4" {
		t.Fatalf("expected second visit platform 4, got %q", platform)
	}
}

func TestFindJourneyStopVisitPrefersIncomingPlatform(t *testing.T) {
	stop := &ctdf.Stop{PrimaryIdentifier: "middle"}
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "start", DestinationStopRef: "middle", DestinationPlatform: "2"},
		{OriginStopRef: "middle", DestinationStopRef: "end", OriginPlatform: "3"},
	}}

	visit, err := findJourneyStopVisit(journey, stop, 1)
	if err != nil {
		t.Fatal(err)
	}
	if visit.previousStopRef != "start" || visit.nextStopRef != "end" {
		t.Fatalf("unexpected adjacent stops: %#v", visit)
	}
	if visit.scheduledPlatform != "2" {
		t.Fatalf("expected incoming platform 2, got %q", visit.scheduledPlatform)
	}
}

func testDoorSideOSMStop() *ctdf.OSMStop {
	return &ctdf.OSMStop{Features: []ctdf.OSMStopFeature{
		{
			Type:     ctdf.OSMStopFeatureTypePlatformEdge,
			Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 101},
			Ref:      "1",
			Geometry: []ctdf.Location{testLocation(-0.00005, -0.001), testLocation(-0.00005, 0.001)},
		},
		{
			Type:     ctdf.OSMStopFeatureTypeTrack,
			Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 301},
			Geometry: []ctdf.Location{testLocation(0, -0.001), testLocation(0, 0.001)},
		},
	}}
}

func testLocation(longitude float64, latitude float64) ctdf.Location {
	return ctdf.Location{Type: "Point", Coordinates: []float64{longitude, latitude}}
}
