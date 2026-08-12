package departuregraph

import (
	"container/heap"
	"context"
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

func TestPlanLabelDominanceKeepsOnlyEarliestCompleteState(t *testing.T) {
	queue := &planQueue{}
	heap.Init(queue)
	best := map[planState]time.Time{}
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	first := &planLabel{stop: 1, arrival: base.Add(10 * time.Minute), vehicleLegs: 1}
	if !pushPlanLabel(queue, best, first) {
		t.Fatal("first label was rejected")
	}
	if pushPlanLabel(queue, best, &planLabel{stop: 1, arrival: base.Add(11 * time.Minute), vehicleLegs: 1}) {
		t.Fatal("dominated later label was retained")
	}
	earlier := &planLabel{stop: 1, arrival: base.Add(9 * time.Minute), vehicleLegs: 1}
	if !pushPlanLabel(queue, best, earlier) || currentPlanLabel(best, first) || !currentPlanLabel(best, earlier) {
		t.Fatal("earlier label did not replace the previous state")
	}
	transferred := &planLabel{stop: 1, arrival: base.Add(12 * time.Minute), vehicleLegs: 1, route: appendPlanLeg(nil, PlanLeg{Type: ctdf.JourneyPlanRouteItemTypeTransfer})}
	if !pushPlanLabel(queue, best, transferred) {
		t.Fatal("transfer-constrained state was incorrectly dominated")
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
