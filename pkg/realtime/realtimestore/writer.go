package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

type storedVehicleLocation struct {
	Location   ctdf.Location    `json:"location"`
	Position   *RailObservation `json:"position,omitempty"`
	Bearing    float64          `json:"bearing"`
	ObservedAt time.Time        `json:"observedAt"`
}

const (
	defaultRealtimeJourneyTTL = 10 * time.Minute
	// LINX sends formation/allocation data early in the morning for every train
	// running that day, so it must remain available before each journey appears.
	railAllocationTTL = 24 * time.Hour
)

// ErrRealtimeJourneyConflict means the tracker snapshot was superseded before
// it could be saved. Callers should read a fresh snapshot and recompute once.
var ErrRealtimeJourneyConflict = errors.New("realtime journey changed during tracker update")

var saveRealtimeJourneySnapshotScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if (current or '') ~= ARGV[1] then
	return 0
end

local ttl = ARGV[3]
redis.call('SET', KEYS[1], ARGV[2], 'EX', ttl)
for index = 4, #KEYS do
	redis.call('SET', KEYS[index], ARGV[4], 'EX', ttl)
end

if ARGV[5] then
	redis.call('SET', KEYS[2], ARGV[5], 'EX', ttl)
	-- Keep the overlay even if the geo-index update fails.
	if ARGV[6] then
		redis.pcall('GEOADD', KEYS[3], ARGV[6], ARGV[7], ARGV[4])
	else
		redis.pcall('ZREM', KEYS[3], ARGV[4])
	end
end

