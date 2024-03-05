package dbwatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type ServiceAlertsWatch struct {
	EventQueue rmq.Queue
}

func NewServiceAlertsWatch() *ServiceAlertsWatch {
	eventQueue, err := redis_client.QueueConnection.OpenQueue("events-queue")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to start event queue")
	}

	return &ServiceAlertsWatch{
		EventQueue: eventQueue,
	}
}

func (w *ServiceAlertsWatch) Run() {
	log.Info().Msg("Starting dbwatch on collection service_alerts")
	collection := database.GetCollection("service_alerts")
	matchPipeline := bson.D{
		{
			Key: "$match", Value: bson.D{
				{Key: "operationType", Value: "insert"},
			},
		},
	}
	stream, err := collection.Watch(context.Background(), mongo.Pipeline{matchPipeline})
	if err != nil {
		panic(err)
	}

	defer stream.Close(context.Background())

	for stream.Next(context.Background()) {
		var data struct {
			OperationType string             `bson:"operationType"`
			FullDocument  *ctdf.ServiceAlert `bson:"fullDocument"`
		}
		if err := stream.Decode(&data); err != nil {
			log.Error().Err(err).Msg("Failed to decode event")
			continue
		}

		if data.OperationType != "insert" {
			continue
		}

		log.Info().Str("id", data.FullDocument.PrimaryIdentifier).Msg("New ServiceAlert inserted")

		eventBytes, _ := json.Marshal(ctdf.Event{
			Type:      ctdf.EventTypeServiceAlertCreated,
			Timestamp: time.Now(),
			Body:      data.FullDocument,
		})
		w.EventQueue.PublishBytes(eventBytes)
	}
}
