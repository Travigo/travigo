package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/railtd"
	"github.com/travigo/travigo/pkg/redis_client"
)

const railPositionMaxAge = 10 * time.Minute

// RecordRailObservation updates the last mapped position and map index together.
func RecordRailObservation(ctx context.Context, journey *ctdf.RealtimeJourney, observation RailObservation, point *ctdf.Location) (bool, error) {
	if point == nil {
		return false, nil
	}
	if observation.ObservedAt.IsZero() || !railtd.ValidPoint(point) {
		return false, errors.New("invalid rail observation")
	}
	if time.Since(observation.ObservedAt) > railPositionMaxAge {
		return false, nil
	}
	id := journey.PrimaryIdentifier
	key := realtimeJourneyLocationKey(id)
	for attempt := 0; attempt < 5; attempt++ {
		changed := false
		err := redis_client.Client.Watch(ctx, func(tx *redis.Tx) error {
			var previous storedVehicleLocation
			raw, err := tx.Get(ctx, key).Bytes()
			if err == nil {
				if err := json.Unmarshal(raw, &previous); err != nil {
					return err
				}
			} else if !errors.Is(err, redis.Nil) {
				return err
			}

			if previous.Position != nil && !observation.ObservedAt.After(previous.Position.ObservedAt) {
				return nil
			}
			changed = true
			previous.Location = *point
			previous.Position = &observation
			raw, err = json.Marshal(previous)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, raw, realtimeJourneyExpiration(journey.TimeoutDurationMinutes))
				pipe.GeoAdd(ctx, realtimeJourneyLocationGeoIndexKey(), &redis.GeoLocation{Name: id, Longitude: point.Coordinates[0], Latitude: point.Coordinates[1]})
				return nil
			})
			return err
		}, key)
		if !errors.Is(err, redis.TxFailedErr) {
			return changed && err == nil, err
		}
	}
	return false, redis.TxFailedErr
}

// RailObservation describes the observation behind VehicleLocation.
type RailObservation struct {
	ObservedAt     time.Time `json:"observedAt"`
	Source         string    `json:"source"`
	Confidence     string    `json:"confidence"`
	SignallingArea string    `json:"signallingArea,omitempty"`
	BerthID        string    `json:"berthId,omitempty"`
}
