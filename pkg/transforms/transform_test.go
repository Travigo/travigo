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
