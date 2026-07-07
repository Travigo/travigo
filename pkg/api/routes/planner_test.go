package routes

import "testing"

func TestParsePlannerCoordinateAcceptsLonLat(t *testing.T) {
	location, isCoordinate, err := parsePlannerCoordinate("-0.123456,52.123456")
	if err != nil {
		t.Fatalf("expected coordinate to parse, got %s", err)
	}
	if !isCoordinate {
		t.Fatal("expected coordinate input to be identified")
	}
	if location == nil || len(location.Coordinates) != 2 {
		t.Fatal("expected location coordinates")
	}
	if location.Coordinates[0] != -0.123456 || location.Coordinates[1] != 52.123456 {
		t.Fatalf("unexpected parsed coordinates %+v", location.Coordinates)
	}
}

func TestParsePlannerCoordinateIgnoresStopIdentifier(t *testing.T) {
	location, isCoordinate, err := parsePlannerCoordinate("gb-atco-0500STEVE012")
	if err != nil {
		t.Fatalf("did not expect stop identifier parse error, got %s", err)
	}
	if isCoordinate {
		t.Fatal("did not expect stop identifier to be treated as coordinate")
	}
	if location != nil {
		t.Fatalf("expected nil location for stop identifier, got %+v", location)
	}
}

func TestParsePlannerCoordinateRejectsInvalidCoordinate(t *testing.T) {
	_, isCoordinate, err := parsePlannerCoordinate("-0.123456,not-a-latitude")
	if !isCoordinate {
		t.Fatal("expected comma-separated input to be treated as coordinate")
	}
	if err == nil {
		t.Fatal("expected invalid coordinate error")
	}
}
