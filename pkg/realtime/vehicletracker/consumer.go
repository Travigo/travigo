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
	id int
}

func NewBatchConsumer(id int) *BatchConsumer {
	return &BatchConsumer{id: id}
}

func (consumer *BatchConsumer) Consume(batch rmq.Deliveries) {
	payloads := batch.Payloads()

	var locationEventOperations []mongo.WriteModel

	for _, payload := range payloads {
		var vehicleLocationEvent *VehicleLocationEvent
		if err := json.Unmarshal([]byte(payload), &vehicleLocationEvent); err != nil {
			if batchErrors := batch.Reject(); err != nil {
				for _, err := range batchErrors {
					log.Error().Err(err).Msg("Failed to reject realtime event")
				}
			}
		}

		identifiedJourneyID := identifyVehicle(vehicleLocationEvent)

		if identifiedJourneyID != "" {
			writeModel, _ := updateRealtimeJourney(identifiedJourneyID, vehicleLocationEvent)

			if writeModel != nil {
				locationEventOperations = append(locationEventOperations, writeModel)
			}
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

func identifyVehicle(vehicleLocationEvent *VehicleLocationEvent) string {
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
			journeyIdentifier := identifiers.SiriVM{
				IdentifyingInformation: vehicleLocationEvent.IdentifyingInformation,
			}
			journey, err = journeyIdentifier.IdentifyJourney()
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

func updateRealtimeJourney(journeyID string, vehicleLocationEvent *VehicleLocationEvent) (mongo.WriteModel, error) {
	currentTime := vehicleLocationEvent.RecordedAt

	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleLocationEvent.Timeframe, journeyID)
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

	var realtimeJourney *ctdf.RealtimeJourney
	var realtimeJourneyReliability ctdf.RealtimeJourneyReliabilityType

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

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

		journeyDate, _ := time.Parse("2006-01-02", vehicleLocationEvent.Timeframe)

		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier:      realtimeJourneyIdentifier,
			ActivelyTracked:        true,
			TimeoutDurationMinutes: 10,
			Journey:                journey,
			JourneyRunDate:         journeyDate,

			CreationDateTime: currentTime,
			DataSource:       vehicleLocationEvent.DataSource,

			VehicleRef: vehicleLocationEvent.VehicleIdentifier,
			Stops:      map[string]*ctdf.RealtimeJourneyStops{},
		}
		newRealtimeJourney = true
	}

	if realtimeJourney.Journey == nil {
		log.Error().Msg("RealtimeJourney without a Journey found, deleting")
		realtimeJourneysCollection.DeleteOne(context.Background(), searchQuery)
		return nil, nil
	}

	closestDistance := 999999999999.0
	var closestDistanceJourneyPath *ctdf.JourneyPathItem
	var closestDistanceJourneyPathIndex int
	var closestDistanceJourneyPathPercentComplete float64 // TODO: this is a hack, replace with actual distance

	// Attempt to calculate using closest journey track
	for i, journeyPathItem := range realtimeJourney.Journey.Path {
		journeyPathClosestDistance := 99999999999999.0 // TODO do this better

		for i := 0; i < len(journeyPathItem.Track)-1; i++ {
			a := journeyPathItem.Track[i]
			b := journeyPathItem.Track[i+1]

			distance := vehicleLocationEvent.Location.DistanceFromLine(a, b)

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

			distance := journeyPathItem.DestinationStop.Location.Distance(&vehicleLocationEvent.Location)

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

			previousJourneyPathDistance := previousJourneyPath.DestinationStop.Location.Distance(&vehicleLocationEvent.Location)

			closestDistanceJourneyPathPercentComplete = (1 + ((previousJourneyPathDistance - closestDistance) / (previousJourneyPathDistance + closestDistance))) / 2
		}

		realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityLocationWithoutTrack
	} else {
		realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityLocationWithTrack
	}

	// Calculate new stop arrival times
	realtimeTimeframe, err := time.Parse("2006-01-02", vehicleLocationEvent.Timeframe)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse realtime time frame")
	}

	if closestDistanceJourneyPath == nil {
		return nil, errors.New("nil closestdistancejourneypath")
	}

	// Get the arrival & departure times with date of the journey
	destinationArrivalTimeWithDate := time.Date(
		realtimeTimeframe.Year(),
		realtimeTimeframe.Month(),
		realtimeTimeframe.Day(),
		closestDistanceJourneyPath.DestinationArrivalTime.Hour(),
		closestDistanceJourneyPath.DestinationArrivalTime.Minute(),
		closestDistanceJourneyPath.DestinationArrivalTime.Second(),
		closestDistanceJourneyPath.DestinationArrivalTime.Nanosecond(),
		time.Local,
	)
	originDepartureTimeWithDate := time.Date(
		realtimeTimeframe.Year(),
		realtimeTimeframe.Month(),
		realtimeTimeframe.Day(),
		closestDistanceJourneyPath.OriginDepartureTime.Hour(),
		closestDistanceJourneyPath.OriginDepartureTime.Minute(),
		closestDistanceJourneyPath.OriginDepartureTime.Second(),
		closestDistanceJourneyPath.OriginDepartureTime.Nanosecond(),
		time.Local,
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
	offset := currentTime.Sub(currentPathPositionExpectedTime)

	// If the offset is too small then just turn it to zero so we can mark buses as on time
	if offset.Seconds() <= 45 {
		offset = time.Duration(0)
	}

	// Calculate all the estimated stop arrival & departure times
	journeyStopUpdates := map[string]*ctdf.RealtimeJourneyStops{}
	for i := closestDistanceJourneyPathIndex; i < len(realtimeJourney.Journey.Path); i++ {
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

	// TODO add back occupancy
	// // Calculate occupancy
	// if vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.SeatedOccupancy != 0 {
	// 	totalCapacity := vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.SeatedCapacity + vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.WheelchairCapacity
	// 	totalOccupancy := vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.SeatedOccupancy + vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.WheelchairOccupancy

	// 	realtimeJourney.Occupancy = ctdf.RealtimeJourneyOccupancy{
	// 		OccupancyAvailable: true,
	// 		ActualValues:       true,

	// 		Capacity:  totalCapacity,
	// 		Occupancy: totalOccupancy,

	// 		SeatedInformation: true,
	// 		SeatedCapacity:    vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.SeatedCapacity,
	// 		SeatedOccupancy:   vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.SeatedOccupancy,

	// 		WheelchairInformation: true,
	// 		WheelchairCapacity:    vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.WheelchairCapacity,
	// 		WheelchairOccupancy:   vehicleLocationEvent.SiriVMActivity.Extensions.VehicleJourney.WheelchairOccupancy,
	// 	}

	// 	if totalCapacity > 0 && totalOccupancy > 0 {
	// 		realtimeJourney.Occupancy.TotalPercentageOccupancy = int((float64(totalOccupancy) / float64(totalCapacity)) * 100)
	// 	}
	// } else if vehicleLocationEvent.SiriVMActivity.MonitoredVehicleJourney.Occupancy != "" {
	// 	realtimeJourney.Occupancy = ctdf.RealtimeJourneyOccupancy{
	// 		OccupancyAvailable: true,
	// 		ActualValues:       false,
	// 	}

	// 	switch vehicleLocationEvent.SiriVMActivity.MonitoredVehicleJourney.Occupancy {
	// 	case "full":
	// 		realtimeJourney.Occupancy.TotalPercentageOccupancy = 100
	// 	case "standingAvailable":
	// 		realtimeJourney.Occupancy.TotalPercentageOccupancy = 75
	// 	case "seatsAvailable":
	// 		realtimeJourney.Occupancy.TotalPercentageOccupancy = 40
	// 	}
	// }

	// Update database
	updateMap := bson.M{
		"reliability":          realtimeJourneyReliability,
		"modificationdatetime": currentTime,
		"vehiclelocation":      vehicleLocationEvent.Location,
		"vehiclebearing":       vehicleLocationEvent.Bearing,
		"departedstopref":      closestDistanceJourneyPath.OriginStopRef,
		"nextstopref":          closestDistanceJourneyPath.DestinationStopRef,
		"offset":               offset,
		"occupancy":            realtimeJourney.Occupancy,
		// "vehiclelocationdescription": fmt.Sprintf("Passed %s", closestDistanceJourneyPath.OriginStop.PrimaryName),
	}
	if newRealtimeJourney {
		updateMap["primaryidentifier"] = realtimeJourney.PrimaryIdentifier
		updateMap["activelytracked"] = realtimeJourney.ActivelyTracked
		updateMap["timeoutdurationminutes"] = realtimeJourney.TimeoutDurationMinutes

		updateMap["journey"] = realtimeJourney.Journey
		updateMap["journeyrundate"] = realtimeJourney.JourneyRunDate

		updateMap["creationdatetime"] = realtimeJourney.CreationDateTime
		updateMap["datasource"] = realtimeJourney.DataSource

		updateMap["vehicleref"] = vehicleLocationEvent.VehicleIdentifier
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
