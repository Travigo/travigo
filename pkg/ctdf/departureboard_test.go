package ctdf

import (
	"reflect"
	"testing"
	"time"
)

func testJourneyCalls(legs ...*JourneyPathItem) []JourneyCall {
	return JourneyCallsFromLegs(legs...)
}

func TestBoardUsesServiceDateForStoredDarwinAmendmentClocks(t *testing.T) {
	serviceDate := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	journey := &Journey{
		PrimaryIdentifier: "amended-train",
		Availability:      &Availability{Match: []AvailabilityRule{{Type: AvailabilityMatchAll}}},
		Calls: testJourneyCalls(&JourneyPathItem{
			OriginStopRef:          "cambridge-south",
			OriginDepartureTime:    time.Date(1, 1, 1, 19, 29, 0, 0, time.UTC),
			DestinationStopRef:     "cambridge",
			DestinationArrivalTime: time.Date(1, 1, 2, 0, 5, 0, 0, time.UTC),
		}),
	}
	for _, test := range []struct {
		boardType BoardType
		stopRef   string
		want      time.Time
	}{
		{BoardTypeDeparture, "cambridge-south", serviceDate.Add(19*time.Hour + 29*time.Minute)},
		{BoardTypeArrival, "cambridge", serviceDate.Add(24*time.Hour + 5*time.Minute)},
	} {
		t.Run(string(test.boardType), func(t *testing.T) {
			entries := GenerateBoardFromJourneysForServiceDate([]*Journey{journey}, []string{test.stopRef}, serviceDate, serviceDate, false, nil, test.boardType)
			if len(entries) != 1 {
				t.Fatalf("got %d board entries, want one", len(entries))
			}
			if !entries[0].Time.Equal(test.want) {
				t.Fatalf("board time = %v, want %v", entries[0].Time, test.want)
			}
		})
	}
}

func TestBoardRealtimeMatchesServiceDate(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	serviceDate := time.Date(2026, 7, 2, 0, 30, 0, 0, london)
	if !boardRealtimeMatchesServiceDate(&RealtimeJourney{JourneyRunDate: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)}, serviceDate) {
		t.Fatal("matching service-date label was rejected")
	}
	if boardRealtimeMatchesServiceDate(&RealtimeJourney{JourneyRunDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}, serviceDate) || boardRealtimeMatchesServiceDate(&RealtimeJourney{}, serviceDate) {
		t.Fatal("different or missing run date was accepted")
	}
}

func TestBoardPreservesRepeatedStopOccurrencesAndServiceDate(t *testing.T) {
	serviceDate := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	clock := func(hours, minutes int) time.Time { return time.Date(0, 1, 1, hours, minutes, 0, 0, time.UTC) }
	journey := &Journey{
		PrimaryIdentifier: "loop",
		Availability:      &Availability{Match: []AvailabilityRule{{Type: AvailabilityMatchAll}}},
		Calls: testJourneyCalls(
			&JourneyPathItem{OriginStopRef: "stop", DestinationStopRef: "stop", OriginDepartureTime: clock(23, 0), DestinationArrivalTime: clock(23, 1)},
			&JourneyPathItem{OriginStopRef: "stop", DestinationStopRef: "stop", OriginDepartureTime: clock(25, 30), DestinationArrivalTime: clock(25, 31)},
			&JourneyPathItem{OriginStopRef: "stop", DestinationStopRef: "terminal", OriginDepartureTime: clock(26, 0), DestinationArrivalTime: clock(27, 0)},
		),
	}
	start := serviceDate.Add(24*time.Hour + time.Hour)
	entries := GenerateBoardFromJourneysForServiceDate([]*Journey{journey}, []string{"stop"}, serviceDate, start, false, nil, BoardTypeDeparture)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want two later occurrences", len(entries))
	}
	if entries[0].OccurrenceIdentifier == "" || entries[0].OccurrenceIdentifier == entries[1].OccurrenceIdentifier {
		t.Fatal("occurrence IDs are missing or identical")
	}
	for _, entry := range entries {
		if entry.ServiceDate != "2026-11-03" || entry.Time.Before(start) {
			t.Fatalf("incorrect occurrence: %#v", entry)
		}
	}
}

