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

func TestMergeRailDetailedOverlaysLoadingOntoAllocation(t *testing.T) {
	merged := mergeRailDetailed(
		ctdf.JourneyDetailedRail{
			TrainLength: 2,
			Carriages: []ctdf.RailCarriage{
				{ID: "front:A", CarriageID: "vehicle-a", FleetID: "700", Occupancy: -1},
				{ID: "front:B", CarriageID: "vehicle-b", FleetID: "700", Occupancy: -1},
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
	if merged.Carriages[0].CarriageID != "vehicle-a" || merged.Carriages[1].CarriageID != "vehicle-b" {
		t.Fatalf("expected allocation vehicle details to be preserved, got %+v", merged.Carriages)
	}
	if merged.Carriages[0].Occupancy != -1 {
		t.Fatalf("expected untouched carriage occupancy to remain unknown, got %d", merged.Carriages[0].Occupancy)
	}
	if merged.Carriages[1].Occupancy != 42 {
		t.Fatalf("expected loading occupancy to overlay matching carriage, got %d", merged.Carriages[1].Occupancy)
	}
}

func TestEnrichRailDetailedAllocationAppliesRailClassTransformsWithoutReplacingLiveCarriages(t *testing.T) {
	setupRailDetailedTransforms(t)

	enriched := enrichRailDetailedAllocation(ctdf.JourneyDetailedRail{
		Carriages: []ctdf.RailCarriage{
			{ID: "700001:vehicle-1", CarriageID: "vehicle-1", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-2", CarriageID: "vehicle-2", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-3", CarriageID: "vehicle-3", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-4", CarriageID: "vehicle-4", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-5", CarriageID: "vehicle-5", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-6", CarriageID: "vehicle-6", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-7", CarriageID: "vehicle-7", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-8", CarriageID: "vehicle-8", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-9", CarriageID: "vehicle-9", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-10", CarriageID: "vehicle-10", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-11", CarriageID: "vehicle-11", FleetID: "700", Occupancy: -1},
			{ID: "700001:vehicle-12", CarriageID: "vehicle-12", FleetID: "700", Occupancy: -1},
		},
	})

	if enriched.VehicleType != "gb-railclass-700" {
		t.Fatalf("expected vehicle type to be inferred from allocation, got %s", enriched.VehicleType)
	}
	if enriched.VehicleTypeName != "Class 700" {
		t.Fatalf("expected class 700 transform to apply, got %s", enriched.VehicleTypeName)
	}
	if enriched.PowerType != "Electric" {
		t.Fatalf("expected class 700 power type to apply, got %s", enriched.PowerType)
	}
	if enriched.SpeedKMH != 160 {
		t.Fatalf("expected class 700 speed to apply, got %d", enriched.SpeedKMH)
	}
	if len(enriched.Carriages) != 12 {
		t.Fatalf("expected live allocation carriage list to be preserved, got %d", len(enriched.Carriages))
	}
	if enriched.Carriages[1].ID != "700001:vehicle-2" || enriched.Carriages[1].CarriageID != "vehicle-2" {
		t.Fatalf("expected live carriage identity to be preserved, got %+v", enriched.Carriages[1])
	}
	if len(enriched.Carriages[1].Toilets) != 1 || enriched.Carriages[1].Toilets[0].Type != "Standard" {
		t.Fatalf("expected standard toilet on carriage 2, got %+v", enriched.Carriages[1].Toilets)
	}
	if len(enriched.Carriages[6].Toilets) != 1 || enriched.Carriages[6].Toilets[0].Type != "Accessible" {
		t.Fatalf("expected accessible toilet on carriage 7, got %+v", enriched.Carriages[6].Toilets)
	}
	if len(enriched.Carriages[10].Toilets) != 1 || enriched.Carriages[10].Toilets[0].Type != "Standard" {
		t.Fatalf("expected standard toilet on carriage 11, got %+v", enriched.Carriages[10].Toilets)
	}
}

func TestEnrichRailDetailedAllocationAppliesLengthSpecificCarriageAmenities(t *testing.T) {
	setupRailDetailedTransforms(t)

	enriched := enrichRailDetailedAllocation(ctdf.JourneyDetailedRail{
		Carriages: []ctdf.RailCarriage{
			{ID: "720001:vehicle-1", CarriageID: "vehicle-1", FleetID: "720", Occupancy: -1},
			{ID: "720001:vehicle-2", CarriageID: "vehicle-2", FleetID: "720", Occupancy: -1},
			{ID: "720001:vehicle-3", CarriageID: "vehicle-3", FleetID: "720", Occupancy: -1},
			{ID: "720001:vehicle-4", CarriageID: "vehicle-4", FleetID: "720", Occupancy: -1},
			{ID: "720001:vehicle-5", CarriageID: "vehicle-5", FleetID: "720", Occupancy: -1},
		},
	})

	if enriched.VehicleTypeName != "Class 720" {
		t.Fatalf("expected class 720 transform to apply, got %s", enriched.VehicleTypeName)
	}
	if !enriched.AirConditioning || !enriched.WiFi || !enriched.Toilets || !enriched.PowerPlugs {
		t.Fatalf("expected class 720 amenities to apply, got %+v", enriched)
	}
	if enriched.Carriages[1].ID != "720001:vehicle-2" || enriched.Carriages[1].Class != "Standard" {
		t.Fatalf("expected live carriage identity and transform class on carriage 2, got %+v", enriched.Carriages[1])
	}
	if len(enriched.Carriages[1].Toilets) != 1 || enriched.Carriages[1].Toilets[0].Type != "Standard" {
		t.Fatalf("expected standard toilet on carriage 2, got %+v", enriched.Carriages[1].Toilets)
	}
	if len(enriched.Carriages[4].Toilets) != 1 || enriched.Carriages[4].Toilets[0].Type != "Accessible" {
		t.Fatalf("expected accessible toilet on carriage 5, got %+v", enriched.Carriages[4].Toilets)
	}
}
