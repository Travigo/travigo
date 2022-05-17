package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/elastic_client"
	"github.com/britbus/britbus/pkg/redis_client"
	"github.com/britbus/britbus/pkg/siri_vm"
	"github.com/eko/gocache/v2/cache"
	"github.com/eko/gocache/v2/store"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

var journeyCache *cache.Cache
var identificationCache *cache.Cache
var cacheExpirationTime = 90 * time.Minute

const numConsumers = 5

type localJourneyIDMap struct {
	JourneyID   string
	LastUpdated string
}

func (j localJourneyIDMap) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}

func CreateIdentificationCache() {
	redisStore := store.NewRedis(redis_client.Client, &store.Options{
		Expiration: cacheExpirationTime,
	})

	identificationCache = cache.New(redisStore)
}
func CreateJourneyCache() {
	redisStore := store.NewRedis(redis_client.Client, &store.Options{
		Expiration: cacheExpirationTime,
	})

	journeyCache = cache.New(redisStore)
}
func StartConsumers() {
	// Create Cache
	CreateIdentificationCache()
	CreateJourneyCache()

	// Mongo indexes
	//TODO: Doesnt really make sense for this package to be managing indexes
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	_, err := realtimeJourneysCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err)
	}

	// Start the background consumers
	log.Info().Msg("Starting realtime consumers")

	queue, err := redis_client.QueueConnection.OpenQueue("realtime-queue")
	if err != nil {
		panic(err)
	}
	if err := queue.StartConsuming(numConsumers*200, 1*time.Second); err != nil {
		panic(err)
	}

	for i := 0; i < numConsumers; i++ {
		go startRealtimeConsumer(queue, i)
	}
}
func startRealtimeConsumer(queue rmq.Queue, id int) {
	log.Info().Msgf("Starting realtime consumer %d", id)

	if _, err := queue.AddBatchConsumer(fmt.Sprintf("realtime-queue-%d", id), 200, 2*time.Second, NewBatchConsumer(id)); err != nil {
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

	maxBatchSize := 10
	numBatches := int(math.Ceil(float64(len(payloads)) / float64(maxBatchSize)))

	processingGroup := sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(payloads) {
			upper = len(payloads)
		}

		batchSlice := payloads[lower:upper]

		go func(payloads []string) {
			locationEventOperations := []mongo.WriteModel{}

			for _, payload := range payloads {
				var vehicleIdentificationEvent *siri_vm.SiriVMVehicleIdentificationEvent
				if err := json.Unmarshal([]byte(payload), &vehicleIdentificationEvent); err != nil {
					if errors := batch.Reject(); err != nil {
						for _, err := range errors {
							log.Error().Err(err).Msg("Failed to reject realtime event")
						}
					}
				}

				vehicleLocationEvent := identifyVehicle(vehicleIdentificationEvent)

				if vehicleLocationEvent != nil {
					writeModel, _ := updateRealtimeJourney(vehicleLocationEvent)
					if writeModel != nil {
						locationEventOperations = append(locationEventOperations, writeModel)
					}
				}
			}

			if len(locationEventOperations) > 0 {
				realtimeJourneysCollection := database.GetCollection("realtime_journeys")
				_, err := realtimeJourneysCollection.BulkWrite(context.TODO(), locationEventOperations, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write Realtime Journeys")
				}
			}
		}(batchSlice)
	}

	if errors := batch.Ack(); len(errors) > 0 {
		for _, err := range errors {
			log.Error().Err(err).Msg("Failed to consume realtime event")
		}
	}
}

