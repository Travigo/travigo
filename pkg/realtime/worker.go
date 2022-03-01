package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/rabbitmq"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

func StartWorker() {
	log.Info().Msg("Starting Realtime calculator worker")
	// Mongo indexes
	//TODO: Doesnt really make sense for this package to be managing indexes
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	_, err := realtimeJourneysCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
	}, options.CreateIndexes())
	if err != nil {
		panic(err)
	}

	// Open RabbitMQ channel and keep polling for messages
	channel, err := rabbitmq.GetChannel()
	if err != nil {
		log.Error().Err(err).Msg("Failed to create RabbitMQ Channel")
	}
	queue, err := channel.QueueDeclare(
		"vehicle_location_events",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create RabbitMQ Queue")
	}

	messages, err := channel.Consume(
		queue.Name, // queue
		"",         // consumer
		true,       // auto-ack
		false,      // exclusive
		false,      // no-local
		false,      // no-wait
		nil,        // args
	)

	forever := make(chan bool)

	go func() {
		for message := range messages {
			var vehicleLocationEvent *ctdf.VehicleLocationEvent
			err := json.Unmarshal(message.Body, &vehicleLocationEvent)

			if err != nil {
				log.Error().Err(err).Msg("Failed to unmarshal VehicleLocationEvent")
				continue
			}

			go UpdateRealtimeJourney(vehicleLocationEvent)
		}
	}()

	<-forever
}

func UpdateRealtimeJourney(vehicleLocationEvent *ctdf.VehicleLocationEvent) error {
	journeysCollection := database.GetCollection("journeys")
	var journey *ctdf.Journey
	journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": vehicleLocationEvent.JourneyRef}).Decode(&journey)

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

	// Update database
	realtimeJourneyIdentifier := fmt.Sprintf("REALTIME:%s:%s", vehicleLocationEvent.Timeframe, vehicleLocationEvent.JourneyRef)

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

	var realtimeJourney *ctdf.RealtimeJourney
	realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

	if realtimeJourney == nil {
		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier: realtimeJourneyIdentifier,
			JourneyRef:        vehicleLocationEvent.JourneyRef,

			CreationDateTime: time.Now(),
			// DataSource: ,

			StopHistory: []*ctdf.RealtimeJourneyStopHistory{},
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
