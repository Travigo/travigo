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

	if got := BoardDestinationDisplay(journey, journey.Path[1].DestinationDisplay, BoardTypeArrival); got != "First Stop" {
		t.Fatalf("arrival display = %q, want first origin stop", got)
	}
	if got := BoardDestinationDisplay(journey, journey.Path[1].DestinationDisplay, BoardTypeDeparture); got != "Final Destination" {
		t.Fatalf("departure display = %q, want path destination display", got)
	}
}

func TestIsBoardJourneyCancelled(t *testing.T) {
	journey := &Journey{PrimaryIdentifier: "journey-1"}

	if !IsBoardJourneyCancelled(journey, &RealtimeJourney{Cancelled: true}, nil) {
		t.Fatal("cancelled realtime journey should cancel the board entry")
	}
	if !IsBoardJourneyCancelled(journey, nil, map[string]struct{}{"journey-1": {}}) {
		t.Fatal("active journey cancellation alert should cancel the board entry")
	}
	if IsBoardJourneyCancelled(journey, nil, nil) {
		t.Fatal("journey without a cancellation signal should not be cancelled")
	}
}

func TestDeduplicateBoardEntriesPrefersFirstRecord(t *testing.T) {
	realtime := &DepartureBoard{Journey: &Journey{PrimaryIdentifier: "journey-1"}, Type: DepartureBoardRecordTypeCancelled}
	scheduledDuplicate := &DepartureBoard{Journey: &Journey{PrimaryIdentifier: "journey-1"}, Type: DepartureBoardRecordTypeScheduled}
	other := &DepartureBoard{Journey: &Journey{PrimaryIdentifier: "journey-2"}}

	entries := DeduplicateBoardEntries([]*DepartureBoard{realtime, scheduledDuplicate, other})
	if len(entries) != 2 {
		t.Fatalf("deduplicated entries = %d, want 2", len(entries))
	}
	if entries[0] != realtime {
		t.Fatal("expected realtime record to take precedence over its scheduled duplicate")
	}
}

func TestRealtimeJourneySuppressesBoardOnlyOnReplacementDates(t *testing.T) {
	realtimeJourney := &RealtimeJourney{
		SuppressFromDepartures:     true,
		SuppressFromDepartureDates: []string{"2026-07-10"},
	}

	if !realtimeJourney.SuppressesBoardAt(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("expected listed replacement date to suppress board entry")
	}
	if realtimeJourney.SuppressesBoardAt(time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("unexpected suppression outside listed replacement dates")
	}
}