func identifyVehicle(siriVMVehicleIdentificationEvent *siri_vm.SiriVMVehicleIdentificationEvent) *VehicleLocationEvent {
	vehicle := siriVMVehicleIdentificationEvent.VehicleActivity
	vehicleJourneyRef := vehicle.MonitoredVehicleJourney.VehicleJourneyRef

	if vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef != "" {
		vehicleJourneyRef = vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef
	}

	// Temporary remap of known incorrect values
	// TODO: A better way fof doing this should be done under https://github.com/BritBus/britbus/issues/46
	operatorRef := vehicle.MonitoredVehicleJourney.OperatorRef

	switch operatorRef {
	case "SCSO":
		// Stagecoach south (GB:NOCID:137728)
		operatorRef = "SCCO"
	case "CT4N":
		// CT4n (GB:NOCID:137286)
		operatorRef = "NOCT"
	case "SCEM":
		// Stagecoach East Midlands (GB:NOCID:136971)
		operatorRef = "SCGR"
	case "UNO":
		// Uno (GB:NOCID:137967)
		operatorRef = "UNOE"
	case "BC", "WA", "WB", "WN", "CV", "PB", "YW", "AG", "PN":
		// National Express West Midlands (GB:NOCID:138032)
		operatorRef = "TCVW"
	}

	localJourneyID := fmt.Sprintf(
		"SIRI-VM:LOCALJOURNEYID:%s:%s:%s:%s",
		fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
		vehicle.MonitoredVehicleJourney.LineRef,
		fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef),
		vehicleJourneyRef,
	)

	var journeyID string

	cachedJourneyMapping, _ := identificationCache.Get(context.Background(), localJourneyID)

	if cachedJourneyMapping == nil || cachedJourneyMapping == "" {
		journey, err := ctdf.IdentifyJourney(map[string]string{
			"ServiceNameRef":           vehicle.MonitoredVehicleJourney.LineRef,
			"DirectionRef":             vehicle.MonitoredVehicleJourney.DirectionRef,
			"PublishedLineName":        vehicle.MonitoredVehicleJourney.PublishedLineName,
			"OperatorRef":              fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
			"VehicleJourneyRef":        vehicleJourneyRef,
			"OriginRef":                fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef),
			"DestinationRef":           fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.DestinationRef),
			"OriginAimedDepartureTime": vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime,
			"FramedVehicleJourneyDate": vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef,
		})

		if err != nil {
			// log.Error().Err(err).Str("localjourneyid", localJourneyID).Msgf("Could not find Journey")

			// Save a cache value of N/A to stop us from constantly rechecking for journeys we cant identify
			identificationCache.Set(context.Background(), localJourneyID, "N/A", nil)

			// Temporary https://github.com/BritBus/britbus/issues/43
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
			}

			// Record the failed identification event
			elasticEvent, _ := json.Marshal(RealtimeIdentifyFailureElasticEvent{
				Timestamp: time.Now(),

				Success:    false,
				FailReason: errorCode,

				Operator: fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
				Service:  vehicle.MonitoredVehicleJourney.PublishedLineName,
			})

			elastic_client.IndexRequest(&esapi.IndexRequest{
				Index:   "realtime-identify-events-1",
				Body:    bytes.NewReader(elasticEvent),
				Refresh: "true",
			})

			return nil
		}
		journeyID = journey.PrimaryIdentifier

		identificationCache.Set(context.Background(), localJourneyID, localJourneyIDMap{
			JourneyID:   journeyID,
			LastUpdated: vehicle.RecordedAtTime,
		}, nil)

		// Record the successful identification event
		elasticEvent, _ := json.Marshal(RealtimeIdentifyFailureElasticEvent{
			Timestamp: time.Now(),

			Success: true,

			Operator: fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
			Service:  vehicle.MonitoredVehicleJourney.PublishedLineName,
		})

		elastic_client.IndexRequest(&esapi.IndexRequest{
			Index:   "realtime-identify-events-1",
			Body:    bytes.NewReader(elasticEvent),
			Refresh: "true",
		})
	} else if cachedJourneyMapping == "N/A" {
		return nil
	} else {
		var journeyMap localJourneyIDMap
		json.Unmarshal([]byte(cachedJourneyMapping.(string)), &journeyMap)

		cachedLastUpdated, err := time.Parse(ctdf.XSDDateTimeFormat, journeyMap.LastUpdated)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse cached journeyMap.LastUpdated time")
		}
		vehicleLastUpdated, err := time.Parse(ctdf.XSDDateTimeFormat, vehicle.RecordedAtTime)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse vehicle.RecordedAtTime time")
		}

		// skip this journey if hasnt changed
		if vehicleLastUpdated.After(cachedLastUpdated) {
			// Update the last updated time
			journeyMap.LastUpdated = vehicle.RecordedAtTime
			identificationCache.Set(context.Background(), localJourneyID, journeyMap, nil)
		} else {
			return nil
		}

		journeyID = journeyMap.JourneyID
	}

	timeframe := vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef
	if timeframe == "" {
		timeframe = time.Now().Format("2006-01-02")
	}

	vehicleLocationEvent := VehicleLocationEvent{
		JourneyRef:  journeyID,
		OperatorRef: fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
		ServiceRef:  vehicle.MonitoredVehicleJourney.PublishedLineName,

		Timeframe:        timeframe,
		CreationDateTime: siriVMVehicleIdentificationEvent.ResponseTime,

		DataSource: siriVMVehicleIdentificationEvent.DataSource,

		VehicleLocation: ctdf.Location{
			Type: "Point",
			Coordinates: []float64{
				vehicle.MonitoredVehicleJourney.VehicleLocation.Longitude,
				vehicle.MonitoredVehicleJourney.VehicleLocation.Latitude,
			},
		},
		VehicleBearing: vehicle.MonitoredVehicleJourney.Bearing,
	}

	if vehicle.MonitoredVehicleJourney.VehicleRef != "" {
		vehicleLocationEvent.VehicleRef = fmt.Sprintf("GB:VEHICLE:%s:%s", operatorRef, vehicle.MonitoredVehicleJourney.VehicleRef)
	}

	return &vehicleLocationEvent
}

