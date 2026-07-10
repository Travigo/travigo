package nrod

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestJourneyRunsDuringVSTPCancellationHonoursDaysRun(t *testing.T) {
	monday := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	journey := &ctdf.Journey{Availability: &ctdf.Availability{
		Match: []ctdf.AvailabilityRule{{Type: ctdf.AvailabilityMatchAll}},
	}}

	if !journeyRunsDuringVSTPCancellation(journey, monday, monday.AddDate(0, 0, 1), "1000000") {
		t.Fatal("Monday-running VSTP cancellation should match the permanent journey")
	}
	if journeyRunsDuringVSTPCancellation(journey, monday, monday, "0000000") {
		t.Fatal("VSTP cancellation with no operating days should not match")
	}
	if got := vstpMatchingServiceDates(journey, monday, monday.AddDate(0, 0, 1), "1000000"); len(got) != 1 || got[0] != "2026-07-06" {
		t.Fatalf("matching VSTP dates = %#v, want only 2026-07-06", got)
	}
}

func TestVSTPRealtimeTimeoutMinutesUsesVSTPExpiryWindow(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)

	if got, want := vstpRealtimeTimeoutMinutes(endDate, now), 3601; got != want {
		t.Fatalf("timeout = %d minutes, want %d", got, want)
	}
	if got := vstpRealtimeTimeoutMinutes(now.AddDate(0, 0, -3), now); got != 0 {
		t.Fatalf("expired VSTP cancellation timeout = %d, want 0", got)
	}
}
