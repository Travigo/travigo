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

func realtimeJourneyLocationKey(identifier string) string {
	return fmt.Sprintf("realtime-journey:location/%s", identifier)
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

func SaveRealtimeJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) error {
	realtimeJourneyJSON, err := json.Marshal(realtimeJourney)
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

	// Store all the other identifiers in a mapping to the primary identifier for easy lookup
	redis_client.Client.Set(ctx, realtimeJourneyMappingKey("travigo-journeyid", realtimeJourney.Journey.PrimaryIdentifier), realtimeJourney.PrimaryIdentifier, timeoutDuration).Err()

	for mappingType, identifier := range realtimeJourney.OtherIdentifiers {
		err = redis_client.Client.Set(ctx, realtimeJourneyMappingKey(mappingType, identifier), realtimeJourney.PrimaryIdentifier, timeoutDuration).Err()

		if err != nil {
			return err
		}
	}

	return nil
}

func IndexTFLDepartureBoardJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) error {
	if realtimeJourney == nil {
		return errors.New("realtime journey is required")
	}

	cleanupBefore := strconv.FormatInt(time.Now().Add(-30*time.Second).Unix(), 10)

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

		if err := redis_client.Client.Expire(ctx, key, 12*time.Hour).Err(); err != nil {
			return err
		}
	}

	return nil
}

func UpdateLocationDescription(ctx context.Context, identifier string, description string) error {
	return redis_client.Client.Set(ctx, realtimeJourneyLocationDescriptionKey(identifier), description, 12*time.Hour).Err()
}

func UpdateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64) error {
	locationJSON, err := json.Marshal(storedVehicleLocation{
		Location: location,
		Bearing:  bearing,
	})
	if err != nil {
		return err
	}

	err = redis_client.Client.Set(ctx, realtimeJourneyLocationKey(identifier), locationJSON, 12*time.Hour).Err()
	return err
}
