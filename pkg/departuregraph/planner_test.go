package departuregraph

import (
	"container/heap"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestPlanDefaultsCoverOvernightJourneys(t *testing.T) {
	config := normalizePlanConfig(PlanRequest{})
	if config.maxDuration != 12*time.Hour {
		t.Fatalf("max duration = %s", config.maxDuration)
	}
	if config.maxExpandedLabels != 200000 {
		t.Fatalf("expanded labels = %d", config.maxExpandedLabels)
	}
}

func TestPlanResultCountDoesNotMultiplyIntermediateState(t *testing.T) {
	config := normalizePlanConfig(PlanRequest{Count: 20})
	if config.maxLabelsPerState != 1 {
		t.Fatalf("labels per intermediate state = %d, want 1", config.maxLabelsPerState)
	}
}

func TestPlanLabelDominanceKeepsOnlyEarliestCompleteState(t *testing.T) {
	queue := &planQueue{maxLabelsPerState: 1}
	heap.Init(queue)
	best := map[planState][]planArrival{}
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	first := &planLabel{stop: 1, arrival: base.Add(10 * time.Minute), vehicleLegs: 1}
	if !pushPlanLabel(queue, best, first) {
		t.Fatal("first label was rejected")
	}
	if pushPlanLabel(queue, best, &planLabel{stop: 1, arrival: base.Add(11 * time.Minute), vehicleLegs: 1}) {
		t.Fatal("dominated later label was retained")
	}
	earlier := &planLabel{stop: 1, arrival: base.Add(9 * time.Minute), vehicleLegs: 1}
	if !pushPlanLabel(queue, best, earlier) || currentPlanLabel(queue, best, first) || !currentPlanLabel(queue, best, earlier) {
		t.Fatal("earlier label did not replace the previous state")
	}
	transferred := &planLabel{stop: 1, arrival: base.Add(12 * time.Minute), vehicleLegs: 1, walked: true, route: appendPlanLeg(nil, PlanLeg{Type: ctdf.JourneyPlanRouteItemTypeTransfer})}
	if !pushPlanLabel(queue, best, transferred) {
		t.Fatal("transfer-constrained state was incorrectly dominated")
	}
}

func TestPlanStateOnlyKeepsIncomingJourneyForRestrictedTransfers(t *testing.T) {
	data := newGraphData()
	data.Stops = make([]stopRecord, 2)
	data.IncomingJourneyStateStops = []bool{false, true}
	queue := &planQueue{data: data, maxLabelsPerState: 1}
	heap.Init(queue)
	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)

	unrestricted := map[planState][]planArrival{}
	if !pushPlanLabel(queue, unrestricted, &planLabel{stop: 0, arrival: base, lastJourney: 1, hasLastJourney: true}) {
		t.Fatal("first unrestricted arrival was rejected")
	}
	if pushPlanLabel(queue, unrestricted, &planLabel{stop: 0, arrival: base, lastJourney: 2, hasLastJourney: true}) {
		t.Fatal("incoming journey unnecessarily split an unrestricted stop state")
	}

	restricted := map[planState][]planArrival{}
	if !pushPlanLabel(queue, restricted, &planLabel{stop: 1, arrival: base, lastJourney: 1, hasLastJourney: true}) ||
		!pushPlanLabel(queue, restricted, &planLabel{stop: 1, arrival: base, lastJourney: 2, hasLastJourney: true}) {
		t.Fatal("route-restricted stop did not retain distinct incoming journeys")
	}
}

