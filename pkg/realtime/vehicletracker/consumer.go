package vehicletracker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/diagnostics"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker/identifiers"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var identificationCache *cache.Cache[string]

const numConsumers = 5
const batchSize = 200
const identificationCacheTTL = 90 * time.Minute
const gtfsJourneyOwnershipTTL = 8 * time.Hour

var touchIdentificationMapping = redis.NewScript(`
local journey_id = redis.call("HGET", KEYS[1], "journey_id")
if not journey_id then
  return {"", "missing"}
end
if journey_id == "N/A" then
  return {"", "suppressed"}
end

local last_updated = tonumber(redis.call("HGET", KEYS[1], "last_updated")) or 0
local incoming_updated = tonumber(ARGV[1])
if incoming_updated <= last_updated then
  return {"", "stale"}
end

redis.call("HSET", KEYS[1], "last_updated", ARGV[1])
redis.call("PEXPIRE", KEYS[1], ARGV[2])
return {journey_id, "updated"}
`)

func CreateIdentificationCache() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(identificationCacheTTL))

	identificationCache = cache.New[string](redisStore)
}
func StartConsumers() {
	// Create Cache
	CreateIdentificationCache()

	// Run the background consumers
	log.Info().Msg("Starting realtime consumers")

	queue, err := redis_client.QueueConnection.OpenQueue("realtime-queue")
	if err != nil {
		panic(err)
	}
	if err := queue.StartConsuming(numConsumers*batchSize, 1*time.Second); err != nil {
		panic(err)
	}

	for i := 0; i < numConsumers; i++ {
		go startRealtimeConsumer(queue, i)
	}
}
func startRealtimeConsumer(queue rmq.Queue, id int) {
	log.Info().Msgf("Starting realtime consumer %d", id)

	if _, err := queue.AddBatchConsumer(fmt.Sprintf("realtime-queue-%d", id), batchSize, 2*time.Second, NewBatchConsumer(id)); err != nil {
		panic(err)
	}
}

type BatchConsumer struct {
	id                                 int
	TfLBusQueue                        rmq.Queue
	journeyCacheMu                     sync.RWMutex
	journeyCache                       map[string]*cachedTrackedJourney
	journeyCacheHead, journeyCacheTail *cachedTrackedJourney
	locationCacheMu                    sync.RWMutex
	locationCache                      map[string]*time.Location
}

func NewBatchConsumer(id int) *BatchConsumer {
	tfLBusQueue, err := redis_client.QueueConnection.OpenQueue("tfl-bus-queue")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to start notify queue")
	}

	return &BatchConsumer{
		id:            id,
		TfLBusQueue:   tfLBusQueue,
		journeyCache:  map[string]*cachedTrackedJourney{},
		locationCache: map[string]*time.Location{},
	}
}

