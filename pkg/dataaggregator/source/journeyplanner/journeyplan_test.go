package journeyplanner

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
)

func TestJourneyPlanConfigDefaultsBoundSearchFanout(t *testing.T) {
	config := journeyPlanConfig(query.JourneyPlan{})

	if config.count != defaultJourneyPlanCount {
		t.Fatalf("expected default count %d, got %d", defaultJourneyPlanCount, config.count)
	}
	if config.departureBoardCount != defaultJourneyPlanDepartureBoardCount {
		t.Fatalf("expected default departure board count %d, got %d", defaultJourneyPlanDepartureBoardCount, config.departureBoardCount)
	}
	if config.originDepartureBoardCount != defaultJourneyPlanOriginDepartureBoardCount {
		t.Fatalf("expected default origin departure board count %d, got %d", defaultJourneyPlanOriginDepartureBoardCount, config.originDepartureBoardCount)
	}
	if config.maxExpandedLabels != defaultJourneyPlanMaxExpandedLabels {
		t.Fatalf("expected default max expanded labels %d, got %d", defaultJourneyPlanMaxExpandedLabels, config.maxExpandedLabels)
	}
	if config.maxSearchDuration != defaultJourneyPlanMaxSearchDuration {
		t.Fatalf("expected default max search duration %s, got %s", defaultJourneyPlanMaxSearchDuration, config.maxSearchDuration)
	}
}

func TestJourneyPlanConfigAllowsSearchBudgetOverrides(t *testing.T) {
	config := journeyPlanConfig(query.JourneyPlan{
		Count:                      7,
		DepartureBoardCountPerStop: 20,
		OriginDepartureBoardCount:  50,
		MaxExpandedLabels:          80,
		MaxSearchDuration:          3 * time.Second,
	})

	if config.count != 7 {
		t.Fatalf("expected count override 7, got %d", config.count)
	}
	if config.departureBoardCount != 20 {
		t.Fatalf("expected departure board count override 20, got %d", config.departureBoardCount)
	}
	if config.originDepartureBoardCount != 50 {
		t.Fatalf("expected origin departure board count override 50, got %d", config.originDepartureBoardCount)
	}
	if config.maxExpandedLabels != 80 {
		t.Fatalf("expected max expanded labels override 80, got %d", config.maxExpandedLabels)
	}
	if config.maxSearchDuration != 3*time.Second {
		t.Fatalf("expected max search duration override 3s, got %s", config.maxSearchDuration)
	}
}

func TestLimitDepartureBoardSortsAndLimits(t *testing.T) {
	start := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	board := []*ctdf.DepartureBoard{
		{Time: start.Add(20 * time.Minute)},
		nil,
		{Time: start.Add(5 * time.Minute)},
		{Time: start.Add(10 * time.Minute)},
	}

	limited := limitDepartureBoard(board, 2)

	if len(limited) != 2 {
		t.Fatalf("expected 2 departures, got %d", len(limited))
	}
	if !limited[0].Time.Equal(start.Add(5 * time.Minute)) {
		t.Fatalf("expected first departure at +5m, got %s", limited[0].Time)
	}
	if !limited[1].Time.Equal(start.Add(10 * time.Minute)) {
		t.Fatalf("expected second departure at +10m, got %s", limited[1].Time)
	}
}
