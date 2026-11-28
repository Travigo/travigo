package gtfs

import (
	"testing"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func TestImportFareDataBestEffortSkipsV2RulesWithoutProducts(t *testing.T) {
	schedule := &Schedule{fileMap: map[string]string{"fare_leg_rules.txt": ""}}
	summary := schedule.importFareDataBestEffort(datasets.DataSet{Identifier: "test-gtfs"}, nil)
	if !summary.ImportFailed {
		t.Fatal("ImportFailed = false, want malformed fare tables to be recorded as a skipped fare import")
	}
	if summary.Present || summary.Products != 0 || summary.Offers != 0 || summary.Rules != 0 {
		t.Fatalf("failed fare import summary = %#v, want no published fare data", summary)
	}
}
