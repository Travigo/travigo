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
	"go.mongodb.org/mongo-driver/mongo/options"
)

type RealtimeJourneysWatch struct {
	EventQueue rmq.Queue
}

type realtimeJourneyUpdate struct {
	OperationType     string `bson:"operationType"`
	UpdateDescription struct {
		UpdatedFields ctdf.RealtimeJourney `bson:"updatedFields"`
	} `bson:"updateDescription"`
	FullDocument             ctdf.RealtimeJourney `bson:"fullDocument"`
	FullDocumentBeforeChange ctdf.RealtimeJourney `bson:"fullDocumentBeforeChange"`
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
				{
					Key: "$and", Value: bson.A{
						bson.D{{Key: "operationType", Value: "update"}}, //ignore inserts for now
						bson.D{
							{
								Key: "$or", Value: bson.A{
									bson.D{
										{
											Key:   "updateDescription.updatedFields.cancelled",
											Value: bson.D{{Key: "$exists", Value: true}},
										},
									},
									// This is prob a bit hacky but it does work so who really cares?
									bson.D{
										{
											Key:   "fullDocument.datasource.provider",
											Value: "National-Rail",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	projectPipeline := bson.D{
		{
			Key: "$project",
			Value: bson.D{
				bson.E{Key: "activelytracked", Value: 0},
				bson.E{Key: "datasource", Value: 0},
				bson.E{Key: "timeoutdurationminutes", Value: 0},
				bson.E{Key: "creationdatetime", Value: 0},
				// bson.E{Key: "modificationdatetime", Value: 0},
				bson.E{Key: "reliability", Value: 0},
				bson.E{Key: "offset", Value: 0},
				bson.E{Key: "vehiclelocation", Value: 0},
				bson.E{Key: "vehiclebearing", Value: 0},
				bson.E{Key: "vehicleref", Value: 0},
			},
		},
	}
	opts := options.ChangeStream().SetFullDocumentBeforeChange(options.WhenAvailable).SetFullDocument(options.WhenAvailable)
	stream, err := collection.Watch(context.Background(), mongo.Pipeline{matchPipeline, projectPipeline}, opts)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to watch collection")
	}

	defer stream.Close(context.Background())

	for stream.Next(context.Background()) {
		var data realtimeJourneyUpdate

		if err := stream.Decode(&data); err != nil {
			log.Error().Err(err).Msg("Failed to decode event")
			continue
		}

		go func(data *realtimeJourneyUpdate) {
			if data.OperationType == "insert" {
				log.Info().Str("id", data.FullDocument.PrimaryIdentifier).Msg("New RealtimeJourney inserted")

				eventBytes, _ := json.Marshal(ctdf.Event{
					Type:      ctdf.EventTypeRealtimeJourneyCreated,
					Timestamp: time.Now(),
					Body:      data.FullDocument,
				})
				w.EventQueue.PublishBytes(eventBytes)
			} else if data.OperationType == "update" {
				if data.FullDocument.PrimaryIdentifier == "" {
					return
				}

				// Detect newly cancelled journeys
				if data.UpdateDescription.UpdatedFields.Cancelled == true && !data.FullDocumentBeforeChange.Cancelled {
					log.Info().Str("id", data.FullDocument.PrimaryIdentifier).Msg("RealtimeJourney has been cancelled")

					eventBytes, _ := json.Marshal(ctdf.Event{
						Type:      ctdf.EventTypeRealtimeJourneyCancelled,
						Timestamp: time.Now(),
						Body:      data.FullDocument,
					})
					w.EventQueue.PublishBytes(eventBytes)

					return
				}

				// Checks for set or changed platforms
				for id, journeyStop := range data.FullDocument.Stops {
					// This shouldnt happen as why would a historical stop change platforms
					if journeyStop.TimeType == ctdf.RealtimeJourneyStopTimeHistorical {
						continue
					}

					newPlatform := journeyStop.Platform

					oldJourneyPlatform := data.FullDocumentBeforeChange.Stops[id]
					if oldJourneyPlatform == nil {
						continue
					}
					oldPlatform := oldJourneyPlatform.Platform

					if oldPlatform == "" && newPlatform != oldPlatform {
						log.Info().
							Str("id", data.FullDocument.PrimaryIdentifier).
							Str("platform", newPlatform).
							Msg("RealtimeJourney stop platform set")

						eventBytes, _ := json.Marshal(ctdf.Event{
							Type:      ctdf.EventTypeRealtimeJourneyPlatformSet,
							Timestamp: time.Now(),
							Body: map[string]interface{}{
								"RealtimeJourney": data.FullDocument,
								"Stop":            id,
							},
						})
						w.EventQueue.PublishBytes(eventBytes)
					} else if oldPlatform != "" && newPlatform != oldPlatform {
						log.Info().
							Str("id", data.FullDocument.PrimaryIdentifier).
							Str("oldplatform", oldPlatform).
							Str("newplatform", newPlatform).
							Msg("RealtimeJourney stop platform changed")

						eventBytes, _ := json.Marshal(ctdf.Event{
							Type:      ctdf.EventTypeRealtimeJourneyPlatformChanged,
							Timestamp: time.Now(),
							Body: map[string]interface{}{
								"RealtimeJourney": data.FullDocument,
								"Stop":            id,
								"OldPlatform":     oldPlatform,
							},
						})
						w.EventQueue.PublishBytes(eventBytes)
					}
				}
			}
		}(&data)
	}

	log.Error().Err(stream.Err()).Msg("realtime journey watch fell over")

	w.Run() // TODO this is a hack and a half
}