func TestIncomingJourneyStateFollowsPlatformAliasesToRestrictedTransfer(t *testing.T) {
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{
		stops: []*ctdf.Stop{
			{PrimaryIdentifier: "arrival"},
			{PrimaryIdentifier: "station"},
			{PrimaryIdentifier: "platform"},
			{PrimaryIdentifier: "destination"},
		},
		transfers: []*ctdf.StopTransfer{
			{FromStopRef: "arrival", ToStopRef: "station", Type: ctdf.StopTransferTypeSameStopGroup, TotalDurationSeconds: 30},
			{FromStopRef: "station", ToStopRef: "platform", Type: ctdf.StopTransferTypePlatformAlias, TotalDurationSeconds: 30},
			{FromStopRef: "platform", ToStopRef: "destination", Type: ctdf.StopTransferTypeTimed, FromRouteRef: "incoming", TotalDurationSeconds: 60},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []string{"arrival", "station", "platform"} {
		stop, exists := data.stopIndex(identifier)
		if !exists || !data.IncomingJourneyStateStops[stop] {
			t.Fatalf("incoming journey state was not retained at %s", identifier)
		}
	}
	destination, exists := data.stopIndex("destination")
	if !exists || data.IncomingJourneyStateStops[destination] {
		t.Fatal("unrelated destination unnecessarily retained incoming journey state")
	}
}

func TestGraphSimpleRouteIsNotStarvedByIncomingJourneyStates(t *testing.T) {
	serviceDate := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "origin", Location: &ctdf.Location{Coordinates: []float64{0, 52}}},
		{PrimaryIdentifier: "station", Location: &ctdf.Location{Coordinates: []float64{0.1, 52}}},
		{PrimaryIdentifier: "distractor", Location: &ctdf.Location{Coordinates: []float64{0.99, 52}}},
		{PrimaryIdentifier: "distractor-2", Location: &ctdf.Location{Coordinates: []float64{0.995, 52}}},
		{PrimaryIdentifier: "distractor-3", Location: &ctdf.Location{Coordinates: []float64{0.997, 52}}},
		{PrimaryIdentifier: "destination", Location: &ctdf.Location{Coordinates: []float64{1, 52}}},
	}}); err != nil {
		t.Fatal(err)
	}
	day := makeDayKey(serviceDate)
	data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: "access-bus", Path: []*ctdf.JourneyPathItem{{
		OriginStopRef: "origin", DestinationStopRef: "station",
		OriginDepartureTime: serviceTime(9 * 3600), DestinationArrivalTime: serviceTime(9*3600 + 20*60),
	}}})
	data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: "destination-train", Path: []*ctdf.JourneyPathItem{{
		OriginStopRef: "station", DestinationStopRef: "destination",
		OriginDepartureTime: serviceTime(9*3600 + 30*60), DestinationArrivalTime: serviceTime(10*3600 + 30*60),
	}}})
	for index := 0; index < 20; index++ {
		data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: fmt.Sprintf("distractor-arrival-%d", index), Path: []*ctdf.JourneyPathItem{{
			OriginStopRef: "origin", DestinationStopRef: "distractor",
			OriginDepartureTime: serviceTime(8*3600 + 55*60), DestinationArrivalTime: serviceTime(9*3600 + 60),
		}}})
	}
	for index, leg := range [][2]string{{"distractor", "distractor-2"}, {"distractor-2", "distractor-3"}, {"distractor-3", "destination"}} {
		data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: fmt.Sprintf("late-connection-%d", index), Path: []*ctdf.JourneyPathItem{{
			OriginStopRef: leg[0], DestinationStopRef: leg[1],
			OriginDepartureTime: serviceTime(int32((11 + index) * 3600)), DestinationArrivalTime: serviceTime(int32((11+index)*3600 + 30*60)),
		}}})
	}
	data.completeScan([]time.Time{serviceDate})

	for _, count := range []int{1, 5} {
		result, err := graph.Plan(context.Background(), PlanRequest{
			OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"},
			StartDateTime: serviceDate.Add(8*time.Hour + 50*time.Minute), Count: count, MaxChanges: 3,
			MaxExpandedLabels: 8, MaxSearchDurationMillis: 1000,
		})
		if err != nil {
			t.Fatal(err)
		}
		foundSimple := false
		for _, plan := range result.Plans {
			if len(plan.Legs) == 2 && plan.Legs[0].JourneyRef == "access-bus" {
				foundSimple = true
				break
			}
		}
		if !foundSimple {
			t.Fatalf("simple access route was starved for count %d: %#v", count, result)
		}
	}
}

