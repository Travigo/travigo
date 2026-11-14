package ctdf

import (
	"testing"
	"time"
)

func TestInterpolateMissingJourneyTimes(t *testing.T) {
	start := time.Date(0, 1, 1, 23, 50, 0, 0, time.UTC)
	journey := &Journey{Calls: JourneyCallsFromLegs([]*JourneyPathItem{
		{OriginDepartureTime: start},
		{},
		{DestinationArrivalTime: start.Add(30 * time.Minute)},
	}...)}
	if err := InterpolateMissingJourneyTimes(journey); err != nil {
		t.Fatal(err)
	}
	for index, path := range journey.Legs() {
		if !path.OriginDepartureTime.Equal(start.Add(time.Duration(index)*10*time.Minute)) ||
			!path.DestinationArrivalTime.Equal(start.Add(time.Duration(index+1)*10*time.Minute)) {
			t.Fatalf("unexpected interpolated path %d: %#v", index, path)
		}
	}
	if !journey.DepartureTime.Equal(start) || journey.Calls[3].Arrival.Day() != 2 {
		t.Fatal("lost departure or next-day offset")
	}
}

func TestInterpolateMissingJourneyTimesPreservesKnownDwell(t *testing.T) {
	start := time.Date(0, 1, 1, 8, 40, 0, 0, time.UTC)
	journey := &Journey{Calls: JourneyCallsFromLegs([]*JourneyPathItem{
		{OriginArrivalTime: start.Add(-time.Minute), OriginDepartureTime: start, DestinationArrivalTime: start.Add(7 * time.Minute)},
		{OriginDepartureTime: start.Add(9 * time.Minute)},
		{DestinationArrivalTime: start.Add(19 * time.Minute)},
	}...)}
	if err := InterpolateMissingJourneyTimes(journey); err != nil {
		t.Fatal(err)
	}
	if !journey.Calls[0].Arrival.Equal(start.Add(-time.Minute)) ||
		!journey.Calls[1].Arrival.Equal(start.Add(7*time.Minute)) ||
		!journey.Calls[1].Departure.Equal(start.Add(9*time.Minute)) ||
		!journey.Calls[2].Departure.Equal(start.Add(14*time.Minute)) {
		t.Fatalf("changed known dwell or interpolated from wrong bound: %#v", journey.Calls)
	}
}

func TestInterpolateMissingJourneyTimesPreservesArrivalSides(t *testing.T) {
	start := time.Date(0, 1, 1, 8, 0, 0, 0, time.UTC)
	journey := &Journey{Calls: JourneyCallsFromLegs(
		&JourneyPathItem{
			OriginStopRef: "a", DestinationStopRef: "b",
			OriginDepartureTime: start, DestinationArrivalTime: start.Add(5 * time.Minute),
		},
		&JourneyPathItem{
			OriginStopRef: "b", DestinationStopRef: "c",
			OriginArrivalTime: start.Add(7 * time.Minute), OriginDepartureTime: start.Add(8 * time.Minute),
			DestinationArrivalTime: start.Add(12 * time.Minute),
		},
	)}

	call := &journey.Calls[1]
	if call.IncomingArrival == nil {
		t.Fatal("fixture did not preserve the incoming arrival")
	}
	if err := InterpolateMissingJourneyTimes(journey); err != nil {
		t.Fatal(err)
	}
	if !call.Arrival.Equal(start.Add(7*time.Minute)) || !call.ArrivalTime().Equal(start.Add(5*time.Minute)) {
		t.Fatalf("interpolation changed the call's arrival sides: %#v", *call)
	}

	journey.Calls[1] = JourneyCallsFromLegs(
		&JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", OriginDepartureTime: start},
		&JourneyPathItem{OriginStopRef: "b", DestinationStopRef: "c", OriginArrivalTime: start.Add(7 * time.Minute), OriginDepartureTime: start.Add(8 * time.Minute)},
	)[1]
	if err := InterpolateMissingJourneyTimes(journey); err != nil {
		t.Fatal(err)
	}
	if !call.ArrivalTime().Equal(start.Add(7*time.Minute)) || !call.Arrival.Equal(start.Add(7*time.Minute)) {
		t.Fatalf("failed to repair the missing incoming arrival: %#v", *call)
	}
}

func TestInterpolateMissingJourneyTimesRejectsUnresolvableTrips(t *testing.T) {
	start := time.Date(0, 1, 1, 8, 0, 0, 0, time.UTC)
	for name, journey := range map[string]*Journey{
		"missing endpoint": {Calls: JourneyCallsFromLegs(&JourneyPathItem{OriginDepartureTime: start})},
		"backwards":        {Calls: JourneyCallsFromLegs(&JourneyPathItem{OriginDepartureTime: start, DestinationArrivalTime: start.Add(-time.Minute)})},
		"no calls":         {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := InterpolateMissingJourneyTimes(journey); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	if err := InterpolateMissingJourneyTimes(&Journey{Calls: []JourneyCall{}}); err != nil {
		t.Fatalf("explicit empty calls array should be a valid no-op: %v", err)
	}
}
