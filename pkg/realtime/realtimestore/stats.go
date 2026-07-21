package realtimestore

import (
	"context"
	"encoding/json"
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

func GetRealtimeJourneys() RealtimeJourneyStats {
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

	journeyIdentifiers := make([]string, 0)
	iter := redis_client.Client.Scan(ctx, 0, realtimeJourneyDetailsKey("*"), 0).Iterator()
	for iter.Next(ctx) {
		journeyIdentifiers = append(journeyIdentifiers, realtimeJourneyIdentifierFromDetailsKey(iter.Val()))
	}

	// Read the detail records in batches. FindByIdentifier performs one GET for
	// the detail record and three more GETs for overlays, which makes this
	// calculation increasingly expensive as the realtime store grows.
	for start := 0; start < len(journeyIdentifiers); start += realtimeJourneyReadBatchSize {
		end := start + realtimeJourneyReadBatchSize
		if end > len(journeyIdentifiers) {
			end = len(journeyIdentifiers)
		}

		identifiers := journeyIdentifiers[start:end]
		detailKeys := make([]string, len(identifiers))
		for i, identifier := range identifiers {
			detailKeys[i] = realtimeJourneyDetailsKey(identifier)
		}
		detailValues, err := redis_client.Client.MGet(ctx, detailKeys...).Result()
		if err != nil {
			continue
		}

		activeJourneys := make([]*ctdf.RealtimeJourney, 0, len(identifiers))
		activeIdentifiers := make([]string, 0, len(identifiers))
		for i, detailValue := range detailValues {
			realtimeJourneyJSON, ok := redisString(detailValue)
			if !ok || realtimeJourneyJSON == "" {
				continue
			}

			realtimeJourney, err := decodeStoredRealtimeJourney(ctx, []byte(realtimeJourneyJSON), false)
			if err != nil || realtimeJourney == nil || !realtimeJourney.IsActive() {
				continue
			}
			activeJourneys = append(activeJourneys, realtimeJourney)
			activeIdentifiers = append(activeIdentifiers, identifiers[i])
		}

		applyRealtimeJourneyRailOverlays(ctx, activeIdentifiers, activeJourneys)

		for _, realtimeJourney := range activeJourneys {
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
				if railDetailedCarriageCount(realtimeJourney.DetailedRailInformation) > 0 {
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

func applyRealtimeJourneyRailOverlays(ctx context.Context, identifiers []string, journeys []*ctdf.RealtimeJourney) {
	if len(identifiers) == 0 {
		return
	}

	allocationKeys := make([]string, len(identifiers))
	loadingKeys := make([]string, len(identifiers))
	for i, identifier := range identifiers {
		allocationKeys[i] = realtimeJourneyRailDetailedKey("allocation", identifier)
		loadingKeys[i] = realtimeJourneyRailDetailedKey("loading", identifier)
	}

	allocations, allocationErr := redis_client.Client.MGet(ctx, allocationKeys...).Result()
	loadings, loadingErr := redis_client.Client.MGet(ctx, loadingKeys...).Result()
	if allocationErr != nil && loadingErr != nil {
		return
	}

	for i, journey := range journeys {
		var allocation, loading ctdf.JourneyDetailedRail
		hasAllocation := allocationErr == nil && decodeRailOverlay(allocations[i], &allocation)
		hasLoading := loadingErr == nil && decodeRailOverlay(loadings[i], &loading)
		if hasAllocation {
			allocation = enrichRailDetailedAllocation(allocation)
		}

		switch {
		case hasAllocation && hasLoading:
			journey.DetailedRailInformation = mergeRailDetailed(allocation, loading)
		case hasAllocation:
			journey.DetailedRailInformation = allocation
		case hasLoading:
			journey.DetailedRailInformation = loading
		}
	}
}

func decodeRailOverlay(value interface{}, rail *ctdf.JourneyDetailedRail) bool {
	valueString, ok := redisString(value)
	if !ok || valueString == "" {
		return false
	}
	return json.Unmarshal([]byte(valueString), rail) == nil
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