func TestGraphReturnsRequestedDirectAlternatives(t *testing.T) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "origin"}, {PrimaryIdentifier: "destination"},
	}}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		departure := int32(10*3600 + index*10*60)
		data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{
			PrimaryIdentifier: "journey-" + string(rune('a'+index)),
			Path: []*ctdf.JourneyPathItem{{
				OriginStopRef: "origin", DestinationStopRef: "destination",
				OriginDepartureTime: serviceTime(departure), DestinationArrivalTime: serviceTime(departure + 30*60),
			}},
		})
	}
	data.completeScan([]time.Time{serviceDate})

	result, err := graph.Plan(context.Background(), PlanRequest{
		OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"},
		StartDateTime: serviceDate.Add(9 * time.Hour), Count: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 3 || result.SearchTruncated {
		t.Fatalf("result = %#v, want three complete alternatives", result)
	}
}

func TestGraphReturnsPartialAlternativesWhenBudgetEnds(t *testing.T) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "origin"}, {PrimaryIdentifier: "destination"},
	}}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		departure := int32(10*3600 + index*10*60)
		data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{PrimaryIdentifier: fmt.Sprintf("journey-%d", index), Path: []*ctdf.JourneyPathItem{{
			OriginStopRef: "origin", DestinationStopRef: "destination",
			OriginDepartureTime: serviceTime(departure), DestinationArrivalTime: serviceTime(departure + 30*60),
		}}})
	}
	data.completeScan([]time.Time{serviceDate})

	result, err := graph.Plan(context.Background(), PlanRequest{
		OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"},
		StartDateTime: serviceDate.Add(9 * time.Hour), Count: 5, MaxExpandedLabels: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) == 0 || !result.SearchTruncated || result.SearchTruncatedReason != "expanded_label_budget" {
		t.Fatalf("partial result = %#v", result)
	}
}

func TestGraphSearchesLowestVehicleLegRoundBeforeWiderCorridors(t *testing.T) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	stops := []*ctdf.Stop{{PrimaryIdentifier: "origin"}, {PrimaryIdentifier: "destination"}}
	for index := 0; index < 20; index++ {
		stops = append(stops,
			&ctdf.Stop{PrimaryIdentifier: fmt.Sprintf("a-%d", index)},
			&ctdf.Stop{PrimaryIdentifier: fmt.Sprintf("b-%d", index)},
			&ctdf.Stop{PrimaryIdentifier: fmt.Sprintf("c-%d", index)},
		)
	}
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: stops}); err != nil {
		t.Fatal(err)
	}
	day := makeDayKey(serviceDate)
	data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: "direct", Path: []*ctdf.JourneyPathItem{{
		OriginStopRef: "origin", DestinationStopRef: "destination",
		OriginDepartureTime: serviceTime(10 * 3600), DestinationArrivalTime: serviceTime(12 * 3600),
	}}})
	for index := 0; index < 20; index++ {
		refs := []string{"origin", fmt.Sprintf("a-%d", index), fmt.Sprintf("b-%d", index), fmt.Sprintf("c-%d", index), "destination"}
		for leg := 0; leg < 4; leg++ {
			departure := int32(9*3600 + index*60 + leg*120)
			data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: fmt.Sprintf("distractor-%d-%d", index, leg), Path: []*ctdf.JourneyPathItem{{
				OriginStopRef: refs[leg], DestinationStopRef: refs[leg+1],
				OriginDepartureTime: serviceTime(departure), DestinationArrivalTime: serviceTime(departure + 60),
			}}})
		}
	}
	data.completeScan([]time.Time{serviceDate})

	result, err := graph.Plan(context.Background(), PlanRequest{
		OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"},
		StartDateTime: serviceDate.Add(8 * time.Hour), Count: 1, MaxChanges: 3, MaxExpandedLabels: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 1 || result.Plans[0].Legs[0].JourneyRef != "direct" || result.SearchTruncated {
		t.Fatalf("iterative round result = %#v", result)
	}
}

func TestGraphResultCountOnlyWidensDestinationPatterns(t *testing.T) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "origin"}, {PrimaryIdentifier: "interchange"}, {PrimaryIdentifier: "destination"},
	}}); err != nil {
		t.Fatal(err)
	}
	day := makeDayKey(serviceDate)
	for index := 0; index < 20; index++ {
		departure := int32(9*3600 + index*60)
		data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: fmt.Sprintf("feeder-%02d", index), ServiceRef: "feeder", Path: []*ctdf.JourneyPathItem{{
			OriginStopRef: "origin", DestinationStopRef: "interchange",
			OriginDepartureTime: serviceTime(departure), DestinationArrivalTime: serviceTime(departure + 10*60),
		}}})
	}
	for index := 0; index < 5; index++ {
		departure := int32(10*3600 + index*10*60)
		data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: fmt.Sprintf("final-%02d", index), ServiceRef: "final", Path: []*ctdf.JourneyPathItem{{
			OriginStopRef: "interchange", DestinationStopRef: "destination",
			OriginDepartureTime: serviceTime(departure), DestinationArrivalTime: serviceTime(departure + 30*60),
		}}})
	}
	data.completeScan([]time.Time{serviceDate})
	request := PlanRequest{
		OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"},
		StartDateTime: serviceDate.Add(8 * time.Hour), MaxChanges: 1,
	}
	request.Count = 1
	first, err := graph.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Count = 5
	alternatives, err := graph.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Plans) != 1 || len(alternatives.Plans) != 5 {
		t.Fatalf("first = %#v, alternatives = %#v", first, alternatives)
	}
	if alternatives.ExpandedLabels > first.ExpandedLabels+5 {
		t.Fatalf("count widened intermediate frontier: first=%d alternatives=%d", first.ExpandedLabels, alternatives.ExpandedLabels)
	}
}

