package routes

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestApplyJourneyServiceStopNameOverrides(t *testing.T) {
	journey := &ctdf.Journey{
		Service: &ctdf.Service{
			StopNameOverrides: map[string]string{
				"origin-alias":     "Service Origin",
				"destination-stop": "Service Destination",
			},
		},
		Path: []*ctdf.JourneyPathItem{
			{
				OriginStop: &ctdf.Stop{
					PrimaryIdentifier: "origin-stop",
					OtherIdentifiers:  []string{"origin-alias"},
					PrimaryName:       "Base Origin",
				},
				DestinationStop: &ctdf.Stop{
					PrimaryIdentifier: "destination-stop",
					PrimaryName:       "Base Destination",
				},
			},
		},
	}

	applyJourneyServiceStopNameOverrides(journey)

	if got := journey.Path[0].OriginStop.PrimaryName; got != "Service Origin" {
		t.Fatalf("origin stop name = %q, want %q", got, "Service Origin")
	}
	if got := journey.Path[0].DestinationStop.PrimaryName; got != "Service Destination" {
		t.Fatalf("destination stop name = %q, want %q", got, "Service Destination")
	}
}

func TestApplyJourneyServiceStopNameOverridesSkipsMissingPathStops(t *testing.T) {
	journey := &ctdf.Journey{
		Service: &ctdf.Service{
			StopNameOverrides: map[string]string{"stop": "Service Stop"},
		},
		Path: []*ctdf.JourneyPathItem{
			nil,
			{DestinationStop: &ctdf.Stop{PrimaryIdentifier: "stop", PrimaryName: "Base Stop"}},
			{OriginStop: &ctdf.Stop{PrimaryIdentifier: "stop", PrimaryName: "Base Stop"}},
		},
	}

	applyJourneyServiceStopNameOverrides(journey)

	if got := journey.Path[1].DestinationStop.PrimaryName; got != "Service Stop" {
		t.Fatalf("destination stop name = %q, want %q", got, "Service Stop")
	}
	if got := journey.Path[2].OriginStop.PrimaryName; got != "Service Stop" {
		t.Fatalf("origin stop name = %q, want %q", got, "Service Stop")
	}
}

func TestStopUpdateNameFromServiceOverridesAcceptsNilStop(t *testing.T) {
	var stop *ctdf.Stop
	stop.UpdateNameFromServiceOverrides(&ctdf.Service{})
}
