package realtimestore

import (
	"context"
	"strings"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

type RealtimeJourneyStats struct {
	Current              int64
	LocationWithTrack    int64
	LocationWithoutTrack int64
	ExternalProvided     int64

	NotActivelyTracked int64

	TransportTypes map[ctdf.TransportType]int
	Features       map[string]int
	Datasets       map[string]int
	Providers      map[string]int
	Countries      map[string]int
}

// TODO MOVE TO REDIS
func GetRealtimeJourneys() RealtimeJourneyStats {
	// realtimeJourneysCollection := collectionOrDefault(nil)

	var numberActiveRealtimeJourneys int64
	var numberActiveRealtimeJourneysWithTrack int64
	var numberActiveRealtimeJourneysWithoutTrack int64
	var numberActiveRealtimeJourneysExternal int64
	var numberActiveRealtimeJourneysNotActivelyTracked int64
	transportTypes := map[ctdf.TransportType]int{}
	features := map[string]int{}
	datasources := map[string]int{}
	providers := map[string]int{}

	ctx := context.Background()

	iter := redis_client.Client.Scan(ctx, 0, realtimeJourneyDetailsKey("*"), 0).Iterator()

	for iter.Next(ctx) {
		realtimeJourney, err := findByIdentifier(ctx, realtimeJourneyIdentifierFromDetailsKey(iter.Val()), false)

		if err != nil || realtimeJourney == nil {
			continue
		}

		if realtimeJourney.IsActive() {
			if realtimeJourney.ActivelyTracked {
				numberActiveRealtimeJourneys += 1

				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityLocationWithTrack {
					numberActiveRealtimeJourneysWithTrack += 1
				}
				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityLocationWithoutTrack {
					numberActiveRealtimeJourneysWithoutTrack += 1
				}
				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityExternalProvided {
					numberActiveRealtimeJourneysExternal += 1
				}

				if realtimeJourney.Service != nil {
					transportTypes[realtimeJourney.Service.TransportType] += 1
				}

				datasources[realtimeJourney.DataSource.DatasetID] += 1
				providers[realtimeJourney.DataSource.ProviderID] += 1

				// Features
				if realtimeJourney.Occupancy.OccupancyAvailable {
					features["Occupancy"] += 1

					if realtimeJourney.Occupancy.ActualValues {
						features["OccupancyActualValues"] += 1
					}
				}
				if len(realtimeJourney.DetailedRailInformation.Carriages) > 0 {
					features["DetailedRailInformationCarriages"] += 1
				}
			} else {
				numberActiveRealtimeJourneysNotActivelyTracked += 1
			}
		}
	}

	return RealtimeJourneyStats{
		Current:              numberActiveRealtimeJourneys,
		LocationWithTrack:    numberActiveRealtimeJourneysWithTrack,
		LocationWithoutTrack: numberActiveRealtimeJourneysWithoutTrack,
		ExternalProvided:     numberActiveRealtimeJourneysExternal,
		TransportTypes:       transportTypes,
		Features:             features,
		Datasets:             datasources,
		Providers:            providers,
		Countries:            countCountries(datasources),
		NotActivelyTracked:   numberActiveRealtimeJourneysNotActivelyTracked,
	}
}

func countCountries(datasources map[string]int) map[string]int {
	countries := map[string]int{}

	for datasource, count := range datasources {
		// Making a big assumption here
		datasourceSplit := strings.Split(datasource, "-")
		country := datasourceSplit[0]

		if country == "travigo" {
			continue
		}

		countries[country] += count
	}

	return countries
}
