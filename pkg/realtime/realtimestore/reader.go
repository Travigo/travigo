package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
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
	start := time.Now()
	realtimeJourneysByJourneyID := map[string]*ctdf.RealtimeJourney{}
	if len(journeyIDs) == 0 {
		return realtimeJourneysByJourneyID, nil
	}

	uniqueJourneyIDs := make([]string, 0, len(journeyIDs))
	seenJourneyIDs := make(map[string]struct{}, len(journeyIDs))
	for _, journeyID := range journeyIDs {
		if journeyID == "" {
			continue
		}
		if _, seen := seenJourneyIDs[journeyID]; seen {
			continue
		}

		seenJourneyIDs[journeyID] = struct{}{}
		uniqueJourneyIDs = append(uniqueJourneyIDs, journeyID)
	}
	if len(uniqueJourneyIDs) == 0 {
		return realtimeJourneysByJourneyID, nil
	}

	mappingKeys := make([]string, 0, len(uniqueJourneyIDs))
	for _, journeyID := range uniqueJourneyIDs {
		mappingKeys = append(mappingKeys, realtimeJourneyMappingKey("travigo-journeyid", journeyID))
	}

	mappingStart := time.Now()
	mappingValues, err := redis_client.Client.MGet(ctx, mappingKeys...).Result()
	mappingDuration := time.Since(mappingStart)
	if err != nil {
		return realtimeJourneysByJourneyID, err
	}

	journeyIDsByRealtimeJourneyID := map[string][]string{}
	realtimeJourneyIDs := make([]string, 0, len(mappingValues))
	seenRealtimeJourneyIDs := map[string]struct{}{}
	mappingHits := 0
	mappingMisses := 0
	for i, mappingValue := range mappingValues {
		realtimeJourneyID, ok := redisString(mappingValue)
		if !ok || realtimeJourneyID == "" {
			mappingMisses++
			continue
		}

		mappingHits++
		journeyIDsByRealtimeJourneyID[realtimeJourneyID] = append(journeyIDsByRealtimeJourneyID[realtimeJourneyID], uniqueJourneyIDs[i])
		if _, seen := seenRealtimeJourneyIDs[realtimeJourneyID]; seen {
			continue
		}

		seenRealtimeJourneyIDs[realtimeJourneyID] = struct{}{}
		realtimeJourneyIDs = append(realtimeJourneyIDs, realtimeJourneyID)
	}
	if len(realtimeJourneyIDs) == 0 {
		log.Debug().
			Int("journey_ids", len(journeyIDs)).
			Int("unique_journey_ids", len(uniqueJourneyIDs)).
			Int("mapping_hits", mappingHits).
			Int("mapping_misses", mappingMisses).
			Dur("mapping_duration", mappingDuration).
			Dur("total_duration", time.Since(start)).
			Msg("Realtime journey batch lookup complete")
		return realtimeJourneysByJourneyID, nil
	}

	detailKeys := make([]string, 0, len(realtimeJourneyIDs))
	for _, realtimeJourneyID := range realtimeJourneyIDs {
		detailKeys = append(detailKeys, realtimeJourneyDetailsKey(realtimeJourneyID))
	}

	detailsStart := time.Now()
	detailValues, err := redis_client.Client.MGet(ctx, detailKeys...).Result()
	detailsDuration := time.Since(detailsStart)
	if err != nil {
		return realtimeJourneysByJourneyID, err
	}

	realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()
	decodeStart := time.Now()
	detailHits := 0
	detailMisses := 0
	decodeErrors := 0
	inactiveRealtimeJourneys := 0
	for i, detailValue := range detailValues {
		realtimeJourneyJSON, ok := redisString(detailValue)
		if !ok || realtimeJourneyJSON == "" {
			detailMisses++
			continue
		}

		detailHits++
		realtimeJourney, err := decodeStoredRealtimeJourney(ctx, []byte(realtimeJourneyJSON), false)
		if err != nil || realtimeJourney == nil {
			decodeErrors++
			continue
		}

		if !realtimeJourney.ModificationDateTime.After(realtimeActiveCutoffDate) {
			inactiveRealtimeJourneys++
			continue
		}

		for _, journeyID := range journeyIDsByRealtimeJourneyID[realtimeJourneyIDs[i]] {
			realtimeJourneysByJourneyID[journeyID] = realtimeJourney
		}
	}
	decodeDuration := time.Since(decodeStart)

	log.Debug().
		Int("journey_ids", len(journeyIDs)).
		Int("unique_journey_ids", len(uniqueJourneyIDs)).
		Int("mapping_hits", mappingHits).
		Int("mapping_misses", mappingMisses).
		Int("unique_realtime_journey_ids", len(realtimeJourneyIDs)).
		Int("detail_hits", detailHits).
		Int("detail_misses", detailMisses).
		Int("decode_errors", decodeErrors).
		Int("inactive_realtime_journeys", inactiveRealtimeJourneys).
		Int("active_realtime_journeys", len(realtimeJourneysByJourneyID)).
		Dur("mapping_duration", mappingDuration).
		Dur("details_duration", detailsDuration).
		Dur("decode_duration", decodeDuration).
		Dur("total_duration", time.Since(start)).
		Msg("Realtime journey batch lookup complete")

	return realtimeJourneysByJourneyID, nil
}

func redisString(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case []byte:
		return string(v), true
	default:
		return "", false
	}
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
	ApplyRailDetailed(ctx, realtimeJourney)
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

func ApplyRailDetailed(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	if realtimeJourney == nil {
		return
	}

	detailedRailInformation, err := GetRailDetailed(ctx, realtimeJourney.PrimaryIdentifier)
	if err == nil {
		realtimeJourney.DetailedRailInformation = detailedRailInformation
	}
}

func GetRailDetailed(ctx context.Context, identifier string) (ctdf.JourneyDetailedRail, error) {
	allocation, allocationErr := getRailDetailedPart(ctx, "allocation", identifier)
	loading, loadingErr := getRailDetailedPart(ctx, "loading", identifier)

	if allocationErr != nil && loadingErr != nil {
		return ctdf.JourneyDetailedRail{}, allocationErr
	}
	if allocationErr != nil {
		return loading, nil
	}
	if loadingErr != nil {
		return allocation, nil
	}

	return mergeRailDetailed(allocation, loading), nil
}

func getRailDetailedPart(ctx context.Context, detailType string, identifier string) (ctdf.JourneyDetailedRail, error) {
	detailedRailResult := redis_client.Client.Get(ctx, realtimeJourneyRailDetailedKey(detailType, identifier))
	if detailedRailResult.Err() != nil {
		return ctdf.JourneyDetailedRail{}, detailedRailResult.Err()
	}

	var detailedRailInformation ctdf.JourneyDetailedRail
	if err := json.Unmarshal([]byte(detailedRailResult.Val()), &detailedRailInformation); err != nil {
		return ctdf.JourneyDetailedRail{}, err
	}

	return detailedRailInformation, nil
}

func mergeRailDetailed(allocation ctdf.JourneyDetailedRail, loading ctdf.JourneyDetailedRail) ctdf.JourneyDetailedRail {
	merged := allocation

	carriageIndexes := map[string]int{}
	for carriageIndex, carriage := range merged.Carriages {
		carriageIndexes[carriage.ID] = carriageIndex
	}

	for _, loadingCarriage := range loading.Carriages {
		if carriageIndex, ok := carriageIndexes[loadingCarriage.ID]; ok {
			merged.Carriages[carriageIndex].Occupancy = loadingCarriage.Occupancy
			continue
		}

		merged.Carriages = append(merged.Carriages, loadingCarriage)
	}

	return merged
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
