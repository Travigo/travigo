package manager

import (
	"testing"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func TestFilterImportReportOnlyIncludesSupportedObjects(t *testing.T) {
	report := filterImportReport(datasets.DataImportReport{
		ImportedStops:           1,
		ImportedStopGroups:      2,
		ImportedServices:        3,
		ImportedJourneys:        4,
		ImportedOperators:       5,
		ImportedOperationGroups: 6,
	}, datasets.SupportedObjects{
		StopGroups:     true,
		Services:       true,
		Journeys:       true,
		OperatorGroups: true,
	})

	if report.ImportedStops != 0 || report.ImportedStopGroups != 2 || report.ImportedServices != 3 || report.ImportedJourneys != 4 || report.ImportedOperators != 0 || report.ImportedOperationGroups != 6 {
		t.Fatalf("unexpected filtered report: %+v", report)
	}
}
