package tflarrivals

import (
	"net/url"
	"testing"
)

func TestModeArrivalsURLRequestsAllPredictionsPerStop(t *testing.T) {
	requestURL, err := url.Parse(modeArrivalsURL("tube", "test-key"))
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}

	if got, want := requestURL.Path, "/mode/tube/arrivals"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got, want := requestURL.Query().Get("count"), "-1"; got != want {
		t.Errorf("count = %q, want %q", got, want)
	}
	if got, want := requestURL.Query().Get("app_key"), "test-key"; got != want {
		t.Errorf("app_key = %q, want %q", got, want)
	}
}

func TestModeArrivalsURLLimitsBusPredictionsPerStop(t *testing.T) {
	requestURL, err := url.Parse(modeArrivalsURL("bus", "test-key"))
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}

	if got, want := requestURL.Query().Get("count"), "8"; got != want {
		t.Errorf("count = %q, want %q", got, want)
	}
}

func TestTFLTrackerRuntimeJourneyFilter(t *testing.T) {
	filter := newTFLTrackerRuntimeJourneyFilter([]TflTracker{
		{Line: "1", TripID: "trip-a"},
		{Line: "1", TripID: "trip-a"},
		{Line: "2", TripID: "trip-b"},
	})

	tests := []struct {
		name   string
		line   string
		tripID string
		want   bool
	}{
		{name: "tracked pair", line: "1", tripID: "trip-a", want: true},
		{name: "second tracked pair", line: "2", tripID: "trip-b", want: true},
		{name: "line does not imply trip", line: "1", tripID: "trip-b", want: false},
		{name: "trip does not imply line", line: "2", tripID: "trip-a", want: false},
		{name: "unknown pair", line: "3", tripID: "trip-c", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := filter(test.line, test.tripID); got != test.want {
				t.Errorf("filter(%q, %q) = %t, want %t", test.line, test.tripID, got, test.want)
			}
		})
	}
}

func TestBusArrivalLookupIdentifiesUniqueVehicleJourney(t *testing.T) {
	lookup := buildBusArrivalLookup([]ArrivalPrediction{
		{ModeName: "bus", LineID: "1", Direction: "inbound", VehicleID: "vehicle-1", DestinationNaptanID: "stop-a", TripID: "trip-a"},
		{ModeName: "bus", LineID: "1", Direction: "inbound", VehicleID: "vehicle-1", DestinationNaptanID: "stop-a", TripID: "trip-a"},
		{ModeName: "bus", LineID: "2", Direction: "outbound", VehicleID: "vehicle-2", DestinationNaptanID: "stop-b", TripID: "trip-b"},
		{ModeName: "bus", LineID: "2", Direction: "outbound", VehicleID: "vehicle-2", DestinationNaptanID: "stop-c", TripID: "trip-c"},
	})

	tripID, err := identifyBusFromLookup(BusMonitorEvent{NumberPlate: "vehicle-1"}, lookup)
	if err != nil {
		t.Fatalf("identify unique vehicle: %v", err)
	}
	if got, want := tripID, "trip-a"; got != want {
		t.Errorf("trip ID = %q, want %q", got, want)
	}

	if _, err := identifyBusFromLookup(BusMonitorEvent{NumberPlate: "vehicle-2"}, lookup); err == nil {
		t.Error("expected ambiguous vehicle to fail identification")
	}
	if _, err := identifyBusFromLookup(BusMonitorEvent{NumberPlate: "unknown"}, lookup); err == nil {
		t.Error("expected unknown vehicle to fail identification")
	}
}
