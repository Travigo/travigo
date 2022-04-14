package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/redis_client"
	"github.com/britbus/britbus/pkg/siri_vm"
	"github.com/dgraph-io/ristretto"
	"github.com/eko/gocache/v2/cache"
	"github.com/eko/gocache/v2/store"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

var journeyCache *cache.ChainCache
var identificationCache *cache.ChainCache

func CreateIdentificationCache() {
	ristrettoCache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 10000,
		MaxCost:     50000000,
		BufferItems: 64,
	})
	if err != nil {
		panic(err)
	}
	ristrettoStore := store.NewRistretto(ristrettoCache, &store.Options{
		Expiration: 30 * time.Minute,
	})

	redisStore := store.NewRedis(redis_client.Client, &store.Options{
		Expiration: 30 * time.Minute,
	})

	identificationCache = cache.NewChain(
		cache.New(ristrettoStore),
		cache.New(redisStore),
	)
}
func CreateJourneyCache() {
	ristrettoCache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 6000,
		MaxCost:     5000000,
		BufferItems: 64,
	})
	if err != nil {
		panic(err)
	}
	ristrettoStore := store.NewRistretto(ristrettoCache, &store.Options{
		Expiration: 30 * time.Minute,
	})

	redisStore := store.NewRedis(redis_client.Client, &store.Options{
		Expiration: 30 * time.Minute,
	})

	journeyCache = cache.NewChain(
		cache.New(ristrettoStore),
		cache.New(redisStore),
	)
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
	log.Info().Msgf("Starting realtime consumers")

	// for i := 0; i < numConsumers; i++ {
	// 	go startRealtimeConsumer(i)
	// }
	queue, err := redis_client.QueueConnection.OpenQueue("realtime-queue")
	if err != nil {
		panic(err)
	}
	if err := queue.StartConsuming(50, 100*time.Millisecond); err != nil {
		panic(err)
	}
	if _, err := queue.AddBatchConsumer("realtime-queue", 20, 1*time.Second, NewBatchConsumer()); err != nil {
		panic(err)
	}
}

type BatchConsumer struct {
}

func NewBatchConsumer() *BatchConsumer {
	return &BatchConsumer{}
}

func (consumer *BatchConsumer) Consume(batch rmq.Deliveries) {
	payloads := batch.Payloads()

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
			updateRealtimeJourney(vehicleLocationEvent)
		}
	}

	if errors := batch.Ack(); len(errors) > 0 {
		for _, err := range errors {
			log.Error().Err(err).Msg("Failed to consume realtime event")
		}
	}
}

func identifyVehicle(siriVMVehicleIdentificationEvent *siri_vm.SiriVMVehicleIdentificationEvent) *ctdf.VehicleLocationEvent {
	vehicle := siriVMVehicleIdentificationEvent.VehicleActivity
	vehicleJourneyRef := vehicle.MonitoredVehicleJourney.VehicleJourneyRef

	if vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef != "" {
		vehicleJourneyRef = vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef
	}

	localJourneyID := fmt.Sprintf(
		"SIRI-VM:LOCALJOURNEYID:%s:%s:%s:%s",
		fmt.Sprintf(ctdf.OperatorNOCFormat, vehicle.MonitoredVehicleJourney.OperatorRef),
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
			"OperatorRef":              fmt.Sprintf(ctdf.OperatorNOCFormat, vehicle.MonitoredVehicleJourney.OperatorRef),
			"VehicleJourneyRef":        vehicleJourneyRef,
			"OriginRef":                fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef),
			"DestinationRef":           fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.DestinationRef),
			"OriginAimedDepartureTime": vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime,
			"FramedVehicleJourneyDate": vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef,
		})

		if err != nil {
			// log.Error().Err(err).Str("localjourneyid", localJourneyID).Msgf("Could not find Journey")

			// Save a cache value of N/A to stop us from constantly rechecking for journeys we cant identify
			identificationCache.Set(context.Background(), localJourneyID, "N/A", &store.Options{
				Expiration: 30 * time.Minute,
			})
			return nil
		}
		journeyID = journey.PrimaryIdentifier

		identificationCache.Set(context.Background(), localJourneyID, journeyID, nil)
	} else if cachedJourneyMapping == "N/A" {
		return nil
	} else {
		journeyID = cachedJourneyMapping.(string)
	}

	timeframe := vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef
	if timeframe == "" {
		timeframe = time.Now().Format("2006-01-02")
	}

	return &ctdf.VehicleLocationEvent{
		JourneyRef:       journeyID,
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
}

func updateRealtimeJourney(vehicleLocationEvent *ctdf.VehicleLocationEvent) error {
	var journey *CacheJourney
	cachedJourney, _ := journeyCache.Get(context.Background(), vehicleLocationEvent.JourneyRef)

	if cachedJourney == nil || cachedJourney == "" {
		journeysCollection := database.GetCollection("journeys")
		journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": vehicleLocationEvent.JourneyRef}).Decode(&journey)

		journeyCache.Set(context.Background(), vehicleLocationEvent.JourneyRef, journey, &store.Options{
			Expiration: 30 * time.Minute,
		})
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

	// Calculate new stop arrival times
	currentTime := time.Now()
	realtimeTimeframe, err := time.Parse("2006-01-02", vehicleLocationEvent.Timeframe)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse realtime time frame")
	}

	if closestDistanceJourneyPath == nil {
		// https://github.com/BritBus/britbus/issues/35
		return errors.New("Could not identify closet journey path")
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
		currentTime.Location(),
	)
	originDepartureTimeWithDate := time.Date(
		realtimeTimeframe.Year(),
		realtimeTimeframe.Month(),
		realtimeTimeframe.Day(),
		closestDistanceJourneyPath.OriginDepartureTime.Hour(),
		closestDistanceJourneyPath.OriginDepartureTime.Minute(),
		closestDistanceJourneyPath.OriginDepartureTime.Second(),
		closestDistanceJourneyPath.OriginDepartureTime.Nanosecond(),
		currentTime.Location(),
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

	// Calculate all the estimated stop arrival & departure times
	estimatedJourneyStops := map[string]*ctdf.RealtimeJourneyStops{}
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

		estimatedJourneyStops[path.DestinationStopRef] = &ctdf.RealtimeJourneyStops{
			StopRef:  path.DestinationStopRef,
			TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,

			ArrivalTime:   arrivalTime,
			DepartureTime: departureTime,
		}
	}

	// Update database
	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleLocationEvent.Timeframe, vehicleLocationEvent.JourneyRef)

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

	var realtimeJourney *ctdf.RealtimeJourney

	realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

	if realtimeJourney == nil {
		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier: realtimeJourneyIdentifier,
			JourneyRef:        vehicleLocationEvent.JourneyRef,

			CreationDateTime: time.Now(),
			DataSource:       vehicleLocationEvent.DataSource,
		}
	}

	realtimeJourney.ModificationDateTime = time.Now()
	realtimeJourney.VehicleLocation = vehicleLocationEvent.VehicleLocation
	realtimeJourney.VehicleBearing = vehicleLocationEvent.VehicleBearing
	realtimeJourney.DepartedStopRef = closestDistanceJourneyPath.OriginStopRef
	realtimeJourney.NextStopRef = closestDistanceJourneyPath.DestinationStopRef
	realtimeJourney.Stops = estimatedJourneyStops

	opts := options.Update().SetUpsert(true)
	realtimeJourneysCollection.UpdateOne(context.Background(), searchQuery, bson.M{"$set": realtimeJourney}, opts)

	return nil
}