func TestBoardAppliesServiceDateCancellationAlerts(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	journey := &Journey{PrimaryIdentifier: "train", Availability: &Availability{Match: []AvailabilityRule{{Type: AvailabilityMatchAll}}}, Calls: testJourneyCalls(&JourneyPathItem{OriginStopRef: "stop", DestinationStopRef: "terminal", OriginDepartureTime: time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC)})}
	for offset, want := range []DepartureBoardRecordType{
		DepartureBoardRecordTypeScheduled,
		DepartureBoardRecordTypeCancelled,
		DepartureBoardRecordTypeScheduled,
		DepartureBoardRecordTypeCancelled,
		DepartureBoardRecordTypeScheduled,
	} {
		date := start.AddDate(0, 0, offset)
		lookup := &DepartureBoardRealtimeLookup{}
		if offset == 1 || offset == 3 {
			lookup.CancelledJourneyIDs = map[string]struct{}{"train": {}}
		}
		entries := GenerateBoardFromJourneysForServiceDate([]*Journey{journey}, []string{"stop"}, date, date, false, lookup, BoardTypeDeparture)
		if len(entries) != 1 || entries[0].Type != want {
			t.Fatalf("%s: expected one %s entry, got %#v", date.Format(YearMonthDayFormat), want, entries)
		}
	}
}

func TestBoardDoesNotApplyAnotherRunDatesCancellation(t *testing.T) {
	date := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	journey := &Journey{PrimaryIdentifier: "journey", Availability: &Availability{Match: []AvailabilityRule{{Type: AvailabilityMatchAll}}}, Calls: testJourneyCalls(&JourneyPathItem{OriginStopRef: "stop", DestinationStopRef: "terminal", OriginDepartureTime: time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC)})}
	update := &RealtimeJourney{JourneyRunDate: date.AddDate(0, 0, -1), Cancelled: true}
	lookup := &DepartureBoardRealtimeLookup{ByJourneyID: map[string]*RealtimeJourney{"journey": update}}
	entries := GenerateBoardFromJourneysForServiceDate([]*Journey{journey}, []string{"stop"}, date, date, false, lookup, BoardTypeDeparture)
	if len(entries) != 1 || entries[0].Type != DepartureBoardRecordTypeScheduled {
		t.Fatalf("other run cancellation applied: %#v", entries)
	}
	update.JourneyRunDate = date
	entries = GenerateBoardFromJourneysForServiceDate([]*Journey{journey}, []string{"stop"}, date, date, false, lookup, BoardTypeDeparture)
	if len(entries) != 1 || entries[0].Type != DepartureBoardRecordTypeCancelled {
		t.Fatalf("matching run cancellation missing: %#v", entries)
	}
}

func TestBoardPathSelectorsUseDestinationForArrivals(t *testing.T) {
	originTime := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	arrivalTime := originTime.Add(12 * time.Minute)
	origin := &JourneyCall{
		Departure: originTime,
		StopRef:   "origin",
		Arrival:   originTime,
		Platform:  "1",
		Activity:  []JourneyPathItemActivity{JourneyPathItemActivityPickup},
	}
	destination := &JourneyCall{
		StopRef:  "destination",
		End:      true,
		Arrival:  arrivalTime,
		Platform: "2",
		Activity: []JourneyPathItemActivity{JourneyPathItemActivitySetdown},
	}

	if got := boardCallStopRef(origin, destination, BoardTypeArrival); got != "destination" {
		t.Fatalf("arrival board matched %q, want destination", got)
	}
	if got := boardCallTime(origin, destination, BoardTypeArrival); !got.Equal(arrivalTime) {
		t.Fatalf("arrival board time = %v, want %v", got, arrivalTime)
	}
	if got := boardCallPlatform(origin, destination, BoardTypeArrival); got != "2" {
		t.Fatalf("arrival board platform = %q, want 2", got)
	}
	if boardCallIsUnavailable(origin, destination, BoardTypeArrival) {
		t.Fatal("setdown stop should be present on arrivals board")
	}
	if boardCallIsUnavailable(origin, destination, BoardTypeDeparture) {
		t.Fatal("pickup stop should be present on departures board")
	}
}

