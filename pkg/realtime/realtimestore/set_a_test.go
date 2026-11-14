package realtimestore

import (
	"fmt"
	"github.com/travigo/travigo/pkg/ctdf"
	"testing"
	"time"
)

func TestDelayPartialLegacyLoopStops(t *testing.T) {
	scheduled := time.Date(0, 1, 1, 10, 0, 0, 0, time.UTC)
	run := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	j := &ctdf.RealtimeJourney{JourneyRunDate: run, Journey: &ctdf.Journey{Calls: ctdf.JourneyCallsFromLegs([]*ctdf.JourneyPathItem{
		{OriginStopRef: "a", DestinationStopRef: "b", OriginDepartureTime: scheduled, DestinationArrivalTime: scheduled.Add(time.Minute)},
		{OriginStopRef: "b", DestinationStopRef: "a", OriginDepartureTime: scheduled.Add(time.Minute), DestinationArrivalTime: scheduled.Add(2 * time.Minute)},
	}...)}, Stops: map[string]*ctdf.RealtimeJourneyStops{"a": {DepartureTime: run.Add(10*time.Hour + 3*time.Minute)}}}
	delay, ref := realtimeJourneyDelay(j)
	if delay != 3*time.Minute || ref != "a" {
		t.Fatalf("legacy delay=%v stop=%s", delay, ref)
	}
	// A legacy first call must never be used for the later occurrence.
	j.Stops["a"].DepartureTime = time.Time{}
	j.Stops["a"].ArrivalTime = run.Add(10*time.Hour + 30*time.Minute)
	delay, _ = realtimeJourneyDelay(j)
	if delay != 0 {
		t.Fatalf("legacy call reused at terminal: %v", delay)
	}
	j.Stops["noncanonical"] = &ctdf.RealtimeJourneyStops{StopRef: "a", JourneyStopIndex: 2, ArrivalTime: run.Add(10*time.Hour + 7*time.Minute)}
	delay, _ = realtimeJourneyDelay(j)
	if delay != 5*time.Minute {
		t.Fatalf("indexed occurrence delay=%v", delay)
	}
}

func BenchmarkDelaySummaries(b *testing.B) {
	for _, n := range []int{100, 1000} {
		for _, sparse := range []bool{false, true} {
			scheduled := time.Date(0, 1, 1, 10, 0, 0, 0, time.UTC)
			run := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
			j := &ctdf.RealtimeJourney{JourneyRunDate: run, Journey: &ctdf.Journey{}, Stops: map[string]*ctdf.RealtimeJourneyStops{}}
			for i := 0; i <= n; i++ {
				clock := scheduled.Add(time.Duration(i) * time.Minute)
				j.Journey.Calls = append(j.Journey.Calls, ctdf.JourneyCall{StopRef: fmt.Sprint(i), Arrival: clock, Departure: clock, Start: i == 0, End: i == n})
			}
			for i := 0; i <= n; i++ {
				if sparse && i%10 != 0 {
					continue
				}
				j.Stops[ctdf.RealtimeJourneyStopKey(fmt.Sprint(i), i)] = &ctdf.RealtimeJourneyStops{StopRef: fmt.Sprint(i), JourneyStopIndex: i, ArrivalTime: run.Add(10*time.Hour + time.Duration(i+5)*time.Minute), DepartureTime: run.Add(10*time.Hour + time.Duration(i+5)*time.Minute)}
			}
			for _, entry := range []struct {
				name string
				fn   func(*ctdf.RealtimeJourney) (time.Duration, string)
			}{{"before", baselineRealtimeJourneyDelay}, {"after", realtimeJourneyDelay}} {
				b.Run(fmt.Sprintf("%d/sparse=%t/%s", n, sparse, entry.name), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						entry.fn(j)
					}
				})
			}
		}
	}
}

// Baseline retained for complete and sparse map performance comparisons.
func baselineRealtimeJourneyDelay(realtimeJourney *ctdf.RealtimeJourney) (time.Duration, string) {
	if realtimeJourney == nil || realtimeJourney.Journey == nil {
		return 0, ""
	}
	maximumDelay := time.Duration(0)
	delayedStop := ""

	for pathIndex, path := range realtimeJourney.Journey.Legs() {
		if path == nil {
			continue
		}
		for _, candidate := range []struct {
			stopRef   string
			stopIndex int
			scheduled time.Time
			actual    time.Time
		}{
			{stopRef: path.OriginStopRef, stopIndex: pathIndex, scheduled: path.OriginDepartureTime, actual: realtimeStopDepartureTime(realtimeJourney, path.OriginStopRef, pathIndex)},
			{stopRef: path.DestinationStopRef, stopIndex: pathIndex + 1, scheduled: path.DestinationArrivalTime, actual: realtimeStopArrivalTime(realtimeJourney, path.DestinationStopRef, pathIndex+1)},
		} {
			if candidate.actual.IsZero() || candidate.scheduled.IsZero() {
				continue
			}
			scheduled := scheduledJourneyTime(realtimeJourney.JourneyRunDate, candidate.scheduled, candidate.actual.Location())
			delay := candidate.actual.Sub(scheduled)
			if delay > maximumDelay {
				maximumDelay, delayedStop = delay, candidate.stopRef
			}
		}
	}
	return maximumDelay, delayedStop
}
