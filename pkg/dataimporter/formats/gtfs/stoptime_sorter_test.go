package gtfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSortedStopTimeGroupsSortsAndGroupsUnorderedFile(t *testing.T) {
	stopTimesPath := filepath.Join(t.TempDir(), "stop_times.txt")
	err := os.WriteFile(stopTimesPath, []byte(`trip_id,arrival_time,departure_time,stop_id,stop_headsign,stop_sequence,pickup_type,drop_off_type
trip-b,09:05:00,09:05:30,stop-4,,2,0,0
trip-a,08:05:00,08:05:30,stop-2,,2,0,0
trip-b,09:00:00,09:00:30,stop-3,,1,0,0
trip-a,08:00:00,08:00:30,stop-1,,1,0,0
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	groups, err := newSortedStopTimeGroups(stopTimesPath)
	if err != nil {
		t.Fatal(err)
	}
	defer groups.Close()

	var gotTripIDs []string
	gotSequences := map[string][]int{}
	err = groups.Process(func(tripID string, stopTimes []StopTime) error {
		gotTripIDs = append(gotTripIDs, tripID)
		for _, stopTime := range stopTimes {
			gotSequences[tripID] = append(gotSequences[tripID], stopTime.StopSequence)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assertStringsEqual(t, gotTripIDs, []string{"trip-a", "trip-b"})
	assertIntsEqual(t, gotSequences["trip-a"], []int{1, 2})
	assertIntsEqual(t, gotSequences["trip-b"], []int{1, 2})
}

func TestSortedStopTimeGroupsMergesTripsAcrossChunks(t *testing.T) {
	chunkA, err := writeSortedStopTimeChunk([]StopTime{
		{TripID: "trip-a", StopSequence: 2},
		{TripID: "trip-b", StopSequence: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	chunkB, err := writeSortedStopTimeChunk([]StopTime{
		{TripID: "trip-a", StopSequence: 1},
		{TripID: "trip-b", StopSequence: 2},
	})
	if err != nil {
		os.Remove(chunkA)
		t.Fatal(err)
	}

	groups := sortedStopTimeGroups{chunkFiles: []string{chunkA, chunkB}}
	defer groups.Close()

	gotSequences := map[string][]int{}
	err = groups.Process(func(tripID string, stopTimes []StopTime) error {
		for _, stopTime := range stopTimes {
			gotSequences[tripID] = append(gotSequences[tripID], stopTime.StopSequence)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assertIntsEqual(t, gotSequences["trip-a"], []int{1, 2})
	assertIntsEqual(t, gotSequences["trip-b"], []int{1, 2})
}

func assertStringsEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func assertIntsEqual(t *testing.T, got []int, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