func updateRealtimeJourney(vehicleLocationEvent *VehicleLocationEvent) (mongo.WriteModel, error) {
	loc, _ := time.LoadLocation("Europe/London")
	currentTime := vehicleLocationEvent.CreationDateTime

	var journey *CacheJourney
	var realtimeJourneyReliability ctdf.RealtimeJourneyReliabilityType
	cachedJourney, _ := journeyCache.Get(context.Background(), vehicleLocationEvent.JourneyRef)

	if cachedJourney == nil || cachedJourney == "" {
		journeysCollection := database.GetCollection("journeys")
		journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": vehicleLocationEvent.JourneyRef}).Decode(&journey)

		for _, pathItem := range journey.Path {
			pathItem.GetDestinationStop()
		}

		journeyCache.Set(context.Background(), vehicleLocationEvent.JourneyRef, journey, nil)
	} else {
		switch cachedJourney.(type) {
		default:
			journey = cachedJourney.(*CacheJourney)
		case string:
			json.Unmarshal([]byte(cachedJourney.(string)), &journey)
		}
	}

	closestDistance := 999999999999.0
	var closestDistanceJourneyPath *ctdf.JourneyPathItem
	var closestDistanceJourneyPathIndex int
	var closestDistanceJourneyPathPercentComplete float64 // TODO: this is a hack, replace with actual distance

	// Attempt to calculate using closest journey track
	for i, journeyPathItem := range journey.Path {
		journeyPathClosestDistance := 99999999999999.0 // TODO do this better

		for i := 0; i < len(journeyPathItem.Track)-1; i++ {
			a := journeyPathItem.Track[i]
			b := journeyPathItem.Track[i+1]

			distance := vehicleLocationEvent.VehicleLocation.DistanceFromLine(a, b)

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
		for i, journeyPathItem := range journey.Path {
			if journeyPathItem.DestinationStop == nil {
				return nil, errors.New(fmt.Sprintf("Cannot get stop %s", journeyPathItem.DestinationStopRef))
			}

			distance := journeyPathItem.DestinationStop.Location.Distance(&vehicleLocationEvent.VehicleLocation)

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
			previousJourneyPath := journey.Path[len(journey.Path)-1]

			if previousJourneyPath.DestinationStop == nil {
				return nil, errors.New(fmt.Sprintf("Cannot get stop %s", previousJourneyPath.DestinationStopRef))
			}

			previousJourneyPathDistance := previousJourneyPath.DestinationStop.Location.Distance(&vehicleLocationEvent.VehicleLocation)

			closestDistanceJourneyPathPercentComplete = (1 + (float64(previousJourneyPathDistance-closestDistance) / float64(previousJourneyPathDistance+closestDistance))) / 2
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
		loc,
	)
	originDepartureTimeWithDate := time.Date(
		realtimeTimeframe.Year(),
		realtimeTimeframe.Month(),
		realtimeTimeframe.Day(),
		closestDistanceJourneyPath.OriginDepartureTime.Hour(),
		closestDistanceJourneyPath.OriginDepartureTime.Minute(),
		closestDistanceJourneyPath.OriginDepartureTime.Second(),
		closestDistanceJourneyPath.OriginDepartureTime.Nanosecond(),
		loc,
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
	for i := closestDistanceJourneyPathIndex; i < len(journey.Path); i++ {
		path := journey.Path[i]

		arrivalTime := path.DestinationArrivalTime.Add(offset).Round(time.Minute)
		var departureTime time.Time

		if i < len(journey.Path)-1 {
			nextPath := journey.Path[i+1]

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

	// Update database
	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleLocationEvent.Timeframe, vehicleLocationEvent.JourneyRef)
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

	var realtimeJourney *ctdf.RealtimeJourney

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

	if realtimeJourney == nil {
		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier: realtimeJourneyIdentifier,
			JourneyRef:        vehicleLocationEvent.JourneyRef,

			CreationDateTime: currentTime,
			DataSource:       vehicleLocationEvent.DataSource,
			Reliability:      realtimeJourneyReliability,

			VehicleRef: vehicleLocationEvent.VehicleRef,
			Stops:      map[string]*ctdf.RealtimeJourneyStops{},
		}
	} else if realtimeJourney.NextStopRef != closestDistanceJourneyPath.DestinationStopRef {
		journeyStopUpdates[realtimeJourney.NextStopRef] = &ctdf.RealtimeJourneyStops{
			StopRef:  realtimeJourney.NextStopRef,
			TimeType: ctdf.RealtimeJourneyStopTimeHistorical,

			// TODO this should obviously be a different time
			// TODO this add 1 hour is a massive hack and will probably break when clocks change again
			ArrivalTime:   currentTime.In(loc).Add(1 * time.Hour),
			DepartureTime: currentTime.In(loc).Add(1 * time.Hour),
		}
	}

	realtimeJourney.ModificationDateTime = currentTime
	realtimeJourney.VehicleLocation = vehicleLocationEvent.VehicleLocation
	realtimeJourney.VehicleBearing = vehicleLocationEvent.VehicleBearing
	realtimeJourney.DepartedStopRef = closestDistanceJourneyPath.OriginStopRef
	realtimeJourney.NextStopRef = closestDistanceJourneyPath.DestinationStopRef

	for key, stopUpdate := range journeyStopUpdates {
		realtimeJourney.Stops[key] = stopUpdate
	}

	bsonRep, _ := bson.Marshal(bson.M{"$set": realtimeJourney})
	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(searchQuery)
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	return updateModel, nil
}
