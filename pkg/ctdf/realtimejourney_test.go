package ctdf

import "testing"

func TestRealtimeStopsPreserveRepeatedStopOccurrences(t *testing.T) {
	journey := &RealtimeJourney{}
	journey.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "waterloo", JourneyStopIndex: 0, Platform: "1"})
	journey.SetRealtimeStop(&RealtimeJourneyStops{StopRef: "waterloo", JourneyStopIndex: 3, Platform: "4"})

	if len(journey.Stops) != 2 {
		t.Fatalf("expected two Waterloo calls, got %d", len(journey.Stops))
	}
	if got := journey.RealtimeStop("waterloo", 0).Platform; got != "1" {
		t.Fatalf("expected first call platform 1, got %s", got)
	}
	if got := journey.RealtimeStop("waterloo", 3).Platform; got != "4" {
		t.Fatalf("expected second call platform 4, got %s", got)
	}
}

func TestRealtimeStopReadsLegacyStopRefKey(t *testing.T) {
	legacy := &RealtimeJourney{
		Journey: &Journey{Path: []*JourneyPathItem{{OriginStopRef: "origin", DestinationStopRef: "waterloo"}}},
		Stops:   map[string]*RealtimeJourneyStops{"waterloo": {StopRef: "waterloo", Platform: "1"}},
	}
	if got := legacy.RealtimeStop("waterloo", 1); got == nil || got.Platform != "1" {
		t.Fatal("expected legacy stop-ref-keyed realtime stop to remain readable")
	}

	legacy.Journey.Path = append(legacy.Journey.Path,
		&JourneyPathItem{OriginStopRef: "waterloo", DestinationStopRef: "other"},
		&JourneyPathItem{OriginStopRef: "other", DestinationStopRef: "waterloo"},
	)
	if got := legacy.RealtimeStop("waterloo", 3); got != nil {
		t.Fatal("legacy stop must not be reused for an ambiguous later occurrence")
	}
}
