package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/railclasses"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
)

type realtimeJourneyBounds struct {
	bottomLeftLon float64
	bottomLeftLat float64
	topRightLon   float64
	topRightLat   float64
}

const realtimeJourneyReadBatchSize = 1000

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

	run, err := FindByIdentifier(ctx, redisJourneyMapping)
	if err != nil || run == nil {
		return run, err
	}
	if run.Journey == nil || run.Journey.PrimaryIdentifier != journeyID {
		return nil, nil
	}
	return run, nil
}

func FindCurrentForJourneyIDs(ctx context.Context, journeyIDs []string) (map[string]*ctdf.RealtimeJourney, error) {
	return findCurrentForJourneyIDs(ctx, journeyIDs, false)
}

// FindBoardRealtimeForJourneyIDs retains unexpired VSTP cancellations for
// board compatibility, even when the last feed update is older than four hours.
func FindBoardRealtimeForJourneyIDs(ctx context.Context, journeyIDs []string) (map[string]*ctdf.RealtimeJourney, error) {
	return findCurrentForJourneyIDs(ctx, journeyIDs, true)
}

func findCurrentForJourneyIDs(ctx context.Context, journeyIDs []string, includeVSTPCancellations bool) (map[string]*ctdf.RealtimeJourney, error) {
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
	// Performance Review [Set K] [PR124] [P2] (boards): Board fallback and block estimation can
	// supply a whole day's journeys. Both mapping MGET and detail MGET below are
	// unbounded, unlike the chunked TfL reader, so one poll can retain a large Redis
	// reply plus every decoded stop map. Chunk the lookup under a shared request
	// budget and select board-relevant candidates/fields before retaining full details.
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

		vstpCancellation := includeVSTPCancellations && realtimeJourney.Cancelled && strings.HasPrefix(realtimeJourney.PrimaryIdentifier, "gb-nationalrail-vstp-cancellation-") && realtimeJourney.TimeoutDurationMinutes > 0 && time.Now().Before(realtimeJourney.ModificationDateTime.Add(time.Duration(realtimeJourney.TimeoutDurationMinutes)*time.Minute))
		if !realtimeJourney.ModificationDateTime.After(realtimeActiveCutoffDate) && !vstpCancellation {
			inactiveRealtimeJourneys++
			continue
		}

		for _, journeyID := range journeyIDsByRealtimeJourneyID[realtimeJourneyIDs[i]] {
			if realtimeJourney.Journey != nil && realtimeJourney.Journey.PrimaryIdentifier == journeyID {
				realtimeJourneysByJourneyID[journeyID] = realtimeJourney
			}
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
	return FindTFLDepartureBoardJourneysBounded(ctx, stopIDs, from, time.Time{}, 0)
}

func FindTFLDepartureBoardJourneysBounded(ctx context.Context, stopIDs []string, from time.Time, until time.Time, perStopLimit int64) ([]ctdf.RealtimeJourney, error) {
	if len(stopIDs) == 0 {
		return nil, nil
	}

	minScore := strconv.FormatInt(from.Unix(), 10)
	maxScore := "+inf"
	if !until.IsZero() {
		maxScore = strconv.FormatInt(until.Unix(), 10)
	}
	seenRealtimeJourneyIDs := map[string]struct{}{}
	var realtimeJourneyIDs []string

	rangeCommands := make([]*redis.StringSliceCmd, 0, len(stopIDs))
	_, err := redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, stopID := range stopIDs {
			key := tflDepartureBoardStopKey(stopID)
			pipe.ZRemRangeByScore(ctx, key, "-inf", minScore)
			rangeQuery := &redis.ZRangeBy{
				Min: minScore,
				Max: maxScore,
			}
			if perStopLimit > 0 {
				// Performance Review [Set G] [PR125] [P2] (boards): The limit is applied before detail
				// hydration/freshness filtering. Future-scored members whose detail keys
				// expired consume every slot and can hide later live journeys. Refill
				// bounded pages after discarding stale members, with race-safe cleanup
				// and a total scan budget across aliases; distinguish exhausted results.
				rangeQuery.Count = perStopLimit
			}
			rangeCommands = append(rangeCommands, pipe.ZRangeByScore(ctx, key, rangeQuery))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, rangeCommand := range rangeCommands {
		ids, err := rangeCommand.Result()
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

	return findStoredRealtimeJourneys(ctx, realtimeJourneyIDs)
}

func findStoredRealtimeJourneys(ctx context.Context, identifiers []string) ([]ctdf.RealtimeJourney, error) {
	journeysByID := make(map[string]*ctdf.RealtimeJourney, len(identifiers))
	for start := 0; start < len(identifiers); start += realtimeJourneyReadBatchSize {
		end := start + realtimeJourneyReadBatchSize
		if end > len(identifiers) {
			end = len(identifiers)
		}

		keys := make([]string, 0, end-start)
		for _, identifier := range identifiers[start:end] {
			keys = append(keys, realtimeJourneyDetailsKey(identifier))
		}
		values, err := redis_client.Client.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, err
		}

		for index, value := range values {
			data, ok := redisString(value)
			if !ok || data == "" {
				continue
			}

			journey, err := decodeStoredRealtimeJourney(ctx, []byte(data), false)
			if err == nil && journey != nil {
				journeysByID[identifiers[start+index]] = journey
			}
		}
	}

	journeys := make([]ctdf.RealtimeJourney, 0, len(journeysByID))
	for _, identifier := range identifiers {
		if journey := journeysByID[identifier]; journey != nil {
			journeys = append(journeys, *journey)
		}
	}
	return journeys, nil
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

	return findActiveWithinBoundsForIDs(ctx, bounds, realtimeJourneyIDs)
}

func findActiveWithinBoundsForIDs(ctx context.Context, bounds realtimeJourneyBounds, realtimeJourneyIDs []string) ([]*ctdf.RealtimeJourney, error) {
	realtimeJourneys := make([]*ctdf.RealtimeJourney, 0, len(realtimeJourneyIDs))
	staleRealtimeJourneyIDs := make([]interface{}, 0)
	for start := 0; start < len(realtimeJourneyIDs); start += realtimeJourneyReadBatchSize {
		end := start + realtimeJourneyReadBatchSize
		if end > len(realtimeJourneyIDs) {
			end = len(realtimeJourneyIDs)
		}

		batchIDs := realtimeJourneyIDs[start:end]
		detailKeys := make([]string, 0, len(batchIDs))
		locationKeys := make([]string, 0, len(batchIDs))
		for _, realtimeJourneyID := range batchIDs {
			detailKeys = append(detailKeys, realtimeJourneyDetailsKey(realtimeJourneyID))
			locationKeys = append(locationKeys, realtimeJourneyLocationKey(realtimeJourneyID))
		}

		// Performance Review [Set Q] [PR126] [P2]: These batched map reads still fetch and decode
		// complete stored runtime records, including paths and predictions, for a small
		// map response. Consider a compact summary with identity, display metadata, position,
		// bearing and freshness. Bound or paginate large viewports, retaining active/stale
		// filtering, and measure payload size and allocations for dense areas.
		var detailCommand, locationCommand *redis.SliceCmd
		_, err := redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			detailCommand = pipe.MGet(ctx, detailKeys...)
			locationCommand = pipe.MGet(ctx, locationKeys...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		detailValues, err := detailCommand.Result()
		if err != nil {
			return nil, err
		}
		locationValues, err := locationCommand.Result()
		if err != nil {
			return nil, err
		}

		for index, detailValue := range detailValues {
			detailsJSON, ok := redisString(detailValue)
			if !ok || detailsJSON == "" {
				staleRealtimeJourneyIDs = append(staleRealtimeJourneyIDs, batchIDs[index])
				continue
			}

			realtimeJourney, err := decodeStoredRealtimeJourney(ctx, []byte(detailsJSON), false)
			if err != nil || realtimeJourney == nil {
				staleRealtimeJourneyIDs = append(staleRealtimeJourneyIDs, batchIDs[index])
				continue
			}

			if locationJSON, ok := redisString(locationValues[index]); ok && locationJSON != "" {
				var vehicleLocation storedVehicleLocation
				if err := json.Unmarshal([]byte(locationJSON), &vehicleLocation); err == nil {
					applyStoredLocation(realtimeJourney, vehicleLocation)
				}
			}

			if (realtimeJourney.VehicleLocationObservedAt != nil && (!realtimeJourney.ActivelyTracked || realtimeJourney.Cancelled)) ||
				!realtimeJourney.IsActive() {
				staleRealtimeJourneyIDs = append(staleRealtimeJourneyIDs, batchIDs[index])
				continue
			}

			if realtimeJourney.VehicleLocationObservedAt != nil && time.Since(*realtimeJourney.VehicleLocationObservedAt) > railPositionMaxAge {
				continue
			}
			if !bounds.contains(realtimeJourney.VehicleLocation) {
				continue
			}

			realtimeJourneys = append(realtimeJourneys, realtimeJourney)
		}
	}

	if len(staleRealtimeJourneyIDs) > 0 {
		_ = redis_client.Client.ZRem(ctx, realtimeJourneyLocationGeoIndexKey(), staleRealtimeJourneyIDs...).Err()
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

	location, err := getStoredLocation(ctx, realtimeJourney.PrimaryIdentifier)
	if err == nil {
		applyStoredLocation(realtimeJourney, location)
	}
}

func applyStoredLocation(journey *ctdf.RealtimeJourney, location storedVehicleLocation) {
	if location.Location.Type != "" {
		journey.VehicleLocation = location.Location
		journey.VehicleBearing = location.Bearing
		journey.VehicleLocationObservedAt = nil
		if !location.ObservedAt.IsZero() {
			journey.VehicleLocationObservedAt = &location.ObservedAt
		}
		journey.VehicleLocationSource = ""
		journey.VehicleLocationConfidence = ""
		if location.Position != nil {
			journey.VehicleLocationSource = location.Position.Source
			journey.VehicleLocationConfidence = location.Position.Confidence
		}
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
	location, err := getStoredLocation(ctx, identifier)
	return location.Location, location.Bearing, err
}

func getStoredLocation(ctx context.Context, identifier string) (storedVehicleLocation, error) {
	var location storedVehicleLocation
	raw, err := redis_client.Client.Get(ctx, realtimeJourneyLocationKey(identifier)).Bytes()
	if err != nil {
		return location, err
	}
	err = json.Unmarshal(raw, &location)
	return location, err
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

	return decodeRailDetailedPart(detailedRailResult.Val(), detailType == "allocation")
}

// Keep the original JSON shape so older readers can decode new allocations.
type storedRailAllocation struct {
	ctdf.JourneyDetailedRail
	Enriched bool `json:"enriched,omitempty"`
}

func decodeRailDetailedPart(raw string, allocation bool) (ctdf.JourneyDetailedRail, error) {
	var stored storedRailAllocation
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return ctdf.JourneyDetailedRail{}, err
	}
	if allocation && !stored.Enriched {
		// Older allocation keys remain readable until rewritten or expired.
		return enrichRailDetailedAllocation(stored.JourneyDetailedRail), nil
	}
	return stored.JourneyDetailedRail, nil
}

func enrichRailDetailedAllocation(allocation ctdf.JourneyDetailedRail) ctdf.JourneyDetailedRail {
	for trainIndex := range allocation.Trains {
		allocation.Trains[trainIndex] = enrichRailTrainAllocation(allocation.Trains[trainIndex])
	}

	return allocation
}

func enrichRailTrainAllocation(train ctdf.RailTrain) ctdf.RailTrain {
	for carriageIndex := range train.Carriages {
		if train.Carriages[carriageIndex].VehicleRole == "" {
			train.Carriages[carriageIndex].VehicleRole = ctdf.RailCarriageVehicleRoleUnknown
		}
	}

	train = inferRailTrainAllocationFields(train)
	railclasses.Enrich(&train)
	return train
}

func inferRailTrainAllocationFields(train ctdf.RailTrain) ctdf.RailTrain {
	if train.TrainLength == 0 || railTrainHasNonLengthCarriage(train) {
		train.TrainLength = railTrainLength(train)
	}
	if train.VehicleType != "" {
		return train
	}

	train.VehicleType = railClassVehicleType(train.FleetID)
	if train.VehicleType != "" {
		return train
	}

	for _, carriage := range train.Carriages {
		vehicleType := railClassVehicleType(carriage.CarriageType)
		if vehicleType == "" {
			vehicleType = railClassVehicleType(carriage.SpecificType)
		}
		if vehicleType != "" {
			train.VehicleType = vehicleType
			return train
		}
	}

	return train
}

func railTrainHasNonLengthCarriage(train ctdf.RailTrain) bool {
	for _, carriage := range train.Carriages {
		if !carriage.CountsTowardsTrainLength() {
			return true
		}
	}
	return false
}

func railTrainLength(train ctdf.RailTrain) int {
	count := 0
	for _, carriage := range train.Carriages {
		if carriage.CountsTowardsTrainLength() {
			count++
		}
	}
	return count
}

func railClassVehicleType(value string) string {
	if value == "" {
		return ""
	}

	class := ""
	for _, char := range value {
		if char >= '0' && char <= '9' {
			class += string(char)
			continue
		}
		if class != "" {
			break
		}
	}

	if class == "" {
		return ""
	}

	return "gb-railclass-" + class
}

func mergeRailDetailed(allocation ctdf.JourneyDetailedRail, loading ctdf.JourneyDetailedRail) ctdf.JourneyDetailedRail {
	merged := allocation

	for _, loadingTrain := range loading.Trains {
		trainIndex := matchingRailTrainIndex(merged.Trains, loadingTrain)
		if trainIndex >= 0 {
			mergeRailTrainLoading(&merged.Trains[trainIndex], loadingTrain)
			continue
		}

		merged.Trains = append(merged.Trains, loadingTrain)
	}

	return merged
}

func matchingRailTrainIndex(trains []ctdf.RailTrain, loadingTrain ctdf.RailTrain) int {
	if loadingTrain.ID != "" {
		for trainIndex := range trains {
			if trains[trainIndex].ID == loadingTrain.ID {
				return trainIndex
			}
		}
		if len(trains) == 1 && trains[0].ID == "" {
			return 0
		}
		return -1
	}

	if loadingTrain.Position > 0 {
		for trainIndex := range trains {
			if trains[trainIndex].Position == loadingTrain.Position {
				return trainIndex
			}
		}
	}

	if len(trains) == 1 {
		return 0
	}

	return -1
}

func mergeRailTrainLoading(train *ctdf.RailTrain, loading ctdf.RailTrain) {
	carriageIndexes := map[string]int{}
	vehicleIndexes := map[string]int{}
	for carriageIndex, carriage := range train.Carriages {
		if carriage.ID != "" {
			carriageIndexes[carriage.ID] = carriageIndex
		}
		if carriage.VehicleID != "" {
			vehicleIndexes[carriage.VehicleID] = carriageIndex
		}
	}

	for _, loadingCarriage := range loading.Carriages {
		carriageIndex, found := carriageIndexes[loadingCarriage.ID]
		if !found && loadingCarriage.VehicleID != "" {
			carriageIndex, found = vehicleIndexes[loadingCarriage.VehicleID]
		}
		if found {
			train.Carriages[carriageIndex].Occupancy = loadingCarriage.Occupancy
			if train.Carriages[carriageIndex].VehicleRole == "" && loadingCarriage.VehicleRole != "" {
				train.Carriages[carriageIndex].VehicleRole = loadingCarriage.VehicleRole
			}
			continue
		}

		if loadingCarriage.VehicleRole == "" {
			loadingCarriage.VehicleRole = ctdf.RailCarriageVehicleRolePassenger
		}
		train.Carriages = append(train.Carriages, loadingCarriage)
	}
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
	realtimeJourney, err := findStoredByIdentifier(ctx, identifier, false)
	if err != nil {
		return nil, err
	}

	ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)
	return realtimeJourney, nil
}

// FindForUpdate loads details with location overlays and skips rail allocation
// and loading data. The vehicle tracker uses FindForTrackerUpdate to save
// against a specific details snapshot.
func FindForUpdate(ctx context.Context, identifier string) (*ctdf.RealtimeJourney, error) {
	realtimeJourney, err := findStoredByIdentifier(ctx, identifier, false)
	if err != nil {
		return nil, err
	}

	ApplyLocation(ctx, realtimeJourney)
	ApplyLocationDescription(ctx, realtimeJourney)
	return realtimeJourney, nil
}

// FindForTrackerUpdate reads one immutable details version and returns a
// mutable working copy with the same location overlays as FindForUpdate.
// SaveRealtimeJourneySnapshot uses the private raw value for its atomic CAS.
func FindForTrackerUpdate(ctx context.Context, identifier string) (*RealtimeJourneySnapshot, error) {
	if identifier == "" {
		return nil, errors.New("realtime journey identifier is required")
	}

	var detailsCommand, locationCommand, descriptionCommand *redis.StringCmd
	_, _ = redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		detailsCommand = pipe.Get(ctx, realtimeJourneyDetailsKey(identifier))
		locationCommand = pipe.Get(ctx, realtimeJourneyLocationKey(identifier))
		descriptionCommand = pipe.Get(ctx, realtimeJourneyLocationDescriptionKey(identifier))
		return nil
	})
	raw, err := detailsCommand.Bytes()
	if errors.Is(err, redis.Nil) {
		return &RealtimeJourneySnapshot{identifier: identifier}, nil
	}
	if err != nil {
		return nil, err
	}

	previous, err := decodeStoredRealtimeJourney(ctx, raw, false)
	if err != nil {
		return nil, err
	}
	working := copyRealtimeJourneyForTracker(previous)
	if locationJSON, locationErr := locationCommand.Bytes(); locationErr == nil {
		var location storedVehicleLocation
		if json.Unmarshal(locationJSON, &location) == nil {
			applyStoredLocation(working, location)
		}
	}
	if description, descriptionErr := descriptionCommand.Result(); descriptionErr == nil {
		working.VehicleLocationDescription = description
	}

	return &RealtimeJourneySnapshot{
		Journey:    working,
		identifier: identifier,
		raw:        raw,
		previous:   previous,
	}, nil
}

func copyRealtimeJourneyForTracker(previous *ctdf.RealtimeJourney) *ctdf.RealtimeJourney {
	if previous == nil {
		return nil
	}

	working := *previous
	if previous.Stops != nil {
		// Tracker updates replace map entries; existing stop values remain
		// read-only so event comparisons keep the previous snapshot intact.
		working.Stops = maps.Clone(previous.Stops)
	}
	return &working
}

func findStoredByIdentifier(ctx context.Context, identifier string, hydrateJourney bool) (*ctdf.RealtimeJourney, error) {
	realtimeJourneyResult := redis_client.Client.Get(ctx, realtimeJourneyDetailsKey(identifier))
	if realtimeJourneyResult.Err() != nil {
		return nil, realtimeJourneyResult.Err()
	}

	realtimeJourney, err := decodeStoredRealtimeJourney(ctx, []byte(realtimeJourneyResult.Val()), hydrateJourney)
	if err != nil {
		return nil, err
	}

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
