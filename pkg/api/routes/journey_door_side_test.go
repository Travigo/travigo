package routes

import (
	"math"
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

func TestCalculateTrainDoorSideIgnoresSameNumberedUndergroundPlatform(t *testing.T) {
	osmStop := testDoorSideOSMStop()
	osmStop.TransportTypes = []ctdf.TransportType{ctdf.TransportTypeRail}
	osmStop.Features = append(osmStop.Features,
		ctdf.OSMStopFeature{
			Type:     ctdf.OSMStopFeatureTypePlatform,
			Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 401},
			Ref:      "1",
			Tags:     map[string]string{"railway": "platform", "subway": "yes"},
			Geometry: []ctdf.Location{testLocation(0.00105, -0.001), testLocation(0.00105, 0.001)},
		},
		ctdf.OSMStopFeature{
			Type:     ctdf.OSMStopFeatureTypeTrack,
			Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 402},
			Tags:     map[string]string{"railway": "subway"},
			Geometry: []ctdf.Location{testLocation(0.001, -0.001), testLocation(0.001, 0.001)},
		},
	)
	osmStop.Features[0].Tags = map[string]string{"railway": "platform", "train": "yes"}
	osmStop.Features[1].Tags = map[string]string{"railway": "rail"}
	stop := testLocation(0, 0)
	south := testLocation(0, -0.01)
	north := testLocation(0, 0.01)

	result := calculateTrainDoorSide(osmStop, "1", stop, &south, &north)
	if result.side != trainDoorSideLeft {
		t.Fatalf("expected surface rail platform on the left, got %s (%s)", result.side, result.reason)
	}
	if result.platformElement == nil || result.platformElement.ID != 101 {
		t.Fatalf("expected surface platform 101, got %#v", result.platformElement)
	}
	if result.trackElement == nil || result.trackElement.ID != 301 {
		t.Fatalf("expected surface track 301, got %#v", result.trackElement)
	}
}

func TestCalculateTrainDoorSidePrefersTrackMatchingPlatformLine(t *testing.T) {
	osmStop := &ctdf.OSMStop{
		TransportTypes: []ctdf.TransportType{ctdf.TransportTypeMetro},
		Features: []ctdf.OSMStopFeature{
			{
				Type:        ctdf.OSMStopFeatureTypePlatform,
				Element:     ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 101},
				Ref:         "5",
				PrimaryName: "Northbound",
				Tags:        map[string]string{"railway": "platform", "subway": "yes", "note": "Bakerloo Line"},
				Geometry:    []ctdf.Location{testLocation(-0.00002, -0.001), testLocation(-0.00002, 0.001)},
			},
			{
				Type:     ctdf.OSMStopFeatureTypeTrack,
				Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 201},
				Tags:     map[string]string{"railway": "subway", "line": "Circle;District"},
				Geometry: []ctdf.Location{testLocation(0, -0.001), testLocation(0, 0.001)},
			},
			{
				Type:     ctdf.OSMStopFeatureTypeTrack,
				Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 202},
				Tags:     map[string]string{"railway": "subway", "line": "Bakerloo"},
				Geometry: []ctdf.Location{testLocation(-0.00007, -0.001), testLocation(-0.00007, 0.001)},
			},
		},
	}
	stop := testLocation(0, 0)
	south := testLocation(0, -0.01)
	north := testLocation(0, 0.01)

	result := calculateTrainDoorSide(osmStop, "5 - Northbound", stop, &south, &north)
	if result.side != trainDoorSideRight {
		t.Fatalf("expected Bakerloo platform to be right of its track, got %s (%s)", result.side, result.reason)
	}
	if result.trackElement == nil || result.trackElement.ID != 202 {
		t.Fatalf("expected Bakerloo track 202, got %#v", result.trackElement)
	}
}

func TestRealtimePlatformDirectionSuffixMatchesOSMRef(t *testing.T) {
	features := []ctdf.OSMStopFeature{
		{
			Type:    ctdf.OSMStopFeatureTypePlatform,
			Element: ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 1465716690},
			Ref:     "1",
			Tags:    map[string]string{"railway": "platform", "subway": "yes", "ref": "1"},
		},
	}

	matches := matchingPlatformFeatureIndexes(features, "1 - Northbound", []ctdf.TransportType{ctdf.TransportTypeMetro})
	if len(matches) != 1 || matches[0] != 0 {
		t.Fatalf("expected realtime platform 1 to match OSM platform ref 1, got %v", matches)
	}
}

