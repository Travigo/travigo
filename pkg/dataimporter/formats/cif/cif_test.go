package cif

import (
	"reflect"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

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
	existing := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "a", DestinationStopRef: "b", TrackRef: "track-a-b"},
		{OriginStopRef: "b", DestinationStopRef: "c", TrackRef: "track-b-c"},
		{OriginStopRef: "c", DestinationStopRef: "d", TrackRef: "track-c-d"},
	}}
	replacement := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "a", DestinationStopRef: "b"},
		{OriginStopRef: "b", DestinationStopRef: "x"},
		{OriginStopRef: "c", DestinationStopRef: "d", TrackRef: "replacement-track"},
	}}

	preserved := preserveMatchingJourneyTrackRefs(existing, replacement)
	if preserved != 1 {
		t.Fatalf("preserved %d track references, want 1", preserved)
	}
	if replacement.Path[0].TrackRef != "track-a-b" {
		t.Fatalf("matching leg track ref = %q, want track-a-b", replacement.Path[0].TrackRef)
	}
	if replacement.Path[1].TrackRef != "" {
		t.Fatalf("changed leg inherited track ref %q", replacement.Path[1].TrackRef)
	}
	if replacement.Path[2].TrackRef != "replacement-track" {
		t.Fatalf("replacement track ref was overwritten with %q", replacement.Path[2].TrackRef)
	}
}

func TestPreserveMatchingJourneyTrackRefsMatchesRepeatedLegsByOccurrence(t *testing.T) {
	existing := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "a", DestinationStopRef: "b", TrackRef: "first"},
		{OriginStopRef: "a", DestinationStopRef: "b", TrackRef: "second"},
	}}
	replacement := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "a", DestinationStopRef: "b"},
		{OriginStopRef: "a", DestinationStopRef: "b"},
	}}

	preserved := preserveMatchingJourneyTrackRefs(existing, replacement)
	if preserved != 2 {
		t.Fatalf("preserved %d track references, want 2", preserved)
	}
	if replacement.Path[0].TrackRef != "first" || replacement.Path[1].TrackRef != "second" {
		t.Fatalf("repeated leg refs = %q, %q", replacement.Path[0].TrackRef, replacement.Path[1].TrackRef)
	}
}
