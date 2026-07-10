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

const (
	defaultRealtimeJourneyTTL = 10 * time.Minute
	// LINX sends formation/allocation data early in the morning for every train
	// running that day, so it must remain available before each journey appears.
	railAllocationTTL = 24 * time.Hour
)

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

	previousRealtimeJourney, previousRealtimeJourneyKnown := previousRealtimeJourneyForEvents(ctx, realtimeJourney.PrimaryIdentifier)

	storedRealtimeJourney := storedRealtimeJourneyFromCTDF(realtimeJourney)
	realtimeJourneyJSON, err := json.Marshal(storedRealtimeJourney)
	if err != nil {
		return err
	}

	timeoutDuration := realtimeJourneyExpiration(realtimeJourney.TimeoutDurationMinutes)

	_, err = redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, realtimeJourneyDetailsKey(realtimeJourney.PrimaryIdentifier), realtimeJourneyJSON, timeoutDuration)
		queueRealtimeJourneyMappings(ctx, pipe, realtimeJourney, timeoutDuration)
		return nil
	})
	if err != nil {
		return err
	}

	publishRealtimeJourneyEvents(previousRealtimeJourney, realtimeJourney, previousRealtimeJourneyKnown)

	return nil
}

func SaveRealtimeJourneyMappings(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) error {
	if realtimeJourney == nil {
		return errors.New("realtime journey is required")
	}

	timeoutDuration := realtimeJourneyExpiration(realtimeJourney.TimeoutDurationMinutes)
	_, err := redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		queueRealtimeJourneyMappings(ctx, pipe, realtimeJourney, timeoutDuration)
		return nil
	})
	return err
}

func queueRealtimeJourneyMappings(ctx context.Context, pipe redis.Pipeliner, realtimeJourney *ctdf.RealtimeJourney, timeoutDuration time.Duration) {
	if realtimeJourney.Journey != nil && realtimeJourney.Journey.PrimaryIdentifier != "" {
		pipe.Set(ctx, realtimeJourneyMappingKey("travigo-journeyid", realtimeJourney.Journey.PrimaryIdentifier), realtimeJourney.PrimaryIdentifier, timeoutDuration)
	}

	for mappingType, identifier := range realtimeJourney.OtherIdentifiers {
		pipe.Set(ctx, realtimeJourneyMappingKey(mappingType, identifier), realtimeJourney.PrimaryIdentifier, timeoutDuration)
	}
}

func UpdateRailDetailedAllocation(ctx context.Context, identifier string, detailedRailInformation ctdf.JourneyDetailedRail) error {
	if railDetailedCarriageCount(detailedRailInformation) == 0 {
		return redis_client.Client.Del(ctx, realtimeJourneyRailDetailedKey("allocation", identifier)).Err()
	}

	for trainIndex := range detailedRailInformation.Trains {
		for carriageIndex := range detailedRailInformation.Trains[trainIndex].Carriages {
			detailedRailInformation.Trains[trainIndex].Carriages[carriageIndex].Occupancy = -1
			if detailedRailInformation.Trains[trainIndex].Carriages[carriageIndex].VehicleRole == "" {
				detailedRailInformation.Trains[trainIndex].Carriages[carriageIndex].VehicleRole = ctdf.RailCarriageVehicleRoleUnknown
			}
		}
	}

	detailedRailInformationJSON, err := json.Marshal(detailedRailInformation)
	if err != nil {
		return err
	}

	return redis_client.Client.Set(ctx, realtimeJourneyRailDetailedKey("allocation", identifier), detailedRailInformationJSON, railAllocationTTL).Err()
}

func UpdateRailDetailedLoading(ctx context.Context, identifier string, detailedRailInformation ctdf.JourneyDetailedRail) error {
	loadingInformation := ctdf.JourneyDetailedRail{}
	for _, train := range detailedRailInformation.Trains {
		loadingTrain := ctdf.RailTrain{
			ID:       train.ID,
			Position: train.Position,
		}

		for _, carriage := range train.Carriages {
			if carriage.Occupancy < 0 {
				continue
			}

			vehicleRole := carriage.VehicleRole
			if vehicleRole == "" {
				vehicleRole = ctdf.RailCarriageVehicleRolePassenger
			}

			loadingTrain.Carriages = append(loadingTrain.Carriages, ctdf.RailCarriage{
				ID:          carriage.ID,
				VehicleID:   carriage.VehicleID,
				VehicleRole: vehicleRole,
				Occupancy:   carriage.Occupancy,
			})
		}

		if len(loadingTrain.Carriages) > 0 {
			loadingInformation.Trains = append(loadingInformation.Trains, loadingTrain)
		}
	}

	if len(loadingInformation.Trains) == 0 {
		return redis_client.Client.Del(ctx, realtimeJourneyRailDetailedKey("loading", identifier)).Err()
	}

	loadingInformationJSON, err := json.Marshal(loadingInformation)
	if err != nil {
		return err
	}

	return redis_client.Client.Set(
		ctx,
		realtimeJourneyRailDetailedKey("loading", identifier),
		loadingInformationJSON,
		realtimeJourneyTTL(ctx, identifier),
	).Err()
}

