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

func TestBoardRealtimeStopUsesPreloadedAlias(t *testing.T) {
	journey := &RealtimeJourney{}
	want := &RealtimeJourneyStops{StopRef: "realtime-stop", JourneyStopIndex: 2}
	journey.SetRealtimeStop(want)

	got := boardRealtimeStop(journey, "scheduled-stop", 2, map[string][]string{
		"scheduled-stop": {"scheduled-stop", "realtime-stop"},
	})
	if got != want {
		t.Fatalf("realtime stop = %#v, want alias match %#v", got, want)
	}
}

func TestBoardEntryIsDelayed(t *testing.T) {
	scheduled := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	if !boardEntryIsDelayed(scheduled, scheduled.Add(4*time.Minute), false) {
		t.Fatal("later realtime time should be delayed")
	}
	if boardEntryIsDelayed(scheduled, scheduled, false) {
		t.Fatal("on-time realtime time should not be delayed")
	}
	if boardEntryIsDelayed(scheduled, scheduled.Add(2*time.Minute), true) {
		t.Fatal("cancelled board entry should not be delayed")
	}
}

func TestBoardRealtimeTimeOnDateFallsBackWhenTimeIsMissing(t *testing.T) {
	scheduled := time.Date(2026, 8, 7, 23, 58, 0, 0, time.FixedZone("BST", 60*60))

	if got := boardRealtimeTimeOnDate(scheduled, time.Time{}); !got.Equal(scheduled) {
		t.Fatalf("missing realtime time = %v, want scheduled time %v", got, scheduled)
	}
}

func TestBoardRealtimeTimeOnDatePlacesDatelessClockNearestSchedule(t *testing.T) {
	location := time.FixedZone("BST", 60*60)
	scheduled := time.Date(2026, 8, 7, 23, 58, 0, 0, location)
	datelessMidnight := time.Date(0, time.January, 1, 0, 3, 0, 0, time.UTC)

	got := boardRealtimeTimeOnDate(scheduled, datelessMidnight)
	want := time.Date(2026, 8, 8, 0, 3, 0, 0, location)
	if !got.Equal(want) {
		t.Fatalf("dateless realtime time = %v, want %v", got, want)
	}
}

func TestBoardRealtimeTimeOnDatePreservesAbsoluteTimestamp(t *testing.T) {
	location := time.FixedZone("BST", 60*60)
	scheduled := time.Date(2026, 8, 7, 22, 47, 0, 0, location)
	absoluteRealtime := time.Date(2026, 8, 7, 22, 53, 0, 0, location)

	if got := boardRealtimeTimeOnDate(scheduled, absoluteRealtime); !got.Equal(absoluteRealtime) {
		t.Fatalf("absolute realtime time = %v, want %v", got, absoluteRealtime)
	}
}

func TestServiceTimeOnDatePreservesServiceDayOverflow(t *testing.T) {
	serviceTime := time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC).Add(25*time.Hour + 30*time.Minute)
	date := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	got := serviceTimeOnDate(date, serviceTime)
	want := time.Date(2026, 7, 31, 1, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("service time on date = %v, want %v", got, want)
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

func TestBoardDestinationDisplayWithRealtimeUsesLastNonCancelledStop(t *testing.T) {
	journey := &Journey{
		DestinationDisplay: "Terminal",
		Path: []*JourneyPathItem{
			{
				OriginStopRef:      "origin",
				OriginStop:         &Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin"},
				DestinationStopRef: "middle",
				DestinationStop:    &Stop{PrimaryIdentifier: "middle", PrimaryName: "Middle"},
			},
			{
				OriginStopRef:      "middle",
				DestinationStopRef: "penultimate",
				DestinationStop:    &Stop{PrimaryIdentifier: "penultimate", PrimaryName: "Penultimate"},
			},
			{
				OriginStopRef:      "penultimate",
				DestinationStopRef: "terminal",
				DestinationStop:    &Stop{PrimaryIdentifier: "terminal", PrimaryName: "Terminal"},
			},
		},
	}
	realtimeJourney := &RealtimeJourney{Journey: journey}
	realtimeJourney.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "terminal", JourneyStopIndex: 3, Cancelled: true})

	if got := BoardDestinationDisplayWithRealtime(journey, realtimeJourney, journey.DestinationDisplay, BoardTypeDeparture, false); got != "Penultimate" {
		t.Fatalf("destination display = %q, want Penultimate", got)
	}
}

func TestBoardDestinationDisplayWithRealtimeSkipsConsecutiveCancelledStops(t *testing.T) {
	journey := &Journey{
		DestinationDisplay: "Terminal",
		Path: []*JourneyPathItem{
			{
				OriginStopRef:      "origin",
				OriginStop:         &Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin"},
				DestinationStopRef: "middle",
				DestinationStop:    &Stop{PrimaryIdentifier: "middle", PrimaryName: "Middle"},
			},
			{
				OriginStopRef:      "middle",
				DestinationStopRef: "penultimate",
				DestinationStop:    &Stop{PrimaryIdentifier: "penultimate", PrimaryName: "Penultimate"},
			},
			{
				OriginStopRef:      "penultimate",
				DestinationStopRef: "terminal",
				DestinationStop:    &Stop{PrimaryIdentifier: "terminal", PrimaryName: "Terminal"},
			},
		},
	}
	realtimeJourney := &RealtimeJourney{Journey: journey}
	realtimeJourney.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "penultimate", JourneyStopIndex: 2, Cancelled: true})
	realtimeJourney.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "terminal", JourneyStopIndex: 3, Cancelled: true})

	if got := BoardDestinationDisplayWithRealtime(journey, realtimeJourney, journey.DestinationDisplay, BoardTypeDeparture, false); got != "Middle" {
		t.Fatalf("destination display = %q, want Middle", got)
	}
}

