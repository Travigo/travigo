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
