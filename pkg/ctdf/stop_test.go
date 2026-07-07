package ctdf

import "testing"

func TestStopGetAllStopIDsIncludesPlatforms(t *testing.T) {
	stop := &Stop{
		PrimaryIdentifier: "station",
		OtherIdentifiers:  []string{"crs", "station"},
		Platforms: []*StopPlatform{
			{PrimaryIdentifier: "platform-1"},
			nil,
			{PrimaryIdentifier: "platform-2"},
			{PrimaryIdentifier: "platform-1"},
		},
	}

	stopIDs := stop.GetAllStopIDs()
	expected := []string{"station", "crs", "platform-1", "platform-2"}

	if len(stopIDs) != len(expected) {
		t.Fatalf("expected %d stop IDs, got %d: %#v", len(expected), len(stopIDs), stopIDs)
	}
	for index, expectedStopID := range expected {
		if stopIDs[index] != expectedStopID {
			t.Fatalf("expected stop ID at index %d to be %s, got %s", index, expectedStopID, stopIDs[index])
		}
	}
}
