package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
)

func FindByMapping(ctx context.Context, mappingType string, identifier string) (*ctdf.RealtimeJourney, error) {
	redisJourneyMapping, err := GetRealtimeJourneyMappingFromRedis(ctx, mappingType, identifier)
	if err != nil {
		return nil, err
	}
	return FindByIdentifier(ctx, redisJourneyMapping)
}

func FindCurrentForJourney(ctx context.Context, journeyID string) (*ctdf.RealtimeJourney, error) {
	// Try redis first
	redisJourneyMapping, err := GetRealtimeJourneyMappingFromRedis(ctx, "travigo-journeyid", journeyID)

	if err != nil {
		return nil, err
	}

	return FindByIdentifier(ctx, redisJourneyMapping)
}

func FindCurrentForJourneyIDs(ctx context.Context, journeyIDs []string) (map[string]*ctdf.RealtimeJourney, error) {
	realtimeJourneysByJourneyID := map[string]*ctdf.RealtimeJourney{}
	if len(journeyIDs) == 0 {
		return realtimeJourneysByJourneyID, nil
	}

	realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()
	for _, journeyID := range journeyIDs {
		if realtimeJourney, err := FindCurrentForJourney(ctx, journeyID); err == nil && realtimeJourney != nil && realtimeJourney.ModificationDateTime.After(realtimeActiveCutoffDate) {
			realtimeJourneysByJourneyID[journeyID] = realtimeJourney
			continue
		}
	}

	return realtimeJourneysByJourneyID, nil
}

func FindCurrentByJourneyRefs(ctx context.Context, journeyRefs []string) (*ctdf.RealtimeJourney, error) {
	potentials, err := FindCurrentForJourneyIDs(ctx, journeyRefs)

	if err != nil {
		return nil, err
	}

	for _, journeyRef := range journeyRefs {
		if realtimeJourney := potentials[journeyRef]; realtimeJourney != nil {
			return realtimeJourney, nil
		}
	}

	return nil, nil
}

func FindTFLDepartureBoardJourneys(ctx context.Context, stopIDs []string, from time.Time) ([]ctdf.RealtimeJourney, error) {
	if len(stopIDs) == 0 {
		return nil, nil
	}

	minScore := strconv.FormatInt(from.Unix(), 10)
	seenRealtimeJourneyIDs := map[string]struct{}{}
	var realtimeJourneyIDs []string

	for _, stopID := range stopIDs {
		key := tflDepartureBoardStopKey(stopID)
		if err := redis_client.Client.ZRemRangeByScore(ctx, key, "-inf", minScore).Err(); err != nil {
			return nil, err
		}

		ids, err := redis_client.Client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
			Min: minScore,
			Max: "+inf",
		}).Result()
		if err != nil {
			return nil, err
		}

		for _, id := range ids {
			if _, seen := seenRealtimeJourneyIDs[id]; seen {
				continue
			}

			seenRealtimeJourneyIDs[id] = struct{}{}
			realtimeJourneyIDs = append(realtimeJourneyIDs, id)
		}
	}

	realtimeJourneys := make([]ctdf.RealtimeJourney, 0, len(realtimeJourneyIDs))
	for _, realtimeJourneyID := range realtimeJourneyIDs {
		realtimeJourney, err := FindByIdentifier(ctx, realtimeJourneyID)
		if err != nil {
			continue
		}

		realtimeJourneys = append(realtimeJourneys, *realtimeJourney)
	}

	return realtimeJourneys, nil
}

func FindActiveWithinBounds(ctx context.Context, boundsQuery bson.M) ([]*ctdf.RealtimeJourney, error) {
	// realtimeActiveCutoffDate := ctdf.GetShortActiveRealtimeJourneyCutOffDate()

	// cursor, err := collectionOrDefault(nil).Find(ctx,
	// 	bson.M{
	// 		"$and": bson.A{
	// 			bson.M{"vehiclelocation.coordinates": boundsQuery},
	// 			bson.M{"modificationdatetime": bson.M{"$gt": realtimeActiveCutoffDate}},
	// 		},
	// 	},
	// )
	// if err != nil {
	// 	return nil, err
	// }
	// defer cursor.Close(ctx)

	// var realtimeJourneys []*ctdf.RealtimeJourney
	// for cursor.Next(ctx) {
	// 	realtimeJourney := &ctdf.RealtimeJourney{}
	// 	if err := cursor.Decode(realtimeJourney); err != nil {
	// 		return nil, err
	// 	}

	// 	ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)
	// 	realtimeJourneys = append(realtimeJourneys, realtimeJourney)
	// }

	// return realtimeJourneys, cursor.Err()

	return nil, errors.ErrUnsupported
}

func ApplyRealtimeJourneyOverlays(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	ApplyLocation(ctx, realtimeJourney)
	ApplyLocationDescription(ctx, realtimeJourney)
}

func ApplyLocation(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	if realtimeJourney == nil {
		return
	}

	location, bearing, err := GetLocation(ctx, realtimeJourney.PrimaryIdentifier)
	if err == nil {
		realtimeJourney.VehicleLocation = location
		realtimeJourney.VehicleBearing = bearing
	}
}

func ApplyLocationDescription(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	if realtimeJourney == nil {
		return
	}

	locationDescription, err := GetLocationDescription(ctx, realtimeJourney.PrimaryIdentifier)
	if err == nil {
		realtimeJourney.VehicleLocationDescription = locationDescription
	}
}

func GetLocationDescription(ctx context.Context, identifier string) (string, error) {
	description := redis_client.Client.Get(ctx, realtimeJourneyLocationDescriptionKey(identifier))
	if description.Err() != nil {
		return "", description.Err()
	}
	return description.Val(), nil
}

func GetLocation(ctx context.Context, identifier string) (ctdf.Location, float64, error) {
	locationResult := redis_client.Client.Get(ctx, realtimeJourneyLocationKey(identifier))
	if locationResult.Err() != nil {
		return ctdf.Location{}, 0, locationResult.Err()
	}

	var vehicleLocation storedVehicleLocation
	if err := json.Unmarshal([]byte(locationResult.Val()), &vehicleLocation); err != nil {
		return ctdf.Location{}, 0, err
	}

	return vehicleLocation.Location, vehicleLocation.Bearing, nil
}

// Temporary name it FromRedis to avoid confusion with the mongo version of this function
func GetRealtimeJourneyMappingFromRedis(ctx context.Context, mappingType string, identifier string) (string, error) {
	mappingResult := redis_client.Client.Get(ctx, realtimeJourneyMappingKey(mappingType, identifier))
	if mappingResult.Err() != nil {
		return "", mappingResult.Err()
	}

	return mappingResult.Val(), nil
}

func FindByIdentifier(ctx context.Context, identifier string) (*ctdf.RealtimeJourney, error) {
	realtimeJourneyResult := redis_client.Client.Get(ctx, realtimeJourneyDetailsKey(identifier))
	if realtimeJourneyResult.Err() != nil {
		return nil, realtimeJourneyResult.Err()
	}

	realtimeJourney := &ctdf.RealtimeJourney{}
	if err := json.Unmarshal([]byte(realtimeJourneyResult.Val()), realtimeJourney); err != nil {
		return nil, err
	}

	ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)

	return realtimeJourney, nil
}
