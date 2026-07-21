package transxchange

import "testing"

func TestTransXChangeTrackIdentifierIsDatasetScopedAndStable(t *testing.T) {
	first := transXChangeTrackIdentifier("dataset-a", "route-link-1")
	if first != transXChangeTrackIdentifier("dataset-a", "route-link-1") {
		t.Fatal("track identifier is not stable")
	}
	if first == transXChangeTrackIdentifier("dataset-b", "route-link-1") {
		t.Fatal("track identifier is not dataset scoped")
	}
}

func TestTransXChangeRouteLinkTrackUsesTranslatedCoordinates(t *testing.T) {
	routeLink := &RouteLink{Track: []Location{
		{LocationInner: LocationInner{Longitude: -0.1, Latitude: 51.5}},
		{Translation: LocationInner{Longitude: -0.2, Latitude: 51.6}},
	}}
	track := transXChangeRouteLinkTrack(routeLink)
	if len(track) != 2 {
		t.Fatalf("track length = %d", len(track))
	}
	if track[1].Coordinates[0] != -0.2 || track[1].Coordinates[1] != 51.6 {
		t.Fatalf("translated coordinates = %v", track[1].Coordinates)
	}
}
