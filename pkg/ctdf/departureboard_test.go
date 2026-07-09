package ctdf

import (
	"testing"
	"time"
)

func TestBoardPathSelectorsUseDestinationForArrivals(t *testing.T) {
	originTime := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	arrivalTime := originTime.Add(12 * time.Minute)
	path := &JourneyPathItem{
		OriginStopRef:          "origin",
		DestinationStopRef:     "destination",
		OriginDepartureTime:    originTime,
		DestinationArrivalTime: arrivalTime,
		OriginPlatform:         "1",
		DestinationPlatform:    "2",
		OriginActivity:         []JourneyPathItemActivity{JourneyPathItemActivityPickup},
		DestinationActivity:    []JourneyPathItemActivity{JourneyPathItemActivitySetdown},
	}

	if got := boardPathStopRef(path, BoardTypeArrival); got != "destination" {
		t.Fatalf("arrival board matched %q, want destination", got)
	}
	if got := boardPathTime(path, BoardTypeArrival); !got.Equal(arrivalTime) {
		t.Fatalf("arrival board time = %v, want %v", got, arrivalTime)
	}
	if got := boardPathPlatform(path, BoardTypeArrival); got != "2" {
		t.Fatalf("arrival board platform = %q, want 2", got)
	}
	if boardPathIsUnavailable(path, BoardTypeArrival) {
		t.Fatal("setdown stop should be present on arrivals board")
	}
	if boardPathIsUnavailable(path, BoardTypeDeparture) {
		t.Fatal("pickup stop should be present on departures board")
	}
}

func TestBoardPathSelectorsExcludeWrongSingleActivity(t *testing.T) {
	path := &JourneyPathItem{
		OriginActivity:      []JourneyPathItemActivity{JourneyPathItemActivitySetdown},
		DestinationActivity: []JourneyPathItemActivity{JourneyPathItemActivityPickup},
	}

	if !boardPathIsUnavailable(path, BoardTypeDeparture) {
		t.Fatal("setdown-only origin should be excluded from departures")
	}
	if !boardPathIsUnavailable(path, BoardTypeArrival) {
		t.Fatal("pickup-only destination should be excluded from arrivals")
	}
}

func TestBoardRealtimeStopTime(t *testing.T) {
	arrivalTime := time.Date(2026, 7, 9, 10, 12, 0, 0, time.UTC)
	departureTime := arrivalTime.Add(2 * time.Minute)
	stop := &RealtimeJourneyStops{ArrivalTime: arrivalTime, DepartureTime: departureTime}

	if got := boardRealtimeStopTime(stop, BoardTypeArrival); !got.Equal(arrivalTime) {
		t.Fatalf("arrival realtime time = %v, want %v", got, arrivalTime)
	}
	if got := boardRealtimeStopTime(stop, BoardTypeDeparture); !got.Equal(departureTime) {
		t.Fatalf("departure realtime time = %v, want %v", got, departureTime)
	}
}

func TestBoardDestinationDisplayUsesJourneyOriginForArrivals(t *testing.T) {
	journey := &Journey{Path: []*JourneyPathItem{
		{OriginStop: &Stop{PrimaryName: "First Stop"}},
		{DestinationDisplay: "Final Destination"},
	}}

	if got := boardDestinationDisplay(journey, journey.Path[1], BoardTypeArrival); got != "First Stop" {
		t.Fatalf("arrival display = %q, want first origin stop", got)
	}
	if got := boardDestinationDisplay(journey, journey.Path[1], BoardTypeDeparture); got != "Final Destination" {
		t.Fatalf("departure display = %q, want path destination display", got)
	}
}
