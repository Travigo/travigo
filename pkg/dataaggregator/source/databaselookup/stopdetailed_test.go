package databaselookup

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestMergeStopBicycleParkingSumsCapacityForMatchingTuples(t *testing.T) {
	parking := []ctdf.StopParking{
		{
			PrimaryName:    "Cycle parking",
			Type:           "stands",
			Cost:           false,
			Covered:        true,
			Accessible:     true,
			Capacity:       8,
			DistanceMetres: 15,
		},
		{
			PrimaryName:    "Cycle parking",
			Type:           "stands",
			Cost:           false,
			Covered:        true,
			Accessible:     true,
			Capacity:       12,
			DistanceMetres: 9,
		},
		{
			PrimaryName: "Cycle parking",
			Type:        "stands",
			Cost:        false,
			Covered:     false,
			Accessible:  true,
			Capacity:    4,
		},
	}

	merged := mergeStopBicycleParking(parking)

	if len(merged) != 2 {
		t.Fatalf("expected 2 merged bicycle parking items, got %d", len(merged))
	}
	if merged[0].Capacity != 20 {
		t.Fatalf("expected matching bicycle parking capacity to be summed to 20, got %d", merged[0].Capacity)
	}
	if merged[0].DistanceMetres != 9 {
		t.Fatalf("expected merged bicycle parking distance to use nearest non-zero value, got %f", merged[0].DistanceMetres)
	}
	if merged[1].Capacity != 4 {
		t.Fatalf("expected different tuple bicycle parking capacity to remain 4, got %d", merged[1].Capacity)
	}
}
