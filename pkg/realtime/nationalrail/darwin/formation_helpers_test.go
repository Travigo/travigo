package darwin

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestBuildDarwinRailCarriagesIncludesAllFormations(t *testing.T) {
	carriages := buildDarwinRailCarriages(ScheduleFormations{
		Formations: []Formation{
			{
				FID: "front",
				Coaches: []FormationCoach{
					{Number: "A", Class: "first"},
				},
			},
			{
				FID: "rear",
				Coaches: []FormationCoach{
					{Number: "A", Class: "standard"},
				},
			},
		},
	})

	if len(carriages) != 2 {
		t.Fatalf("expected carriages from both formations, got %d", len(carriages))
	}
	if carriages[0].ID != "front:A" || carriages[1].ID != "rear:A" {
		t.Fatalf("expected composite carriage IDs for multiple formations, got %s and %s", carriages[0].ID, carriages[1].ID)
	}
}

func TestApplyDarwinFormationLoadingUpdatesExistingCarriage(t *testing.T) {
	realtimeJourney := &ctdf.RealtimeJourney{
		DetailedRailInformation: ctdf.RealtimeJourneyDetailedRail{
			Carriages: []ctdf.RailCarriage{
				{ID: "front:A", Occupancy: -1},
			},
		},
	}

	applyDarwinFormationLoading(realtimeJourney, FormationLoading{
		FID: "front",
		Loading: []FormationLoadingLoading{
			{
				CoachNumber:       "A",
				LoadingPercentage: "42",
			},
		},
	})

	if len(realtimeJourney.DetailedRailInformation.Carriages) != 1 {
		t.Fatalf("expected existing carriage to be updated, got %d carriages", len(realtimeJourney.DetailedRailInformation.Carriages))
	}
	if realtimeJourney.DetailedRailInformation.Carriages[0].Occupancy != 42 {
		t.Fatalf("expected existing carriage occupancy to be updated, got %d", realtimeJourney.DetailedRailInformation.Carriages[0].Occupancy)
	}
}
