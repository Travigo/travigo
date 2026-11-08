package travelinenoc

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func TestApplyOperatorMetadataOverrides(t *testing.T) {
	phone := "0141 352 4444"
	override := datasets.OperatorMetadata{
		Datasets:  []string{"noc"},
		Operators: []string{"gb-nocid-137625"},
	}
	override.Set.PhoneNumber = &phone
	dataset := datasets.DataSet{
		Identifier:       "gb-traveline-noc",
		DataSourceRef:    "gb-traveline",
		OperatorMetadata: []datasets.OperatorMetadata{override},
	}
	citylink := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-137625", PhoneNumber: "08705505050"}
	unrelated := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-other", PhoneNumber: "01234567890"}

	applyOperatorMetadataOverrides(dataset, []*ctdf.Operator{citylink, unrelated})
	if citylink.PhoneNumber != phone {
		t.Fatalf("Citylink phone = %q, want %q", citylink.PhoneNumber, phone)
	}
	if unrelated.PhoneNumber != "01234567890" {
		t.Fatalf("unrelated phone = %q, expected it to remain unchanged", unrelated.PhoneNumber)
	}
}

func TestApplyOperatorMetadataOverrideReplacesRegionsForExactOperator(t *testing.T) {
	regions := []string{"UK:REGION:SCOTLAND"}
	override := datasets.OperatorMetadata{
		Datasets:  []string{"noc"},
		Operators: []string{"gb-nocid-137004"},
	}
	override.Set.Regions = &regions
	dataset := datasets.DataSet{
		Identifier:       "gb-traveline-noc",
		DataSourceRef:    "gb-traveline",
		OperatorMetadata: []datasets.OperatorMetadata{override},
	}
	lothian := &ctdf.Operator{
		PrimaryIdentifier: "gb-nocid-137004",
		Regions:           []string{"UK:REGION:NORTHEAST", "UK:REGION:SCOTLAND"},
	}
	unrelated := &ctdf.Operator{
		PrimaryIdentifier: "gb-nocid-other",
		Regions:           []string{"UK:REGION:NORTHEAST"},
	}
	applyOperatorMetadataOverrides(dataset, []*ctdf.Operator{lothian, unrelated})
	if len(lothian.Regions) != 1 || lothian.Regions[0] != "UK:REGION:SCOTLAND" {
		t.Fatalf("Lothian regions = %v, want only Scotland", lothian.Regions)
	}
	if len(unrelated.Regions) != 1 || unrelated.Regions[0] != "UK:REGION:NORTHEAST" {
		t.Fatalf("unrelated operator regions changed: %v", unrelated.Regions)
	}
}
