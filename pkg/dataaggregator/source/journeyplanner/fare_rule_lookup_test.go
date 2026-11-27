package journeyplanner

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestFareRuleEndpointRefsPreservesTransfersAndGroups(t *testing.T) {
	first := &ctdf.Journey{Calls: []ctdf.JourneyCall{
		{StopRef: "start", Outgoing: &ctdf.JourneyLeg{OriginFareLocationRef: "stage-start"}},
		{StopRef: "intermediate", Outgoing: &ctdf.JourneyLeg{DestinationFareLocationRef: "stage-transfer"}},
		{StopRef: "transfer"},
	}}
	second := &ctdf.Journey{Calls: []ctdf.JourneyCall{
		{StopRef: "transfer", Outgoing: &ctdf.JourneyLeg{OriginFareLocationRef: "stage-transfer", DestinationFareLocationRef: "stage-end"}},
		{StopRef: "end"},
	}}
	items := []ctdf.JourneyPlanRouteItem{
		{Journey: first, JourneyOriginStopIndex: 0, JourneyDestinationStopIndex: 2, OriginStopRef: "start", DestinationStopRef: "transfer"},
		{Journey: second, JourneyOriginStopIndex: 0, JourneyDestinationStopIndex: 1, OriginStopRef: "transfer", DestinationStopRef: "end"},
	}
	locations := map[string]ctdf.FareLocation{
		"stage-start": {ParentRef: "start-group"}, "start-group": {},
		"stage-transfer": {ParentRef: "transfer-group"}, "transfer-group": {},
		"stage-end": {ParentRef: "end-group"}, "end-group": {},
		"intermediate-group": {StopRefs: []string{"intermediate"}},
		"start-stop-group":   {StopRefs: []string{"start"}},
	}
	origins, destinations := fareRuleEndpointRefs(items, locations)
	for _, ref := range []string{"start", "stage-start", "start-group", "start-stop-group", "transfer", "stage-transfer", "transfer-group"} {
		if !origins[ref] {
			t.Errorf("missing origin %q: %v", ref, origins)
		}
	}
	for _, ref := range []string{"transfer", "stage-transfer", "transfer-group", "end", "stage-end", "end-group"} {
		if !destinations[ref] {
			t.Errorf("missing destination %q: %v", ref, destinations)
		}
	}
	if origins["intermediate-group"] || destinations["intermediate-group"] || origins["end-group"] || destinations["start-group"] {
		t.Fatalf("intermediate or wrong-side group included: origins=%v destinations=%v", origins, destinations)
	}
	// Cross-item origin/destination combinations must remain available for joined
	// legs and rules whose scope is the entire journey.
	if !origins["start-group"] || !destinations["end-group"] {
		t.Fatal("lost whole-journey endpoints")
	}
}

func TestFareEndpointRefsIncludesParentOfMatchingStageNumber(t *testing.T) {
	refs := map[string]bool{}
	locations := map[string]ctdf.FareLocation{
		"catalogue-stage": {FareRecord: ctdf.FareRecord{SourceIdentifier: "123"}, Type: ctdf.FareLocationTypeStage, ParentRef: "stage-parent"},
		"stage-parent":    {},
	}
	addFareEndpointRefs(refs, "stop", "schedule-fare-location-stage-123", locations)
	if !refs["stage-parent"] {
		t.Fatalf("stage parent missing: %v", refs)
	}
}
