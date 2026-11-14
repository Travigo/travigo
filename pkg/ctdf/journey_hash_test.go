package ctdf

import (
	"fmt"
	"testing"
	"time"
)

func TestFunctionalHashDeduplication(t *testing.T) {
	clock := time.Date(1, 1, 1, 23, 55, 0, 123, time.UTC)
	journey := &Journey{ServiceRef: "service", DepartureTime: clock, Availability: &Availability{Match: []AvailabilityRule{{Type: AvailabilityDate, Value: "2026-11-14"}}}, Calls: []JourneyCall{
		{StopRef: "A", Departure: clock, Start: true}, {StopRef: "B", Arrival: clock.Add(10 * time.Minute), End: true},
	}}
	clone := func() *Journey {
		copy := *journey
		copy.Calls = append([]JourneyCall(nil), journey.Calls...)
		availability := *journey.Availability
		availability.Match = append([]AvailabilityRule(nil), availability.Match...)
		copy.Availability = &availability
		return &copy
	}
	duplicate := clone()
	if len(FilterIdenticalJourneys([]*Journey{journey, duplicate}, true)) != 1 {
		t.Fatal("identical journeys were not deduplicated")
	}
	cases := []struct {
		name   string
		change func(*Journey)
	}{
		{"service", func(j *Journey) { j.ServiceRef = "other" }},
		{"stop", func(j *Journey) { j.Calls[1].StopRef = "C" }},
		{"arrival", func(j *Journey) { j.Calls[1].Arrival = j.Calls[1].Arrival.Add(time.Nanosecond) }},
		{"departure", func(j *Journey) { j.Calls[0].Departure = j.Calls[0].Departure.Add(time.Minute) }},
		{"location", func(j *Journey) {
			j.Calls[0].Departure = time.Date(1, 1, 1, 23, 55, 0, 123, time.FixedZone("other", 0))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := clone()
			tc.change(changed)
			if journey.GenerateFunctionalHash(true) == changed.GenerateFunctionalHash(true) {
				t.Fatal("distinct journeys have the same fingerprint")
			}
		})
	}
	changed := clone()
	changed.Availability.Match[0].Value = "2026-11-15"
	if journey.GenerateFunctionalHash(true) == changed.GenerateFunctionalHash(true) || journey.GenerateFunctionalHash(false) != changed.GenerateFunctionalHash(false) {
		t.Fatal("availability option was not respected")
	}
}

func BenchmarkGenerateFunctionalHash(b *testing.B) {
	for _, count := range []int{10, 50, 200} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			j := &Journey{ServiceRef: "service", Calls: make([]JourneyCall, count+1)}
			for i := range j.Calls {
				j.Calls[i].StopRef = fmt.Sprintf("stop:%d", i)
				j.Calls[i].Arrival = time.Date(1, 1, 1, 0, i*2, 0, 0, time.UTC)
				j.Calls[i].Departure = j.Calls[i].Arrival.Add(time.Minute)
			}
			b.ReportAllocs()
			for b.Loop() {
				j.GenerateFunctionalHash(true)
			}
		})
	}
}
