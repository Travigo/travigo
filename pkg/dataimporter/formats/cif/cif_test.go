package cif

import (
	"reflect"
	"testing"
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
