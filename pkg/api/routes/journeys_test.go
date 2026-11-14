package routes

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestApplyJourneyServiceStopNameOverrides(t *testing.T) {
	journey := &ctdf.Journey{
		Service: &ctdf.Service{StopNameOverrides: map[string]string{
			"origin-alias":     "Service Origin",
			"destination-stop": "Service Destination",
		}},
		Calls: ctdf.JourneyCallsFromLegs(&ctdf.JourneyPathItem{
			OriginStopRef: "origin-stop", DestinationStopRef: "destination-stop",
			OriginStop: &ctdf.Stop{
				PrimaryIdentifier: "origin-stop", OtherIdentifiers: []string{"origin-alias"}, PrimaryName: "Base Origin",
			},
			DestinationStop: &ctdf.Stop{PrimaryIdentifier: "destination-stop", PrimaryName: "Base Destination"},
		}),
	}

	applyJourneyServiceStopNameOverrides(journey)

	if got := journey.Calls[0].Stop.PrimaryName; got != "Service Origin" {
		t.Fatalf("origin stop name = %q, want %q", got, "Service Origin")
	}
	if got := journey.Calls[1].Stop.PrimaryName; got != "Service Destination" {
		t.Fatalf("destination stop name = %q, want %q", got, "Service Destination")
	}
}

func TestApplyJourneyServiceStopNameOverridesSkipsMissingCallStops(t *testing.T) {
	journey := &ctdf.Journey{
		Service: &ctdf.Service{StopNameOverrides: map[string]string{"stop": "Service Stop"}},
		Calls: []ctdf.JourneyCall{
			{StopRef: "origin", Start: true},
			{StopRef: "stop", Stop: &ctdf.Stop{PrimaryIdentifier: "stop", PrimaryName: "Base Stop"}},
			{StopRef: "stop", Stop: &ctdf.Stop{PrimaryIdentifier: "stop", PrimaryName: "Base Stop"}, End: true},
		},
	}

	applyJourneyServiceStopNameOverrides(journey)

	if got := journey.Calls[1].Stop.PrimaryName; got != "Service Stop" {
		t.Fatalf("middle stop name = %q, want %q", got, "Service Stop")
	}
	if got := journey.Calls[2].Stop.PrimaryName; got != "Service Stop" {
		t.Fatalf("terminal stop name = %q, want %q", got, "Service Stop")
	}
}

func TestApplyJourneyServiceStopNameOverridesDoesNotMutateSharedStop(t *testing.T) {
	sharedStop := &ctdf.Stop{PrimaryIdentifier: "stop", PrimaryName: "Base Stop"}
	journey := &ctdf.Journey{
		Service: &ctdf.Service{StopNameOverrides: map[string]string{"stop": "Service Stop"}},
		Calls: []ctdf.JourneyCall{
			{StopRef: "stop", Stop: sharedStop, Start: true},
			{StopRef: "stop", Stop: sharedStop},
			{StopRef: "stop", Stop: sharedStop, End: true},
		},
	}

	applyJourneyServiceStopNameOverrides(journey)

	if sharedStop.PrimaryName != "Base Stop" {
		t.Fatalf("shared stop name mutated to %q", sharedStop.PrimaryName)
	}
	for index, call := range journey.Calls {
		if call.Stop == sharedStop || call.Stop.PrimaryName != "Service Stop" {
			t.Fatalf("call %d override = %#v", index, call.Stop)
		}
	}
}

func TestStopUpdateNameFromServiceOverridesAcceptsNilStop(t *testing.T) {
	var stop *ctdf.Stop
	stop.UpdateNameFromServiceOverrides(&ctdf.Service{})
}