return 1
`)

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

// RealtimeJourneySnapshot is the tracker update's read version. Journey is a
// mutable working copy; the original Redis value and decoded details remain
// private so saving can compare-and-set and publish events against the exact
// previous state without another GET or decode.
type RealtimeJourneySnapshot struct {
	Journey *ctdf.RealtimeJourney

	identifier string
	raw        []byte
	previous   *ctdf.RealtimeJourney
	location   []byte
	point      *[2]float64
}

// SetLocation records the tracker's location overlay to commit atomically with
// the journey. GPS-only callers should continue to use UpdateLocation.
func (snapshot *RealtimeJourneySnapshot) SetLocation(location ctdf.Location, bearing float64, observedAt time.Time) error {
	if snapshot == nil {
		return errors.New("realtime journey snapshot is required")
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	value, err := json.Marshal(storedVehicleLocation{
		Location:   location,
		Bearing:    bearing,
		ObservedAt: observedAt,
	})
	if err != nil {
		return err
	}

	snapshot.location = value
	snapshot.point = nil
	if location.Type == "Point" && len(location.Coordinates) >= 2 {
		snapshot.point = &[2]float64{location.Coordinates[0], location.Coordinates[1]}
	}
	return nil
}

// SaveRealtimeJourneySnapshot atomically saves the tracker snapshot only if
// the persisted details still match the value returned by FindForTrackerUpdate.
// A location overlay set with SetLocation is committed in the same script.
func SaveRealtimeJourneySnapshot(ctx context.Context, snapshot *RealtimeJourneySnapshot) error {
	if snapshot == nil || snapshot.Journey == nil {
		return errors.New("realtime journey snapshot is required")
	}
	if snapshot.identifier == "" || snapshot.Journey.PrimaryIdentifier != snapshot.identifier {
		return errors.New("realtime journey snapshot identifier does not match journey")
	}

	storedRealtimeJourney := storedRealtimeJourneyFromCTDF(snapshot.Journey)
	realtimeJourneyJSON, err := json.Marshal(storedRealtimeJourney)
	if err != nil {
		return err
	}
	timeoutDuration := realtimeJourneyExpiration(snapshot.Journey.TimeoutDurationMinutes)

	keys := []string{
		realtimeJourneyDetailsKey(snapshot.identifier),
		realtimeJourneyLocationKey(snapshot.identifier),
		realtimeJourneyLocationGeoIndexKey(),
	}
	if snapshot.Journey.Journey != nil && snapshot.Journey.Journey.PrimaryIdentifier != "" {
		keys = append(keys, realtimeJourneyMappingKey("travigo-journeyid", snapshot.Journey.Journey.PrimaryIdentifier))
	}
	for mappingType, identifier := range snapshot.Journey.OtherIdentifiers {
		keys = append(keys, realtimeJourneyMappingKey(mappingType, identifier))
	}
	args := []interface{}{snapshot.raw, realtimeJourneyJSON, int64(timeoutDuration / time.Second), snapshot.identifier}
	if snapshot.location != nil {
		args = append(args, snapshot.location)
		if snapshot.point != nil {
			args = append(args, snapshot.point[0], snapshot.point[1])
		}
	}
	result, err := saveRealtimeJourneySnapshotScript.Run(ctx, redis_client.Client, keys, args...).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrRealtimeJourneyConflict
	}
	publishRealtimeJourneyEvents(snapshot.previous, snapshot.Journey, true)
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

	detailedRailInformation.Trains = slices.Clone(detailedRailInformation.Trains)
	for i := range detailedRailInformation.Trains {
		train := &detailedRailInformation.Trains[i]
		train.Carriages = slices.Clone(train.Carriages)
		for j := range train.Carriages {
			train.Carriages[j].Occupancy = -1
		}
	}

	detailedRailInformationJSON, err := json.Marshal(storedRailAllocation{
		JourneyDetailedRail: enrichRailDetailedAllocation(detailedRailInformation),
		Enriched:            true,
	})
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
	earliestIndexedStops := make(map[string]time.Time, len(realtimeJourney.Stops))

	for _, stop := range realtimeJourney.Stops {
		if stop == nil || stop.TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture || stop.ArrivalTime.IsZero() {
			continue
		}

		if arrival, ok := earliestIndexedStops[stop.StopRef]; !ok || stop.ArrivalTime.Before(arrival) {
			earliestIndexedStops[stop.StopRef] = stop.ArrivalTime
		}
	}
	for stopRef, arrival := range earliestIndexedStops {
		indexedStops = append(indexedStops, indexedStop{identifier: stopRef, arrival: arrival})
		currentStopIDs = append(currentStopIDs, stopRef)
	}

	// Performance Review [Set K] [PR129] [P2]: Each refresh removes/recreates all memberships and
	// repeats sorted-set cleanup per journey. ZADD already updates existing scores:
	// remove only departed memberships, update current scores, and consolidate stale
	// cleanup per stop/polling cycle. Preserve expiry and earliest-arrival handling
	// for repeated stops; measure Redis command volume at busy stations.
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

func UpdateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64, observedAt ...time.Time) error {
	return updateLocation(ctx, identifier, location, bearing, realtimeJourneyTTL(ctx, identifier), observedAt...)
}

func UpdateLocationForRealtimeJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney, location ctdf.Location, bearing float64, observedAt ...time.Time) error {
	if realtimeJourney == nil {
		return errors.New("realtime journey is required")
	}

	return updateLocation(ctx, realtimeJourney.PrimaryIdentifier, location, bearing, realtimeJourneyExpiration(realtimeJourney.TimeoutDurationMinutes), observedAt...)
}

func updateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64, timeoutDuration time.Duration, observedAt ...time.Time) error {
	updatedAt := time.Now()
	if len(observedAt) > 0 && !observedAt[0].IsZero() {
		updatedAt = observedAt[0]
	}
	locationJSON, err := json.Marshal(storedVehicleLocation{
		Location:   location,
		Bearing:    bearing,
		ObservedAt: updatedAt,
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
	// Performance Review [Set J] [PR130] [P2] (tracker): A location-only update GETs the entire
	// realtime journey and scans its JSON just to read TimeoutDurationMinutes before
	// writing a small location record. Keep expiration policy in compact metadata or
	// pass/cache it from identification so GPS-only writes do not scale with route
	// length. Preserve the distinction between the configured duration and remaining
	// TTL; substituting PTTL would change the current location-renewal semantics.
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

func DeleteRealtimeJourneyDetails(ctx context.Context, identifier string) error {
	return redis_client.Client.Del(ctx, realtimeJourneyDetailsKey(identifier)).Err()
}
