package darwin

import (
	"strconv"
	"strings"

	"github.com/travigo/travigo/pkg/ctdf"
)

func buildDarwinRailTrains(scheduleFormation ScheduleFormations) []ctdf.RailTrain {
	trains := make([]ctdf.RailTrain, 0, len(scheduleFormation.Formations))
	for formationIndex, formation := range scheduleFormation.Formations {
		train := ctdf.RailTrain{
			ID:          formation.FID,
			Position:    formationIndex + 1,
			TrainLength: len(formation.Coaches),
			Carriages:   make([]ctdf.RailCarriage, 0, len(formation.Coaches)),
		}

		for _, carriage := range formation.Coaches {
			toilets := []ctdf.RailCarriageToilet{}

			for _, toilet := range carriage.Toilets {
				toilets = append(toilets, ctdf.RailCarriageToilet{
					Type:   toilet.Type,
					Status: toilet.Status,
				})
			}
			train.Carriages = append(train.Carriages, ctdf.RailCarriage{
				ID:           carriage.Number,
				SeatingClass: darwinSeatingClass(carriage.SeatingClass),
				Toilets:      toilets,
				Occupancy:    -1,
			})
		}

		trains = append(trains, train)
	}

	return trains
}

func darwinSeatingClass(value string) ctdf.JourneyDetailedRailSeating {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "first", "f", "1":
		return ctdf.JourneyDetailedRailSeatingFirst
	case "standard", "s", "2":
		return ctdf.JourneyDetailedRailSeatingStandard
	case "":
		return ""
	default:
		return ctdf.JourneyDetailedRailSeatingUnknown
	}
}

func applyDarwinFormationLoading(realtimeJourney *ctdf.RealtimeJourney, formationLoading FormationLoading) {
	trainIndex := findDarwinRailTrain(
		realtimeJourney.DetailedRailInformation.Trains,
		formationLoading.FID,
		formationLoading.Loading,
	)
	if trainIndex < 0 {
		realtimeJourney.DetailedRailInformation.Trains = append(realtimeJourney.DetailedRailInformation.Trains, ctdf.RailTrain{
			ID:       formationLoading.FID,
			Position: len(realtimeJourney.DetailedRailInformation.Trains) + 1,
		})
		trainIndex = len(realtimeJourney.DetailedRailInformation.Trains) - 1
	}

	train := &realtimeJourney.DetailedRailInformation.Trains[trainIndex]
	for _, loading := range formationLoading.Loading {
		occupancy, _ := strconv.Atoi(loading.LoadingPercentage)
		carriageFound := false

		for carriageIndex := range train.Carriages {
			if train.Carriages[carriageIndex].ID == loading.CoachNumber {
				train.Carriages[carriageIndex].Occupancy = occupancy
				carriageFound = true
				break
			}
		}

		if !carriageFound {
			train.Carriages = append(train.Carriages, ctdf.RailCarriage{
				ID:        loading.CoachNumber,
				Occupancy: occupancy,
			})
		}
	}
}

func findDarwinRailTrain(trains []ctdf.RailTrain, formationID string, loadings []FormationLoadingLoading) int {
	if formationID != "" {
		for trainIndex := range trains {
			if trains[trainIndex].ID == formationID {
				return trainIndex
			}
		}
	}

	if len(trains) == 1 {
		return 0
	}

	matchingTrain := -1
	for trainIndex, train := range trains {
		if !railTrainContainsAllCoaches(train, loadings) {
			continue
		}
		if matchingTrain >= 0 {
			return -1
		}
		matchingTrain = trainIndex
	}

	return matchingTrain
}

func railTrainContainsAllCoaches(train ctdf.RailTrain, loadings []FormationLoadingLoading) bool {
	if len(loadings) == 0 {
		return false
	}

	coachIDs := make(map[string]struct{}, len(train.Carriages))
	for _, carriage := range train.Carriages {
		coachIDs[carriage.ID] = struct{}{}
	}
	for _, loading := range loadings {
		if _, found := coachIDs[loading.CoachNumber]; !found {
			return false
		}
	}

	return true
}
