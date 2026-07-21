package vehicletracker

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestMatchJourneyPositionUsesDistanceAlongLeg(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{
		Track: []ctdf.Location{
			{Type: "Point", Coordinates: []float64{0, 51}},
			{Type: "Point", Coordinates: []float64{0.01, 51}},
			{Type: "Point", Coordinates: []float64{0.02, 51}},
		},
	}}}

	match, ok := matchJourneyPosition(journey, ctdf.Location{Type: "Point", Coordinates: []float64{0.015, 51}})
	if !ok {
		t.Fatal("expected match")
	}
	if match.PathIndex != 0 || match.LegProgress < 0.74 || match.LegProgress > 0.76 {
		t.Fatalf("got %#v", match)
	}
}

func TestMatchJourneyPositionFallsBackToStopPairWithoutTrack(t *testing.T) {
	firstStop := &ctdf.Stop{Location: &ctdf.Location{Type: "Point", Coordinates: []float64{0, 51}}}
	secondStop := &ctdf.Stop{Location: &ctdf.Location{Type: "Point", Coordinates: []float64{0.01, 51}}}
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{DestinationStop: firstStop},
		{DestinationStop: secondStop},
	}}

	match, ok := matchJourneyPositionWithStopFallback(journey, ctdf.Location{Type: "Point", Coordinates: []float64{0.0075, 51}})
	if !ok {
		t.Fatal("expected stop fallback match")
	}
	if match.PathIndex != 1 || !match.UsedGlobalTrack || match.LegProgress < 0.7 || match.LegProgress > 0.8 {
		t.Fatalf("got %#v", match)
	}
}

func TestServiceTimeOnDateRetainsServiceDayOffset(t *testing.T) {
	serviceDate := time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)
	encoded := time.Date(0, time.January, 2, 1, 30, 0, 0, time.UTC)
	got := serviceTimeOnDate(serviceDate, encoded, time.UTC)
	want := time.Date(2026, time.July, 11, 1, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEstimateFutureStopsAbsorbsDelayDuringScheduledDwell(t *testing.T) {
	base := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	paths := []*ctdf.JourneyPathItem{
		{DestinationStopRef: "timed-stop", DestinationArrivalTime: base.Add(5 * time.Minute)},
		{
			OriginStopRef:          "timed-stop",
			OriginDepartureTime:    base.Add(7 * time.Minute),
			DestinationStopRef:     "downstream-stop",
			DestinationArrivalTime: base.Add(12 * time.Minute),
		},
		{OriginStopRef: "downstream-stop", OriginDepartureTime: base.Add(12 * time.Minute), DestinationStopRef: "terminus", DestinationArrivalTime: base.Add(18 * time.Minute)},
	}

	tests := []struct {
		name                  string
		offset                time.Duration
		wantTimedArrival      time.Time
		wantTimedDeparture    time.Time
		wantDownstreamArrival time.Time
	}{
		{
			name:                  "delay is fully absorbed",
			offset:                time.Minute,
			wantTimedArrival:      base.Add(6 * time.Minute),
			wantTimedDeparture:    base.Add(7 * time.Minute),
			wantDownstreamArrival: base.Add(12 * time.Minute),
		},
		{
			name:                  "remaining delay is carried forward",
			offset:                3 * time.Minute,
			wantTimedArrival:      base.Add(8 * time.Minute),
			wantTimedDeparture:    base.Add(8 * time.Minute),
			wantDownstreamArrival: base.Add(13 * time.Minute),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stops := estimateFutureStops(paths, 0, test.offset)
			if !stops[0].ArrivalTime.Equal(test.wantTimedArrival) || !stops[0].DepartureTime.Equal(test.wantTimedDeparture) {
				t.Fatalf("timed stop = arrival %s, departure %s; want arrival %s, departure %s", stops[0].ArrivalTime, stops[0].DepartureTime, test.wantTimedArrival, test.wantTimedDeparture)
			}
			if !stops[1].ArrivalTime.Equal(test.wantDownstreamArrival) {
				t.Fatalf("downstream arrival = %s, want %s", stops[1].ArrivalTime, test.wantDownstreamArrival)
			}
		})
	}
}

func TestJourneyStopOccurrenceIndexHandlesRepeatedStop(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "waterloo", DestinationStopRef: "vauxhall"},
		{OriginStopRef: "vauxhall", DestinationStopRef: "waterloo"},
		{OriginStopRef: "waterloo", DestinationStopRef: "clapham"},
	}}
	if got := journeyStopOccurrenceIndex(journey, "waterloo", 0); got != 0 {
		t.Fatalf("expected first Waterloo call at 0, got %d", got)
	}
	if got := journeyStopOccurrenceIndex(journey, "waterloo", 1); got != 2 {
		t.Fatalf("expected second Waterloo call at 2, got %d", got)
	}
}

func TestSelectLocationCandidateRequiresClearWinner(t *testing.T) {
	if got := selectLocationCandidate([]locationJourneyCandidate{{journeyID: "a", distance: 10, score: 10}, {journeyID: "b", distance: 20, score: 20}}); got != "" {
		t.Fatalf("got %q, want no ambiguous match", got)
	}
	if got := selectLocationCandidate([]locationJourneyCandidate{{journeyID: "a", distance: 10, score: 10}, {journeyID: "b", distance: 50, score: 50}}); got != "a" {
		t.Fatalf("got %q, want a", got)
	}
}

func TestScoreLocationCandidatePrefersForwardProgressOnSameJourney(t *testing.T) {
	history := &vehicleJourneyHistory{JourneyID: "same", Progress: 0.5}
	if got := scoreLocationCandidate(100, 0.6, "same", history); got >= 100 {
		t.Fatalf("forward same-journey score = %f", got)
	}
	if got := scoreLocationCandidate(100, 0.2, "same", history); got <= 100 {
		t.Fatalf("backwards same-journey score = %f", got)
	}
}