func (consumer *BatchConsumer) Consume(batch rmq.Deliveries) {
	started := time.Now()
	telemetry.BatchStarted(consumer.id)
	defer func() { telemetry.Observe(telemetry.Batch, time.Since(started)); telemetry.BatchFinished(consumer.id) }()
	payloads := batch.Payloads()
	telemetry.Add(telemetry.Received, int64(len(payloads)))

	serviceAlertOperations := make([]mongo.WriteModel, 0, len(payloads))

	// Performance Review [Set M] [PR140] [P2] (tracker): A consumer handles up to 200 deliveries
	// serially, including identification, cold-cache hydration, Redis, and telemetry.
	// There is no total per-event work budget: Background contexts and expensive
	// fallback searches can delay the whole batch. Propagate bounded contexts through
	// those stages, distinguish timeouts from genuine no-match results, and consider
	// bounded work lanes keyed by journey before increasing prefetch or concurrency.
	// Measure event age and slow-stage time, not just deliveries per batch.
	for _, payload := range payloads {
		var vehicleUpdateEvent *VehicleUpdateEvent
		if err := json.Unmarshal([]byte(payload), &vehicleUpdateEvent); err != nil || vehicleUpdateEvent == nil {
			// The whole batch is acknowledged below. A malformed payload cannot
			// become processable through retry, so retaining it in rmq's
			// non-expiring rejected list only leaks Redis memory.
			telemetry.Add(telemetry.Malformed, 1)
			log.Warn().Err(err).Msg("Discarding malformed realtime event")
			continue
		}

		if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeTrip || vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeLocationOnly {
			identifiedJourneyID := consumer.identifyVehicle(vehicleUpdateEvent, vehicleUpdateEvent.SourceType, vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation)

			if identifiedJourneyID != "" {
				if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeTrip {
					err := consumer.updateRealtimeJourney(identifiedJourneyID, vehicleUpdateEvent)
					if err != nil {
						telemetry.Add(telemetry.UpdateFailed, 1)
						log.Error().Err(err).Msg("Failed to update realtime journey")
					}
				} else if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeLocationOnly {
					err := consumer.updateRealtimeJourneyLocationOnly(identifiedJourneyID, vehicleUpdateEvent)
					if err != nil {
						telemetry.Add(telemetry.UpdateFailed, 1)
						log.Error().Err(err).Msg("Failed to update realtime journey location only")
					}
				}
			} else {
				log.Debug().Interface("event", vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation).Msg("Couldnt identify journey")
			}
		} else if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeServiceAlert {
			var matchedIdentifiers []string
			for _, identifyingInformation := range vehicleUpdateEvent.ServiceAlertUpdate.IdentifyingInformation {
				identifiedJourneyID := consumer.identifyVehicle(vehicleUpdateEvent, vehicleUpdateEvent.SourceType, identifyingInformation)
				identifiedStopID := consumer.identifyStop(vehicleUpdateEvent.SourceType, identifyingInformation)
				identifiedServiceID := consumer.identifyService(vehicleUpdateEvent.SourceType, identifyingInformation)

				if identifiedJourneyID != "" {
					matchedIdentifiers = append(matchedIdentifiers, identifiedJourneyID)
				}
				if identifiedStopID != "" {
					matchedIdentifiers = append(matchedIdentifiers, identifiedStopID)
				}
				if identifiedServiceID != "" {
					matchedIdentifiers = append(matchedIdentifiers, identifiedServiceID)
				}
			}

			writeModel, _ := consumer.updateServiceAlert(matchedIdentifiers, vehicleUpdateEvent)
			if writeModel != nil {
				serviceAlertOperations = append(serviceAlertOperations, writeModel)
			}
		}
		telemetry.HandledEvent(vehicleUpdateEvent.RecordedAt)
	}

	if len(serviceAlertOperations) > 0 {
		serviceAlertsCollection := database.GetCollection("service_alerts")

		startTime := time.Now()
		_, err := serviceAlertsCollection.BulkWrite(context.Background(), serviceAlertOperations, &options.BulkWriteOptions{})
		log.Info().Int("Length", len(serviceAlertOperations)).Str("Time", time.Now().Sub(startTime).String()).Msg("Bulk write service_alerts")

		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Service Alerts")
		}
	}

	// Performance Review [Set M] [PR141] [P2] (tracker): rmq v5.2.0 Deliveries.Ack loops serially
	// over deliveries, issuing one LREM each. A 200-item batch needs 200 initial Redis
	// calls, plus retries for dependency errors; Ack stops retrying on success,
	// ErrorNotFound or consuming shutdown. The consumer cannot process its next batch
	// while acknowledgement stalls. Profile this separately and evaluate batching with
	// per-delivery outcomes and unchanged retry semantics.
	ackStarted := time.Now()
	ackErrors := batch.Ack()
	telemetry.Observe(telemetry.Ack, time.Since(ackStarted))
	if len(ackErrors) > 0 {
		for _, err := range ackErrors {
			log.Fatal().Err(err).Msg("Failed to consume realtime event")
		}
	}
}

func (consumer *BatchConsumer) identifyStop(sourceType string, identifyingInformation map[string]string) string {
	if sourceType == "GTFS-RT" {
		stopIdentifier := identifiers.GTFSRT{
			IdentifyingInformation: identifyingInformation,
		}
		stop, err := stopIdentifier.IdentifyStop()

		if err != nil {
			return ""
		}

		return stop
	} else if sourceType == "siri-sx" {
		stopIdentifier := identifiers.SiriSX{
			IdentifyingInformation: identifyingInformation,
		}
		stop, err := stopIdentifier.IdentifyStop()

		if err != nil {
			return ""
		}

		return stop
	} else {
		log.Error().Str("sourcetype", sourceType).Msg("Unknown sourcetype")
		return ""
	}
}

