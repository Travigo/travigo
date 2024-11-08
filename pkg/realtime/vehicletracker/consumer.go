package vehicletracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker/identifiers"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
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

	var locationEventOperations []mongo.WriteModel

	for _, payload := range payloads {
		var vehicleUpdateEvent *VehicleUpdateEvent
		if err := json.Unmarshal([]byte(payload), &vehicleUpdateEvent); err != nil {
			if batchErrors := batch.Reject(); err != nil {
				for _, err := range batchErrors {
					log.Error().Err(err).Msg("Failed to reject realtime event")
				}
			}
		}

		identifiedJourneyID := consumer.identifyVehicle(vehicleUpdateEvent)

		if identifiedJourneyID != "" {
			var writeModel mongo.WriteModel

			if vehicleUpdateEvent.MessageType == VehicleUpdateEventTypeTrip {
				writeModel, _ = consumer.updateRealtimeJourney(identifiedJourneyID, vehicleUpdateEvent)
			}

			if writeModel != nil {
				locationEventOperations = append(locationEventOperations, writeModel)
			}
		} else {
			log.Debug().Interface("event", vehicleUpdateEvent.IdentifyingInformation).Msg("Couldnt identify journey")
		}
	}

	if len(locationEventOperations) > 0 {
		realtimeJourneysCollection := database.GetCollection("realtime_journeys")

		startTime := time.Now()
		_, err := realtimeJourneysCollection.BulkWrite(context.Background(), locationEventOperations, &options.BulkWriteOptions{})
		log.Info().Int("Length", len(locationEventOperations)).Str("Time", time.Now().Sub(startTime).String()).Msg("Bulk write")

		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Realtime Journeys")
		}
	}

	if ackErrors := batch.Ack(); len(ackErrors) > 0 {
		for _, err := range ackErrors {
			log.Fatal().Err(err).Msg("Failed to consume realtime event")
		}
	}
}

