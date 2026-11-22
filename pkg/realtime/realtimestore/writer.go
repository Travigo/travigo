package realtimestore

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
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
	defaultRealtimeJourneyTTL    = 10 * time.Minute
	realtimeJourneyUpdateLockTTL = 5 * time.Second
	// LINX sends formation/allocation data early in the morning for every train
	// running that day, so it must remain available before each journey appears.
	railAllocationTTL = 24 * time.Hour
)

// ErrRealtimeJourneyConflict means a realtime journey snapshot was superseded
// before it could be saved.
var ErrRealtimeJourneyConflict = errors.New("realtime journey changed during update")

var saveRealtimeJourneySnapshotScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if (current or '') ~= ARGV[1] then
	return 0
end

local mappingEnd = #KEYS
if ARGV[5] ~= '' then
	-- Append first. If the stream write fails, leave the journey uncreated.
	redis.call('XADD', KEYS[#KEYS], '*', 'journey', ARGV[4], 'created', ARGV[6], 'registration', ARGV[5])
	mappingEnd = mappingEnd - 1
end

local ttl = ARGV[3]
redis.call('SET', KEYS[1], ARGV[2], 'EX', ttl)
for index = 4, mappingEnd do
	redis.call('SET', KEYS[index], ARGV[4], 'EX', ttl)
end

if ARGV[7] then
	redis.call('SET', KEYS[2], ARGV[7], 'EX', ttl)
	-- Keep the overlay even if the geo-index update fails.
	if ARGV[8] then
		redis.pcall('GEOADD', KEYS[3], ARGV[8], ARGV[9], ARGV[4])
	else
		redis.pcall('ZREM', KEYS[3], ARGV[4])
	end
end

return 1
`)

var releaseRealtimeJourneyUpdateLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('DEL', KEYS[1])
end
return 0
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
	return SaveVehicleTrackerSnapshot(ctx, snapshot, nil)
}

// SaveVehicleTrackerSnapshot uses the prepared representation of the scheduled
// journey selected by the tracker. Live fields and overlays are still encoded
// and committed with the original compare-and-set.
func SaveVehicleTrackerSnapshot(ctx context.Context, snapshot *RealtimeJourneySnapshot, schedule *TrackerSchedule) error {
	if snapshot == nil || snapshot.Journey == nil {
		return errors.New("realtime journey snapshot is required")
	}
	if snapshot.identifier == "" || snapshot.Journey.PrimaryIdentifier != snapshot.identifier {
		return errors.New("realtime journey snapshot identifier does not match journey")
	}

	var realtimeJourneyJSON []byte
	var err error
	if journey := snapshot.Journey.Journey; schedule != nil && journey != nil &&
		journey.PrimaryIdentifier == schedule.timetable.PrimaryIdentifier && journey.PrimaryIdentifier != snapshot.identifier &&
		journey.ModificationDateTime.Equal(schedule.timetable.ModificationDateTime) {
		realtimeJourneyJSON, err = json.Marshal(storedTrackerSnapshot{
			storedRealtimeJourney: storedRealtimeJourneyWithoutSchedule(snapshot.Journey),
			Timetable:             schedule.encoded,
		})
	} else {
		realtimeJourneyJSON, err = json.Marshal(storedRealtimeJourneyFromCTDF(snapshot.Journey))
	}
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
	registration, createdAt := "", ""
	if len(snapshot.raw) == 0 && snapshot.Journey.VehicleRegistration != "" {
		keys = append(keys, VehicleEnrichmentStream)
		registration = snapshot.Journey.VehicleRegistration
		createdAt = snapshot.Journey.CreationDateTime.Format(time.RFC3339Nano)
	}
	args := []interface{}{snapshot.raw, realtimeJourneyJSON, int64(timeoutDuration / time.Second), snapshot.identifier, registration, createdAt}
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

// SaveRealtimeJourneyMerged retries a full-record update from a snapshot
// returned by FindForMergeUpdate, merging the caller's changes onto the latest
// stored version. Disjoint field
// changes are preserved; when both snapshots change the same field, the
// incoming update wins.
func SaveRealtimeJourneyMerged(ctx context.Context, snapshot *RealtimeJourneySnapshot) error {
	if snapshot == nil || snapshot.Journey == nil {
		return errors.New("realtime journey snapshot is required")
	}

	unlock, err := lockRealtimeJourneyUpdate(ctx, snapshot.identifier)
	if err != nil {
		return err
	}
	defer unlock()

	base := snapshot.previous
	if base == nil {
		return SaveRealtimeJourneySnapshot(ctx, snapshot)
	}

	updated := snapshot.Journey
	current, err := FindForTrackerUpdate(ctx, snapshot.identifier)
	if err != nil {
		return err
	}
	if current.Journey == nil {
		return ErrRealtimeJourneyConflict
	}
	for attempt := 0; attempt < 8; attempt++ {
		candidate := *current
		candidate.Journey = mergeRealtimeJourney(reflect.ValueOf(base), reflect.ValueOf(updated), reflect.ValueOf(current.Journey)).Interface().(*ctdf.RealtimeJourney)
		if err := SaveRealtimeJourneySnapshot(ctx, &candidate); !errors.Is(err, ErrRealtimeJourneyConflict) {
			if err == nil {
				*snapshot.Journey = *candidate.Journey
			}
			return err
		}
		latest, err := FindForTrackerUpdate(ctx, snapshot.identifier)
		if err != nil {
			return err
		}
		if latest.Journey == nil {
			return ErrRealtimeJourneyConflict
		}
		current = latest
	}
	return ErrRealtimeJourneyConflict
}

func lockRealtimeJourneyUpdate(ctx context.Context, identifier string) (func(), error) {
	var tokenBytes [16]byte
	if _, err := cryptorand.Read(tokenBytes[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes[:])
	key := fmt.Sprintf("realtime-journey:update-lock/%s", identifier)

	for {
		locked, err := redis_client.Client.SetNX(ctx, key, token, realtimeJourneyUpdateLockTTL).Result()
		if err != nil {
			return nil, err
		}
		if locked {
			return func() {
				_ = releaseRealtimeJourneyUpdateLockScript.Run(context.Background(), redis_client.Client, []string{key}, token).Err()
			}, nil
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func mergeRealtimeJourney(base, updated, current reflect.Value) reflect.Value {
	if !base.IsValid() || !updated.IsValid() || !current.IsValid() || base.Type() != updated.Type() || base.Type() != current.Type() {
		return updated
	}
	if reflect.DeepEqual(updated.Interface(), base.Interface()) {
		return current
	}
	if reflect.DeepEqual(current.Interface(), base.Interface()) {
		return updated
	}
	switch updated.Kind() {
	case reflect.Pointer:
		if base.IsNil() || updated.IsNil() || current.IsNil() {
			return updated
		}
		merged := reflect.New(updated.Type().Elem())
		merged.Elem().Set(mergeRealtimeJourney(base.Elem(), updated.Elem(), current.Elem()))
		return merged
	case reflect.Struct:
		hasExportedField := false
		for i := 0; i < updated.NumField(); i++ {
			if updated.Type().Field(i).PkgPath == "" {
				hasExportedField = true
				break
			}
		}
		if !hasExportedField {
			return updated
		}
		merged := reflect.New(updated.Type()).Elem()
		merged.Set(current)
		for i := 0; i < updated.NumField(); i++ {
			if updated.Type().Field(i).PkgPath != "" {
				continue
			}
			merged.Field(i).Set(mergeRealtimeJourney(base.Field(i), updated.Field(i), current.Field(i)))
		}
		return merged
	case reflect.Map:
		if updated.IsNil() {
			return updated
		}
		merged := reflect.MakeMapWithSize(updated.Type(), current.Len()+updated.Len())
		if !current.IsNil() {
			iter := current.MapRange()
			for iter.Next() {
				merged.SetMapIndex(iter.Key(), iter.Value())
			}
		}
		keys := map[any]struct{}{}
		if !base.IsNil() {
			iter := base.MapRange()
			for iter.Next() {
				keys[iter.Key().Interface()] = struct{}{}
			}
		}
		iter := updated.MapRange()
		for iter.Next() {
			keys[iter.Key().Interface()] = struct{}{}
		}
		for keyValue := range keys {
			key := reflect.ValueOf(keyValue)
			baseValue, updatedValue, currentValue := base.MapIndex(key), updated.MapIndex(key), current.MapIndex(key)
			if !updatedValue.IsValid() {
				if baseValue.IsValid() {
					merged.SetMapIndex(key, reflect.Value{})
				}
				continue
			}
			if !baseValue.IsValid() || !currentValue.IsValid() {
				merged.SetMapIndex(key, updatedValue)
				continue
			}
			merged.SetMapIndex(key, mergeRealtimeJourney(baseValue, updatedValue, currentValue))
		}
		return merged
	default:
		return updated
	}
}

func cloneRealtimeJourney(journey *ctdf.RealtimeJourney) *ctdf.RealtimeJourney {
	if journey == nil {
		return nil
	}
	encoded, err := json.Marshal(journey)
	if err != nil {
		return journey
	}
	var clone ctdf.RealtimeJourney
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return journey
	}
	clone.VehicleRefs = maps.Clone(journey.VehicleRefs)
	return &clone
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
