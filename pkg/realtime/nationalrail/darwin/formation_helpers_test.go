package darwin

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestBuildDarwinRailTrainsPreservesFormationBoundaries(t *testing.T) {
	trains := buildDarwinRailTrains(ScheduleFormations{
		Formations: []Formation{
			{
				FID: "front",
				Coaches: []FormationCoach{
					{Number: "A", SeatingClass: "first"},
				},
			},
			{
				FID: "rear",
				Coaches: []FormationCoach{
					{Number: "A", SeatingClass: "standard"},
				},
			},
		},
	})

	if len(trains) != 2 {
		t.Fatalf("expected both formations, got %d", len(trains))
	}
	if trains[0].ID != "front" || trains[1].ID != "rear" {
		t.Fatalf("expected formation IDs to identify trains, got %s and %s", trains[0].ID, trains[1].ID)
	}
	if trains[0].Carriages[0].ID != "A" || trains[1].Carriages[0].ID != "A" {
		t.Fatalf("expected coach IDs to be scoped by train, got %+v", trains)
	}
	if trains[0].Carriages[0].SeatingClass != ctdf.JourneyDetailedRailSeatingFirst ||
		trains[1].Carriages[0].SeatingClass != ctdf.JourneyDetailedRailSeatingStandard {
		t.Fatalf("expected Darwin coach class to map to seating class, got %+v", trains)
	}
	if trains[0].Carriages[0].CarriageType != "" || trains[1].Carriages[0].CarriageType != "" {
		t.Fatalf("expected Darwin seating class not to populate carriage type, got %+v", trains)
	}
}

func TestApplyDarwinFormationLoadingUpdatesOnlyMatchingTrain(t *testing.T) {
	realtimeJourney := &ctdf.RealtimeJourney{
		DetailedRailInformation: ctdf.JourneyDetailedRail{
			Trains: []ctdf.RailTrain{
				{ID: "front", Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: -1}}},
				{ID: "rear", Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: -1}}},
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

	if realtimeJourney.DetailedRailInformation.Trains[0].Carriages[0].Occupancy != 42 {
		t.Fatalf("expected matching train carriage occupancy to be updated")
	}
	if realtimeJourney.DetailedRailInformation.Trains[1].Carriages[0].Occupancy != -1 {
		t.Fatalf("expected duplicate coach ID on other train to remain unchanged")
	}
}

func TestApplyDarwinFormationLoadingWithoutFIDUsesUniqueCoachSet(t *testing.T) {
	realtimeJourney := &ctdf.RealtimeJourney{
		DetailedRailInformation: ctdf.JourneyDetailedRail{
			Trains: []ctdf.RailTrain{
				{ID: "front", Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: -1}}},
				{ID: "rear", Carriages: []ctdf.RailCarriage{{ID: "B", Occupancy: -1}}},
			},
		},
	}

	applyDarwinFormationLoading(realtimeJourney, FormationLoading{
		Loading: []FormationLoadingLoading{{CoachNumber: "B", LoadingPercentage: "27"}},
	})

	if realtimeJourney.DetailedRailInformation.Trains[0].Carriages[0].Occupancy != -1 {
		t.Fatalf("expected non-matching train to remain unchanged")
	}
	if realtimeJourney.DetailedRailInformation.Trains[1].Carriages[0].Occupancy != 27 {
		t.Fatalf("expected unique coach set to identify rear train")
	}
}