func TestCompetingTracksCanAgreeOnDoorSide(t *testing.T) {
	direction := projectedPoint{x: 0, y: 10}
	matches := []trackMatch{
		{
			trackPoint:    projectedPoint{x: 0, y: 0},
			platformPoint: projectedPoint{x: 2, y: 0},
			segmentStart:  projectedPoint{x: 0, y: -5},
			segmentEnd:    projectedPoint{x: 0, y: 5},
		},
		{
			trackPoint:    projectedPoint{x: -1, y: 0},
			platformPoint: projectedPoint{x: 2, y: 0},
			segmentStart:  projectedPoint{x: -1, y: -5},
			segmentEnd:    projectedPoint{x: -1, y: 5},
		},
	}

	if !trackMatchesAgreeOnDoorSide(matches, direction) {
		t.Fatal("expected parallel tracks on the same platform side to agree")
	}
	matches[1].trackPoint = projectedPoint{x: 3, y: 0}
	matches[1].segmentStart.x = 3
	matches[1].segmentEnd.x = 3
	if trackMatchesAgreeOnDoorSide(matches, direction) {
		t.Fatal("expected tracks on opposite platform sides to remain ambiguous")
	}
}

func TestConnectedTrackWaysAreNotCompetingTracks(t *testing.T) {
	best := trackMatch{
		trackFeatureIndex:    1,
		platformFeatureIndex: 10,
		distance:             4,
		segmentStart:         projectedPoint{x: 0, y: 0},
		segmentEnd:           projectedPoint{x: 10, y: 0},
		trackStart:           projectedPoint{x: 0, y: 0},
		trackEnd:             projectedPoint{x: 10, y: 0},
	}
	continuation := trackMatch{
		trackFeatureIndex:    2,
		platformFeatureIndex: 10,
		distance:             4.2,
		segmentStart:         projectedPoint{x: 10, y: 0},
		segmentEnd:           projectedPoint{x: 20, y: 0},
		trackStart:           projectedPoint{x: 10, y: 0},
		trackEnd:             projectedPoint{x: 20, y: 0},
	}

	competing := competingTrackMatches([]trackMatch{best, continuation}, best)
	if len(competing) != 1 {
		t.Fatalf("expected connected OSM ways to represent one track, got %d candidates", len(competing))
	}

	parallel := continuation
	parallel.segmentStart = projectedPoint{x: 0, y: 5}
	parallel.segmentEnd = projectedPoint{x: 10, y: 5}
	parallel.trackStart = projectedPoint{x: 0, y: 5}
	parallel.trackEnd = projectedPoint{x: 10, y: 5}
	competing = competingTrackMatches([]trackMatch{best, parallel}, best)
	if len(competing) != 2 {
		t.Fatalf("expected an unconnected parallel way to remain competing, got %d candidates", len(competing))
	}
}

func TestDirectionalPlatformSelectsJourneyAlignedTrack(t *testing.T) {
	platform := ctdf.OSMStopFeature{
		PrimaryName: "Northbound Platform 9",
		Tags: map[string]string{
			"ref:GB:tfl_uid": "940GZZLUBST-Plat09-NB-bakerloo",
		},
	}
	if !platformHasDirectionHint(platform, "9 - Northbound") {
		t.Fatal("expected TfL platform metadata to provide a direction hint")
	}
	if _, matches := featureLineTokens(platform, true)["bakerloo"]; !matches {
		t.Fatal("expected TfL platform identifier to provide the Bakerloo line")
	}

	direction := projectedPoint{x: -10, y: 0}
	matches := []trackMatch{
		{
			trackFeatureIndex: 1,
			segmentStart:      projectedPoint{x: 0, y: 0},
			segmentEnd:        projectedPoint{x: 10, y: 0},
		},
		{
			trackFeatureIndex: 2,
			segmentStart:      projectedPoint{x: 10, y: 5},
			segmentEnd:        projectedPoint{x: 0, y: 5},
		},
	}
	match, found := directionAlignedTrackMatch(matches, direction)
	if !found || match.trackFeatureIndex != 2 {
		t.Fatalf("expected journey-aligned track 2, got %#v (found=%t)", match, found)
	}
}