func (consumer *BatchConsumer) identifyService(sourceType string, identifyingInformation map[string]string) string {
	if sourceType == "GTFS-RT" {
		serviceIdentifier := identifiers.GTFSRT{
			IdentifyingInformation: identifyingInformation,
		}
		service, err := serviceIdentifier.IdentifyService()

		if err != nil {
			return ""
		}

		return service
	} else if sourceType == "siri-sx" {
		serviceIdentifier := identifiers.SiriSX{
			IdentifyingInformation: identifyingInformation,
		}
		service, err := serviceIdentifier.IdentifyService()

		if err != nil {
			return ""
		}

		return service
	} else {
		log.Error().Str("sourcetype", sourceType).Msg("Unknown sourcetype")
		return ""
	}
}

func (consumer *BatchConsumer) identifyVehicle(vehicleUpdateEvent *VehicleUpdateEvent, sourceType string, identifyingInformation map[string]string) string {
	started := time.Now()
	defer func() { telemetry.Observe(telemetry.Identification, time.Since(started)) }()
	currentTime := time.Now()
	yearNumber, weekNumber := currentTime.ISOWeek()
	identifyEventsIndexName := fmt.Sprintf("realtime-identify-events-%d-%d", yearNumber, weekNumber)

	operatorRef := identifyingInformation["OperatorRef"]

	// Performance Review [Set L] [PR142] [P1] (tracker): This Lua check orders admission, not commits.
	// Two workers can accept older then newer observations but save them in reverse
	// order; different LocalIDs can also resolve to the same journey. SaveRealtimeJourney
	// has no source-version check to reject the older snapshot. Before adding parallelism,
	// serialize by canonical journey/run or use a commit version check spanning state,
	// location, history and emitted events. Keep independent location/prediction streams
	// distinct where their timestamps differ.
	journeyID, mappingFound, err := touchExistingIdentificationMapping(context.Background(), vehicleUpdateEvent.LocalID, vehicleUpdateEvent.RecordedAt)
	if err != nil {
		log.Warn().Err(err).Str("local_id", vehicleUpdateEvent.LocalID).Msg("Failed to read realtime identification mapping")
	}
	if mappingFound {
		if sourceType == "siri-vm" && journeyID != "" && consumer.gtfsOwnsJourney(vehicleUpdateEvent, identifyingInformation, journeyID) {
			consumer.recordSiriIdentificationOutcome(vehicleUpdateEvent, identifyingInformation, false, "suppressed_by_gtfs_same_journey")
			return ""
		}
		consumer.recordSiriIdentificationOutcome(vehicleUpdateEvent, identifyingInformation, journeyID != "" && journeyID != "N/A", "cached")
		return journeyID
	}

	{
		var journey string
		var err error

		// TODO use an interface here to reduce duplication
		if sourceType == "siri-vm" {
			// perform the actual sirivm
			journeyIdentifier := identifiers.SiriVM{
				IdentifyingInformation: identifyingInformation,
				CurrentTime:            vehicleUpdateEvent.RecordedAt,
			}
			journey, err = journeyIdentifier.IdentifyJourney()

			// TODO yet another special TfL only thing that shouldn't be here
			if err != nil && identifyingInformation["OperatorRef"] == "gb-noc-TFLO" {
				tflEventBytes, _ := json.Marshal(map[string]string{
					"Line":                     identifyingInformation["PublishedLineName"],
					"DirectionRef":             identifyingInformation["DirectionRef"],
					"NumberPlate":              vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier,
					"OriginRef":                identifyingInformation["OriginRef"],
					"DestinationRef":           identifyingInformation["DestinationRef"],
					"OriginAimedDepartureTime": identifyingInformation["OriginAimedDepartureTime"],
				})
				consumer.TfLBusQueue.PublishBytes(tflEventBytes)
			}
		} else if sourceType == "GTFS-RT" {
			journeyIdentifier := identifiers.GTFSRT{
				IdentifyingInformation: identifyingInformation,
			}
			journey, err = journeyIdentifier.IdentifyJourney()
		} else if sourceType == "siri-sx" {
			return "" // TODO not now
		} else {
			log.Error().Str("sourcetype", sourceType).Msg("Unknown sourcetype")
			return ""
		}

		if err != nil {
			if locationJourneyID := consumer.identifyJourneyFromLocation(vehicleUpdateEvent, sourceType, identifyingInformation); locationJourneyID != "" {
				storeIdentificationMapping(context.Background(), vehicleUpdateEvent.LocalID, locationJourneyID, vehicleUpdateEvent.RecordedAt)
				consumer.recordSiriIdentificationOutcome(vehicleUpdateEvent, identifyingInformation, true, "location_matched")
				return locationJourneyID
			}

			// Performance Review [Set J] [PR143] [P2] (tracker): The same 90-minute negative cache covers
			// missing timetable data, ambiguous matches, and dependency failures returned
			// by identification; subsequent observations skip identification entirely.
			// Separate transient failures from confirmed no-match results, using short,
			// jittered retry windows and timetable-version invalidation for the latter.
			// Coalesce retries so recovery does not create a database stampede, and avoid
			// trading lower query counts for vehicles disappearing after brief failures.
			// Save a cache value of N/A to stop us from constantly rechecking for journeys we cant identify
			storeIdentificationMapping(context.Background(), vehicleUpdateEvent.LocalID, "N/A", vehicleUpdateEvent.RecordedAt)

			// Set cross dataset ID
			if vehicleUpdateEvent.VehicleLocationUpdate != nil && vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier != "" {
				identificationCache.Set(context.Background(), fmt.Sprintf("failedvehicleid/%s/%s", identifyingInformation["LinkedDataset"], vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier), sourceType)
			}

			// Temporary https://github.com/travigo/travigo/issues/43
			// TODO dont just compare the string value here!!
			errorCode := "UNKNOWN"
			switch err.Error() {
			case "Could not find referenced Operator":
				errorCode = "NONREF_OPERATOR"
			case "Could not find related Service":
				errorCode = "NONREF_SERVICE"
			case "Could not find related Journeys":
				errorCode = "NONREF_JOURNEY"
			case "Could not narrow down to single Journey with departure time. Now zero":
				errorCode = "JOURNEYNARROW_ZERO"
			case "Could not narrow down to single Journey by time. Still many remaining":
				errorCode = "JOURNEYNARROW_MANY"
			case "Could not find referenced trip":
				errorCode = "NONREF_TRIP"
			}

			// Record the failed identification event
			diagnostics.Identification(identifyEventsIndexName, false, func() any {
				return RealtimeIdentifyFailureElasticEvent{
					Timestamp: time.Now(),

					Success:    false,
					FailReason: errorCode,
					Outcome:    "unmatched",

					Operator: operatorRef,
					Service:  identifyingInformation["PublishedLineName"],
					Trip:     identifyingInformation["TripID"],

					SourceType: sourceType,
				}
			})

			return ""
		}
		journeyID = journey
		if sourceType == "siri-vm" && consumer.gtfsOwnsJourney(vehicleUpdateEvent, identifyingInformation, journeyID) {
			// Deliberately do not cache this as N/A: the vehicle may turn onto a
			// different journey while the GTFS ownership marker remains valid.
			consumer.recordSiriIdentificationOutcome(vehicleUpdateEvent, identifyingInformation, false, "suppressed_by_gtfs_same_journey")
			return ""
		}

		storeIdentificationMapping(context.Background(), vehicleUpdateEvent.LocalID, journeyID, vehicleUpdateEvent.RecordedAt)

		if sourceType == "GTFS-RT" {
			storeGTFSJourneyOwnership(context.Background(), vehicleUpdateEvent, identifyingInformation, journeyID)
		}

		// Record the successful identification event
		diagnostics.Identification(identifyEventsIndexName, true, func() any {
			return RealtimeIdentifyFailureElasticEvent{
				Timestamp: currentTime,

				Success: true,
				Outcome: "matched",

				Operator: operatorRef,
				Service:  identifyingInformation["PublishedLineName"],
				Trip:     identifyingInformation["TripID"],

				SourceType: sourceType,
			}
		})
	}

	return journeyID
}

