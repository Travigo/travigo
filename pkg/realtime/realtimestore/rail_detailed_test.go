package realtimestore

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestMergeRailDetailedOverlaysLoadingOntoAllocation(t *testing.T) {
	merged := mergeRailDetailed(
		ctdf.JourneyDetailedRail{
			TrainLength: 2,
			Carriages: []ctdf.RailCarriage{
				{ID: "front:A", VehicleID: "vehicle-a", FleetID: "700", Occupancy: -1},
				{ID: "front:B", VehicleID: "vehicle-b", FleetID: "700", Occupancy: -1},
			},
		},
		ctdf.JourneyDetailedRail{
			Carriages: []ctdf.RailCarriage{
				{ID: "front:B", Occupancy: 42},
			},
		},
	)

	if merged.TrainLength != 2 {
		t.Fatalf("expected train length to come from allocation, got %d", merged.TrainLength)
	}
	if merged.Carriages[0].VehicleID != "vehicle-a" || merged.Carriages[1].VehicleID != "vehicle-b" {
		t.Fatalf("expected allocation vehicle details to be preserved, got %+v", merged.Carriages)
	}
	if merged.Carriages[0].Occupancy != -1 {
		t.Fatalf("expected untouched carriage occupancy to remain unknown, got %d", merged.Carriages[0].Occupancy)
	}
	if merged.Carriages[1].Occupancy != 42 {
		t.Fatalf("expected loading occupancy to overlay matching carriage, got %d", merged.Carriages[1].Occupancy)
	}
}
