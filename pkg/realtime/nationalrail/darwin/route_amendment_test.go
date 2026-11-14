package darwin

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestBuildDarwinCallsPreservesRepeatedStopsAndTimingSides(t *testing.T) {
	base := time.Date(2026, time.November, 12, 0, 0, 0, 0, time.UTC)
	central := &ctdf.Stop{PrimaryIdentifier: "central", PrimaryName: "Central"}
	market := &ctdf.Stop{PrimaryIdentifier: "market", PrimaryName: "Market"}
	calls := buildDarwinCalls([]darwinCall{
		{stop: central, arrival: base.Add(23*time.Hour + 58*time.Minute), departure: base.Add(23*time.Hour + 59*time.Minute), platform: "1", activity: []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup}},
		{stop: market, arrival: base.Add(5 * time.Minute), departure: base.Add(6 * time.Minute), platform: "2", activity: []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown}},
		{stop: central, arrival: base.Add(20 * time.Minute), departure: base.Add(21 * time.Minute), platform: "3", activity: []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup}},
	})

	if len(calls) != 3 {
		t.Fatalf("got %d calls, want 3", len(calls))
	}
	if calls[0].StopRef != "central" || !calls[0].Start || calls[0].End {
		t.Fatalf("first call = %+v", calls[0])
	}
	if calls[1].StopRef != "market" || calls[1].Start || calls[1].End {
		t.Fatalf("middle call = %+v", calls[1])
	}
	if calls[2].StopRef != "central" || calls[2].Start || !calls[2].End {
		t.Fatalf("terminal call = %+v", calls[2])
	}
	if got, want := calls[1].Arrival, base.AddDate(0, 0, 1).Add(5*time.Minute); !got.Equal(want) {
		t.Fatalf("middle arrival = %s, want %s", got, want)
	}
	if got, want := calls[1].Departure, base.AddDate(0, 0, 1).Add(6*time.Minute); !got.Equal(want) {
		t.Fatalf("middle departure = %s, want %s", got, want)
	}
	if calls[1].Platform != "2" || len(calls[1].OriginActivity) != 1 || len(calls[1].DestinationActivity) != 1 {
		t.Fatalf("middle call sides were lost: %+v", calls[1])
	}
	if len(calls[0].OriginActivity) != 1 || len(calls[0].DestinationActivity) != 0 ||
		len(calls[2].OriginActivity) != 0 || len(calls[2].DestinationActivity) != 1 {
		t.Fatalf("start/end activity sides were wrong: first=%+v terminal=%+v", calls[0], calls[2])
	}
	if calls[0].Outgoing == nil || calls[0].Outgoing.DestinationDisplay != "Central" || calls[2].Outgoing != nil {
		t.Fatalf("outgoing metadata = %+v, %+v", calls[0].Outgoing, calls[2].Outgoing)
	}

	before := &ctdf.Journey{Calls: calls}
	if darwinPathChanged(before, calls) {
		t.Fatal("same repeated-stop sequence was reported as changed")
	}
	changed := append([]ctdf.JourneyCall(nil), calls...)
	changed[1].StopRef = "other"
	if !darwinPathChanged(before, changed) {
		t.Fatal("changed repeated-stop sequence was reported as unchanged")
	}
}