func TestBoardDestinationDisplayWithRealtimeKeepsWholeJourneyCancellationDestination(t *testing.T) {
	journey := &Journey{
		DestinationDisplay: "Terminal",
		Path: []*JourneyPathItem{{
			OriginStopRef:      "origin",
			OriginStop:         &Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin"},
			DestinationStopRef: "terminal",
			DestinationStop:    &Stop{PrimaryIdentifier: "terminal", PrimaryName: "Terminal"},
		}},
	}
	realtimeJourney := &RealtimeJourney{Journey: journey}
	realtimeJourney.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "terminal", JourneyStopIndex: 1, Cancelled: true})

	if got := BoardDestinationDisplayWithRealtime(journey, realtimeJourney, journey.DestinationDisplay, BoardTypeDeparture, true); got != "Terminal" {
		t.Fatalf("destination display = %q, want Terminal", got)
	}
}

func TestBoardDestinationDisplayWithRealtimeKeepsUncancelledTerminalDestination(t *testing.T) {
	journey := &Journey{
		DestinationDisplay: "Terminal",
		Path: []*JourneyPathItem{{
			OriginStopRef:      "origin",
			OriginStop:         &Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin"},
			DestinationStopRef: "terminal",
			DestinationStop:    &Stop{PrimaryIdentifier: "terminal", PrimaryName: "Terminal"},
		}},
	}
	realtimeJourney := &RealtimeJourney{Journey: journey}

	if got := BoardDestinationDisplayWithRealtime(journey, realtimeJourney, journey.DestinationDisplay, BoardTypeDeparture, false); got != "Terminal" {
		t.Fatalf("destination display = %q, want Terminal", got)
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

func TestPrecedingBlockJourneyRefsUsesOnlyEarlierRunsNewestFirst(t *testing.T) {
	base := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	blockJourneys := []blockJourneyReference{
		{PrimaryIdentifier: "route-a-early", DepartureTime: base},
		{PrimaryIdentifier: "route-b-current", DepartureTime: base.Add(time.Hour)},
		{PrimaryIdentifier: "route-c-target", DepartureTime: base.Add(2 * time.Hour)},
		{PrimaryIdentifier: "route-d-later", DepartureTime: base.Add(3 * time.Hour)},
	}
	target := &Journey{PrimaryIdentifier: "route-c-target", DepartureTime: base.Add(2 * time.Hour)}

	refs := precedingBlockJourneyRefs(blockJourneys, target)
	if len(refs) != 2 || refs[0] != "route-b-current" || refs[1] != "route-a-early" {
		t.Fatalf("preceding block refs = %v, want [route-b-current route-a-early]", refs)
	}
}

func TestApplyBlockRealtimeEstimatesBatchesRefsAndUsesNewestPrecedingRun(t *testing.T) {
	base := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	firstEntry := &DepartureBoard{
		Journey: &Journey{PrimaryIdentifier: "target-1", DepartureTime: base.Add(2 * time.Hour)},
		Time:    base.Add(2 * time.Hour),
		Type:    DepartureBoardRecordTypeScheduled,
	}
	secondEntry := &DepartureBoard{
		Journey: &Journey{PrimaryIdentifier: "target-2", DepartureTime: base.Add(3 * time.Hour)},
		Time:    base.Add(3 * time.Hour),
		Type:    DepartureBoardRecordTypeScheduled,
	}
	blockJourneys := map[string][]blockJourneyReference{
		"block": {
			{PrimaryIdentifier: "early", DepartureTime: base},
			{PrimaryIdentifier: "current", DepartureTime: base.Add(time.Hour)},
			{PrimaryIdentifier: "target-1", DepartureTime: base.Add(2 * time.Hour)},
		},
	}

	lookupCalls := 0
	var lookedUpRefs []string
	stats := applyBlockRealtimeEstimates(
		[]blockEstimateCandidate{
			{entry: firstEntry, blockKey: "block"},
			{entry: secondEntry, blockKey: "block"},
		},
		blockJourneys,
		&DepartureBoardRealtimeLookup{
			FindByJourneyIDs: func(refs []string) map[string]*RealtimeJourney {
				lookupCalls++
				lookedUpRefs = append(lookedUpRefs, refs...)
				return map[string]*RealtimeJourney{
					"early":   {Offset: 2 * time.Minute},
					"current": {Offset: 5 * time.Minute},
				}
			},
		},
	)

	if lookupCalls != 1 {
		t.Fatalf("batch lookup calls = %d, want 1", lookupCalls)
	}
	if len(lookedUpRefs) != 3 {
		t.Fatalf("looked up refs = %v, want three unique preceding refs", lookedUpRefs)
	}
	if !firstEntry.Time.Equal(base.Add(2*time.Hour + 5*time.Minute)) {
		t.Fatalf("first estimate = %s, want current-run offset", firstEntry.Time)
	}
	if !secondEntry.Time.Equal(base.Add(3*time.Hour + 5*time.Minute)) {
		t.Fatalf("second estimate = %s, want newest preceding offset", secondEntry.Time)
	}
	if firstEntry.Type != DepartureBoardRecordTypeEstimated || secondEntry.Type != DepartureBoardRecordTypeEstimated {
		t.Fatal("expected both entries to be estimated")
	}
	if stats.realtimeMatched != 2 || stats.estimated != 2 {
		t.Fatalf("unexpected estimate stats: %+v", stats)
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
