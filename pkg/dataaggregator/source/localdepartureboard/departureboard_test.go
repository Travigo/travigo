package localdepartureboard

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/departuregraph"
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

type localGraphLoader struct {
	journey *ctdf.Journey
	loads   int
}

func (l *localGraphLoader) LoadStopJourneys(_ context.Context, _ []string, _ time.Time) ([]*ctdf.Journey, error) {
	l.loads++
	return []*ctdf.Journey{l.journey}, nil
}

func (l *localGraphLoader) ScanJourneys(_ context.Context, _ []time.Time, _ string, visit func(*ctdf.Journey, string) error) error {
	return visit(l.journey, l.journey.PrimaryIdentifier)
}

func TestDepartureBoardCandidateLookupUsesLazyGraph(t *testing.T) {
	serviceTime := time.Date(0, time.January, 1, 8, 30, 0, 0, time.UTC)
	journey := &ctdf.Journey{
		PrimaryIdentifier: "journey-graph",
		DepartureTime:     serviceTime,
		Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{
			Type: ctdf.AvailabilityMatchAll,
		}}},
		Path: []*ctdf.JourneyPathItem{{
			OriginStopRef:       "stop-a",
			DestinationStopRef:  "stop-b",
			OriginDepartureTime: serviceTime,
		}},
	}
	loader := &localGraphLoader{journey: journey}
	graph := departuregraph.New(loader, departuregraph.Config{Enabled: true})
	source := Source{DepartureGraph: graph}
	stop := &ctdf.Stop{PrimaryIdentifier: "stop-a"}
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)

	journeys := source.getBoardJourneys(query.DepartureBoard{Stop: stop}, "unused", nil, serviceDate, ctdf.BoardTypeDeparture)
	if len(journeys) != 1 || journeys[0].PrimaryIdentifier != "journey-graph" {
		t.Fatalf("graph journeys = %#v", journeys)
	}
	if loader.loads != 1 {
		t.Fatalf("graph loads = %d, want 1", loader.loads)
	}

	journeys = source.getBoardJourneys(query.DepartureBoard{Stop: stop}, "unused", nil, serviceDate, ctdf.BoardTypeDeparture)
	if len(journeys) != 1 || loader.loads != 1 {
		t.Fatalf("completed graph lookup journeys=%d loads=%d, want 1 and 1", len(journeys), loader.loads)
	}
}

func TestDepartureGraphCandidateLimitOverfetchesAndBoundsResults(t *testing.T) {
	for _, test := range []struct {
		count int
		want  int
	}{
		{count: 0, want: 128},
		{count: 12, want: 128},
		{count: 24, want: 192},
		{count: 3000, want: 20000},
	} {
		if got := departureGraphCandidateLimit(test.count); got != test.want {
			t.Errorf("candidate limit for count %d = %d, want %d", test.count, got, test.want)
		}
	}
}

func TestBoardStopAliasFallbackIncludesAllBoardIdentifiers(t *testing.T) {
	aliases, requested := boardStopAliasFallback([]string{"station", "platform", "station"})

	want := []string{"station", "platform"}
	if !reflect.DeepEqual(aliases["station"], want) || !reflect.DeepEqual(aliases["platform"], want) {
		t.Fatalf("fallback aliases = %#v, want both board identifiers: %v", aliases, want)
	}
	if len(requested) != 2 {
		t.Fatalf("requested identifiers = %#v, want two unique identifiers", requested)
	}
}
