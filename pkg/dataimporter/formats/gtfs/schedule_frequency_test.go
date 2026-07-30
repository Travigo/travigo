package gtfs

import (
	"fmt"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestExpandJourneyForFrequenciesShiftsEveryPathTime(t *testing.T) {
	baseDeparture, err := parseGTFSTime("06:00:00")
	if err != nil {
		t.Fatal(err)
	}
	baseArrival, err := parseGTFSTime("06:10:00")
	if err != nil {
		t.Fatal(err)
	}
	windowStart, err := parseGTFSTime("06:10:00")
	if err != nil {
		t.Fatal(err)
	}
	windowEnd, err := parseGTFSTime("07:00:00")
	if err != nil {
		t.Fatal(err)
	}

	journey := &ctdf.Journey{
		PrimaryIdentifier: "dataset-journey-trip-1",
		DepartureTime:     baseDeparture,
		Path: []*ctdf.JourneyPathItem{{
			OriginDepartureTime:    baseDeparture,
			DestinationArrivalTime: baseArrival,
		}},
	}

	expanded := expandJourneyForFrequencies(journey, []frequencyWindow{{
		StartTime:      windowStart,
		EndTime:        windowEnd,
		HeadwaySeconds: 15 * 60,
	}})
	if got, want := len(expanded), 4; got != want {
		t.Fatalf("expanded %d journeys, want %d", got, want)
	}

	for index, wantMinute := range []int{10, 25, 40, 55} {
		wantDeparture := time.Date(0, time.January, 1, 6, wantMinute, 0, 0, time.UTC)
		if !expanded[index].DepartureTime.Equal(wantDeparture) {
			t.Fatalf("journey %d departure = %v, want %v", index, expanded[index].DepartureTime, wantDeparture)
		}
		wantArrival := wantDeparture.Add(10 * time.Minute)
		if !expanded[index].Path[0].DestinationArrivalTime.Equal(wantArrival) {
			t.Fatalf("journey %d arrival = %v, want %v", index, expanded[index].Path[0].DestinationArrivalTime, wantArrival)
		}
		if expanded[index].PrimaryIdentifier == journey.PrimaryIdentifier {
			t.Fatalf("journey %d kept the template identifier", index)
		}
		wantStartTime := fmt.Sprintf("06:%02d:00", wantMinute)
		if got := expanded[index].OtherIdentifiers["GTFS-TripStartTime"]; got != wantStartTime {
			t.Fatalf("journey %d GTFS start time = %q, want %q", index, got, wantStartTime)
		}
	}

	if !journey.DepartureTime.Equal(baseDeparture) || !journey.Path[0].DestinationArrivalTime.Equal(baseArrival) {
		t.Fatal("expansion mutated the template journey")
	}
}

func TestExpandJourneyForFrequenciesSupportsServiceTimesAbove24HoursAndWindowBoundaries(t *testing.T) {
	baseDeparture, err := parseGTFSTime("23:00:00")
	if err != nil {
		t.Fatal(err)
	}
	journey := &ctdf.Journey{
		PrimaryIdentifier: "dataset-journey-trip-2",
		DepartureTime:     baseDeparture,
		Path:              []*ctdf.JourneyPathItem{{OriginDepartureTime: baseDeparture}},
	}

	parse := func(value string) time.Time {
		t.Helper()
		parsed, parseErr := parseGTFSTime(value)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return parsed
	}
	expanded := expandJourneyForFrequencies(journey, []frequencyWindow{
		{StartTime: parse("23:30:00"), EndTime: parse("25:00:00"), HeadwaySeconds: 30 * 60},
		{StartTime: parse("25:00:00"), EndTime: parse("26:00:00"), HeadwaySeconds: 30 * 60},
	})

	if got, want := len(expanded), 5; got != want {
		t.Fatalf("expanded %d journeys, want %d", got, want)
	}
	for index, want := range []string{"23:30:00", "24:00:00", "24:30:00", "25:00:00", "25:30:00"} {
		if got := expanded[index].DepartureTime.Sub(time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC)); got != parseDuration(want) {
			t.Fatalf("journey %d offset = %v, want %v", index, got, parseDuration(want))
		}
	}
}

func TestParseExactTimes(t *testing.T) {
	for _, value := range []string{"", "0"} {
		got, err := parseExactTimes(value)
		if err != nil || got != 0 {
			t.Fatalf("parseExactTimes(%q) = %d, %v", value, got, err)
		}
	}
	if got, err := parseExactTimes("1"); err != nil || got != 1 {
		t.Fatalf("parseExactTimes(1) = %d, %v", got, err)
	}
	if _, err := parseExactTimes("2"); err == nil {
		t.Fatal("expected invalid exact_times to fail")
	}
}

func parseDuration(value string) time.Duration {
	parsed, err := parseGTFSTime(value)
	if err != nil {
		panic(err)
	}
	return parsed.Sub(time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC))
}