func TestGraphUsesPatternDepartureIndexAndHonoursExclusions(t *testing.T) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "origin"}, {PrimaryIdentifier: "destination"},
	}}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		departure := int32(10*3600 + index*60)
		data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{
			PrimaryIdentifier: fmt.Sprintf("journey-%02d", index),
			Path: []*ctdf.JourneyPathItem{{
				OriginStopRef: "origin", DestinationStopRef: "destination",
				OriginDepartureTime: serviceTime(departure), DestinationArrivalTime: serviceTime(departure + 30*60),
			}},
		})
	}
	data.completeScan([]time.Time{serviceDate})

	originID := data.StopIDs["origin"]
	bucket := bucketKey{Day: makeDayKey(serviceDate), StopRef: originID}
	patternBucket, exists := data.PatternDepartureBuckets[bucket]
	if !exists || patternBucket.GroupCount != 1 {
		t.Fatalf("departure pattern bucket = %#v, exists = %t, want one shared pattern", patternBucket, exists)
	}
	group := data.PatternDepartureGroups[patternBucket.GroupStart]
	if group.IndexCount != 20 || len(data.PatternDepartureIndexes) != 20 {
		t.Fatalf("pattern departure group = %#v, indexes = %d, want 20", group, len(data.PatternDepartureIndexes))
	}

	result, err := graph.Plan(context.Background(), PlanRequest{
		OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"},
		StartDateTime: serviceDate.Add(9 * time.Hour), Count: 1,
		ExcludedJourneyRefs: []string{"journey-00"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 1 || result.Plans[0].Legs[0].JourneyRef != "journey-01" {
		t.Fatalf("excluded journey was not bypassed: %#v", result.Plans)
	}
}

func BenchmarkGraphPatternIndexedPlanner(b *testing.B) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "origin"}, {PrimaryIdentifier: "destination"},
	}}); err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 5000; index++ {
		departure := int32(10*3600 + index*60)
		data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{
			PrimaryIdentifier: fmt.Sprintf("journey-%04d", index), ServiceRef: "service",
			Path: []*ctdf.JourneyPathItem{{
				OriginStopRef: "origin", DestinationStopRef: "destination",
				OriginDepartureTime: serviceTime(departure), DestinationArrivalTime: serviceTime(departure + 30*60),
			}},
		})
	}
	data.completeScan([]time.Time{serviceDate})
	request := PlanRequest{
		OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"},
		StartDateTime: serviceDate.Add(9 * time.Hour), Count: 3,
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		result, err := graph.Plan(context.Background(), request)
		if err != nil || len(result.Plans) != 3 {
			b.Fatalf("plan result = %#v, err = %v", result, err)
		}
	}
}

