package localdepartureboard

import (
	"encoding/json"
	"testing"

	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
)

func TestDepartureBoardCachePreservesBlockEstimationMetadata(t *testing.T) {
	journeys := []*ctdf.Journey{{
		PrimaryIdentifier: "journey",
		OtherIdentifiers:  map[string]string{"BlockNumber": "block-1"},
		DataSource:        &ctdf.DataSourceReference{DatasetID: "dataset-1"},
	}}

	reduced, err := sheriff.Marshal(&sheriff.Options{Groups: []string{"departureboard-cache"}}, journeys)
	if err != nil {
		t.Fatalf("reduce cache value: %s", err)
	}
	encoded, err := json.Marshal(reduced)
	if err != nil {
		t.Fatalf("encode cache value: %s", err)
	}
	var decoded []*ctdf.Journey
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode cache value: %s", err)
	}

	if len(decoded) != 1 || decoded[0].OtherIdentifiers["BlockNumber"] != "block-1" {
		t.Fatalf("block metadata was not preserved: %#v", decoded)
	}
	if decoded[0].DataSource == nil || decoded[0].DataSource.DatasetID != "dataset-1" {
		t.Fatalf("dataset metadata was not preserved: %#v", decoded[0].DataSource)
	}
}