func (consumer *BatchConsumer) identifyVehicle(vehicleLocationEvent *VehicleUpdateEvent) string {
	currentTime := time.Now()
	yearNumber, weekNumber := currentTime.ISOWeek()
	identifyEventsIndexName := fmt.Sprintf("realtime-identify-events-%d-%d", yearNumber, weekNumber)

	operatorRef := vehicleLocationEvent.IdentifyingInformation["OperatorRef"]

	var journeyID string

	cachedJourneyMapping, _ := identificationCache.Get(context.Background(), vehicleLocationEvent.LocalID)

	if cachedJourneyMapping == "" {
		var journey string
		var err error

		// TODO use an interface here to reduce duplication
		if vehicleLocationEvent.SourceType == "siri-vm" {
			// Save a cache value of N/A to stop us from constantly rechecking for journeys handled somewhere else
			successVehicleID, _ := identificationCache.Get(context.Background(), fmt.Sprintf("successvehicleid/%s/%s", vehicleLocationEvent.IdentifyingInformation["LinkedDataset"], vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier))
			if vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier != "" && successVehicleID != "" {
				identificationCache.Set(context.Background(), vehicleLocationEvent.LocalID, "N/A")
				return ""
			}

			// TODO only exists here if siri-vm only comes from the 1 source
			failedVehicleID, _ := identificationCache.Get(context.Background(), fmt.Sprintf("failedvehicleid/%s/%s", vehicleLocationEvent.IdentifyingInformation["LinkedDataset"], vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier))
			if vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier != "" && failedVehicleID == "" {
				return ""
			}

			// perform the actual sirivm
			journeyIdentifier := identifiers.SiriVM{
				IdentifyingInformation: vehicleLocationEvent.IdentifyingInformation,
			}
			journey, err = journeyIdentifier.IdentifyJourney()

			// TODO yet another special TfL only thing that shouldn't be here
			if err != nil && vehicleLocationEvent.IdentifyingInformation["OperatorRef"] == "gb-noc-TFLO" {
				tflEventBytes, _ := json.Marshal(map[string]string{
					"Line":                     vehicleLocationEvent.IdentifyingInformation["PublishedLineName"],
					"DirectionRef":             vehicleLocationEvent.IdentifyingInformation["DirectionRef"],
					"NumberPlate":              vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier,
					"OriginRef":                vehicleLocationEvent.IdentifyingInformation["OriginRef"],
					"DestinationRef":           vehicleLocationEvent.IdentifyingInformation["DestinationRef"],
					"OriginAimedDepartureTime": vehicleLocationEvent.IdentifyingInformation["OriginAimedDepartureTime"],
				})
				consumer.TfLBusQueue.PublishBytes(tflEventBytes)
			}
		} else if vehicleLocationEvent.SourceType == "GTFS-RT" {
			journeyIdentifier := identifiers.GTFSRT{
				IdentifyingInformation: vehicleLocationEvent.IdentifyingInformation,
			}
			journey, err = journeyIdentifier.IdentifyJourney()
		} else {
			log.Error().Str("sourcetype", vehicleLocationEvent.SourceType).Msg("Unknown sourcetype")
			return ""
		}

		if err != nil {
			// Save a cache value of N/A to stop us from constantly rechecking for journeys we cant identify
			identificationCache.Set(context.Background(), vehicleLocationEvent.LocalID, "N/A")

			// Set cross dataset ID
			if vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier != "" {
				identificationCache.Set(context.Background(), fmt.Sprintf("failedvehicleid/%s/%s", vehicleLocationEvent.IdentifyingInformation["LinkedDataset"], vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier), vehicleLocationEvent.SourceType)
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
				Service:  vehicleLocationEvent.IdentifyingInformation["PublishedLineName"],
				Trip:     vehicleLocationEvent.IdentifyingInformation["TripID"],

				SourceType: vehicleLocationEvent.SourceType,
			})

			elastic_client.IndexRequest(identifyEventsIndexName, bytes.NewReader(elasticEvent))

			return ""
		}
		journeyID = journey

		journeyMapJson, _ := json.Marshal(localJourneyIDMap{
			JourneyID:   journeyID,
			LastUpdated: vehicleLocationEvent.RecordedAt,
		})

		identificationCache.Set(context.Background(), vehicleLocationEvent.LocalID, string(journeyMapJson))

		// Set cross dataset ID
		if vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier != "" {
			identificationCache.Set(context.Background(), fmt.Sprintf("successvehicleid/%s/%s", vehicleLocationEvent.IdentifyingInformation["LinkedDataset"], vehicleLocationEvent.VehicleLocationUpdate.VehicleIdentifier), vehicleLocationEvent.SourceType)
		}

		// Record the successful identification event
		elasticEvent, _ := json.Marshal(RealtimeIdentifyFailureElasticEvent{
			Timestamp: currentTime,

			Success: true,

			Operator: operatorRef,
			Service:  vehicleLocationEvent.IdentifyingInformation["PublishedLineName"],
			Trip:     vehicleLocationEvent.IdentifyingInformation["TripID"],

			SourceType: vehicleLocationEvent.SourceType,
		})

		elastic_client.IndexRequest(identifyEventsIndexName, bytes.NewReader(elasticEvent))
	} else if cachedJourneyMapping == "N/A" {
		return ""
	} else {
		var journeyMap localJourneyIDMap
		json.Unmarshal([]byte(cachedJourneyMapping), &journeyMap)

		// skip this journey if hasnt changed
		if vehicleLocationEvent.RecordedAt.After(journeyMap.LastUpdated) {
			// Update the last updated time
			journeyMap.LastUpdated = vehicleLocationEvent.RecordedAt

			journeyMapJson, _ := json.Marshal(journeyMap)

			identificationCache.Set(context.Background(), vehicleLocationEvent.LocalID, string(journeyMapJson))
		} else {
			return ""
		}

		journeyID = journeyMap.JourneyID
	}

	return journeyID
}

