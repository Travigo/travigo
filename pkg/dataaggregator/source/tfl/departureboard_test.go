package tfl

import (
	"reflect"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestTfLRealtimeRespectsSelectedWindow(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	start := now.AddDate(0, 0, -3)
	end := start.Add(48 * time.Hour)
	if tflRealtimeWindowOverlaps(start, end, now) || tflRealtimeTimeInWindow(now.Add(time.Hour), start, end, now) {
		t.Fatal("current predictions must not appear on a historical board")
	}
	start, end = now.Add(-time.Hour), now.Add(time.Hour)
	if !tflRealtimeWindowOverlaps(start, end, now) || !tflRealtimeTimeInWindow(now, start, end, now) || tflRealtimeTimeInWindow(end, start, end, now) {
		t.Fatal("realtime prediction window or exclusive end is incorrect")
	}
}

func TestTfLCancellationLookupGroupsByOperatingDate(t *testing.T) {
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	journeys := []ctdf.RealtimeJourney{
		{Journey: &ctdf.Journey{PrimaryIdentifier: "first"}, JourneyRunDate: date},
		{Journey: &ctdf.Journey{PrimaryIdentifier: "second"}, JourneyRunDate: date.AddDate(0, 0, 1)},
		{Journey: &ctdf.Journey{PrimaryIdentifier: "unknown"}},
	}
	want := map[string][]string{"2026-11-03": {"first"}, "2026-11-04": {"second"}}
	if got := tflJourneyIDsByServiceDate(journeys); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestFilterScheduledBoardByDirectionUsesIndependentWatermarks(t *testing.T) {
	baseTime := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	stopIDs := []string{"station", "station-platform"}

	towardsB := boardEntry("service", "station", "stop-b", baseTime.Add(20*time.Minute), ctdf.BoardTypeDeparture)
	towardsC := boardEntry("service", "station-platform", "stop-c", baseTime.Add(5*time.Minute), ctdf.BoardTypeDeparture)
	watermarks := map[boardDirectionKey]time.Time{}
	for _, entry := range []*ctdf.DepartureBoard{towardsB, towardsC} {
		direction, ok := departureBoardDirection(entry, stopIDs, ctdf.BoardTypeDeparture)
		if !ok {
			t.Fatal("expected realtime entry to have a direction")
		}
		watermarks[direction] = entry.Time
	}

	beforeB := boardEntry("service", "station", "stop-b", baseTime.Add(15*time.Minute), ctdf.BoardTypeDeparture)
	afterB := boardEntry("service", "station", "stop-b", baseTime.Add(25*time.Minute), ctdf.BoardTypeDeparture)
	afterC := boardEntry("service", "station", "stop-c", baseTime.Add(10*time.Minute), ctdf.BoardTypeDeparture)
	noRealtimeDirection := boardEntry("service", "station", "stop-d", baseTime.Add(time.Minute), ctdf.BoardTypeDeparture)
	otherService := boardEntry("other-service", "station", "stop-b", baseTime.Add(2*time.Minute), ctdf.BoardTypeDeparture)

	filtered := filterScheduledBoardByDirection(
		[]*ctdf.DepartureBoard{beforeB, afterB, afterC, noRealtimeDirection, otherService},
		stopIDs,
		ctdf.BoardTypeDeparture,
		watermarks,
	)

	expected := []*ctdf.DepartureBoard{afterB, afterC, noRealtimeDirection, otherService}
	assertBoardEntries(t, filtered, expected)
}

func TestDepartureBoardDirectionUsesPreviousStopForArrivals(t *testing.T) {
	entry := boardEntry(
		"service",
		"previous-stop",
		"station",
		time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
		ctdf.BoardTypeArrival,
	)

	direction, ok := departureBoardDirection(entry, []string{"station"}, ctdf.BoardTypeArrival)
	if !ok {
		t.Fatal("expected arrival entry to have a direction")
	}
	if direction.adjacentStopRef != "previous-stop" {
		t.Fatalf("adjacent stop = %q, want %q", direction.adjacentStopRef, "previous-stop")
	}
}

func TestDepartureBoardDirectionUsesMatchingRepeatedStopOccurrence(t *testing.T) {
	firstTime := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(30 * time.Minute)
	entry := &ctdf.DepartureBoard{
		Time: secondTime,
		Journey: &ctdf.Journey{
			ServiceRef: "service",
			Calls: ctdf.JourneyCallsFromLegs(
				&ctdf.JourneyPathItem{
					OriginStopRef:       "station",
					DestinationStopRef:  "first-next-stop",
					OriginDepartureTime: firstTime,
				},
				&ctdf.JourneyPathItem{
					OriginStopRef:       "station",
					DestinationStopRef:  "second-next-stop",
					OriginDepartureTime: secondTime,
				}),
		},
	}

	direction, ok := departureBoardDirection(entry, []string{"station"}, ctdf.BoardTypeDeparture)
	if !ok {
		t.Fatal("expected repeated stop entry to have a direction")
	}
	if direction.adjacentStopRef != "second-next-stop" {
		t.Fatalf("adjacent stop = %q, want %q", direction.adjacentStopRef, "second-next-stop")
	}
}

func TestDepartureBoardDirectionUsesNextPathTimeForRepeatedArrival(t *testing.T) {
	firstTime := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(30 * time.Minute)
	entry := &ctdf.DepartureBoard{
		Time: secondTime,
		Journey: &ctdf.Journey{
			ServiceRef: "service",
			Calls: ctdf.JourneyCallsFromLegs(
				&ctdf.JourneyPathItem{
					OriginStopRef:      "first-previous-stop",
					DestinationStopRef: "station",
				},
				&ctdf.JourneyPathItem{
					OriginStopRef:      "station",
					OriginArrivalTime:  firstTime,
					DestinationStopRef: "middle-stop",
				},
				&ctdf.JourneyPathItem{
					OriginStopRef:      "second-previous-stop",
					DestinationStopRef: "station",
				},
				&ctdf.JourneyPathItem{
					OriginStopRef:      "station",
					OriginArrivalTime:  secondTime,
					DestinationStopRef: "final-stop",
				}),
		},
	}

	direction, ok := departureBoardDirection(entry, []string{"station"}, ctdf.BoardTypeArrival)
	if !ok {
		t.Fatal("expected repeated arrival stop entry to have a direction")
	}
	if direction.adjacentStopRef != "second-previous-stop" {
		t.Fatalf("adjacent stop = %q, want %q", direction.adjacentStopRef, "second-previous-stop")
	}
}

func boardEntry(serviceRef, originStopRef, destinationStopRef string, boardTime time.Time, boardType ctdf.BoardType) *ctdf.DepartureBoard {
	pathItem := &ctdf.JourneyPathItem{
		OriginStopRef:          originStopRef,
		DestinationStopRef:     destinationStopRef,
		OriginArrivalTime:      boardTime,
		OriginDepartureTime:    boardTime,
		DestinationArrivalTime: boardTime,
	}
	return &ctdf.DepartureBoard{
		Time: boardTime,
		Journey: &ctdf.Journey{
			PrimaryIdentifier: serviceRef + originStopRef + destinationStopRef + boardTime.String(),
			ServiceRef:        serviceRef,
			Calls:             ctdf.JourneyCallsFromLegs(pathItem),
		},
	}
}

func assertBoardEntries(t *testing.T, got []*ctdf.DepartureBoard, want []*ctdf.DepartureBoard) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("entry %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}