func gtfsJourneyOwnershipKey(linkedDataset, vehicleID, timeframe string) string {
	return fmt.Sprintf("realtime-gtfs-journey:%s:%s:%s", linkedDataset, vehicleID, timeframe)
}

func storeGTFSJourneyOwnership(ctx context.Context, event *VehicleUpdateEvent, information map[string]string, journeyID string) {
	if event == nil || event.VehicleLocationUpdate == nil || journeyID == "" {
		return
	}
	vehicleID := event.VehicleLocationUpdate.VehicleIdentifier
	if vehicleID == "" {
		return
	}
	key := gtfsJourneyOwnershipKey(information["LinkedDataset"], vehicleID, event.VehicleLocationUpdate.Timeframe)
	if err := redis_client.Client.Set(ctx, key, journeyID, gtfsJourneyOwnershipTTL).Err(); err != nil {
		log.Warn().Err(err).Str("vehicle_id", vehicleID).Str("journey_id", journeyID).Msg("Failed to store GTFS journey ownership")
	}
}

func (consumer *BatchConsumer) gtfsOwnsJourney(event *VehicleUpdateEvent, information map[string]string, journeyID string) bool {
	if event == nil || event.VehicleLocationUpdate == nil || journeyID == "" {
		return false
	}
	vehicleID := event.VehicleLocationUpdate.VehicleIdentifier
	if vehicleID == "" {
		return false
	}
	key := gtfsJourneyOwnershipKey(information["LinkedDataset"], vehicleID, event.VehicleLocationUpdate.Timeframe)
	gtfsJourneyID, err := redis_client.Client.Get(context.Background(), key).Result()
	if err != nil && err != redis.Nil {
		log.Warn().Err(err).Str("vehicle_id", vehicleID).Msg("Failed to read GTFS journey ownership")
		return false
	}
	return gtfsJourneyID == journeyID
}

