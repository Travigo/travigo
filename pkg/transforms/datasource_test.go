package transforms

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func TestDatasourceOverrides(t *testing.T) {
	t.Chdir("../..")
	originalTransforms, originalBranding := transforms, serviceBranding
	t.Cleanup(func() {
		transforms, serviceBranding = originalTransforms, originalBranding
	})
	SetupClient()

	for _, source := range datasets.LoadDataSources() {
		if source.Identifier != "gb-dft" {
			continue
		}
		for _, dataset := range source.Datasets {
			got := source.Overrides.AliasesFor(dataset.Identifier)["gb-noc-ABLO"]
			want := ""
			if dataset.Format == datasets.DataSetFormatGTFSSchedule {
				want = "gb-noc-TFLO"
			}
			if got != want {
				t.Errorf("%s alias = %q, want %q", dataset.Identifier, got, want)
			}
		}
	}

	stop := &ctdf.Stop{Services: []*ctdf.Service{
		{OperatorRef: "gb-noc-SCCM", ServiceName: "A", SecondaryBrandColour: "#FFFFFF"},
		{OperatorRef: "gb-noc-SCCM", ServiceName: "Other"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Bakerloo"},
	}}
	Transform(stop, 1)

	for i, want := range []string{"#04A387", "#F2A83B", "#994f14"} {
		if got := stop.Services[i].BrandColour; got != want {
			t.Errorf("%s colour = %q, want %q", stop.Services[i].ServiceName, got, want)
		}
	}
	if got := stop.Services[0].SecondaryBrandColour; got != "#FFFFFF" {
		t.Errorf("secondary colour = %q, want existing colour", got)
	}
	if got := stop.Services[2].BrandIcon; got != "/icons/tfl-roundel-underground.svg" {
		t.Errorf("Bakerloo icon = %q, want Underground roundel", got)
	}
}
