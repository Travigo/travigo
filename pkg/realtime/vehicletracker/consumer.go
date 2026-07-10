package vehicletracker

import (
	"bytes"
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
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker/identifiers"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var identificationCache *cache.Cache[string]

const numConsumers = 5
const batchSize = 200
const identificationCacheTTL = 90 * time.Minute

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
	id              int
	TfLBusQueue     rmq.Queue
	journeyCacheMu  sync.RWMutex
	journeyCache    map[string]*cachedTrackedJourney
	locationCacheMu sync.RWMutex
	locationCache   map[string]*time.Location
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
	payloads := batch.Payloads()

	serviceAlertOperations := make([]mongo.WriteModel, 0, len(payloads))

	for _, payload := range payloads {
		var vehicleUpdateEvent *VehicleUpdateEvent
		if err := json.Unmarshal([]byte(payload), &vehicleUpdateEvent); err != nil || vehicleUpdateEvent == nil {
			// The whole batch is acknowledged below. A malformed payload cannot
			// become processable through retry, so retaining it in rmq's
			// non-expiring rejected list only leaks Redis memory.
			log.Warn().Err(err).Msg("Discarding malformed realtime event")
			continue
		}

		if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeTrip || vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeLocationOnly {
			identifiedJourneyID := consumer.identifyVehicle(vehicleUpdateEvent, vehicleUpdateEvent.SourceType, vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation)

			if identifiedJourneyID != "" {
				if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeTrip {
					err := consumer.updateRealtimeJourney(identifiedJourneyID, vehicleUpdateEvent)
					if err != nil {
						log.Error().Err(err).Msg("Failed to update realtime journey")
					}
				} else if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeLocationOnly {
					err := consumer.updateRealtimeJourneyLocationOnly(identifiedJourneyID, vehicleUpdateEvent)
					if err != nil {
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

	if ackErrors := batch.Ack(); len(ackErrors) > 0 {
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
	currentTime := time.Now()
	yearNumber, weekNumber := currentTime.ISOWeek()
	identifyEventsIndexName := fmt.Sprintf("realtime-identify-events-%d-%d", yearNumber, weekNumber)

	operatorRef := identifyingInformation["OperatorRef"]

	journeyID, mappingFound, err := touchExistingIdentificationMapping(context.Background(), vehicleUpdateEvent.LocalID, vehicleUpdateEvent.RecordedAt)
	if err != nil {
		log.Warn().Err(err).Str("local_id", vehicleUpdateEvent.LocalID).Msg("Failed to read realtime identification mapping")
	}
	if mappingFound {
		consumer.recordSiriIdentificationOutcome(vehicleUpdateEvent, identifyingInformation, journeyID != "" && journeyID != "N/A", "cached")
		return journeyID
	}

	{
		var journey string
		var err error

		// TODO use an interface here to reduce duplication
		if sourceType == "siri-vm" {
			// Save a cache value of N/A to stop us from constantly rechecking for journeys handled somewhere else
			successVehicleID, _ := identificationCache.Get(context.Background(), fmt.Sprintf("successvehicleid/%s/%s", identifyingInformation["LinkedDataset"], vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier))
			if vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier != "" && successVehicleID != "" {
				storeIdentificationMapping(context.Background(), vehicleUpdateEvent.LocalID, "N/A", vehicleUpdateEvent.RecordedAt)
				consumer.recordSiriIdentificationOutcome(vehicleUpdateEvent, identifyingInformation, false, "suppressed_by_gtfs")
				return ""
			}

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
			elasticEvent, _ := json.Marshal(RealtimeIdentifyFailureElasticEvent{
				Timestamp: time.Now(),

				Success:    false,
				FailReason: errorCode,
				Outcome:    "unmatched",

				Operator: operatorRef,
				Service:  identifyingInformation["PublishedLineName"],
				Trip:     identifyingInformation["TripID"],

				SourceType: sourceType,
			})

			elastic_client.IndexRequest(identifyEventsIndexName, bytes.NewReader(elasticEvent))

			return ""
		}
		journeyID = journey

		storeIdentificationMapping(context.Background(), vehicleUpdateEvent.LocalID, journeyID, vehicleUpdateEvent.RecordedAt)

		// Set cross dataset ID
		if vehicleUpdateEvent.VehicleLocationUpdate != nil && vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier != "" {
			identificationCache.Set(context.Background(), fmt.Sprintf("successvehicleid/%s/%s", identifyingInformation["LinkedDataset"], vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier), sourceType)
		}

		// Record the successful identification event
		elasticEvent, _ := json.Marshal(RealtimeIdentifyFailureElasticEvent{
			Timestamp: currentTime,

			Success: true,
			Outcome: "matched",

			Operator: operatorRef,
			Service:  identifyingInformation["PublishedLineName"],
			Trip:     identifyingInformation["TripID"],

			SourceType: sourceType,
		})

		elastic_client.IndexRequest(identifyEventsIndexName, bytes.NewReader(elasticEvent))
	}

	return journeyID
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
	payload, err := json.Marshal(RealtimeIdentifyFailureElasticEvent{
		Timestamp: event.RecordedAt, Success: success, Outcome: outcome,
		Operator: information["OperatorRef"], Service: information["PublishedLineName"], Trip: information["VehicleJourneyRef"], SourceType: event.SourceType,
	})
	if err == nil {
		elastic_client.IndexRequest(index, bytes.NewReader(payload))
	}
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