func railDetailedCarriageCount(detailedRailInformation ctdf.JourneyDetailedRail) int {
	count := 0
	for _, train := range detailedRailInformation.Trains {
		count += len(train.Carriages)
	}
	return count
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
	cleanupBefore := strconv.FormatInt(time.Now().Add(-30*time.Second).Unix(), 10)
	currentStopIDs := make([]interface{}, 0, len(realtimeJourney.Stops))
	type indexedStop struct {
		identifier string
		arrival    time.Time
	}
	indexedStops := make([]indexedStop, 0, len(realtimeJourney.Stops))

	for stopID, stop := range realtimeJourney.Stops {
		if stop == nil || stop.TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture || stop.ArrivalTime.IsZero() {
			continue
		}

		indexedStops = append(indexedStops, indexedStop{identifier: stopID, arrival: stop.ArrivalTime})
		currentStopIDs = append(currentStopIDs, stopID)
	}

	_, err = redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, stopID := range previousStopIDs {
			pipe.ZRem(ctx, tflDepartureBoardStopKey(stopID), realtimeJourney.PrimaryIdentifier)
		}
		if len(previousStopIDs) > 0 {
			pipe.Del(ctx, indexedStopsKey)
		}

		for _, stop := range indexedStops {
			key := tflDepartureBoardStopKey(stop.identifier)
			pipe.ZRemRangeByScore(ctx, key, "-inf", cleanupBefore)
			pipe.ZAdd(ctx, key, redis.Z{
				Score:  float64(stop.arrival.Unix()),
				Member: realtimeJourney.PrimaryIdentifier,
			})
			pipe.Expire(ctx, key, time.Hour)
		}

		if len(currentStopIDs) > 0 {
			pipe.SAdd(ctx, indexedStopsKey, currentStopIDs...)
			pipe.Expire(ctx, indexedStopsKey, time.Hour)
		}
		return nil
	})
	return err
}

func UpdateLocationDescription(ctx context.Context, identifier string, description string) error {
	return redis_client.Client.Set(ctx, realtimeJourneyLocationDescriptionKey(identifier), description, realtimeJourneyTTL(ctx, identifier)).Err()
}

func UpdateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64) error {
	return updateLocation(ctx, identifier, location, bearing, realtimeJourneyTTL(ctx, identifier))
}

func UpdateLocationForRealtimeJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney, location ctdf.Location, bearing float64) error {
	if realtimeJourney == nil {
		return errors.New("realtime journey is required")
	}

	return updateLocation(ctx, realtimeJourney.PrimaryIdentifier, location, bearing, realtimeJourneyExpiration(realtimeJourney.TimeoutDurationMinutes))
}

func updateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64, timeoutDuration time.Duration) error {
	locationJSON, err := json.Marshal(storedVehicleLocation{
		Location: location,
		Bearing:  bearing,
	})
	if err != nil {
		return err
	}

	_, err = redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, realtimeJourneyLocationKey(identifier), locationJSON, timeoutDuration)
		if location.Type != "Point" || len(location.Coordinates) < 2 {
			pipe.ZRem(ctx, realtimeJourneyLocationGeoIndexKey(), identifier)
			return nil
		}

		pipe.GeoAdd(ctx, realtimeJourneyLocationGeoIndexKey(), &redis.GeoLocation{
			Name:      identifier,
			Longitude: location.Coordinates[0],
			Latitude:  location.Coordinates[1],
		})
		return nil
	})
	return err
}

func realtimeJourneyTTL(ctx context.Context, identifier string) time.Duration {
	result := redis_client.Client.Get(ctx, realtimeJourneyDetailsKey(identifier))
	if result.Err() != nil {
		return defaultRealtimeJourneyTTL
	}

	var stored struct {
		TimeoutDurationMinutes int `json:"to"`
	}
	if err := json.Unmarshal([]byte(result.Val()), &stored); err == nil && stored.TimeoutDurationMinutes > 0 {
		return realtimeJourneyExpiration(stored.TimeoutDurationMinutes)
	}

	var legacy struct {
		TimeoutDurationMinutes int
	}
	if err := json.Unmarshal([]byte(result.Val()), &legacy); err == nil && legacy.TimeoutDurationMinutes > 0 {
		return realtimeJourneyExpiration(legacy.TimeoutDurationMinutes)
	}

	return defaultRealtimeJourneyTTL
}

func realtimeJourneyExpiration(timeoutDurationMinutes int) time.Duration {
	if timeoutDurationMinutes <= 0 {
		return defaultRealtimeJourneyTTL
	}
	return time.Duration(timeoutDurationMinutes) * time.Minute
}
