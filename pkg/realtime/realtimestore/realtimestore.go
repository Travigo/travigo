package realtimestore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

const (
	ActiveJourneyTTL   = 4 * time.Hour
	JourneyLookupTTL   = 48 * time.Hour
	JourneySnapshotTTL = 48 * time.Hour

	ambiguousLookupValue = "AMBIGUOUS"

	activeJourneyPrefix   = "rt:journey:"
	journeySnapshotPrefix = "rt:journey-snapshot:"
	activeJourneyGeoKey   = "rt:geo"
	activeJourneyZSetKey  = "rt:active"
)

var ErrNotFound = errors.New("not found")

func keyPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}

	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func ActiveJourneyKey(identifier string) string {
	return activeJourneyPrefix + keyPart(identifier)
}

func JourneySnapshotKey(journeyID string) string {
	return journeySnapshotPrefix + keyPart(journeyID)
}

func GTFSJourneyLookupKey(linkedDataset string, tripID string) string {
	if linkedDataset == "" || tripID == "" {
		return ""
	}

	return fmt.Sprintf("lookup:journey:gtfs:%s:%s", keyPart(linkedDataset), keyPart(tripID))
}

func GTFSAnyJourneyLookupKey(tripID string) string {
	if tripID == "" {
		return ""
	}

	return fmt.Sprintf("lookup:journey:gtfs-any:%s", keyPart(tripID))
}

func SIRIJourneyRefLookupKey(operatorRef string, lineRef string, vehicleJourneyRef string) string {
	if operatorRef == "" || lineRef == "" || vehicleJourneyRef == "" {
		return ""
	}

	return fmt.Sprintf("lookup:journey:siri:journeyref:%s:%s:%s", keyPart(operatorRef), keyPart(lineRef), keyPart(vehicleJourneyRef))
}

func SIRIBlockLookupKey(operatorRef string, lineRef string, blockRef string) string {
	if operatorRef == "" || lineRef == "" || blockRef == "" {
		return ""
	}

	return fmt.Sprintf("lookup:journey:siri:block:%s:%s:%s", keyPart(operatorRef), keyPart(lineRef), keyPart(blockRef))
}

func SIRIOriginDestinationTimeLookupKey(operatorRef string, lineRef string, originRef string, destinationRef string, departureHHMM string) string {
	if operatorRef == "" || lineRef == "" || originRef == "" || destinationRef == "" || departureHHMM == "" {
		return ""
	}

	return fmt.Sprintf(
		"lookup:journey:siri:odt:%s:%s:%s:%s:%s",
		keyPart(operatorRef),
		keyPart(lineRef),
		keyPart(originRef),
		keyPart(destinationRef),
		keyPart(departureHHMM),
	)
}

func SetJourneyLookup(ctx context.Context, key string, journeyID string) {
	if redis_client.Client == nil || key == "" || journeyID == "" {
		return
	}

	created, err := redis_client.Client.SetNX(ctx, key, journeyID, JourneyLookupTTL).Result()
	if err != nil {
		return
	}
	if created {
		return
	}

	currentValue, err := redis_client.Client.Get(ctx, key).Result()
	if err == nil && currentValue != "" && currentValue != journeyID {
		redis_client.Client.Set(ctx, key, ambiguousLookupValue, JourneyLookupTTL)
		return
	}

	if err != nil && err != redis.Nil {
		return
	}

	redis_client.Client.Set(ctx, key, journeyID, JourneyLookupTTL)
}

func GetJourneyLookup(ctx context.Context, keys ...string) (string, error) {
	if redis_client.Client == nil {
		return "", ErrNotFound
	}

	for _, key := range keys {
		if key == "" {
			continue
		}

		value, err := redis_client.Client.Get(ctx, key).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			continue
		}
		if value == "" || value == ambiguousLookupValue {
			continue
		}

		return value, nil
	}

	return "", ErrNotFound
}

func SetRealtimeJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	if redis_client.Client == nil || realtimeJourney == nil || realtimeJourney.PrimaryIdentifier == "" {
		return
	}

	realtimeJourneyJSON, err := json.Marshal(realtimeJourney)
	if err != nil {
		return
	}

	identifier := realtimeJourney.PrimaryIdentifier
	redis_client.Client.Set(ctx, ActiveJourneyKey(identifier), realtimeJourneyJSON, ActiveJourneyTTL)

	// Keep writing the legacy direct key while the API/read paths migrate.
	redis_client.Client.Set(ctx, identifier, realtimeJourneyJSON, ActiveJourneyTTL)
	redis_client.Client.ZAdd(ctx, activeJourneyZSetKey, redis.Z{
		Score:  float64(realtimeJourney.ModificationDateTime.Unix()),
		Member: identifier,
	})

	if realtimeJourney.VehicleLocation.Type == "Point" && len(realtimeJourney.VehicleLocation.Coordinates) == 2 {
		redis_client.Client.GeoAdd(ctx, activeJourneyGeoKey, &redis.GeoLocation{
			Name:      identifier,
			Longitude: realtimeJourney.VehicleLocation.Coordinates[0],
			Latitude:  realtimeJourney.VehicleLocation.Coordinates[1],
		})
	}
}

func GetRealtimeJourney(ctx context.Context, identifier string) (*ctdf.RealtimeJourney, error) {
	if redis_client.Client == nil || identifier == "" {
		return nil, ErrNotFound
	}

	for _, key := range []string{ActiveJourneyKey(identifier), identifier} {
		realtimeJourneyJSON, err := redis_client.Client.Get(ctx, key).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			continue
		}

		var realtimeJourney ctdf.RealtimeJourney
		if err := json.Unmarshal([]byte(realtimeJourneyJSON), &realtimeJourney); err != nil {
			return nil, err
		}

		return &realtimeJourney, nil
	}

	return nil, ErrNotFound
}

func SetJourneySnapshot(ctx context.Context, journey *ctdf.Journey) {
	if redis_client.Client == nil || journey == nil || journey.PrimaryIdentifier == "" {
		return
	}

	journeyJSON, err := json.Marshal(journey)
	if err != nil {
		return
	}

	redis_client.Client.Set(ctx, JourneySnapshotKey(journey.PrimaryIdentifier), journeyJSON, JourneySnapshotTTL)
}

func GetJourneySnapshot(ctx context.Context, journeyID string) (*ctdf.Journey, error) {
	if redis_client.Client == nil || journeyID == "" {
		return nil, ErrNotFound
	}

	journeyJSON, err := redis_client.Client.Get(ctx, JourneySnapshotKey(journeyID)).Result()
	if err == redis.Nil {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var journey ctdf.Journey
	if err := json.Unmarshal([]byte(journeyJSON), &journey); err != nil {
		return nil, err
	}

	return &journey, nil
}