func TestBoardPathSelectorsExcludeWrongSingleActivity(t *testing.T) {
	origin := &JourneyCall{Activity: []JourneyPathItemActivity{JourneyPathItemActivitySetdown}}
	destination := &JourneyCall{Activity: []JourneyPathItemActivity{JourneyPathItemActivityPickup}}

	if !boardCallIsUnavailable(origin, destination, BoardTypeDeparture) {
		t.Fatal("setdown-only origin should be excluded from departures")
	}
	if !boardCallIsUnavailable(origin, destination, BoardTypeArrival) {
		t.Fatal("pickup-only destination should be excluded from arrivals")
	}
	pass := &JourneyCall{Activity: []JourneyPathItemActivity{JourneyPathItemActivityPass}}
	if !boardCallIsUnavailable(pass, pass, BoardTypeDeparture) || !boardCallIsUnavailable(pass, pass, BoardTypeArrival) {
		t.Fatal("a passing call should be excluded from both boards")
	}
	if boardCallIsUnavailable(&JourneyCall{}, &JourneyCall{}, BoardTypeDeparture) || boardCallIsUnavailable(&JourneyCall{}, &JourneyCall{}, BoardTypeArrival) {
		t.Fatal("unspecified activity should retain the existing board behavior")
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

func TestServiceTimeOnDateAcrossClockChanges(t *testing.T) {
	location, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ day, clock, want string }{
		{"2026-03-29", "08:00", "2026-03-29T08:00:00+01:00"},
		{"2026-10-25", "08:00", "2026-10-25T08:00:00Z"},
		{"2026-10-25", "00:30", "2026-10-25T01:30:00+01:00"},
		{"2026-10-25", "01:30", "2026-10-25T01:30:00Z"},
	} {
		date, err := time.ParseInLocation("2006-01-02", test.day, location)
		if err != nil {
			t.Fatal(err)
		}
		clock, err := time.Parse("15:04", test.clock)
		if err != nil {
			t.Fatal(err)
		}
		want, err := time.Parse(time.RFC3339, test.want)
		if err != nil {
			t.Fatal(err)
		}
		if got := serviceTimeOnDate(date, clock); !got.Equal(want) {
			t.Errorf("%s %s: got %s, want %s", test.day, test.clock, got, want)
		}
	}
	date := time.Date(2026, 3, 29, 0, 0, 0, 0, location)
	overflow := time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC).Add(25*time.Hour + 30*time.Minute)
	want := time.Date(2026, 3, 30, 1, 30, 0, 0, location)
	if got := serviceTimeOnDate(date, overflow); !got.Equal(want) {
		t.Errorf("overflow: got %s, want %s", got, want)
	}
}

func TestBoardDestinationDisplayUsesJourneyOriginForArrivals(t *testing.T) {
	journey := &Journey{Calls: JourneyCallsFromLegs([]*JourneyPathItem{
		{OriginStop: &Stop{PrimaryName: "First Stop"}},
		{DestinationDisplay: "Final Destination"},
	}...)}

	if got := BoardDestinationDisplay(journey, journey.Calls[1].Outgoing.DestinationDisplay, BoardTypeArrival); got != "First Stop" {
		t.Fatalf("arrival display = %q, want first origin stop", got)
	}
	if got := BoardDestinationDisplay(journey, journey.Calls[1].Outgoing.DestinationDisplay, BoardTypeDeparture); got != "Final Destination" {
		t.Fatalf("departure display = %q, want path destination display", got)
	}
}

