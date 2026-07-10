package databaselookup

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestQueryOverpassTriesNextEndpointAfterFailure(t *testing.T) {
	failedEndpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failedEndpoint.Close()

	requestMethods := make(chan string, 1)
	successfulEndpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestMethods <- request.Method
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(`{"elements":[{"type":"node","id":1,"lat":51,"lon":0}]}`))
	}))
	defer successfulEndpoint.Close()

	elements, endpoint, err := queryOverpassWithEndpoints("[out:json];node(1);out;", []string{failedEndpoint.URL, successfulEndpoint.URL})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != successfulEndpoint.URL {
		t.Fatalf("expected successful endpoint %q, got %q", successfulEndpoint.URL, endpoint)
	}
	if len(elements) != 1 || elements[0].ID != 1 {
		t.Fatalf("unexpected Overpass elements: %#v", elements)
	}
	if requestMethod := <-requestMethods; requestMethod != http.MethodPost {
		t.Fatalf("expected POST request, got %s", requestMethod)
	}
}

func TestOverpassEndpointsIncludeSwissOnlyForSwissLocations(t *testing.T) {
	ukEndpoints := overpassEndpointsForLocation(&ctdf.Location{Coordinates: []float64{-0.263, 51.953}})
	if slicesContain(ukEndpoints, "https://overpass.osm.ch/api/interpreter") {
		t.Fatal("Swiss endpoint should not be queried for a UK stop")
	}

	swissEndpoints := overpassEndpointsForLocation(&ctdf.Location{Coordinates: []float64{8.54, 47.37}})
	if !slicesContain(swissEndpoints, "https://overpass.osm.ch/api/interpreter") {
		t.Fatal("Swiss endpoint should be queried for a Swiss stop")
	}
}

func slicesContain(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestExactOverpassQueryExpandsDirectlyMatchedStopPositions(t *testing.T) {
	query := buildOSMStopExactOverpassQuery(`  node["ref:crs"="HIT"];`)

	for _, expected := range []string{
		`node.station["public_transport"="stop_position"]->.matched_stop_positions;`,
		`way(bn.all_stop_positions)["railway"~"^(rail|light_rail|subway|tram)$"]`,
		`way(around.all_stop_positions:15)["railway"~"^(rail|light_rail|subway|tram)$"]`,
		`way(around.all_stop_positions:40)["railway"="platform"]->.platform_ways_near_stops;`,
		`.all_platform_ways;`,
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("expected exact Overpass query to contain %q", expected)
		}
	}
}

func TestCoordinateOverpassQueryExpandsTracksAndPlatformsFromStopPositions(t *testing.T) {
	query := buildOSMStopCoordinateOverpassQuery(&ctdf.Location{Coordinates: []float64{-0.263, 51.953}}, 700)

	for _, expected := range []string{
		`way(bn.all_stop_positions)["railway"~"^(rail|light_rail|subway|tram)$"]`,
		`way(around.all_stop_positions:15)["railway"~"^(rail|light_rail|subway|tram)$"]`,
		`way(around.all_stop_positions:40)["railway"="platform"]->.platform_ways_near_stops;`,
		`.all_platform_ways;`,
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("expected coordinate Overpass query to contain %q", expected)
		}
	}
}

func TestClassifyParkingAssociation(t *testing.T) {
	tests := []struct {
		name        string
		tags        map[string]string
		role        string
		association ctdf.OSMStopParkingAssociation
	}{
		{
			name: "station car park",
			tags: map[string]string{
				"amenity":  "parking",
				"name":     "Cambridge Station Long Stay Car Park",
				"operator": "National Car Parks",
				"access":   "yes",
			},
			association: ctdf.OSMStopParkingOfficial,
		},
		{
			name: "cyclepoint",
			tags: map[string]string{
				"amenity": "bicycle_parking",
				"name":    "Cambridge Cyclepoint",
			},
			association: ctdf.OSMStopParkingLikely,
		},
		{
			name: "nearby unnamed parking",
			tags: map[string]string{
				"amenity": "parking",
				"access":  "yes",
			},
			association: ctdf.OSMStopParkingNearby,
		},
		{
			name: "unrelated hotel parking",
			tags: map[string]string{
				"amenity": "parking",
				"name":    "Travelodge car park",
				"access":  "customers",
			},
			association: ctdf.OSMStopParkingUnrelated,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			element := overpassElement{Tags: test.tags}

			association, _ := classifyParkingAssociation(element, test.role)
			if association != test.association {
				t.Fatalf("expected %s, got %s", test.association, association)
			}
		})
	}
}

