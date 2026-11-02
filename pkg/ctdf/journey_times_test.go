package ctdf

import (
	"testing"
	"time"
)

func TestInterpolateMissingJourneyTimes(t *testing.T) {
	start := time.Date(0, 1, 1, 23, 50, 0, 0, time.UTC)
	journey := &Journey{Path: []*JourneyPathItem{
		{OriginDepartureTime: start},
		{},
		{DestinationArrivalTime: start.Add(30 * time.Minute)},
	}}
	if err := InterpolateMissingJourneyTimes(journey); err != nil {
		t.Fatal(err)
	}
	for index, path := range journey.Path {
		if !path.OriginDepartureTime.Equal(start.Add(time.Duration(index)*10*time.Minute)) ||
			!path.DestinationArrivalTime.Equal(start.Add(time.Duration(index+1)*10*time.Minute)) {
			t.Fatalf("unexpected interpolated path %d: %#v", index, path)
		}
	}
	if !journey.DepartureTime.Equal(start) || journey.Path[2].DestinationArrivalTime.Day() != 2 {
		t.Fatal("lost departure or next-day offset")
	}
}

func TestInterpolateMissingJourneyTimesPreservesKnownDwell(t *testing.T) {
	start := time.Date(0, 1, 1, 8, 40, 0, 0, time.UTC)
	journey := &Journey{Path: []*JourneyPathItem{
		{OriginArrivalTime: start.Add(-time.Minute), OriginDepartureTime: start, DestinationArrivalTime: start.Add(7 * time.Minute)},
		{OriginDepartureTime: start.Add(9 * time.Minute)},
		{DestinationArrivalTime: start.Add(19 * time.Minute)},
	}}
	if err := InterpolateMissingJourneyTimes(journey); err != nil {
		t.Fatal(err)
	}
	if !journey.Path[0].OriginArrivalTime.Equal(start.Add(-time.Minute)) ||
		!journey.Path[1].OriginArrivalTime.Equal(start.Add(7*time.Minute)) ||
		!journey.Path[1].OriginDepartureTime.Equal(start.Add(9*time.Minute)) ||
		!journey.Path[2].OriginDepartureTime.Equal(start.Add(14*time.Minute)) {
		t.Fatalf("changed known dwell or interpolated from wrong bound: %#v", journey.Path)
	}
}

func TestInterpolateMissingJourneyTimesRejectsUnresolvableTrips(t *testing.T) {
	start := time.Date(0, 1, 1, 8, 0, 0, 0, time.UTC)
	for name, journey := range map[string]*Journey{
		"missing endpoint": {Path: []*JourneyPathItem{{OriginDepartureTime: start}}},
		"backwards":        {Path: []*JourneyPathItem{{OriginDepartureTime: start, DestinationArrivalTime: start.Add(-time.Minute)}}},
		"nil leg":          {Path: []*JourneyPathItem{nil}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := InterpolateMissingJourneyTimes(journey); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
