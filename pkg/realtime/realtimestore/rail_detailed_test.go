package realtimestore

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/transforms"
)

var setupRailDetailedTransformsOnce sync.Once

func setupRailDetailedTransforms(t *testing.T) {
	t.Helper()

	setupRailDetailedTransformsOnce.Do(func() {
		_, filename, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("failed to resolve test path")
		}

		originalWorkingDirectory, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}

		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../.."))
		if err := os.Chdir(repoRoot); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(originalWorkingDirectory)

		transforms.SetupClient()
	})
}

func TestMergeRailDetailedScopesLoadingToTrain(t *testing.T) {
	merged := mergeRailDetailed(
		ctdf.JourneyDetailedRail{
			Trains: []ctdf.RailTrain{
				{
					ID:          "front",
					TrainLength: 1,
					Carriages:   []ctdf.RailCarriage{{ID: "A", VehicleID: "vehicle-a", Occupancy: -1}},
				},
				{
					ID:          "rear",
					TrainLength: 1,
					Carriages:   []ctdf.RailCarriage{{ID: "A", VehicleID: "vehicle-b", Occupancy: -1}},
				},
			},
		},
		ctdf.JourneyDetailedRail{
			Trains: []ctdf.RailTrain{
				{ID: "rear", Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: 42}}},
			},
		},
	)

	if merged.Trains[0].Carriages[0].Occupancy != -1 {
		t.Fatalf("expected front train occupancy to remain unknown")
	}
	if merged.Trains[1].Carriages[0].Occupancy != 42 {
		t.Fatalf("expected loading to overlay rear train, got %+v", merged.Trains[1])
	}
	if merged.Trains[1].Carriages[0].VehicleID != "vehicle-b" {
		t.Fatalf("expected allocation identity to be preserved, got %+v", merged.Trains[1].Carriages[0])
	}
}

func TestEnrichRailDetailedAllocationTransformsMixedUnitsByOwnLength(t *testing.T) {
	setupRailDetailedTransforms(t)

	enriched := enrichRailDetailedAllocation(ctdf.JourneyDetailedRail{
		Trains: []ctdf.RailTrain{
			{
				ID:      "156406",
				FleetID: "156",
				Carriages: []ctdf.RailCarriage{
					{ID: "156406:1", VehicleID: "1", Occupancy: -1},
					{ID: "156406:2", VehicleID: "2", Occupancy: -1},
				},
			},
			{
				ID:      "720001",
				FleetID: "720",
				Carriages: []ctdf.RailCarriage{
					{ID: "720001:1", VehicleID: "1", Occupancy: -1},
					{ID: "720001:2", VehicleID: "2", Occupancy: -1},
					{ID: "720001:3", VehicleID: "3", Occupancy: -1},
					{ID: "720001:4", VehicleID: "4", Occupancy: -1},
					{ID: "720001:5", VehicleID: "5", Occupancy: -1},
				},
			},
		},
	})

	if enriched.Trains[0].VehicleType != "gb-railclass-156" || enriched.Trains[0].VehicleTypeName != "Class 156" {
		t.Fatalf("expected class 156 transform on first train, got %+v", enriched.Trains[0])
	}
	if enriched.Trains[0].TrainLength != 2 || len(enriched.Trains[0].Carriages[0].Toilets) != 1 {
		t.Fatalf("expected two-car class 156 layout, got %+v", enriched.Trains[0])
	}
	if len(enriched.Trains[0].Carriages[0].SeatingClasses) != 1 ||
		enriched.Trains[0].Carriages[0].SeatingClasses[0] != ctdf.JourneyDetailedRailSeatingStandard {
		t.Fatalf("expected transformed carriage seating class to be standard, got %+v", enriched.Trains[0].Carriages[0])
	}

	if enriched.Trains[1].VehicleType != "gb-railclass-720" || enriched.Trains[1].VehicleTypeName != "Class 720" {
		t.Fatalf("expected class 720 transform on second train, got %+v", enriched.Trains[1])
	}
	if enriched.Trains[1].TrainLength != 5 || len(enriched.Trains[1].Carriages[4].Toilets) != 1 {
		t.Fatalf("expected five-car class 720 layout, got %+v", enriched.Trains[1])
	}
	if len(enriched.Trains[1].Carriages[4].SeatingClasses) != 1 ||
		enriched.Trains[1].Carriages[4].SeatingClasses[0] != ctdf.JourneyDetailedRailSeatingStandard {
		t.Fatalf("expected transformed carriage seating class to be standard, got %+v", enriched.Trains[1].Carriages[4])
	}
	if enriched.Trains[1].Carriages[4].ID != "720001:5" || enriched.Trains[1].Carriages[4].VehicleID != "5" {
		t.Fatalf("expected live carriage identity to be preserved, got %+v", enriched.Trains[1].Carriages[4])
	}
}

