package realtime

import (
	"context"
	"fmt"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/dgraph-io/ristretto"
	"github.com/eko/gocache/v2/cache"
	"github.com/eko/gocache/v2/store"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

const numConsumers = 10

var vehicleLocationEventQueue chan *ctdf.VehicleLocationEvent = make(chan *ctdf.VehicleLocationEvent, 2000)
var journeyCache *cache.Cache

func StartConsumers() {
	// Create Cache
	ristrettoCache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 10000,
		MaxCost:     1 << 29,
		BufferItems: 64,
	})
	if err != nil {
		panic(err)
	}
	ristrettoStore := store.NewRistretto(ristrettoCache, &store.Options{
		Expiration: 30 * time.Minute,
	})
	journeyCache = cache.New(ristrettoStore)

	// Mongo indexes
	//TODO: Doesnt really make sense for this package to be managing indexes
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	_, err = realtimeJourneysCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err)
	}

	// Start the background consumers
	log.Info().Msgf("Starting realtime consumers")

	for i := 0; i < numConsumers; i++ {
		go startRealtimeConsumer(i)
	}
}

func AddToQueue(vehicleLocationEvent *ctdf.VehicleLocationEvent) {
	vehicleLocationEventQueue <- vehicleLocationEvent
}

func startRealtimeConsumer(id int) {
	log.Info().Msgf("Realtime consumer %d started", id)

	for vehicleLocationEvent := range vehicleLocationEventQueue {
		updateRealtimeJourney(vehicleLocationEvent)
	}
}

func updateRealtimeJourney(vehicleLocationEvent *ctdf.VehicleLocationEvent) error {
	var journey *ctdf.Journey
	cachedJourney, _ := journeyCache.Get(context.Background(), vehicleLocationEvent.JourneyRef)

	if cachedJourney == nil {
		journeysCollection := database.GetCollection("journeys")
		journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": vehicleLocationEvent.JourneyRef}).Decode(&journey)

		journeyCache.Set(context.Background(), vehicleLocationEvent.JourneyRef, journey, nil)
	} else {
		journey = cachedJourney.(*ctdf.Journey)
	}

	closestDistance := 999999999999.0
	var closestDistanceJourneyPath *ctdf.JourneyPathItem

	for _, journeyPathItem := range journey.Path {
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
		}
	}

	// Calculate new stop arrival times
	currentTime := time.Now()
	realtimeTimeframe, err := time.Parse("2006-01-02", vehicleLocationEvent.Timeframe)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse realtime time frame")
	}

	// Get the arrival & departure times with date of the journey
	destinationArrivalTimeWithDate := time.Date(
		realtimeTimeframe.Year(),
		realtimeTimeframe.Month(),
		realtimeTimeframe.Day(),
		closestDistanceJourneyPath.DestinationArivalTime.Hour(),
		closestDistanceJourneyPath.DestinationArivalTime.Minute(),
		closestDistanceJourneyPath.DestinationArivalTime.Second(),
		closestDistanceJourneyPath.DestinationArivalTime.Nanosecond(),
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
	currentPathPercentageComplete := 0.5

	// Calculate what the expected time of the current position of the vehicle should be
	currentPathPositionExpectedTime := originDepartureTimeWithDate.Add(
		time.Duration(int(currentPathPercentageComplete * float64(currentPathTraversalTime.Nanoseconds()))))

	// Offset is how far behind or ahead the vehicle is from its positions expected time
	offset := currentTime.Sub(currentPathPositionExpectedTime)

	pretty.Println(originDepartureTimeWithDate, destinationArrivalTimeWithDate)
	pretty.Println(currentPathPositionExpectedTime)
	pretty.Println(currentTime)
	pretty.Println(offset.Minutes())

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

			Stops: []*ctdf.RealtimeJourneyStops{},
		}
	}

	realtimeJourney.ModificationDateTime = time.Now()
	realtimeJourney.VehicleLocation = vehicleLocationEvent.VehicleLocation
	realtimeJourney.VehicleBearing = vehicleLocationEvent.VehicleBearing
	realtimeJourney.DepartedStopRef = closestDistanceJourneyPath.OriginStopRef
	realtimeJourney.NextStopRef = closestDistanceJourneyPath.DestinationStopRef

	opts := options.Update().SetUpsert(true)
	realtimeJourneysCollection.UpdateOne(context.Background(), searchQuery, bson.M{"$set": realtimeJourney}, opts)

	return nil
}