func TestSelectOSMStopElementsIncludesStationRetailAndExcludesNearbyRetail(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "gb-crs-CBG",
		TransportTypes:    []ctdf.TransportType{ctdf.TransportTypeRail},
		Location: &ctdf.Location{
			Type:        "Point",
			Coordinates: []float64{0, 0},
		},
	}

	elements := []overpassElement{
		{
			Type:   string(ctdf.OSMElementTypeRelation),
			ID:     1,
			Center: &overpassPoint{Lat: 0, Lon: 0},
			Tags: map[string]string{
				"type":             "public_transport",
				"public_transport": "stop_area",
			},
			Members: []overpassMember{
				{Type: string(ctdf.OSMElementTypeNode), Ref: 2, Role: "station"},
				{Type: string(ctdf.OSMElementTypeWay), Ref: 3, Role: "platform"},
			},
		},
		{
			Type: string(ctdf.OSMElementTypeNode),
			ID:   2,
			Lat:  0,
			Lon:  0,
			Tags: map[string]string{
				"railway": "station",
				"name":    "Cambridge",
			},
		},
		{
			Type: string(ctdf.OSMElementTypeWay),
			ID:   3,
			Tags: map[string]string{
				"railway": "platform",
			},
			Geometry: []overpassPoint{
				{Lat: 0, Lon: 0},
				{Lat: 0, Lon: 0.0005},
			},
		},
		{
			Type: string(ctdf.OSMElementTypeNode),
			ID:   4,
			Lat:  0,
			Lon:  0.00062,
			Tags: map[string]string{
				"name":  "M&S Simply Food",
				"shop":  "convenience",
				"level": "0",
			},
		},
		{
			Type: string(ctdf.OSMElementTypeNode),
			ID:   5,
			Lat:  0,
			Lon:  0.002,
			Tags: map[string]string{
				"name":     "WHSmith",
				"shop":     "newsagent",
				"location": "On Cambridge railway station inside the ticket barriers.",
			},
		},
		{
			Type: string(ctdf.OSMElementTypeNode),
			ID:   6,
			Lat:  0,
			Lon:  0.00105,
			Tags: map[string]string{
				"name": "Nearby unrelated shop",
				"shop": "convenience",
			},
		},
		{
			Type: string(ctdf.OSMElementTypeNode),
			ID:   7,
			Lat:  0,
			Lon:  0.0001,
			Tags: map[string]string{
				"name": "Vacant unit",
				"shop": "vacant",
			},
		},
		{
			Type: string(ctdf.OSMElementTypeWay),
			ID:   8,
			Tags: map[string]string{},
			Geometry: []overpassPoint{
				{Lat: 0.0025, Lon: 0.0025},
				{Lat: 0.0025, Lon: 0.0035},
				{Lat: 0.0035, Lon: 0.0035},
				{Lat: 0.0035, Lon: 0.0025},
				{Lat: 0.0025, Lon: 0.0025},
			},
		},
		{
			Type: string(ctdf.OSMElementTypeNode),
			ID:   9,
			Lat:  0.003,
			Lon:  0.003,
			Tags: map[string]string{
				"name": "WHSmith",
				"shop": "newsagent",
			},
		},
	}
	elements[0].Members = append(elements[0].Members, overpassMember{
		Type: string(ctdf.OSMElementTypeWay),
		Ref:  8,
		Role: "station",
		Geometry: []overpassPoint{
			{Lat: 0.0025, Lon: 0.0025},
			{Lat: 0.0025, Lon: 0.0035},
			{Lat: 0.0035, Lon: 0.0035},
			{Lat: 0.0035, Lon: 0.0025},
			{Lat: 0.0025, Lon: 0.0025},
		},
	})

	selected, stopArea, _ := selectOSMStopElements(elements, stop)
	selectedKeys := map[string]bool{}
	for _, element := range selected {
		selectedKeys[overpassElementKey(element)] = true
	}

	if !selectedKeys[overpassElementRefKey(string(ctdf.OSMElementTypeNode), 4)] {
		t.Fatal("expected station retail near platform anchors to be selected")
	}
	if !selectedKeys[overpassElementRefKey(string(ctdf.OSMElementTypeNode), 5)] {
		t.Fatal("expected station retail with explicit station context to be selected")
	}
	if selectedKeys[overpassElementRefKey(string(ctdf.OSMElementTypeNode), 6)] {
		t.Fatal("expected nearby retail without station context to be excluded")
	}
	if selectedKeys[overpassElementRefKey(string(ctdf.OSMElementTypeNode), 7)] {
		t.Fatal("expected vacant shops to be excluded")
	}
	if !selectedKeys[overpassElementRefKey(string(ctdf.OSMElementTypeNode), 9)] {
		t.Fatal("expected station retail inside station relation geometry to be selected")
	}

	features := buildOSMStopFeatures(selected, stopArea)
	featureAssociations := map[int64]ctdf.OSMStopFeatureAssociation{}
	featureDistances := map[int64]float64{}
	for _, feature := range features {
		featureAssociations[feature.Element.ID] = feature.Association
		featureDistances[feature.Element.ID] = feature.DistanceMetres
	}

	if featureAssociations[4] != ctdf.OSMStopFeatureAssociationNearby {
		t.Fatalf("expected station retail near platform anchors to be tagged nearby, got %s", featureAssociations[4])
	}
	if featureDistances[4] <= 0 {
		t.Fatalf("expected nearby station retail to include distance, got %f", featureDistances[4])
	}
	if featureAssociations[5] != ctdf.OSMStopFeatureAssociationInside {
		t.Fatalf("expected station retail with explicit station context to be tagged inside, got %s", featureAssociations[5])
	}
	if featureAssociations[9] != ctdf.OSMStopFeatureAssociationInside {
		t.Fatalf("expected station retail inside station relation geometry to be tagged inside, got %s", featureAssociations[9])
	}
}
