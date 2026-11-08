package datasets

import (
	"strings"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestDatasourceOverrides(t *testing.T) {
	t.Chdir("../../..")
	sources := LoadDataSources()
	rules := NewServiceOverrides(sources)

	for _, source := range sources {
		if source.Identifier != "gb-dft" {
			continue
		}
		for _, dataset := range source.Datasets {
			got := source.Overrides.AliasesFor(dataset.Identifier)["gb-noc-ABLO"]
			want := ""
			if dataset.Format == DataSetFormatGTFSSchedule && dataset.Identifier != "gb-dft-bods-gtfs-schedule-north-west" {
				want = "gb-noc-TFLO"
			}
			if got != want {
				t.Errorf("%s alias = %q, want %q", dataset.Identifier, got, want)
			}
		}
	}

	services := []*ctdf.Service{
		{OperatorRef: "gb-noc-SCCM", ServiceName: "A", SecondaryBrandColour: "#FFFFFF"},
		{OperatorRef: "gb-noc-SCCM", ServiceName: "Other"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Bakerloo"},
		{OperatorRef: "gb-noc-SCCM", ServiceName: "Feed colour", BrandColour: "#123456"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "SL5", TransportType: ctdf.TransportTypeBus, BrandColour: "#ABCDEF", DataSource: &ctdf.DataSourceReference{DatasetID: "gb-dft-bods-gtfs-schedule-london"}},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "SL12", TransportType: ctdf.TransportTypeBus, DataSource: &ctdf.DataSourceReference{DatasetID: "gb-dft-bods-gtfs-schedule-london"}},
		{OperatorRef: "gb-noc-METR", ServiceName: "SL7", TransportType: ctdf.TransportTypeBus, DataSource: &ctdf.DataSourceReference{DatasetID: "gb-dft-bods-gtfs-schedule-london"}},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "SL7", TransportType: ctdf.TransportTypeBus, DataSource: &ctdf.DataSourceReference{DatasetID: "gb-dft-bods-gtfs-schedule-london"}},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Central"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Circle"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "District"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Hammersmith & City"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Jubilee"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Metropolitan"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Northern"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Piccadilly"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Victoria"},
		{OperatorRef: "gb-noc-TFLO", ServiceName: "Waterloo & City"},
	}
	for _, service := range services {
		rules.Apply(service)
	}

	for i, want := range []string{"", "", "#B26300", "#123456", "#009FE3", "#E42313", "#C80B62", "#DC241F", "#DC241F", "#FFC80A", "#007D32", "#F589A6", "#838D93", "#9B0058", "#000000", "#0019A8", "#039BE5", "#76D0BD"} {
		if got := services[i].BrandColour; got != want {
			t.Errorf("%s colour = %q, want %q", services[i].ServiceName, got, want)
		}
	}
	if got := services[0].SecondaryBrandColour; got != "#FFFFFF" {
		t.Errorf("secondary colour = %q, want existing colour", got)
	}
	if got := services[2].BrandIcon; got != "/icons/tfl-roundel-underground.svg" {
		t.Errorf("Bakerloo icon = %q, want Underground roundel", got)
	}
}

func TestServiceOverridesKeepPublisherIdentityAndMergeRoutes(t *testing.T) {
	name, description, website := "Brand 42", "Via the station", "https://example.com/42"
	rename := ServiceMetadata{
		Datasets:  []string{"gb-dft-bods-gtfs-schedule-north-west"},
		Operators: []string{"operator"}, Services: []string{"42"},
	}
	rename.Set.ServiceName = &name
	details := rename
	details.Set.ServiceName = nil
	details.Set.Website = &website
	details.Set.Routes = &[]ctdf.Route{
		{Origin: "A", Destination: "B", Description: description},
		{Origin: "B", Destination: "A"},
	}
	rules := NewServiceOverrides([]DataSource{{Overrides: Overrides{ServiceMetadata: []ServiceMetadata{rename, details}}}})
	service := ctdf.Service{
		PrimaryIdentifier: "linked-id", OperatorRef: "operator", ServiceName: "42",
		DataSource: &ctdf.DataSourceReference{DatasetID: "gb-dft-gb-dft-bods-gtfs-schedule-north-west"},
		Routes:     []ctdf.Route{{Origin: "A", Destination: "B"}, {Origin: "A", Destination: "C"}},
	}
	var before, after strings.Builder
	service.GenerateDeterministicID(&before)

	if !rules.Apply(&service) {
		t.Fatal("expected the North West service to match")
	}
	if service.ServiceName != name || service.LineName() != "42" || service.Website != website {
		t.Fatalf("wrong published service: %+v", service)
	}
	if len(service.Routes) != 3 || service.Routes[0].Description != description || service.Routes[1].Destination != "C" {
		t.Fatalf("routes were not merged with the publisher endpoints: %+v", service.Routes)
	}
	service.GenerateDeterministicID(&after)
	if before.String() != after.String() || service.PrimaryIdentifier != "linked-id" ||
		service.DataSource.DatasetID != "gb-dft-gb-dft-bods-gtfs-schedule-north-west" {
		t.Fatal("publishing changed the service identity")
	}
}