func TestPolygonCentroidIsStableAtProjectedMapCoordinates(t *testing.T) {
	points := []projectedPoint{
		{x: 18000, y: 5_790_000},
		{x: 18020, y: 5_790_000},
		{x: 18020, y: 5_790_100},
		{x: 18000, y: 5_790_100},
		{x: 18000, y: 5_790_000},
	}

	centroid, ok := polygonCentroid(points)
	if !ok {
		t.Fatal("expected a valid polygon centroid")
	}
	if math.Abs(centroid.x-18010) > 0.001 || math.Abs(centroid.y-5_790_050) > 0.001 {
		t.Fatalf("unexpected projected centroid: %#v", centroid)
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

func TestCalculateTrainDoorSideUsesPlatformStopPositionForIslandPlatform(t *testing.T) {
	osmStop := &ctdf.OSMStop{
		TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail},
		Features: []ctdf.OSMStopFeature{
			{
				Type:    ctdf.OSMStopFeatureTypeStopPosition,
				Element: ctdf.OSMElementRef{Type: ctdf.OSMElementTypeNode, ID: 100},
				Ref:     "7",
				Tags:    map[string]string{"railway": "stop", "train": "yes", "ref": "7"},
				Location: func() *ctdf.Location {
					location := testLocation(-0.00005, 0)
					return &location
				}(),
			},
			{
				Type:    ctdf.OSMStopFeatureTypePlatform,
				Element: ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 201},
				Ref:     "7",
				Tags:    map[string]string{"railway": "platform", "train": "yes"},
				Geometry: []ctdf.Location{
					testLocation(-0.00004, -0.001),
					testLocation(0.00001, -0.001),
					testLocation(0.00001, 0.001),
					testLocation(-0.00004, 0.001),
					testLocation(-0.00004, -0.001),
				},
			},
			{
				Type:     ctdf.OSMStopFeatureTypeTrack,
				Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 301},
				Tags:     map[string]string{"railway": "rail"},
				Geometry: []ctdf.Location{testLocation(-0.00005, -0.001), testLocation(-0.00005, 0.001)},
			},
			{
				Type:     ctdf.OSMStopFeatureTypeTrack,
				Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 302},
				Tags:     map[string]string{"railway": "rail"},
				Geometry: []ctdf.Location{testLocation(0.00002, -0.001), testLocation(0.00002, 0.001)},
			},
		},
	}
	stop := testLocation(0, 0)
	south := testLocation(0, -0.01)
	north := testLocation(0, 0.01)

	result := calculateTrainDoorSide(osmStop, "7", stop, &south, &north)
	if result.side != trainDoorSideRight {
		t.Fatalf("expected platform east of the identified track to be on the right, got %s (%s)", result.side, result.reason)
	}
	if result.trackElement == nil || result.trackElement.ID != 301 {
		t.Fatalf("expected stop-position track 301, got %#v", result.trackElement)
	}
}

func TestStopPositionTrackMatchOverridesLongPlatformCentroidDistance(t *testing.T) {
	stopPosition := testLocation(0, 0)
	osmStop := &ctdf.OSMStop{
		TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail},
		Features: []ctdf.OSMStopFeature{
			{
				Type:     ctdf.OSMStopFeatureTypeStopPosition,
				Ref:      "1",
				Tags:     map[string]string{"railway": "stop", "train": "yes", "ref": "1"},
				Location: &stopPosition,
			},
			{
				Type:     ctdf.OSMStopFeatureTypePlatform,
				Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 100},
				Ref:      "1",
				Tags:     map[string]string{"railway": "platform", "train": "yes"},
				Geometry: []ctdf.Location{testLocation(0.0004, -0.001), testLocation(0.0004, 0.001)},
			},
			{
				Type:     ctdf.OSMStopFeatureTypeTrack,
				Element:  ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 200},
				Tags:     map[string]string{"railway": "rail"},
				Geometry: []ctdf.Location{testLocation(0, -0.001), testLocation(0, 0.001)},
			},
		},
	}
	stop := testLocation(0, 0)
	south := testLocation(0, -0.01)
	north := testLocation(0, 0.01)

	result := calculateTrainDoorSide(osmStop, "1", stop, &south, &north)
	if result.side != trainDoorSideRight {
		t.Fatalf("expected stop-position matched track to calculate Right despite platform distance, got %s (%s)", result.side, result.reason)
	}
	if result.trackElement == nil || result.trackElement.ID != 200 {
		t.Fatalf("expected stop-position track 200, got %#v", result.trackElement)
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
