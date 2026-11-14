package cif

import (
	"reflect"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func journeyWithLegs(legs ...*ctdf.JourneyPathItem) *ctdf.Journey {
	return &ctdf.Journey{Calls: ctdf.JourneyCallsFromLegs(legs...)}
}

func TestReferencedTIPLOCsMatchesJourneyStopSelection(t *testing.T) {
	cif := &CommonInterfaceFormat{
		TrainDefinitionSets: []*TrainDefinitionSet{
			{
				OriginLocation: OriginLocation{Location: " ORIGIN "},
				IntermediateLocations: []*IntermediateLocation{
					{Location: "MIDWAYA2", PublicArrivalTime: "1200"},
					{Location: "PASS   ", PublicArrivalTime: "0000"},
				},
				TerminatingLocation: TerminatingLocation{Location: " TERMINA3"},
			},
		},
	}

	got := cif.referencedTIPLOCs()
	want := map[string]struct{}{
		"ORIGIN":  {},
		"MIDWAYA": {},
		"TERMINA": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestPreserveMatchingJourneyTrackRefs(t *testing.T) {
	existing := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", TrackRef: "track-a-b"},
		&ctdf.JourneyPathItem{OriginStopRef: "b", DestinationStopRef: "c", TrackRef: "track-b-c"},
		&ctdf.JourneyPathItem{OriginStopRef: "c", DestinationStopRef: "d", TrackRef: "track-c-d"},
	)
	replacement := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b"},
		&ctdf.JourneyPathItem{OriginStopRef: "b", DestinationStopRef: "x"},
		&ctdf.JourneyPathItem{OriginStopRef: "c", DestinationStopRef: "d", TrackRef: "replacement-track"},
	)

	preserved := preserveMatchingJourneyTrackRefs(existing, replacement)
	if preserved != 1 {
		t.Fatalf("preserved %d track references, want 1", preserved)
	}
	if replacement.Leg(0).TrackRef != "track-a-b" {
		t.Fatalf("matching leg track ref = %q, want track-a-b", replacement.Leg(0).TrackRef)
	}
	if replacement.Leg(1).TrackRef != "" {
		t.Fatalf("changed leg inherited track ref %q", replacement.Leg(1).TrackRef)
	}
	if replacement.Leg(2).TrackRef != "replacement-track" {
		t.Fatalf("replacement track ref was overwritten with %q", replacement.Leg(2).TrackRef)
	}
}

func TestPreserveMatchingJourneyTrackRefsMatchesRepeatedLegsByOccurrence(t *testing.T) {
	existing := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", TrackRef: "first"},
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", TrackRef: "second"},
	)
	replacement := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b"},
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b"},
	)

	preserved := preserveMatchingJourneyTrackRefs(existing, replacement)
	if preserved != 2 {
		t.Fatalf("preserved %d track references, want 2", preserved)
	}
	if replacement.Leg(0).TrackRef != "first" || replacement.Leg(1).TrackRef != "second" {
		t.Fatalf("repeated leg refs = %q, %q", replacement.Leg(0).TrackRef, replacement.Leg(1).TrackRef)
	}
}

func TestCreateJourneyStoresPassingPathBetweenCalls(t *testing.T) {
	previousCache := stopTIPLOCCache
	t.Cleanup(func() { stopTIPLOCCache = previousCache })
	stopTIPLOCCache = map[string]*ctdf.Stop{
		"ORIGIN": {PrimaryIdentifier: "a"},
		"MID":    {PrimaryIdentifier: "b"},
		"END":    {PrimaryIdentifier: "c"},
	}
	cif := &CommonInterfaceFormat{}
	journey := cif.CreateJourneyFromTraindef("test", &TrainDefinitionSet{
		BasicSchedule:  BasicSchedule{PowerType: "DMU", TimingLoad: "156"},
		OriginLocation: OriginLocation{Location: "ORIGIN"},
		IntermediateLocations: []*IntermediateLocation{
			{Location: "PASSING2", PublicArrivalTime: "0000"},
			{Location: "OTHER", PublicArrivalTime: "0000"},
			{Location: "MID", PublicArrivalTime: "1200"},
			{Location: "PASSING2", PublicArrivalTime: "0000"},
		},
		TerminatingLocation: TerminatingLocation{Location: "END"},
	})
	if train := journey.DetailedRailInformation.Trains[0]; train.VehicleTypeName != "Class 156" || len(train.Carriages) != 0 {
		t.Fatalf("scheduled train has incorrect class details or a layout without a known length: %+v", train)
	}
	if got := journey.GetStopRefs(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("passenger stops = %v", got)
	}
	for i, want := range [][]string{{"gb-tiploc-PASSING", "gb-tiploc-OTHER"}, {"gb-tiploc-PASSING"}} {
		if got := journey.Leg(i).PassingPath; !reflect.DeepEqual(got, want) {
			t.Fatalf("leg %d passing path = %v, want %v", i, got, want)
		}
	}
}

func TestPassingPathBackfillKeepsLegacyTrack(t *testing.T) {
	existing := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", TrackRef: "legacy"},
		&ctdf.JourneyPathItem{OriginStopRef: "b", DestinationStopRef: "c", PassingPath: []string{"old"}, TrackRef: "old-variant"},
	)
	replacement := journeyWithLegs(
		&ctdf.JourneyPathItem{OriginStopRef: "a", DestinationStopRef: "b", PassingPath: []string{"via"}},
		&ctdf.JourneyPathItem{OriginStopRef: "b", DestinationStopRef: "c", PassingPath: []string{"new"}},
	)
	if got := preserveMatchingJourneyTrackRefs(existing, replacement); got != 1 {
		t.Fatalf("preserved %d track references, want 1", got)
	}
	if replacement.Leg(0).TrackRef != "legacy" || replacement.Leg(1).TrackRef != "" {
		t.Fatalf("track refs = %q, %q", replacement.Leg(0).TrackRef, replacement.Leg(1).TrackRef)
	}
}
