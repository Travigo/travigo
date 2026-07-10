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

func TestServiceTimeOnDateRetainsServiceDayOffset(t *testing.T) {
	serviceDate := time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)
	encoded := time.Date(0, time.January, 2, 1, 30, 0, 0, time.UTC)
	got := serviceTimeOnDate(serviceDate, encoded, time.UTC)
	want := time.Date(2026, time.July, 11, 1, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}