func TestBoardDestinationDisplayWithRealtimeUsesLastNonCancelledStop(t *testing.T) {
	journey := &Journey{
		DestinationDisplay: "Terminal",
		Calls: JourneyCallsFromLegs([]*JourneyPathItem{
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
		}...),
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
		Calls: JourneyCallsFromLegs([]*JourneyPathItem{
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
		}...),
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
		Calls: JourneyCallsFromLegs([]*JourneyPathItem{{
			OriginStopRef:      "origin",
			OriginStop:         &Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin"},
			DestinationStopRef: "terminal",
			DestinationStop:    &Stop{PrimaryIdentifier: "terminal", PrimaryName: "Terminal"},
		}}...),
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
		Calls: JourneyCallsFromLegs([]*JourneyPathItem{{
			OriginStopRef:      "origin",
			OriginStop:         &Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin"},
			DestinationStopRef: "terminal",
			DestinationStop:    &Stop{PrimaryIdentifier: "terminal", PrimaryName: "Terminal"},
		}}...),
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

func TestBoardDisplayReferenceDefersMissingStopsWithoutDatabase(t *testing.T) {
	// No database is configured: selecting references and fallback display text
	// must not attempt to hydrate any candidate.
	j := &Journey{Calls: JourneyCallsFromLegs([]*JourneyPathItem{{OriginStopRef: "origin", DestinationStopRef: "middle"}, {OriginStopRef: "middle", DestinationStopRef: "terminal"}}...)}
	rt := &RealtimeJourney{Journey: j}
	rt.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "terminal", JourneyStopIndex: 2, Cancelled: true})
	arrival := BoardDisplayReferenceFor(j, rt, BoardTypeArrival, false)
	if arrival == nil || arrival.StopRef != "origin" || !arrival.Arrival {
		t.Fatal("arrival reference lost")
	}
	shortened := BoardDisplayReferenceFor(j, rt, BoardTypeDeparture, false)
	if shortened == nil || shortened.StopRef != "middle" || shortened.Arrival {
		t.Fatal("shortened reference lost")
	}
	if BoardDisplayReferenceFor(j, rt, BoardTypeDeparture, true) != nil {
		t.Fatal("whole cancellation changed destination")
	}
	if got := BoardDestinationDisplayWithRealtime(j, rt, "Terminal", BoardTypeDeparture, false); got != "Terminal" {
		t.Fatalf("missing shortened stop must preserve fallback: %s", got)
	}
	if got := BoardDestinationDisplay(j, "Terminal", BoardTypeArrival); got != "origin" {
		t.Fatalf("arrival fallback = %s", got)
	}
}

func TestBoardArrivalCallsKeepPriorSegmentEndAtGap(t *testing.T) {
	journey := &Journey{Calls: JourneyCallsFromLegs(
		&JourneyPathItem{OriginStopRef: "origin", DestinationStopRef: "before-gap", DestinationStop: &Stop{PrimaryName: "Before gap"}},
		&JourneyPathItem{OriginStopRef: "after-gap", DestinationStopRef: "middle", DestinationStop: &Stop{PrimaryName: "Middle"}},
		&JourneyPathItem{OriginStopRef: "middle", DestinationStopRef: "terminal", DestinationStop: &Stop{PrimaryName: "Terminal"}},
	)}
	arrivalCalls := boardArrivalCalls(journey)
	var got []string
	for _, call := range arrivalCalls {
		got = append(got, call.StopRef)
	}
	want := []string{"origin", "before-gap", "middle", "terminal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arrival calls = %v, want %v", got, want)
	}

	realtime := &RealtimeJourney{Journey: journey}
	realtime.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "terminal", JourneyStopIndex: len(arrivalCalls) - 1, Cancelled: true})
	if got := boardDestinationDisplayWithRealtimeAndCalls(journey, realtime, "Terminal", BoardTypeDeparture, false, arrivalCalls); got != "Middle" {
		t.Fatalf("shortened destination = %q, want Middle", got)
	}
	if got := boardDisplayReferenceForCalls(journey, realtime, BoardTypeDeparture, false, arrivalCalls); got == nil || got.StopRef != "middle" {
		t.Fatalf("shortened display reference = %#v, want middle", got)
	}
}
