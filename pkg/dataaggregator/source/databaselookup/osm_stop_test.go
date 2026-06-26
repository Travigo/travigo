package databaselookup

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

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
