package realtimestore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

type storedVehicleLocation struct {
	Location ctdf.Location `json:"location"`
	Bearing  float64       `json:"bearing"`
}

func realtimeJourneyDetailsKey(identifier string) string {
	return fmt.Sprintf("realtime-journey:%s/details", identifier)
}

func realtimeJourneyMappingKey(mappingType string, identifier string) string {
	return fmt.Sprintf("realtime-journey-mapping:%s:%s", mappingType, identifier)
}

func SaveRealtimeJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) error {
	if realtimeJourney == nil {
		return ErrEmptyIdentifier
	}

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

func UpdateLocationDescription(ctx context.Context, identifier string, description string) error {
	return redis_client.Client.Set(ctx, fmt.Sprintf("realtime-journey:%s/locationdescription", identifier), description, 12*time.Hour).Err()
}

func UpdateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64) error {
	locationJSON, err := json.Marshal(storedVehicleLocation{
		Location: location,
		Bearing:  bearing,
	})
	if err != nil {
		return err
	}

	err = redis_client.Client.Set(ctx, fmt.Sprintf("realtime-journey:%s/location", identifier), locationJSON, 12*time.Hour).Err()
	return err
}