func TestGraphCoordinatesAllowAccessInterchangeAndEgressWalks(t *testing.T) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{
		stops: []*ctdf.Stop{
			{PrimaryIdentifier: "access", Location: &ctdf.Location{Coordinates: []float64{0, 52}}},
			{PrimaryIdentifier: "arrival", Location: &ctdf.Location{Coordinates: []float64{0.1, 52}}},
			{PrimaryIdentifier: "departure", Location: &ctdf.Location{Coordinates: []float64{0.101, 52}}},
			{PrimaryIdentifier: "approach", Location: &ctdf.Location{Coordinates: []float64{0.2, 52}}},
		},
		transfers: []*ctdf.StopTransfer{{
			FromStopRef: "arrival", ToStopRef: "departure", Type: ctdf.StopTransferTypeNearbyWalk,
			DistanceMetres: 100, WalkDurationSeconds: 90, TotalDurationSeconds: 90,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	day := makeDayKey(serviceDate)
	data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: "first", Path: []*ctdf.JourneyPathItem{{
		OriginStopRef: "access", DestinationStopRef: "arrival",
		OriginDepartureTime: serviceTime(9 * 3600), DestinationArrivalTime: serviceTime(9*3600 + 20*60),
	}}})
	data.addJourney(day, &ctdf.Journey{PrimaryIdentifier: "second", Path: []*ctdf.JourneyPathItem{{
		OriginStopRef: "departure", DestinationStopRef: "approach",
		OriginDepartureTime: serviceTime(9*3600 + 30*60), DestinationArrivalTime: serviceTime(10 * 3600),
	}}})
	data.completeScan([]time.Time{serviceDate})

	result, err := graph.Plan(context.Background(), PlanRequest{
		OriginLocation:      &PlanLocation{Longitude: 0, Latitude: 52},
		DestinationLocation: &PlanLocation{Longitude: 0.2005, Latitude: 52},
		StartDateTime:       serviceDate.Add(8 * time.Hour), Count: 1, MaxChanges: 1,
		MaxTransferDistanceMetres: 500, OriginLocationStopCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 1 || len(result.Plans[0].Legs) != 5 {
		t.Fatalf("coordinate plan = %#v", result.Plans)
	}
	legs := result.Plans[0].Legs
	if legs[0].Type != ctdf.JourneyPlanRouteItemTypeTransfer || legs[2].TransferType != ctdf.StopTransferTypeNearbyWalk || legs[4].DestinationStopRef[:22] != "coordinate-destination" {
		t.Fatalf("access/interchange/egress legs = %#v", legs)
	}
}

func TestGraphCoordinateAccessCanTraversePlatformAlias(t *testing.T) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{
		stops: []*ctdf.Stop{
			{PrimaryIdentifier: "access", Location: &ctdf.Location{Coordinates: []float64{0, 52}}},
			{PrimaryIdentifier: "station", Location: &ctdf.Location{Coordinates: []float64{0.001, 52}}},
			{PrimaryIdentifier: "destination", Location: &ctdf.Location{Coordinates: []float64{0.1, 52}}},
		},
		transfers: []*ctdf.StopTransfer{{
			FromStopRef: "access", ToStopRef: "station", Type: ctdf.StopTransferTypePlatformAlias,
			DistanceMetres: 50, WalkDurationSeconds: 60, TotalDurationSeconds: 60,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{
		PrimaryIdentifier: "journey",
		Path: []*ctdf.JourneyPathItem{{
			OriginStopRef: "station", DestinationStopRef: "destination",
			OriginDepartureTime: serviceTime(10 * 3600), DestinationArrivalTime: serviceTime(11 * 3600),
		}},
	})
	data.completeScan([]time.Time{serviceDate})

	result, err := graph.Plan(context.Background(), PlanRequest{
		OriginLocation: &PlanLocation{Longitude: 0, Latitude: 52}, DestinationRefs: []string{"destination"},
		StartDateTime: serviceDate.Add(9 * time.Hour), Count: 1,
		MaxTransferDistanceMetres: 80, OriginLocationStopCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 1 || len(result.Plans[0].Legs) != 3 || result.Plans[0].Legs[1].TransferType != ctdf.StopTransferTypePlatformAlias {
		t.Fatalf("plans = %#v", result.Plans)
	}
}

func TestGraphAppliesRouteRestrictedTransfer(t *testing.T) {
	serviceDate := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{
		stops: []*ctdf.Stop{{PrimaryIdentifier: "origin"}, {PrimaryIdentifier: "arrival"}, {PrimaryIdentifier: "departure"}, {PrimaryIdentifier: "destination"}},
		transfers: []*ctdf.StopTransfer{{
			FromStopRef: "arrival", ToStopRef: "departure", Type: ctdf.StopTransferTypeTimed,
			FromRouteRef: "service-in", ToRouteRef: "service-out", TotalDurationSeconds: 120,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{PrimaryIdentifier: "in", ServiceRef: "service-in", Path: []*ctdf.JourneyPathItem{{
		OriginStopRef: "origin", DestinationStopRef: "arrival", OriginDepartureTime: serviceTime(9 * 3600), DestinationArrivalTime: serviceTime(9*3600 + 30*60),
	}}})
	data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{PrimaryIdentifier: "out", ServiceRef: "service-out", Path: []*ctdf.JourneyPathItem{{
		OriginStopRef: "departure", DestinationStopRef: "destination", OriginDepartureTime: serviceTime(10 * 3600), DestinationArrivalTime: serviceTime(11 * 3600),
	}}})
	data.completeScan([]time.Time{serviceDate})

	result, err := graph.Plan(context.Background(), PlanRequest{
		OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"}, StartDateTime: serviceDate.Add(8 * time.Hour), Count: 1, MaxChanges: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 1 || len(result.Plans[0].Legs) != 3 {
		t.Fatalf("plans = %#v", result.Plans)
	}
}

func TestPlanQueuePrioritisesAdmissibleDestinationEstimate(t *testing.T) {
	data := newGraphData()
	data.Stops = []stopRecord{
		{Longitude: 0, Latitude: 52, HasLocation: true},
		{Longitude: 1.99, Latitude: 52, HasLocation: true},
		{Longitude: 2, Latitude: 52, HasLocation: true},
	}
	queue := newPlanQueue(data, map[uint32]bool{2: true})
	heap.Init(queue)
	base := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	heap.Push(queue, &planLabel{stop: 0, arrival: base})
	heap.Push(queue, &planLabel{stop: 1, arrival: base.Add(10 * time.Minute)})
	if got := heap.Pop(queue).(*planLabel).stop; got != 1 {
		t.Fatalf("first stop = %d, want destination-directed stop 1", got)
	}
}

func TestPlanQueuePrioritisesUsefulLowChangeRoute(t *testing.T) {
	queue := &planQueue{}
	heap.Init(queue)
	base := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	heap.Push(queue, &planLabel{stop: 0, arrival: base, vehicleLegs: 3})
	heap.Push(queue, &planLabel{stop: 1, arrival: base.Add(3 * time.Minute), vehicleLegs: 1})
	if got := heap.Pop(queue).(*planLabel).stop; got != 1 {
		t.Fatalf("first stop = %d, want lower-change route", got)
	}
}

type planningTopologyLoader struct {
	stops     []*ctdf.Stop
	transfers []*ctdf.StopTransfer
}

func (loader planningTopologyLoader) ScanStops(_ context.Context, visit func(*ctdf.Stop) error) error {
	for _, stop := range loader.stops {
		if err := visit(stop); err != nil {
			return err
		}
	}
	return nil
}

func (loader planningTopologyLoader) ScanTransfers(_ context.Context, visit func(*ctdf.StopTransfer) error) error {
	for _, transfer := range loader.transfers {
		if err := visit(transfer); err != nil {
			return err
		}
	}
	return nil
}

func TestGraphPlansJourneyAndFinalTransferEntirelyInMemory(t *testing.T) {
	serviceDate := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	loader := planningTopologyLoader{
		stops: []*ctdf.Stop{
			{PrimaryIdentifier: "origin", OtherIdentifiers: []string{"origin-alias"}, Location: &ctdf.Location{Coordinates: []float64{0, 52}}},
			{PrimaryIdentifier: "middle", OtherIdentifiers: []string{"middle-alias"}, Location: &ctdf.Location{Coordinates: []float64{0.05, 52}}},
			{PrimaryIdentifier: "approach", OtherIdentifiers: []string{"approach-alias"}, Location: &ctdf.Location{Coordinates: []float64{0.1, 52}}},
			{PrimaryIdentifier: "destination", Location: &ctdf.Location{Coordinates: []float64{0.101, 52}}},
		},
		transfers: []*ctdf.StopTransfer{{FromStopRef: "approach", ToStopRef: "destination", Type: ctdf.StopTransferTypeNearbyWalk, DistanceMetres: 100, WalkDurationSeconds: 80, TotalDurationSeconds: 80}},
	}
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), loader); err != nil {
		t.Fatal(err)
	}
	data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{
		PrimaryIdentifier: "journey-1",
		Path: []*ctdf.JourneyPathItem{
			{
				OriginStopRef: "origin-alias", DestinationStopRef: "middle-alias",
				OriginDepartureTime: serviceTime(10*3600 + 5*60), DestinationArrivalTime: serviceTime(10*3600 + 15*60),
			},
			{
				OriginStopRef: "middle-alias", DestinationStopRef: "approach-alias",
				OriginDepartureTime: serviceTime(10*3600 + 15*60), DestinationArrivalTime: serviceTime(10*3600 + 25*60),
			},
		},
	})
	data.completeScan([]time.Time{serviceDate})

	result, err := graph.Plan(context.Background(), PlanRequest{OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"}, StartDateTime: serviceDate.Add(10 * time.Hour), Count: 1})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(result.Plans) != 1 || len(result.Plans[0].Legs) != 2 {
		t.Fatalf("plans = %#v", result.Plans)
	}
	if result.Plans[0].Legs[0].JourneyRef != "journey-1" || result.Plans[0].Legs[1].TransferType != ctdf.StopTransferTypeNearbyWalk {
		t.Fatalf("legs = %#v", result.Plans[0].Legs)
	}
}

func TestStaticCorridorFindsUpstreamRideAndTransferLinks(t *testing.T) {
	serviceDate := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	loader := planningTopologyLoader{
		stops: []*ctdf.Stop{
			{PrimaryIdentifier: "origin"},
			{PrimaryIdentifier: "approach"},
			{PrimaryIdentifier: "destination"},
			{PrimaryIdentifier: "unrelated"},
		},
		transfers: []*ctdf.StopTransfer{{FromStopRef: "approach", ToStopRef: "destination", Type: ctdf.StopTransferTypeNearbyWalk, TotalDurationSeconds: 60}},
	}
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), loader); err != nil {
		t.Fatal(err)
	}
	data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{PrimaryIdentifier: "journey-1", Path: []*ctdf.JourneyPathItem{{OriginStopRef: "origin", DestinationStopRef: "approach", OriginDepartureTime: serviceTime(3600), DestinationArrivalTime: serviceTime(4200)}}})
	data.completeScan([]time.Time{serviceDate})
	destination, _ := data.stopIndex("destination")
	origin, _ := data.stopIndex("origin")
	approach, _ := data.stopIndex("approach")
	unrelated, _ := data.stopIndex("unrelated")
	corridor := data.planCorridor(map[uint32]bool{destination: true}, 2)
	if corridor[destination] != 0 || corridor[approach] != 0 || corridor[origin] != 1 {
		t.Fatalf("corridor rides destination=%d approach=%d origin=%d", corridor[destination], corridor[approach], corridor[origin])
	}
	if corridor[unrelated] != unreachableCorridorRides {
		t.Fatalf("unrelated stop rides = %d", corridor[unrelated])
	}
	if cached := data.planCorridor(map[uint32]bool{destination: true}, 2); len(cached) == 0 || &cached[0] != &corridor[0] {
		t.Fatal("destination corridor was not cached")
	}
}

func TestStaticCorridorDeduplicatesIdenticalStopPatterns(t *testing.T) {
	serviceDate := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	loader := planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "origin"},
		{PrimaryIdentifier: "middle"},
		{PrimaryIdentifier: "destination"},
	}}
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), loader); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{
			PrimaryIdentifier: fmt.Sprintf("journey-%d", index),
			Path: []*ctdf.JourneyPathItem{
				{OriginStopRef: "origin", DestinationStopRef: "middle", OriginDepartureTime: serviceTime(3600 + int32(index*600)), DestinationArrivalTime: serviceTime(3900 + int32(index*600))},
				{OriginStopRef: "middle", DestinationStopRef: "destination", OriginDepartureTime: serviceTime(3960 + int32(index*600)), DestinationArrivalTime: serviceTime(4260 + int32(index*600))},
			},
		})
	}
	data.completeScan([]time.Time{serviceDate})
	if len(data.StaticPatterns) != 1 || len(data.StaticPatternStops) != 3 || len(data.ArrivalPatterns) != 2 {
		t.Fatalf("patterns=%d stops=%d arrivals=%d", len(data.StaticPatterns), len(data.StaticPatternStops), len(data.ArrivalPatterns))
	}
}

