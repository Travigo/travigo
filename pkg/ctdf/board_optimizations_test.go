package ctdf

import (
	"reflect"
	"testing"
	"time"
)

func TestBoardDisplayWithRealtimeMatchesSeparateHelpers(t *testing.T) {
	journey := &Journey{Calls: JourneyCallsFromLegs(
		&JourneyPathItem{OriginStopRef: "origin", DestinationStopRef: "before-gap"},
		&JourneyPathItem{OriginStopRef: "after-gap", DestinationStopRef: "terminal"},
	)}
	realtime := &RealtimeJourney{Journey: journey}
	realtime.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "terminal", JourneyStopIndex: 2, Cancelled: true})
	for _, kind := range []BoardType{BoardTypeArrival, BoardTypeDeparture} {
		for _, cancelled := range []bool{false, true} {
			display, reference := BoardDisplayWithRealtime(journey, realtime, "fallback", kind, cancelled)
			if display != BoardDestinationDisplayWithRealtime(journey, realtime, "fallback", kind, cancelled) || !reflect.DeepEqual(reference, BoardDisplayReferenceFor(journey, realtime, kind, cancelled)) {
				t.Fatalf("display or reference differs for %s, cancelled=%v", kind, cancelled)
			}
		}
	}
}

func TestBlockDelayUsesTerminalAfterGap(t *testing.T) {
	clock := func(h, m int) time.Time { return time.Date(1, 1, 1, h, m, 0, 0, time.UTC) }
	date := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	block := &Journey{Calls: JourneyCallsFromLegs(
		&JourneyPathItem{OriginStopRef: "A", OriginDepartureTime: clock(23, 30), DestinationStopRef: "B", DestinationArrivalTime: clock(23, 45)},
		&JourneyPathItem{OriginStopRef: "C", OriginDepartureTime: clock(23, 50), DestinationStopRef: "D", DestinationArrivalTime: clock(0, 10)},
	)}
	target := &DepartureBoard{Journey: &Journey{Calls: JourneyCallsFromLegs(&JourneyPathItem{OriginStopRef: "D", OriginDepartureTime: clock(0, 20), DestinationStopRef: "E", DestinationArrivalTime: clock(0, 30)})}, Time: date.Add(24 * time.Hour), ServiceDate: "2026-11-15"}
	// The block and target use the target service date for schedule resolution.
	rt := &RealtimeJourney{Journey: block, Offset: 15 * time.Minute}
	if !BlockDelayCarriesIntoTarget(rt, target) {
		t.Fatal("scheduled delayed arrival should carry into target")
	}
	rt.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "D", JourneyStopIndex: 2, ArrivalTime: date.Add(24*time.Hour + 10*time.Minute)})
	if BlockDelayCarriesIntoTarget(rt, target) {
		t.Fatal("realtime arrival before target should not carry delay")
	}
}
