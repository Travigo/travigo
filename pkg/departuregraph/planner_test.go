package departuregraph

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

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
