package ctdf

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestMongoJourneyRoundTrip(t *testing.T) {
	departure := time.Date(0, 1, 1, 23, 50, 0, 0, time.UTC)
	arrival := departure.Add(20 * time.Minute)
	geometry := []Location{{Type: "Point", Coordinates: []float64{-1, 51}}}
	journey := &Journey{
		PrimaryIdentifier: "loop", ServiceRef: "service", OperatorRef: "operator",
		DepartureTime: departure, DepartureTimezone: "Europe/London",
		TrackRef: "whole", Track: geometry,
		OtherIdentifiers:  map[string]string{"TrainUID": "test"},
		AmendmentStopRefs: []string{"old-stop"}, ReplacesJourneyRefs: []string{"old-journey"},
		Calls: JourneyCallsFromLegs(
			&JourneyPathItem{
				OriginStopRef: "A", DestinationStopRef: "B", OriginDepartureTime: departure,
				OriginActivity:      []JourneyPathItemActivity{JourneyPathItemActivityPickup, JourneyPathItemActivitySetdown},
				DestinationActivity: []JourneyPathItemActivity{},
				TrackRef:            "leg", Track: geometry, Distance: 3, DestinationDisplay: "B",
				PassingPath: []string{"TIPLOC-1", "TIPLOC-2"}, OriginStop: &Stop{PrimaryIdentifier: "A"},
			},
			&JourneyPathItem{
				OriginStopRef: "B", DestinationStopRef: "B",
				OriginArrivalTime: arrival, OriginDepartureTime: arrival.Add(time.Minute),
				DestinationArrivalTime: arrival.Add(2 * time.Minute),
				OriginPlatform:         "2", DestinationPlatform: "3",
				OriginActivity: []JourneyPathItemActivity{JourneyPathItemActivityPass},
			},
			&JourneyPathItem{
				OriginStopRef: "B", DestinationStopRef: "A",
				OriginArrivalTime: arrival.Add(3 * time.Minute), OriginDepartureTime: arrival.Add(3 * time.Minute),
				DestinationArrivalTime: arrival.Add(15 * time.Minute),
				OriginPlatform:         "4", DestinationPlatform: "5",
				DestinationActivity: []JourneyPathItemActivity{JourneyPathItemActivitySetdown},
			},
			&JourneyPathItem{OriginStopRef: "X", DestinationStopRef: "Y"},
		),
	}

	journey.RefreshStopRefs()
	if err := journey.ValidatePath(); err != nil {
		t.Fatal(err)
	}
	data, err := bson.Marshal(journey)
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(data)
	for _, field := range []string{"path", "track"} {
		if _, err := raw.LookupErr(field); err == nil {
			t.Fatalf("unexpected field %s in persisted journey", field)
		}
	}
	if _, err := raw.LookupErr("calls", "0", "outgoing", "track"); err == nil {
		t.Fatal("persisted inline leg geometry")
	}
	var decoded Journey
	if err := bson.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Calls) != 6 || !decoded.Calls[3].End || !decoded.Calls[4].Start {
		t.Fatalf("unexpected calls for disconnected looping journey: %+v", decoded.Calls)
	}
	if !reflect.DeepEqual(decoded.OriginStopRefs, []string{"A", "B", "X"}) ||
		!reflect.DeepEqual(decoded.DestinationStopRefs, []string{"B", "A", "Y"}) {
		t.Fatalf("unexpected indexed stops: %v / %v", decoded.OriginStopRefs, decoded.DestinationStopRefs)
	}
	if err := decoded.ValidatePath(); err != nil {
		t.Fatal(err)
	}
	journey.Track = nil
	journey.Calls[0].Outgoing.Track = nil
	journey.Calls[0].Stop = nil
	if !reflect.DeepEqual(&decoded, journey) {
		t.Errorf("journey metadata differs after BSON round trip: %+v", decoded)
		for i, call := range decoded.Calls {
			if !reflect.DeepEqual(call, journey.Calls[i]) {
				t.Errorf("call %d got %+v, want %+v", i, call, journey.Calls[i])
			}
		}
	}
	legs := decoded.Legs()
	if len(legs) != 4 || !legs[0].DestinationArrivalTime.IsZero() ||
		legs[0].DestinationPlatform != "" || legs[1].OriginPlatform != "2" ||
		legs[2].DestinationStopRef != "A" || legs[3].OriginStopRef != "X" {
		t.Fatalf("legacy leg conversion lost call sides or segment boundaries: %+v", legs)
	}
}

func TestMongoJourneyDistinguishesEmptyAndMissingCalls(t *testing.T) {
	raw, err := bson.Marshal(&Journey{Calls: []JourneyCall{}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded Journey
	if err := bson.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.ValidatePath(); err != nil {
		t.Fatal(err)
	}
	if err := (&Journey{}).ValidatePath(); err == nil {
		t.Fatal("expected missing calls to fail")
	}
}

func TestMongoJourneyRejectsGeometryWithoutTrackRef(t *testing.T) {
	geometry := []Location{{Type: "Point", Coordinates: []float64{-1, 51}}}
	for _, journey := range []*Journey{
		{Track: geometry, Calls: []JourneyCall{}},
		{Calls: JourneyCallsFromLegs(&JourneyPathItem{OriginStopRef: "A", DestinationStopRef: "B", Track: geometry})},
	} {
		if err := journey.ValidatePath(); err == nil {
			t.Fatal("expected unreferenced track to fail")
		}
	}
}
