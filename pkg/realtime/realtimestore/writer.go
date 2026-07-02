package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

type storedVehicleLocation struct {
	Location ctdf.Location `json:"location"`
	Bearing  float64       `json:"bearing"`
}

func realtimeJourneyDetailsKey(identifier string) string {
	return fmt.Sprintf("realtime-journey:details/%s", identifier)
}

func realtimeJourneyRailDetailedKey(detailType string, identifier string) string {
	return fmt.Sprintf("realtime-journey:raildetailed:%s/%s", detailType, identifier)
}

func realtimeJourneyLocationKey(identifier string) string {
	return fmt.Sprintf("realtime-journey:location/%s", identifier)
}

func realtimeJourneyLocationGeoIndexKey() string {
	return "realtime-journey:location:index"
}

func realtimeJourneyLocationDescriptionKey(identifier string) string {
	return fmt.Sprintf("realtime-journey:locationdescription/%s", identifier)
}

func realtimeJourneyMappingKey(mappingType string, identifier string) string {
	return fmt.Sprintf("realtime-journey:mapping:%s:%s", mappingType, identifier)
}

func tflDepartureBoardStopKey(stopID string) string {
	return fmt.Sprintf("realtime-journeys:tfl-stop:%s", stopID)
}

func tflDepartureBoardIndexedStopsKey(identifier string) string {
	return fmt.Sprintf("realtime-journey:tfl-indexed-stops/%s", identifier)
}

func SaveRealtimeJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) error {
	if realtimeJourney == nil {
		return errors.New("realtime journey is required")
	}

	storedRealtimeJourney := storedRealtimeJourneyFromCTDF(realtimeJourney)
	realtimeJourneyJSON, err := json.Marshal(storedRealtimeJourney)
	if err != nil {
		return err
	}

	timeoutDuration := time.Duration(realtimeJourney.TimeoutDurationMinutes) * time.Minute

	err = redis_client.Client.Set(
		ctx,
		realtimeJourneyDetailsKey(realtimeJourney.PrimaryIdentifier),
		realtimeJourneyJSON,
		timeoutDuration,
	).Err()

	if err != nil {
		return err
	}

	return SaveRealtimeJourneyMappings(ctx, realtimeJourney)
}

func SaveRealtimeJourneyMappings(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) error {
	if realtimeJourney == nil {
		return errors.New("realtime journey is required")
	}

	timeoutDuration := realtimeJourneyTTL(ctx, realtimeJourney.PrimaryIdentifier)
	if realtimeJourney.Journey != nil && realtimeJourney.Journey.PrimaryIdentifier != "" {
		if err := redis_client.Client.Set(ctx, realtimeJourneyMappingKey("travigo-journeyid", realtimeJourney.Journey.PrimaryIdentifier), realtimeJourney.PrimaryIdentifier, timeoutDuration).Err(); err != nil {
			return err
		}
	}

	for mappingType, identifier := range realtimeJourney.OtherIdentifiers {
		if err := redis_client.Client.Set(ctx, realtimeJourneyMappingKey(mappingType, identifier), realtimeJourney.PrimaryIdentifier, timeoutDuration).Err(); err != nil {
			return err
		}
	}

	return nil
}

func UpdateRailDetailedAllocation(ctx context.Context, identifier string, detailedRailInformation ctdf.JourneyDetailedRail) error {
	if len(detailedRailInformation.Carriages) == 0 {
		return redis_client.Client.Del(ctx, realtimeJourneyRailDetailedKey("allocation", identifier)).Err()
	}

	for carriageIndex := range detailedRailInformation.Carriages {
		detailedRailInformation.Carriages[carriageIndex].Occupancy = -1
	}

	detailedRailInformationJSON, err := json.Marshal(detailedRailInformation)
	if err != nil {
		return err
	}

	return redis_client.Client.Set(ctx, realtimeJourneyRailDetailedKey("allocation", identifier), detailedRailInformationJSON, realtimeJourneyTTL(ctx, identifier)).Err()
}

func UpdateRailDetailedLoading(ctx context.Context, identifier string, detailedRailInformation ctdf.JourneyDetailedRail) error {
	loadingInformation := ctdf.JourneyDetailedRail{}
	for _, carriage := range detailedRailInformation.Carriages {
		if carriage.Occupancy < 0 {
			continue
		}

		loadingInformation.Carriages = append(loadingInformation.Carriages, ctdf.RailCarriage{
			ID:        carriage.ID,
			Occupancy: carriage.Occupancy,
		})
	}

	if len(loadingInformation.Carriages) == 0 {
		return redis_client.Client.Del(ctx, realtimeJourneyRailDetailedKey("loading", identifier)).Err()
	}

	loadingInformationJSON, err := json.Marshal(loadingInformation)
	if err != nil {
		return err
	}

	return redis_client.Client.Set(ctx, realtimeJourneyRailDetailedKey("loading", identifier), loadingInformationJSON, realtimeJourneyTTL(ctx, identifier)).Err()
}

func IndexTFLDepartureBoardJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) error {
	if realtimeJourney == nil {
		return errors.New("realtime journey is required")
	}

	indexedStopsKey := tflDepartureBoardIndexedStopsKey(realtimeJourney.PrimaryIdentifier)
	previousStopIDs, err := redis_client.Client.SMembers(ctx, indexedStopsKey).Result()
	if err != nil {
		return err
	}
	for _, stopID := range previousStopIDs {
		if err := redis_client.Client.ZRem(ctx, tflDepartureBoardStopKey(stopID), realtimeJourney.PrimaryIdentifier).Err(); err != nil {
			return err
		}
	}
	if len(previousStopIDs) > 0 {
		if err := redis_client.Client.Del(ctx, indexedStopsKey).Err(); err != nil {
			return err
		}
	}

	cleanupBefore := strconv.FormatInt(time.Now().Add(-30*time.Second).Unix(), 10)
	currentStopIDs := make([]interface{}, 0, len(realtimeJourney.Stops))

	for stopID, stop := range realtimeJourney.Stops {
		if stop == nil || stop.TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture || stop.ArrivalTime.IsZero() {
			continue
		}

		key := tflDepartureBoardStopKey(stopID)
		if err := redis_client.Client.ZRemRangeByScore(ctx, key, "-inf", cleanupBefore).Err(); err != nil {
			return err
		}

		if err := redis_client.Client.ZAdd(ctx, key, redis.Z{
			Score:  float64(stop.ArrivalTime.Unix()),
			Member: realtimeJourney.PrimaryIdentifier,
		}).Err(); err != nil {
			return err
		}

		if err := redis_client.Client.Expire(ctx, key, time.Hour).Err(); err != nil {
			return err
		}

		currentStopIDs = append(currentStopIDs, stopID)
	}

	if len(currentStopIDs) > 0 {
		if err := redis_client.Client.SAdd(ctx, indexedStopsKey, currentStopIDs...).Err(); err != nil {
			return err
		}
		if err := redis_client.Client.Expire(ctx, indexedStopsKey, time.Hour).Err(); err != nil {
			return err
		}
	}

	return nil
}

func UpdateLocationDescription(ctx context.Context, identifier string, description string) error {
	return redis_client.Client.Set(ctx, realtimeJourneyLocationDescriptionKey(identifier), description, realtimeJourneyTTL(ctx, identifier)).Err()
}

func UpdateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64) error {
	locationJSON, err := json.Marshal(storedVehicleLocation{
		Location: location,
		Bearing:  bearing,
	})
	if err != nil {
		return err
	}

	if err := redis_client.Client.Set(ctx, realtimeJourneyLocationKey(identifier), locationJSON, realtimeJourneyTTL(ctx, identifier)).Err(); err != nil {
		return err
	}

	if location.Type != "Point" || len(location.Coordinates) < 2 {
		return redis_client.Client.ZRem(ctx, realtimeJourneyLocationGeoIndexKey(), identifier).Err()
	}

	return redis_client.Client.GeoAdd(ctx, realtimeJourneyLocationGeoIndexKey(), &redis.GeoLocation{
		Name:      identifier,
		Longitude: location.Coordinates[0],
		Latitude:  location.Coordinates[1],
	}).Err()
}

func realtimeJourneyTTL(ctx context.Context, identifier string) time.Duration {
	const defaultTTL = 10 * time.Minute

	result := redis_client.Client.Get(ctx, realtimeJourneyDetailsKey(identifier))
	if result.Err() != nil {
		return defaultTTL
	}

	var stored struct {
		TimeoutDurationMinutes int `json:"to"`
	}
	if err := json.Unmarshal([]byte(result.Val()), &stored); err == nil && stored.TimeoutDurationMinutes > 0 {
		return time.Duration(stored.TimeoutDurationMinutes) * time.Minute
	}

	var legacy struct {
		TimeoutDurationMinutes int
	}
	if err := json.Unmarshal([]byte(result.Val()), &legacy); err == nil && legacy.TimeoutDurationMinutes > 0 {
		return time.Duration(legacy.TimeoutDurationMinutes) * time.Minute
	}

	return defaultTTL
}
