package journeyplanner

import (
	"testing"
	"time"

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
		MaxExpandedLabels:          80,
		MaxSearchDuration:          3 * time.Second,
	})

	if config.count != 7 {
		t.Fatalf("expected count override 7, got %d", config.count)
	}
	if config.departureBoardCount != 20 {
		t.Fatalf("expected departure board count override 20, got %d", config.departureBoardCount)
	}
	if config.maxExpandedLabels != 80 {
		t.Fatalf("expected max expanded labels override 80, got %d", config.maxExpandedLabels)
	}
	if config.maxSearchDuration != 3*time.Second {
		t.Fatalf("expected max search duration override 3s, got %s", config.maxSearchDuration)
	}
}