func TestGraphCoordinateOriginUsesSpatialStopIndex(t *testing.T) {
	serviceDate := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	loader := planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "nearby", Location: &ctdf.Location{Coordinates: []float64{0, 52}}},
		{PrimaryIdentifier: "destination", Location: &ctdf.Location{Coordinates: []float64{0.01, 52}}},
	}}
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), loader); err != nil {
		t.Fatal(err)
	}
	data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{PrimaryIdentifier: "journey-1", Path: []*ctdf.JourneyPathItem{{OriginStopRef: "nearby", DestinationStopRef: "destination", OriginDepartureTime: serviceTime(3600), DestinationArrivalTime: serviceTime(4200)}}})
	data.completeScan([]time.Time{serviceDate})
	result, err := graph.Plan(context.Background(), PlanRequest{OriginLocation: &PlanLocation{Longitude: 0.0001, Latitude: 52}, DestinationRefs: []string{"destination"}, StartDateTime: serviceDate.Add(30 * time.Minute), Count: 1, MaxTransferDistanceMetres: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 1 || len(result.Plans[0].Legs) != 2 || result.Plans[0].Legs[0].Type != ctdf.JourneyPlanRouteItemTypeTransfer {
		t.Fatalf("plans = %#v", result.Plans)
	}
}

func TestGraphDoesNotTreatStopTransfersAsWalkingNetwork(t *testing.T) {
	serviceDate := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	loader := planningTopologyLoader{
		stops: []*ctdf.Stop{
			{PrimaryIdentifier: "origin"},
			{PrimaryIdentifier: "middle"},
			{PrimaryIdentifier: "destination"},
		},
		transfers: []*ctdf.StopTransfer{
			{FromStopRef: "origin", ToStopRef: "middle", Type: ctdf.StopTransferTypeNearbyWalk, TotalDurationSeconds: 60},
			{FromStopRef: "middle", ToStopRef: "destination", Type: ctdf.StopTransferTypeNearbyWalk, TotalDurationSeconds: 60},
		},
	}
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), loader); err != nil {
		t.Fatal(err)
	}
	data.completeScan([]time.Time{serviceDate})
	result, err := graph.Plan(context.Background(), PlanRequest{OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"}, StartDateTime: serviceDate, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 0 {
		t.Fatalf("transfer chain was returned as a journey: %#v", result.Plans)
	}
}

func TestPlanClientUsesJourneyGraphEndpoint(t *testing.T) {
	serviceDate := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "origin"}, {PrimaryIdentifier: "destination"},
	}}); err != nil {
		t.Fatal(err)
	}
	data.addJourney(makeDayKey(serviceDate), &ctdf.Journey{PrimaryIdentifier: "journey-1", Path: []*ctdf.JourneyPathItem{{OriginStopRef: "origin", DestinationStopRef: "destination", OriginDepartureTime: serviceTime(3600), DestinationArrivalTime: serviceTime(4200)}}})
	data.completeScan([]time.Time{serviceDate})
	client := NewClient("http://journey-graph", &http.Client{Transport: handlerTransport{handler: NewServer(graph).Handler()}})
	result, err := client.Plan(context.Background(), PlanRequest{OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"}, StartDateTime: serviceDate.Add(30 * time.Minute), Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 1 || result.Plans[0].Legs[0].JourneyRef != "journey-1" {
		t.Fatalf("result = %#v", result)
	}
}
