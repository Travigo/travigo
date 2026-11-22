package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

// VehicleEnrichmentStream records enrichment jobs alongside their journey creation.
const VehicleEnrichmentStream = "realtime-journey:vehicle-enrichment"

type vehicleEnrichmentState struct {
	CreationDateTime    time.Time       `json:"cdt"`
	VehicleRegistration string          `json:"vrn"`
	DetailedBus         json.RawMessage `json:"bus"`
}

func (journey vehicleEnrichmentState) needsEnrichment(createdAt time.Time, registration string) bool {
	return journey.CreationDateTime.Equal(createdAt) && journey.VehicleRegistration == registration && len(journey.DetailedBus) == 0
}

// VehicleEnrichmentPending checks that the queued job still applies to the live journey.
func VehicleEnrichmentPending(ctx context.Context, identifier string, createdAt time.Time, registration string) (bool, error) {
	raw, err := redis_client.Client.Get(ctx, realtimeJourneyDetailsKey(identifier)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var journey vehicleEnrichmentState
	if err := json.Unmarshal(raw, &journey); err != nil {
		return false, err
	}
	return journey.needsEnrichment(createdAt, registration), nil
}

var saveBusDetailsScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
	return 0
end
redis.call('SET', KEYS[1], ARGV[2], 'KEEPTTL')
return 1
`)

// UpdateBusDetails attaches enrichment from the creation event to the same
// tracked run, preserving intervening prediction updates and the Redis expiry.
func UpdateBusDetails(ctx context.Context, identifier string, createdAt time.Time, details ctdf.JourneyDetailedBus) error {
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	key := realtimeJourneyDetailsKey(identifier)
	for range 4 {
		raw, err := redis_client.Client.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}

		var journey vehicleEnrichmentState
		if err := json.Unmarshal(raw, &journey); err != nil {
			return err
		}
		if !journey.needsEnrichment(createdAt, details.RegistrationNumber) {
			return nil
		}

		// Keep the exact stored values for timetable, stops and other fields.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		fields["bus"] = data
		updated, err := json.Marshal(fields)
		if err != nil {
			return err
		}

		applied, err := saveBusDetailsScript.Run(ctx, redis_client.Client, []string{key}, raw, updated).Bool()
		if err != nil || applied {
			return err
		}
	}
	return ErrRealtimeJourneyConflict
}
