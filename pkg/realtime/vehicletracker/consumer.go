package vehicletracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
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

type localJourneyIDMap struct {
	JourneyID   string
	LastUpdated time.Time
}

func (j localJourneyIDMap) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}

func CreateIdentificationCache() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(90*time.Minute))

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
	id          int
	TfLBusQueue rmq.Queue
}

func NewBatchConsumer(id int) *BatchConsumer {
	tfLBusQueue, err := redis_client.QueueConnection.OpenQueue("tfl-bus-queue")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to start notify queue")
	}

	return &BatchConsumer{id: id, TfLBusQueue: tfLBusQueue}
}

func (consumer *BatchConsumer) Consume(batch rmq.Deliveries) {
	payloads := batch.Payloads()

	var realtimeJourneyOperations []mongo.WriteModel
	var serviceAlertOperations []mongo.WriteModel

	for _, payload := range payloads {
		var vehicleUpdateEvent *VehicleUpdateEvent
		if err := json.Unmarshal([]byte(payload), &vehicleUpdateEvent); err != nil {
			if batchErrors := batch.Reject(); err != nil {
				for _, err := range batchErrors {
					log.Error().Err(err).Msg("Failed to reject realtime event")
				}
			}
		}

		if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeTrip {
			identifiedJourneyID := consumer.identifyVehicle(vehicleUpdateEvent)

			if identifiedJourneyID != "" {
				writeModel, _ := consumer.updateRealtimeJourney(identifiedJourneyID, vehicleUpdateEvent)

				if writeModel != nil {
					realtimeJourneyOperations = append(realtimeJourneyOperations, writeModel)
				}
			} else {
				log.Debug().Interface("event", vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation).Msg("Couldnt identify journey")
			}
		} else if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeServiceAlert {
			identifiedJourneyID := consumer.identifyVehicle(vehicleUpdateEvent)
			identifiedStopID := consumer.identifyStop(vehicleUpdateEvent)
			identifiedServiceID := consumer.identifyService(vehicleUpdateEvent)

			writeModel, _ := consumer.updateServiceAlert(identifiedJourneyID, identifiedStopID, identifiedServiceID, vehicleUpdateEvent)
			if writeModel != nil {
				serviceAlertOperations = append(serviceAlertOperations, writeModel)
			}
		}
	}

	if len(realtimeJourneyOperations) > 0 {
		realtimeJourneysCollection := database.GetCollection("realtime_journeys")

		startTime := time.Now()
		_, err := realtimeJourneysCollection.BulkWrite(context.Background(), realtimeJourneyOperations, &options.BulkWriteOptions{})
		log.Info().Int("Length", len(realtimeJourneyOperations)).Str("Time", time.Now().Sub(startTime).String()).Msg("Bulk write realtime_journeys")

		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Realtime Journeys")
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

func (consumer *BatchConsumer) identifyStop(vehicleUpdateEvent *VehicleUpdateEvent) string {
	if vehicleUpdateEvent.SourceType == "GTFS-RT" {
		stopIdentifier := identifiers.GTFSRT{
			IdentifyingInformation: vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation,
		}
		stop, err := stopIdentifier.IdentifyStop()

		if err != nil {
			return ""
		}

		return stop
	}

	return ""
}

func (consumer *BatchConsumer) identifyService(vehicleUpdateEvent *VehicleUpdateEvent) string {
	if vehicleUpdateEvent.SourceType == "GTFS-RT" {
		serviceIdentifier := identifiers.GTFSRT{
			IdentifyingInformation: vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation,
		}
		service, err := serviceIdentifier.IdentifyService()

		if err != nil {
			return ""
		}

		return service
	}

	return ""
}

func (consumer *BatchConsumer) identifyVehicle(vehicleUpdateEvent *VehicleUpdateEvent) string {
	currentTime := time.Now()
	yearNumber, weekNumber := currentTime.ISOWeek()
	identifyEventsIndexName := fmt.Sprintf("realtime-identify-events-%d-%d", yearNumber, weekNumber)

	operatorRef := vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["OperatorRef"]

	var journeyID string

	cachedJourneyMapping, _ := identificationCache.Get(context.Background(), vehicleUpdateEvent.LocalID)

	if cachedJourneyMapping == "" {
		var journey string
		var err error

		// TODO use an interface here to reduce duplication
		if vehicleUpdateEvent.SourceType == "siri-vm" {
			// Save a cache value of N/A to stop us from constantly rechecking for journeys handled somewhere else
			successVehicleID, _ := identificationCache.Get(context.Background(), fmt.Sprintf("successvehicleid/%s/%s", vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["LinkedDataset"], vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier))
			if vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier != "" && successVehicleID != "" {
				identificationCache.Set(context.Background(), vehicleUpdateEvent.LocalID, "N/A")
				return ""
			}

			// TODO only exists here if siri-vm only comes from the 1 source
			failedVehicleID, _ := identificationCache.Get(context.Background(), fmt.Sprintf("failedvehicleid/%s/%s", vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["LinkedDataset"], vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier))
			if vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier != "" && failedVehicleID == "" {
				return ""
			}

			// perform the actual sirivm
			journeyIdentifier := identifiers.SiriVM{
				IdentifyingInformation: vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation,
			}
			journey, err = journeyIdentifier.IdentifyJourney()

			// TODO yet another special TfL only thing that shouldn't be here
			if err != nil && vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["OperatorRef"] == "gb-noc-TFLO" {
				tflEventBytes, _ := json.Marshal(map[string]string{
					"Line":                     vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["PublishedLineName"],
					"DirectionRef":             vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["DirectionRef"],
					"NumberPlate":              vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier,
					"OriginRef":                vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["OriginRef"],
					"DestinationRef":           vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["DestinationRef"],
					"OriginAimedDepartureTime": vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["OriginAimedDepartureTime"],
				})
				consumer.TfLBusQueue.PublishBytes(tflEventBytes)
			}
		} else if vehicleUpdateEvent.SourceType == "GTFS-RT" {
			journeyIdentifier := identifiers.GTFSRT{
				IdentifyingInformation: vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation,
			}
			journey, err = journeyIdentifier.IdentifyJourney()
		} else {
			log.Error().Str("sourcetype", vehicleUpdateEvent.SourceType).Msg("Unknown sourcetype")
			return ""
		}

		if err != nil {
			// Save a cache value of N/A to stop us from constantly rechecking for journeys we cant identify
			identificationCache.Set(context.Background(), vehicleUpdateEvent.LocalID, "N/A")

			// Set cross dataset ID
			if vehicleUpdateEvent.VehicleLocationUpdate != nil && vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier != "" {
				identificationCache.Set(context.Background(), fmt.Sprintf("failedvehicleid/%s/%s", vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["LinkedDataset"], vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier), vehicleUpdateEvent.SourceType)
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

				Operator: operatorRef,
				Service:  vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["PublishedLineName"],
				Trip:     vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["TripID"],

				SourceType: vehicleUpdateEvent.SourceType,
			})

			elastic_client.IndexRequest(identifyEventsIndexName, bytes.NewReader(elasticEvent))

			return ""
		}
		journeyID = journey

		journeyMapJson, _ := json.Marshal(localJourneyIDMap{
			JourneyID:   journeyID,
			LastUpdated: vehicleUpdateEvent.RecordedAt,
		})

		identificationCache.Set(context.Background(), vehicleUpdateEvent.LocalID, string(journeyMapJson))

		// Set cross dataset ID
		if vehicleUpdateEvent.VehicleLocationUpdate != nil && vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier != "" {
			identificationCache.Set(context.Background(), fmt.Sprintf("successvehicleid/%s/%s", vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["LinkedDataset"], vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier), vehicleUpdateEvent.SourceType)
		}

		// Record the successful identification event
		elasticEvent, _ := json.Marshal(RealtimeIdentifyFailureElasticEvent{
			Timestamp: currentTime,

			Success: true,

			Operator: operatorRef,
			Service:  vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["PublishedLineName"],
			Trip:     vehicleUpdateEvent.VehicleLocationUpdate.IdentifyingInformation["TripID"],

			SourceType: vehicleUpdateEvent.SourceType,
		})

		elastic_client.IndexRequest(identifyEventsIndexName, bytes.NewReader(elasticEvent))
	} else if cachedJourneyMapping == "N/A" {
		return ""
	} else {
		var journeyMap localJourneyIDMap
		json.Unmarshal([]byte(cachedJourneyMapping), &journeyMap)

		// skip this journey if hasnt changed
		if vehicleUpdateEvent.RecordedAt.After(journeyMap.LastUpdated) {
			// Update the last updated time
			journeyMap.LastUpdated = vehicleUpdateEvent.RecordedAt

			journeyMapJson, _ := json.Marshal(journeyMap)

			identificationCache.Set(context.Background(), vehicleUpdateEvent.LocalID, string(journeyMapJson))
		} else {
			return ""
		}

		journeyID = journeyMap.JourneyID
	}

	return journeyID
}