func (consumer *BatchConsumer) updateRealtimeJourney(journeyID string, vehicleUpdateEvent *VehicleUpdateEvent) (mongo.WriteModel, error) {
	currentTime := vehicleUpdateEvent.RecordedAt

	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleUpdateEvent.VehicleLocationUpdate.Timeframe, journeyID)
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

	var realtimeJourney *ctdf.RealtimeJourney
	var realtimeJourneyReliability ctdf.RealtimeJourneyReliabilityType

	opts := options.FindOne().SetProjection(bson.D{
		{Key: "journey.path", Value: 1},
		{Key: "journey.departuretimezone", Value: 1},
		{Key: "nextstopref", Value: 1},
		{Key: "offset", Value: 1},
	})

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeJourneysCollection.FindOne(context.Background(), searchQuery, opts).Decode(&realtimeJourney)

	newRealtimeJourney := false
	if realtimeJourney == nil {
		var journey *ctdf.Journey
		journeysCollection := database.GetCollection("journeys")
		err := journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": journeyID}).Decode(&journey)

		if err != nil {
			return nil, err
		}

		for _, pathItem := range journey.Path {
			pathItem.GetDestinationStop()
		}

		journey.GetService()

		journeyDate, _ := time.Parse("2006-01-02", vehicleUpdateEvent.VehicleLocationUpdate.Timeframe)

		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier:      realtimeJourneyIdentifier,
			ActivelyTracked:        true,
			TimeoutDurationMinutes: 10,
			Journey:                journey,
			JourneyRunDate:         journeyDate,
			Service:                journey.Service,

			CreationDateTime: currentTime,

			VehicleRef: vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier,
			Stops:      map[string]*ctdf.RealtimeJourneyStops{},
		}
		newRealtimeJourney = true
	}

	if realtimeJourney.Journey == nil {
		log.Error().Msg("RealtimeJourney without a Journey found, deleting")
		realtimeJourneysCollection.DeleteOne(context.Background(), searchQuery)
		return nil, errors.New("RealtimeJourney without a Journey found, deleting")
	}

	var offset time.Duration
	journeyStopUpdates := map[string]*ctdf.RealtimeJourneyStops{}
	var closestDistanceJourneyPath *ctdf.JourneyPathItem // TODO maybe not here?

	// Calculate everything based on location if we aren't provided with updates
	if len(vehicleUpdateEvent.VehicleLocationUpdate.StopUpdates) == 0 && vehicleUpdateEvent.VehicleLocationUpdate.Location.Type == "Point" {
		closestDistance := 999999999999.0
		var closestDistanceJourneyPathIndex int
		var closestDistanceJourneyPathPercentComplete float64 // TODO: this is a hack, replace with actual distance

		// Attempt to calculate using closest journey track
		for i, journeyPathItem := range realtimeJourney.Journey.Path {
			journeyPathClosestDistance := 99999999999999.0 // TODO do this better

			for i := 0; i < len(journeyPathItem.Track)-1; i++ {
				a := journeyPathItem.Track[i]
				b := journeyPathItem.Track[i+1]

				distance := vehicleUpdateEvent.VehicleLocationUpdate.Location.DistanceFromLine(a, b)

				if distance < journeyPathClosestDistance {
					journeyPathClosestDistance = distance
				}
			}

			if journeyPathClosestDistance < closestDistance {
				closestDistance = journeyPathClosestDistance
				closestDistanceJourneyPath = journeyPathItem
				closestDistanceJourneyPathIndex = i

				// TODO: this is a hack, replace with actual distance
				// this is a rough estimation based on what part of path item track we are on
				closestDistanceJourneyPathPercentComplete = float64(i) / float64(len(journeyPathItem.Track))
			}
		}

		// If we fail to identify closest journey path item using track use fallback stop location method
		if closestDistanceJourneyPath == nil {
			closestDistance = 999999999999.0
			for i, journeyPathItem := range realtimeJourney.Journey.Path {
				if journeyPathItem.DestinationStop == nil {
					return nil, errors.New(fmt.Sprintf("Cannot get stop %s", journeyPathItem.DestinationStopRef))
				}

				distance := journeyPathItem.DestinationStop.Location.Distance(&vehicleUpdateEvent.VehicleLocationUpdate.Location)

				if distance < closestDistance {
					closestDistance = distance
					closestDistanceJourneyPath = journeyPathItem
					closestDistanceJourneyPathIndex = i
				}
			}

			if closestDistanceJourneyPathIndex == 0 {
				// TODO this seems a bit hacky but I dont think we care much if we're on the first item
				closestDistanceJourneyPathPercentComplete = 0.5
			} else {
				previousJourneyPath := realtimeJourney.Journey.Path[len(realtimeJourney.Journey.Path)-1]

				if previousJourneyPath.DestinationStop == nil {
					return nil, errors.New(fmt.Sprintf("Cannot get stop %s", previousJourneyPath.DestinationStopRef))
				}

				previousJourneyPathDistance := previousJourneyPath.DestinationStop.Location.Distance(&vehicleUpdateEvent.VehicleLocationUpdate.Location)

				closestDistanceJourneyPathPercentComplete = (1 + ((previousJourneyPathDistance - closestDistance) / (previousJourneyPathDistance + closestDistance))) / 2
			}

			realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityLocationWithoutTrack
		} else {
			realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityLocationWithTrack
		}

		// Calculate new stop arrival times
		realtimeTimeframe, err := time.Parse("2006-01-02", vehicleUpdateEvent.VehicleLocationUpdate.Timeframe)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse realtime time frame")
		}

		if closestDistanceJourneyPath == nil {
			return nil, errors.New("nil closestdistancejourneypath")
		}

		journeyTimezone, _ := time.LoadLocation(realtimeJourney.Journey.DepartureTimezone)

		// Get the arrival & departure times with date of the journey
		destinationArrivalTimeWithDate := time.Date(
			realtimeTimeframe.Year(),
			realtimeTimeframe.Month(),
			realtimeTimeframe.Day(),
			closestDistanceJourneyPath.DestinationArrivalTime.Hour(),
			closestDistanceJourneyPath.DestinationArrivalTime.Minute(),
			closestDistanceJourneyPath.DestinationArrivalTime.Second(),
			closestDistanceJourneyPath.DestinationArrivalTime.Nanosecond(),
			journeyTimezone,
		)
		originDepartureTimeWithDate := time.Date(
			realtimeTimeframe.Year(),
			realtimeTimeframe.Month(),
			realtimeTimeframe.Day(),
			closestDistanceJourneyPath.OriginDepartureTime.Hour(),
			closestDistanceJourneyPath.OriginDepartureTime.Minute(),
			closestDistanceJourneyPath.OriginDepartureTime.Second(),
			closestDistanceJourneyPath.OriginDepartureTime.Nanosecond(),
			journeyTimezone,
		)

		// How long it take to travel between origin & destination
		currentPathTraversalTime := destinationArrivalTimeWithDate.Sub(originDepartureTimeWithDate)

		// How far we are between origin & departure (% of journey path, NOT time or metres)
		// TODO: this is a hack, replace with actual distance
		currentPathPercentageComplete := closestDistanceJourneyPathPercentComplete

		// Calculate what the expected time of the current position of the vehicle should be
		currentPathPositionExpectedTime := originDepartureTimeWithDate.Add(
			time.Duration(int(currentPathPercentageComplete * float64(currentPathTraversalTime.Nanoseconds()))))

		// Offset is how far behind or ahead the vehicle is from its positions expected time
		offset = currentTime.Sub(currentPathPositionExpectedTime).Round(10 * time.Second)

		// If the offset is too small then just turn it to zero so we can mark buses as on time
		if offset.Seconds() <= 45 {
			offset = time.Duration(0)
		}

		// Calculate all the estimated stop arrival & departure times
		for i := closestDistanceJourneyPathIndex; i < len(realtimeJourney.Journey.Path); i++ {
			// Don't update the database if theres no actual change
			if (offset.Seconds() == realtimeJourney.Offset.Seconds()) && !newRealtimeJourney {
				break
			}

			path := realtimeJourney.Journey.Path[i]

			arrivalTime := path.DestinationArrivalTime.Add(offset).Round(time.Minute)
			var departureTime time.Time

			if i < len(realtimeJourney.Journey.Path)-1 {
				nextPath := realtimeJourney.Journey.Path[i+1]

				if arrivalTime.Before(nextPath.OriginDepartureTime) {
					departureTime = nextPath.OriginDepartureTime
				} else {
					departureTime = arrivalTime
				}
			}

			journeyStopUpdates[path.DestinationStopRef] = &ctdf.RealtimeJourneyStops{
				StopRef:  path.DestinationStopRef,
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,

				ArrivalTime:   arrivalTime,
				DepartureTime: departureTime,
			}
		}
	} else {
		for _, stopUpdate := range vehicleUpdateEvent.VehicleLocationUpdate.StopUpdates {
			arrivalTime := stopUpdate.ArrivalTime
			departureTime := stopUpdate.DepartureTime

			if arrivalTime.Year() == 1970 {
				for _, path := range realtimeJourney.Journey.Path {
					if path.OriginStopRef == stopUpdate.StopID {
						arrivalTime = path.OriginArrivalTime.Add(time.Duration(stopUpdate.ArrivalOffset) * time.Second)
						break
					}
				}
			}
			if departureTime.Year() == 1970 {
				for _, path := range realtimeJourney.Journey.Path {
					if path.OriginStopRef == stopUpdate.StopID {
						departureTime = path.OriginDepartureTime.Add(time.Duration(stopUpdate.DepartureOffset) * time.Second)
						break
					}
				}
			}

			journeyStopUpdates[stopUpdate.StopID] = &ctdf.RealtimeJourneyStops{
				StopRef:  stopUpdate.StopID,
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,

				ArrivalTime:   arrivalTime,
				DepartureTime: departureTime,
			}
		}

		closestPathTime := 9999999 * time.Minute
		now := time.Now()
		realtimeTimeframe, err := time.Parse("2006-01-02", vehicleUpdateEvent.VehicleLocationUpdate.Timeframe)

		journeyTimezone, _ := time.LoadLocation(realtimeJourney.Journey.DepartureTimezone)

		if err != nil {
			log.Error().Err(err).Msg("Failed to parse realtime time frame")
		}
		for _, path := range realtimeJourney.Journey.Path {
			refTime := time.Date(
				realtimeTimeframe.Year(),
				realtimeTimeframe.Month(),
				realtimeTimeframe.Day(),
				path.OriginArrivalTime.Hour(),
				path.OriginArrivalTime.Minute(),
				path.OriginArrivalTime.Second(),
				path.OriginArrivalTime.Nanosecond(),
				journeyTimezone,
			)

			if journeyStopUpdates[path.OriginStopRef] != nil {
				refTime = journeyStopUpdates[path.OriginStopRef].ArrivalTime
			}

			if refTime.Before(now) && now.Sub(refTime) < closestPathTime {
				closestDistanceJourneyPath = path

				closestPathTime = now.Sub(refTime)
			}
		}
	}

	if closestDistanceJourneyPath == nil {
		return nil, errors.New("unable to find next journeypath")
	}

	// Update database
	updateMap := bson.M{
		"modificationdatetime": currentTime,
		"vehiclebearing":       vehicleUpdateEvent.VehicleLocationUpdate.Bearing,
		"departedstopref":      closestDistanceJourneyPath.OriginStopRef,
		"nextstopref":          closestDistanceJourneyPath.DestinationStopRef,
		"occupancy":            vehicleUpdateEvent.VehicleLocationUpdate.Occupancy,
		// "vehiclelocationdescription": fmt.Sprintf("Passed %s", closestDistanceJourneyPath.OriginStop.PrimaryName),
	}
	if vehicleUpdateEvent.VehicleLocationUpdate.Location.Type != "" {
		updateMap["vehiclelocation"] = vehicleUpdateEvent.VehicleLocationUpdate.Location
	}
	if newRealtimeJourney {
		updateMap["primaryidentifier"] = realtimeJourney.PrimaryIdentifier
		updateMap["activelytracked"] = realtimeJourney.ActivelyTracked
		updateMap["timeoutdurationminutes"] = realtimeJourney.TimeoutDurationMinutes

		updateMap["journey"] = realtimeJourney.Journey
		updateMap["journeyrundate"] = realtimeJourney.JourneyRunDate

		updateMap["service"] = realtimeJourney.Service

		updateMap["creationdatetime"] = realtimeJourney.CreationDateTime

		updateMap["vehicleref"] = vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier
		updateMap["datasource"] = vehicleUpdateEvent.DataSource

		updateMap["reliability"] = realtimeJourneyReliability
	} else {
		updateMap["datasource.timestamp"] = vehicleUpdateEvent.DataSource.Timestamp
	}

	if (offset.Seconds() != realtimeJourney.Offset.Seconds()) || newRealtimeJourney {
		updateMap["offset"] = offset
	}

	if realtimeJourney.NextStopRef != closestDistanceJourneyPath.DestinationStopRef {
		journeyStopUpdates[realtimeJourney.NextStopRef] = &ctdf.RealtimeJourneyStops{
			StopRef:  realtimeJourney.NextStopRef,
			TimeType: ctdf.RealtimeJourneyStopTimeHistorical,

			// TODO this should obviously be a different time
			ArrivalTime:   currentTime,
			DepartureTime: currentTime,
		}
	}

	for key, stopUpdate := range journeyStopUpdates {
		if key != "" {
			updateMap[fmt.Sprintf("stops.%s", key)] = stopUpdate
		}
	}

	bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(searchQuery)
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	return updateModel, nil
}
