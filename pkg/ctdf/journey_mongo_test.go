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
				DestinationActivity: []JourneyPathItemActivity{JourneyPathItemActivitySetdown},
				TrackRef:            "leg", Track: geometry, Distance: 3, DestinationDisplay: "B",
				PassingPath: []string{"TIPLOC-1", "TIPLOC-2"}, OriginStop: &Stop{PrimaryIdentifier: "A"},
			},
			&JourneyPathItem{
				OriginStopRef: "B", DestinationStopRef: "B",
				OriginArrivalTime: arrival, OriginDepartureTime: arrival.Add(time.Minute),
				DestinationArrivalTime: arrival.Add(2 * time.Minute),
				OriginPlatform:         "2", DestinationPlatform: "3",
				OriginActivity:      []JourneyPathItemActivity{JourneyPathItemActivityPickup},
				DestinationActivity: []JourneyPathItemActivity{JourneyPathItemActivityPickup},
			},
			&JourneyPathItem{
				OriginStopRef: "B", DestinationStopRef: "A",
				OriginArrivalTime: arrival.Add(3 * time.Minute), OriginDepartureTime: arrival.Add(3 * time.Minute),
				DestinationArrivalTime: arrival.Add(15 * time.Minute),
				OriginPlatform:         "4", DestinationPlatform: "5",
				OriginActivity:      []JourneyPathItemActivity{JourneyPathItemActivitySetdown},
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
	call := raw.Lookup("calls", "1").Document()
	for _, field := range []string{"incomingarrival", "incomingplatform", "originactivity", "destinationactivity"} {
		if _, err := call.LookupErr(field); err == nil {
			t.Fatalf("unexpected field %s in stored call", field)
		}
	}
	if call.Lookup("activity").Array().Index(0).Value().StringValue() != JourneyPathItemActivityPickup {
		t.Fatal("missing stored call activity")
	}
	legs := decoded.Legs()
	if len(legs) != 4 || !legs[0].DestinationArrivalTime.Equal(arrival) ||
		legs[0].DestinationPlatform != "2" || legs[1].OriginPlatform != "2" ||
		!legs[1].DestinationArrivalTime.Equal(arrival.Add(2*time.Minute)) ||
		!legs[2].OriginArrivalTime.Equal(legs[1].DestinationArrivalTime) ||
		legs[1].DestinationPlatform != "4" || legs[2].OriginPlatform != "4" ||
		legs[2].DestinationStopRef != "A" || legs[3].OriginStopRef != "X" {
		t.Fatalf("legacy legs disagree with normalized calls or segment boundaries: %+v", legs)
	}
	both := []JourneyPathItemActivity{JourneyPathItemActivityPickup, JourneyPathItemActivitySetdown}
	if !reflect.DeepEqual(legs[0].DestinationActivity, both) || !reflect.DeepEqual(legs[1].OriginActivity, both) {
		t.Fatalf("legacy activity did not preserve boarding and alighting: %v / %v", legs[0].DestinationActivity, legs[1].OriginActivity)
	}
	if !reflect.DeepEqual(decoded.Calls[2].Activity, []JourneyPathItemActivity{JourneyPathItemActivityPass}) {
		t.Fatalf("call with neither boarding nor alighting allowed = %v, want pass", decoded.Calls[2].Activity)
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
