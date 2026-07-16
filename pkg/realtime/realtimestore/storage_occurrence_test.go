package realtimestore

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestStoredRealtimeStopsPreserveRepeatedOccurrences(t *testing.T) {
	journey := &ctdf.RealtimeJourney{}
	journey.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "waterloo", JourneyStopIndex: 0, Platform: "1"})
	journey.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "waterloo", JourneyStopIndex: 3, Platform: "4"})

	stored := storedRealtimeStopsFromCTDF(journey.Stops)
	roundTrip := (&storedRealtimeJourney{Stops: stored}).realtimeStops()
	result := &ctdf.RealtimeJourney{Stops: roundTrip}

	if len(result.Stops) != 2 {
		t.Fatalf("expected two stored calls, got %d", len(result.Stops))
	}
	if got := result.RealtimeStop("waterloo", 3); got == nil || got.Platform != "4" {
		t.Fatal("expected second call occurrence to survive storage round trip")
	}
}

func TestStoredRealtimeJourneyPreservesNextStopIndex(t *testing.T) {
	journey := &ctdf.RealtimeJourney{NextStopRef: "waterloo", NextStopIndex: 3}
	stored := storedRealtimeJourneyFromCTDF(journey)
	result := stored.toCTDF(nil, false)

	if result.NextStopRef != "waterloo" || result.NextStopIndex != 3 {
		t.Fatalf("unexpected next stop after round trip: %#v", result)
	}
}
