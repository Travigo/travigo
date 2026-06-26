package darwin

import (
	"fmt"
	"strconv"

	"github.com/travigo/travigo/pkg/ctdf"
)

func darwinRailCarriageID(formationID string, coachNumber string, includeFormationID bool) string {
	if includeFormationID && formationID != "" {
		return fmt.Sprintf("%s:%s", formationID, coachNumber)
	}
	return coachNumber
}

func buildDarwinRailCarriages(scheduleFormation ScheduleFormations) []ctdf.RailCarriage {
	realtimeCarriages := []ctdf.RailCarriage{}
	multipleFormations := len(scheduleFormation.Formations) > 1

	for _, formation := range scheduleFormation.Formations {
		for _, carriage := range formation.Coaches {
			toilets := []ctdf.RailCarriageToilet{}

			for _, toilet := range carriage.Toilets {
				toilets = append(toilets, ctdf.RailCarriageToilet{
					Type:   toilet.Type,
					Status: toilet.Status,
				})
			}
			realtimeCarriages = append(realtimeCarriages, ctdf.RailCarriage{
				ID:        darwinRailCarriageID(formation.FID, carriage.Number, multipleFormations),
				Class:     carriage.Class,
				Toilets:   toilets,
				Occupancy: -1,
			})
		}
	}

	return realtimeCarriages
}

func applyDarwinFormationLoading(realtimeJourney *ctdf.RealtimeJourney, formationLoading FormationLoading) {
	for _, loading := range formationLoading.Loading {
		occupancy, _ := strconv.Atoi(loading.LoadingPercentage)
		loadingCarriageID := darwinRailCarriageID(formationLoading.FID, loading.CoachNumber, formationLoading.FID != "")
		carriageFound := false

		for carriageIndex := range realtimeJourney.DetailedRailInformation.Carriages {
			if realtimeJourney.DetailedRailInformation.Carriages[carriageIndex].ID == loadingCarriageID ||
				realtimeJourney.DetailedRailInformation.Carriages[carriageIndex].ID == loading.CoachNumber {
				realtimeJourney.DetailedRailInformation.Carriages[carriageIndex].Occupancy = occupancy
				carriageFound = true
				break
			}
		}

		if !carriageFound {
			realtimeJourney.DetailedRailInformation.Carriages = append(realtimeJourney.DetailedRailInformation.Carriages, ctdf.RailCarriage{
				ID:        loadingCarriageID,
				Occupancy: occupancy,
			})
		}
	}
}
