package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
)

type realtimeJourneyBounds struct {
	bottomLeftLon float64
	bottomLeftLat float64
	topRightLon   float64
	topRightLat   float64
}

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
	bounds, err := realtimeJourneyBoundsFromBSON(boundsQuery)
	if err != nil {
		return nil, err
	}

	realtimeJourneyIDs, err := redis_client.Client.GeoSearch(ctx, realtimeJourneyLocationGeoIndexKey(), &redis.GeoSearchQuery{
		Longitude: bounds.centerLon(),
		Latitude:  bounds.centerLat(),
		BoxWidth:  bounds.widthKM(),
		BoxHeight: bounds.heightKM(),
		BoxUnit:   "km",
	}).Result()
	if err != nil {
		return nil, err
	}

	realtimeJourneys := make([]*ctdf.RealtimeJourney, 0, len(realtimeJourneyIDs))
	for _, realtimeJourneyID := range realtimeJourneyIDs {
		realtimeJourney, err := FindByIdentifier(ctx, realtimeJourneyID)
		if err != nil || realtimeJourney == nil {
			removeRealtimeJourneyFromLocationIndex(ctx, realtimeJourneyID)
			continue
		}
		if !realtimeJourney.IsActive() {
			removeRealtimeJourneyFromLocationIndex(ctx, realtimeJourneyID)
			continue
		}
		if !bounds.contains(realtimeJourney.VehicleLocation) {
			continue
		}

		realtimeJourneys = append(realtimeJourneys, realtimeJourney)
	}

	return realtimeJourneys, nil
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
	return findByIdentifier(ctx, identifier, false)
}

func findByIdentifier(ctx context.Context, identifier string, hydrateJourney bool) (*ctdf.RealtimeJourney, error) {
	realtimeJourneyResult := redis_client.Client.Get(ctx, realtimeJourneyDetailsKey(identifier))
	if realtimeJourneyResult.Err() != nil {
		return nil, realtimeJourneyResult.Err()
	}

	realtimeJourney, err := decodeStoredRealtimeJourney(ctx, []byte(realtimeJourneyResult.Val()), hydrateJourney)
	if err != nil {
		return nil, err
	}

	ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)

	return realtimeJourney, nil
}

func realtimeJourneyBoundsFromBSON(boundsQuery bson.M) (realtimeJourneyBounds, error) {
	geoWithin, ok := boundsQuery["$geoWithin"].(bson.M)
	if !ok {
		return realtimeJourneyBounds{}, errors.New("bounds must contain $geoWithin")
	}

	box, ok := geoWithin["$box"].(bson.A)
	if !ok || len(box) != 2 {
		return realtimeJourneyBounds{}, errors.New("bounds must contain a $box with two points")
	}

	bottomLeft, ok := coordinatePairFromBSON(box[0])
	if !ok {
		return realtimeJourneyBounds{}, errors.New("bounds bottom-left point is invalid")
	}

	topRight, ok := coordinatePairFromBSON(box[1])
	if !ok {
		return realtimeJourneyBounds{}, errors.New("bounds top-right point is invalid")
	}

	return realtimeJourneyBounds{
		bottomLeftLon: bottomLeft[0],
		bottomLeftLat: bottomLeft[1],
		topRightLon:   topRight[0],
		topRightLat:   topRight[1],
	}, nil
}

func coordinatePairFromBSON(value interface{}) ([2]float64, bool) {
	coordinates, ok := value.(bson.A)
	if !ok || len(coordinates) != 2 {
		return [2]float64{}, false
	}

	lon, ok := floatFromBSON(coordinates[0])
	if !ok {
		return [2]float64{}, false
	}
	lat, ok := floatFromBSON(coordinates[1])
	if !ok {
		return [2]float64{}, false
	}

	return [2]float64{lon, lat}, true
}

func floatFromBSON(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func (bounds realtimeJourneyBounds) centerLon() float64 {
	return (bounds.bottomLeftLon + bounds.topRightLon) / 2
}

func (bounds realtimeJourneyBounds) centerLat() float64 {
	return (bounds.bottomLeftLat + bounds.topRightLat) / 2
}

func (bounds realtimeJourneyBounds) widthKM() float64 {
	width := math.Abs(bounds.topRightLon-bounds.bottomLeftLon) * 111.32 * math.Cos(bounds.centerLat()*math.Pi/180)
	return math.Max(width, 0.001)
}

func (bounds realtimeJourneyBounds) heightKM() float64 {
	height := math.Abs(bounds.topRightLat-bounds.bottomLeftLat) * 111.32
	return math.Max(height, 0.001)
}

func (bounds realtimeJourneyBounds) contains(location ctdf.Location) bool {
	if location.Type != "Point" || len(location.Coordinates) < 2 {
		return false
	}

	lon := location.Coordinates[0]
	lat := location.Coordinates[1]
	minLon := math.Min(bounds.bottomLeftLon, bounds.topRightLon)
	maxLon := math.Max(bounds.bottomLeftLon, bounds.topRightLon)
	minLat := math.Min(bounds.bottomLeftLat, bounds.topRightLat)
	maxLat := math.Max(bounds.bottomLeftLat, bounds.topRightLat)

	return lon >= minLon && lon <= maxLon && lat >= minLat && lat <= maxLat
}

func removeRealtimeJourneyFromLocationIndex(ctx context.Context, identifier string) {
	redis_client.Client.ZRem(ctx, realtimeJourneyLocationGeoIndexKey(), identifier)
}
