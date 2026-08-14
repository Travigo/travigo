package journeyplanner

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestApplyRealtimeLegTimesUsesOccurrenceAndRejectsCancelledCall(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{OriginStopRef: "origin", DestinationStopRef: "destination"}}}
	journey.RealtimeJourney = &ctdf.RealtimeJourney{Journey: journey, Stops: map[string]*ctdf.RealtimeJourneyStops{}}
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{
		StopRef:          "origin",
		JourneyStopIndex: 0,
		DepartureTime:    time.Date(0, time.January, 1, 10, 5, 0, 0, time.UTC),
	})
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{
		StopRef:          "destination",
		JourneyStopIndex: 1,
		ArrivalTime:      time.Date(0, time.January, 1, 10, 45, 0, 0, time.UTC),
	})
	item := &ctdf.JourneyPlanRouteItem{
		Journey: journey, OriginStopRef: "origin", DestinationStopRef: "destination",
		StartTime: serviceDate.Add(10 * time.Hour), ArrivalTime: serviceDate.Add(40 * time.Minute),
	}
	if !applyRealtimeLegTimes(item, 0, 1) {
		t.Fatal("valid realtime calls were rejected")
	}
	if got := item.StartTime; !got.Equal(serviceDate.Add(10*time.Hour + 5*time.Minute)) {
		t.Fatalf("start time = %v", got)
	}
	if got := item.ArrivalTime; !got.Equal(serviceDate.Add(10*time.Hour + 45*time.Minute)) {
		t.Fatalf("arrival time = %v", got)
	}

	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "origin", JourneyStopIndex: 0, Cancelled: true})
	if applyRealtimeLegTimes(item, 0, 1) {
		t.Fatal("cancelled boarding call was accepted")
	}
}

func TestApplyRealtimeLegTimesUsesGraphOccurrenceForRepeatedStop(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "loop", DestinationStopRef: "middle"},
		{OriginStopRef: "middle", DestinationStopRef: "loop"},
		{OriginStopRef: "loop", DestinationStopRef: "destination"},
	}}
	journey.RealtimeJourney = &ctdf.RealtimeJourney{Journey: journey, Stops: map[string]*ctdf.RealtimeJourneyStops{}}
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "loop", JourneyStopIndex: 0, Cancelled: true})
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "loop", JourneyStopIndex: 2, DepartureTime: serviceDate.Add(11 * time.Hour)})
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "destination", JourneyStopIndex: 3, ArrivalTime: serviceDate.Add(12 * time.Hour)})
	item := &ctdf.JourneyPlanRouteItem{
		Journey: journey, OriginStopRef: "loop", DestinationStopRef: "destination",
		StartTime: serviceDate.Add(10 * time.Hour), ArrivalTime: serviceDate.Add(12 * time.Hour),
	}
	if !applyRealtimeLegTimes(item, 2, 3) {
		t.Fatal("later occurrence was confused with the cancelled first call")
	}
	if !item.StartTime.Equal(serviceDate.Add(11 * time.Hour)) {
		t.Fatalf("start time = %v", item.StartTime)
	}
}
