package dbwatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type RealtimeJourneysWatch struct {
	EventQueue rmq.Queue
}

func NewRealtimeJourneysWatch() *RealtimeJourneysWatch {
	eventQueue, err := redis_client.QueueConnection.OpenQueue("events-queue")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to start event queue")
	}

	return &RealtimeJourneysWatch{
		EventQueue: eventQueue,
	}
}

func (w *RealtimeJourneysWatch) Run() {
	log.Info().Msg("Starting dbwatch on collection realtime_journeys")
	collection := database.GetCollection("realtime_journeys")
	matchPipeline := bson.D{
		{
			Key: "$match", Value: bson.D{
				{Key: "operationType", Value: "update"}, //ignore inserts for now
			},
		},
	}
	opts := options.ChangeStream().SetFullDocumentBeforeChange(options.WhenAvailable).SetFullDocument(options.Required)
	stream, err := collection.Watch(context.TODO(), mongo.Pipeline{matchPipeline}, opts)
	if err != nil {
		panic(err)
	}

	defer stream.Close(context.TODO())

	for stream.Next(context.TODO()) {
		var data struct {
			OperationType     string `bson:"operationType"`
			UpdateDescription struct {
				UpdatedFields bson.M `bson:"updatedFields"`
			} `bson:"updateDescription"`
			FullDocument             ctdf.RealtimeJourney `bson:"fullDocument"`
			FullDocumentBeforeChange ctdf.RealtimeJourney `bson:"fullDocumentBeforeChange"`
		}
		if err := stream.Decode(&data); err != nil {
			log.Error().Err(err).Msg("Failed to decode event")
			continue
		}

		if data.OperationType == "insert" {
			log.Info().Str("id", data.FullDocument.PrimaryIdentifier).Msg("New RealtimeJourneys inserted")

			eventBytes, _ := json.Marshal(ctdf.Event{
				Type:      ctdf.EventTypeRealtimeJourneyCreated,
				Timestamp: time.Now(),
				Body:      data.FullDocument,
			})
			w.EventQueue.PublishBytes(eventBytes)
		} else if data.OperationType == "update" {
			// Detect newly cancelled journeys
			if data.UpdateDescription.UpdatedFields["cancelled"] == true && !data.FullDocumentBeforeChange.Cancelled {
				pretty.Println(data.UpdateDescription.UpdatedFields["cancelled"])
				log.Info().Str("id", data.FullDocument.PrimaryIdentifier).Msg("RealtimeJourneys has been cancelled")

				eventBytes, _ := json.Marshal(ctdf.Event{
					Type:      ctdf.EventTypeRealtimeJourneyCancelled,
					Timestamp: time.Now(),
					Body:      data.FullDocument,
				})
				w.EventQueue.PublishBytes(eventBytes)
			}
		}
	}
}
