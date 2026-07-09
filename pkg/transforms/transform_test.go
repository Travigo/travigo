package transforms

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestTransformAppliesNestedObjectsInSingleWalk(t *testing.T) {
	originalTransforms := transforms
	t.Cleanup(func() {
		transforms = originalTransforms
	})

	transforms = []TransformDefinition{
		{
			Type:  "ctdf.Stop",
			Match: map[string]string{"PrimaryIdentifier": "stop-1"},
			Data:  map[string]interface{}{"PrimaryName": "Renamed Stop"},
		},
		{
			Type:  "ctdf.Service",
			Match: map[string]string{"PrimaryIdentifier": "service-1"},
			Data:  map[string]interface{}{"ServiceName": "Renamed Service"},
		},
	}

	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-1",
		PrimaryName:       "Original Stop",
		Services: []*ctdf.Service{
			{
				PrimaryIdentifier: "service-1",
				ServiceName:       "Original Service",
			},
		},
	}

	Transform(stop, 3)

	if stop.PrimaryName != "Renamed Stop" {
		t.Fatalf("expected stop transform to apply, got %q", stop.PrimaryName)
	}
	if stop.Services[0].ServiceName != "Renamed Service" {
		t.Fatalf("expected nested service transform to apply, got %q", stop.Services[0].ServiceName)
	}
}

func TestTransformFiltersByGroup(t *testing.T) {
	originalTransforms := transforms
	t.Cleanup(func() {
		transforms = originalTransforms
	})

	transforms = []TransformDefinition{
		{
			Type:  "ctdf.Stop",
			Group: "custom",
			Match: map[string]string{"PrimaryIdentifier": "stop-1"},
			Data:  map[string]interface{}{"PrimaryName": "Renamed Stop"},
		},
	}

	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-1",
		PrimaryName:       "Original Stop",
	}

	Transform(stop, 3)
	if stop.PrimaryName != "Original Stop" {
		t.Fatalf("expected default transform group to be ignored, got %q", stop.PrimaryName)
	}

	Transform(stop, 3, "custom")
	if stop.PrimaryName != "Renamed Stop" {
		t.Fatalf("expected custom transform group to apply, got %q", stop.PrimaryName)
	}
}

func TestTransformMatchesEachRailTrainLengthIndependently(t *testing.T) {
	originalTransforms := transforms
	t.Cleanup(func() {
		transforms = originalTransforms
	})

	transforms = []TransformDefinition{
		{
			Type:  "ctdf.RailTrain",
			Match: map[string]string{"VehicleType": "class-a", "TrainLength": "2"},
			Data:  map[string]interface{}{"VehicleTypeName": "Two car"},
		},
		{
			Type:  "ctdf.RailTrain",
			Match: map[string]string{"VehicleType": "class-b", "TrainLength": "5"},
			Data:  map[string]interface{}{"VehicleTypeName": "Five car"},
		},
	}

	detailed := &ctdf.JourneyDetailedRail{
		Trains: []ctdf.RailTrain{
			{VehicleType: "class-a", TrainLength: 2},
			{VehicleType: "class-b", TrainLength: 5},
		},
	}

	Transform(detailed, 1)

	if detailed.Trains[0].VehicleTypeName != "Two car" {
		t.Fatalf("expected two-car transform on first train, got %q", detailed.Trains[0].VehicleTypeName)
	}
	if detailed.Trains[1].VehicleTypeName != "Five car" {
		t.Fatalf("expected five-car transform on second train, got %q", detailed.Trains[1].VehicleTypeName)
	}
}