func TestEnrichRailTrainAllocationPreservesMixedSeatingClasses(t *testing.T) {
	setupRailDetailedTransforms(t)

	carriages := make([]ctdf.RailCarriage, 8)
	for index := range carriages {
		carriages[index] = ctdf.RailCarriage{ID: string(rune('1' + index)), Occupancy: -1}
	}

	enriched := enrichRailTrainAllocation(ctdf.RailTrain{
		FleetID:   "700",
		Carriages: carriages,
	})

	if len(enriched.Carriages[0].SeatingClasses) != 2 ||
		enriched.Carriages[0].SeatingClasses[0] != ctdf.JourneyDetailedRailSeatingFirst ||
		enriched.Carriages[0].SeatingClasses[1] != ctdf.JourneyDetailedRailSeatingStandard {
		t.Fatalf("expected leading Class 700 carriage to contain first and standard seating, got %+v", enriched.Carriages[0])
	}
	if len(enriched.Carriages[1].SeatingClasses) != 1 ||
		enriched.Carriages[1].SeatingClasses[0] != ctdf.JourneyDetailedRailSeatingStandard {
		t.Fatalf("expected second Class 700 carriage to remain standard only, got %+v", enriched.Carriages[1])
	}
}

func TestEnrichRailTrainAllocationAppliesCarriageTransformsAcrossPassengerCarriages(t *testing.T) {
	setupRailDetailedTransforms(t)

	enriched := enrichRailTrainAllocation(ctdf.RailTrain{
		FleetID: "755/4",
		Carriages: []ctdf.RailCarriage{
			{ID: "755401:1", VehicleID: "1", VehicleRole: ctdf.RailCarriageVehicleRolePassenger, Occupancy: -1},
			{ID: "755401:2", VehicleID: "2", VehicleRole: ctdf.RailCarriageVehicleRolePassenger, Occupancy: -1},
			{ID: "755401:P", VehicleID: "P", VehicleRole: ctdf.RailCarriageVehicleRolePowerCar, Occupancy: -1},
			{ID: "755401:3", VehicleID: "3", VehicleRole: ctdf.RailCarriageVehicleRolePassenger, Occupancy: -1},
			{ID: "755401:4", VehicleID: "4", VehicleRole: ctdf.RailCarriageVehicleRolePassenger, Occupancy: -1},
		},
	})

	if enriched.TrainLength != 4 {
		t.Fatalf("expected passenger train length 4, got %+v", enriched)
	}
	if len(enriched.Carriages) != 5 {
		t.Fatalf("expected power car to remain in physical carriage list, got %+v", enriched.Carriages)
	}
	if len(enriched.Carriages[0].Toilets) != 1 {
		t.Fatalf("expected first passenger carriage to receive 755/4 layout, got %+v", enriched.Carriages[0])
	}
	if len(enriched.Carriages[2].SeatingClasses) != 0 || len(enriched.Carriages[2].Toilets) != 0 {
		t.Fatalf("expected power car to be skipped by passenger layout transform, got %+v", enriched.Carriages[2])
	}
	if len(enriched.Carriages[3].SeatingClasses) != 1 ||
		enriched.Carriages[3].SeatingClasses[0] != ctdf.JourneyDetailedRailSeatingStandard {
		t.Fatalf("expected layout to continue after power car, got %+v", enriched.Carriages[3])
	}
	if enriched.Carriages[2].VehicleRole != ctdf.RailCarriageVehicleRolePowerCar {
		t.Fatalf("expected power car role to be preserved, got %+v", enriched.Carriages[2])
	}
}