func realtimeIdentificationMappingKey(localID string) string {
	return fmt.Sprintf("realtime-identification:%s", localID)
}

func (consumer *BatchConsumer) recordSiriIdentificationOutcome(event *VehicleUpdateEvent, information map[string]string, success bool, outcome string) {

	if event == nil || event.SourceType != "siri-vm" {
		return
	}
	year, week := event.RecordedAt.ISOWeek()
	index := fmt.Sprintf("realtime-identify-events-%d-%d", year, week)
	diagnostics.Identification(index, success, func() any {
		return RealtimeIdentifyFailureElasticEvent{
			Timestamp: event.RecordedAt, Success: success, Outcome: outcome,
			Operator: information["OperatorRef"], Service: information["PublishedLineName"], Trip: information["VehicleJourneyRef"], SourceType: event.SourceType,
		}
	})
}

func touchExistingIdentificationMapping(ctx context.Context, localID string, recordedAt time.Time) (string, bool, error) {
	result, err := touchIdentificationMapping.Run(
		ctx,
		redis_client.Client,
		[]string{realtimeIdentificationMappingKey(localID)},
		recordedAt.UnixNano(),
		identificationCacheTTL.Milliseconds(),
	).StringSlice()
	if err != nil {
		return "", false, err
	}
	if len(result) != 2 {
		return "", false, nil
	}

	return result[0], result[1] != "missing", nil
}

func storeIdentificationMapping(ctx context.Context, localID string, journeyID string, recordedAt time.Time) {
	_, err := redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, realtimeIdentificationMappingKey(localID), map[string]interface{}{
			"journey_id":   journeyID,
			"last_updated": recordedAt.UnixNano(),
		})
		pipe.Expire(ctx, realtimeIdentificationMappingKey(localID), identificationCacheTTL)
		return nil
	})
	if err != nil {
		log.Warn().Err(err).Str("local_id", localID).Msg("Failed to store realtime identification mapping")
	}
}
