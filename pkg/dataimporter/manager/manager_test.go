package manager

import (
	"os"
	"testing"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func TestFilterImportReportOnlyIncludesSupportedObjects(t *testing.T) {
	report := filterImportReport(datasets.DataImportReport{
		ImportedStops:           1,
		ImportedStopGroups:      2,
		ImportedServices:        3,
		ImportedJourneys:        4,
		ImportedJourneyTracks:   7,
		ImportedOperators:       5,
		ImportedOperationGroups: 6,
	}, datasets.SupportedObjects{
		StopGroups:     true,
		Services:       true,
		Journeys:       true,
		JourneyTracks:  true,
		OperatorGroups: true,
	})

	if report.ImportedStops != 0 || report.ImportedStopGroups != 2 || report.ImportedServices != 3 || report.ImportedJourneys != 4 || report.ImportedJourneyTracks != 7 || report.ImportedOperators != 0 || report.ImportedOperationGroups != 6 {
		t.Fatalf("unexpected filtered report: %+v", report)
	}
}

func TestTfLRouteTracksIsARegisteredBatchDataset(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)

	dataset, err := GetDataset("gb-tfl-route-tracks")
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Format != datasets.DataSetFormatTfLRouteTracks {
		t.Fatalf("format = %q", dataset.Format)
	}
	if dataset.ImportDestination != "" {
		t.Fatalf("import destination = %q, want normal database import", dataset.ImportDestination)
	}
	if dataset.DatasetSize != "enrichment" || !dataset.SupportedObjects.JourneyTracks {
		t.Fatalf("dataset is not configured as a journey-track enrichment import: %+v", dataset)
	}
}

func TestOSMRailTracksIsARegisteredBatchDataset(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)

	dataset, err := GetDataset("global-openstreetmap-gb-national-rail-tracks")
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Format != datasets.DataSetFormatOSMRailTracks {
		t.Fatalf("format = %q", dataset.Format)
	}
	if dataset.DatasetSize != "enrichment" || !dataset.SupportedObjects.JourneyTracks {
		t.Fatalf("dataset is not configured as a journey-track enrichment import: %+v", dataset)
	}
	if dataset.LinkedDataset != "gb-nationalrail-timetable" {
		t.Fatalf("linked dataset = %q", dataset.LinkedDataset)
	}
}
