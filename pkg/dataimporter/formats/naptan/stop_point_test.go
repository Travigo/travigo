package naptan

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestStopPointToCTDFTreatsActiveStatusCaseInsensitively(t *testing.T) {
	stopPoint := testStopPoint("RLY")
	stopPoint.Status = "Active"

	stop := stopPoint.ToCTDF()

	if !stop.Active {
		t.Fatal("expected stop to be active when NaPTAN status is Active")
	}
}

func TestStopPointToCTDFTreatsRPLAsRail(t *testing.T) {
	stopPoint := testStopPoint("RPL")

	stop := stopPoint.ToCTDF()

	if len(stop.TransportTypes) != 1 || stop.TransportTypes[0] != ctdf.TransportTypeRail {
		t.Fatalf("expected RPL stop to be Rail, got %#v", stop.TransportTypes)
	}
}

func testStopPoint(stopType string) *StopPoint {
	return &StopPoint{
		CreationDateTime:     "2026-01-01T00:00:00",
		ModificationDateTime: "2026-01-01T00:00:00",
		Status:               "active",
		AtcoCode:             "9100TEST",
		Descriptor: &StopPointDescriptor{
			CommonName: "Test Rail Station",
		},
		Location: &Location{
			Longitude: -0.1,
			Latitude:  51.5,
		},
		StopClassification: StopClassification{
			StopType: stopType,
		},
	}
}
